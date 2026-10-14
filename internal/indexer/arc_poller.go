package indexer

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/deodes/backend/internal/domain/circle"
	"github.com/deodes/backend/internal/domain/contribution"
	"github.com/deodes/backend/internal/domain/payout"
	"github.com/deodes/backend/pkg/arc"
	"github.com/deodes/backend/pkg/metrics"
)

// EthClient is the minimal Arc RPC surface the poller needs. *arc.Client
// satisfies it; tests stub it.
type EthClient interface {
	BlockNumber(ctx context.Context) (uint64, error)
	GetLogs(ctx context.Context, fromBlock, toBlock uint64, addresses []string, topicFilter [][]string) ([]arc.Log, error)
	GetTransactionReceipt(ctx context.Context, txHash string) (*arc.Receipt, error)
}

// Broadcaster relays decoded chain events to circle rooms (WS + Redis).
// *websocket.Broadcaster satisfies it.
type Broadcaster interface {
	BroadcastChainEvent(ctx context.Context, circleID, eventType string, payload map[string]any)
}

// Config tunes the poller. Zero values are normalized by normalize.
type Config struct {
	// ChainID selects the arc_cursor row and the lag-metric label.
	ChainID uint64
	// Addresses are the watched contracts (circle + factory).
	Addresses []string
	// StartBlock is the backfill origin when no cursor exists.
	StartBlock uint64
	// BatchBlocks caps blocks per eth_getLogs call.
	BatchBlocks uint64
	// MaxBatchesPerTick caps catch-up work per tick so a deep backfill
	// yields to fresh polls instead of starving them.
	MaxBatchesPerTick int
	// ReorgWindow keeps the cursor this many blocks behind latest: only
	// finalized ranges advance the cursor; unfinalized logs still process
	// (dedup makes replays safe) and `removed` logs roll back.
	ReorgWindow uint64
	// PollInterval paces Run ticks.
	PollInterval time.Duration
}

func (c Config) normalize() Config {
	if c.BatchBlocks == 0 {
		c.BatchBlocks = 50
	}
	if c.MaxBatchesPerTick <= 0 {
		c.MaxBatchesPerTick = 20
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 3 * time.Second
	}
	return c
}

// Poller tails DeodesCircle/DeodesFactory logs: backfill + live tail with
// cursor, idempotent persist on (tx_hash, log_index), poison deadlettering,
// reorg rollback, verification settlement, and WS fan-out.
type Poller struct {
	eth       EthClient
	repo      Repository
	contribs  contribution.Repository
	payouts   payout.Repository
	circles   circle.Repository
	broadcast Broadcaster
	cfg       Config
}

// NewPoller builds the Arc poller. Nil store/circle lookups disable the
// settlement/broadcast stages they feed (audit persist always runs).
func NewPoller(eth EthClient, repo Repository, contribs contribution.Repository, payouts payout.Repository, circles circle.Repository, broadcast Broadcaster, cfg Config) *Poller {
	return &Poller{
		eth: eth, repo: repo,
		contribs: contribs, payouts: payouts, circles: circles,
		broadcast: broadcast, cfg: cfg.normalize(),
	}
}

// Run ticks RunOnce until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(p.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.RunOnce(ctx); err != nil {
				log.Warn().Err(err).Msg("arc poller tick failed")
			}
		}
	}
}

// RunOnce advances the cursor over finalized ranges. At-least-once: a batch
// commits its cursor only after every log in it processed; replays dedup.
func (p *Poller) RunOnce(ctx context.Context) error {
	latest, err := p.eth.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("indexer: blockNumber: %w", err)
	}
	from, err := p.repo.GetNextFrom(ctx, p.cfg.ChainID, p.cfg.StartBlock)
	if err != nil {
		return fmt.Errorf("indexer: cursor: %w", err)
	}
	head := latest
	if p.cfg.ReorgWindow > 0 {
		if latest < p.cfg.ReorgWindow {
			head = 0
		} else {
			head = latest - p.cfg.ReorgWindow
		}
	}
	processed := from
	if from > 0 {
		processed = from - 1
	}
	batchesLeft := p.cfg.MaxBatchesPerTick
	for from <= head {
		if err := ctx.Err(); err != nil {
			return err
		}
		if batchesLeft <= 0 {
			break
		}
		batchesLeft--
		to := from + p.cfg.BatchBlocks - 1
		if to > head || to < from { // overflow guard
			to = head
		}
		logs, err := p.eth.GetLogs(ctx, from, to, p.cfg.Addresses, nil)
		if err != nil {
			return fmt.Errorf("indexer: getLogs [%d,%d]: %w", from, to, err)
		}
		for i := range logs {
			if err := p.processLog(ctx, &logs[i]); err != nil {
				return fmt.Errorf("indexer: log %s/%d: %w", logs[i].TransactionHash, logs[i].LogIndex, err)
			}
		}
		if err := p.repo.SetLastBlock(ctx, p.cfg.ChainID, to); err != nil {
			return fmt.Errorf("indexer: cursor commit %d: %w", to, err)
		}
		processed = to
		from = to + 1
		if from == 0 { // overflowed past MaxUint64: done
			break
		}
	}
	lag := uint64(0)
	if latest > processed {
		lag = latest - processed
	}
	metrics.IndexerLagBlocks.WithLabelValues(strconv.FormatUint(p.cfg.ChainID, 10)).Set(float64(lag))
	return nil
}

// processLog decodes, persists (idempotent), settles, and broadcasts one log.
// Poison is deadlettered, never fatal: one bad log cannot stall the poller.
func (p *Poller) processLog(ctx context.Context, l *arc.Log) error {
	if l.Removed {
		// Reorg orphan: roll back the audit row so a replay re-inserts it,
		// and park a note for the operator.
		if err := p.repo.DeleteByTxLog(ctx, l.TransactionHash, l.LogIndex); err != nil {
			return err
		}
		p.deadletter(ctx, l, "reorg", "log removed by reorg; audit row rolled back", nil)
		metrics.IndexerEventsTotal.WithLabelValues("unknown", "reorged").Inc()
		return nil
	}
	ev, ok, err := DecodeLog(l.Topics, l.Data)
	if err != nil {
		p.deadletter(ctx, l, "decode", err.Error(), nil)
		metrics.IndexerEventsTotal.WithLabelValues("unknown", "deadlettered").Inc()
		return nil
	}
	if !ok {
		metrics.IndexerEventsTotal.WithLabelValues("unknown", "skipped").Inc()
		return nil
	}
	inserted, err := p.repo.Insert(ctx, PersistedEvent{
		TxHash:      l.TransactionHash,
		LogIndex:    l.LogIndex,
		BlockNumber: l.BlockNumber,
		Address:     l.Address,
		Event:       ev.Schema.Name,
		Payload:     ev.Fields,
	})
	if err != nil {
		p.deadletter(ctx, l, "persist", err.Error(), ev.Fields)
		metrics.IndexerEventsTotal.WithLabelValues(ev.Schema.Name, "deadlettered").Inc()
		return nil
	}
	if !inserted {
		// Replay: side effects ran on first sight, never twice.
		metrics.IndexerEventsTotal.WithLabelValues(ev.Schema.Name, "replay").Inc()
		return nil
	}
	metrics.IndexerEventsTotal.WithLabelValues(ev.Schema.Name, "persisted").Inc()
	p.settle(ctx, l, &ev)
	p.broadcastEvent(ctx, l, &ev)
	return nil
}

// settle resolves on-chain verification for money events. A mined log whose
// backend record has not arrived yet (user posted the tx before the API
// call) parks in settle-retry for replay instead of failing.
func (p *Poller) settle(ctx context.Context, l *arc.Log, ev *DecodedEvent) {
	switch ev.Schema.Name {
	case "ContributionReceived":
		if p.contribs == nil {
			return
		}
		c, err := p.contribs.FindByTxnHash(ctx, l.TransactionHash)
		if err != nil || c == nil {
			p.deadletter(ctx, l, "settle-retry", "no contribution row for tx yet", ev.Fields)
			return
		}
		p.settleWithReceipt(ctx, l, ev,
			func(ok bool) error {
				if !ok {
					return p.contribs.UpdateVerificationStatus(ctx, c.ID, false, contribution.VerificationStatusFailed)
				}
				if err := p.contribs.UpdateVerificationStatus(ctx, c.ID, true, contribution.VerificationStatusVerified); err != nil {
					return err
				}
				return p.contribs.UpdateStatus(ctx, c.ID, contribution.StatusConfirmed, l.TransactionHash)
			})
	case "PayoutExecuted":
		if p.payouts == nil {
			return
		}
		py, err := p.payouts.FindByTxnHash(ctx, l.TransactionHash)
		if err != nil || py == nil {
			p.deadletter(ctx, l, "settle-retry", "no payout row for tx yet", ev.Fields)
			return
		}
		p.settleWithReceipt(ctx, l, ev,
			func(ok bool) error {
				if !ok {
					return p.payouts.UpdateVerificationStatus(ctx, py.ID, false, payout.VerificationStatusFailed)
				}
				return p.payouts.UpdateVerificationStatus(ctx, py.ID, true, payout.VerificationStatusVerified)
			})
	}
}

// settleWithReceipt gates settlement on execution success: a reverted tx
// marks FAILED, never verified.
func (p *Poller) settleWithReceipt(ctx context.Context, l *arc.Log, ev *DecodedEvent, apply func(ok bool) error) {
	if p.eth == nil {
		return
	}
	receipt, err := p.eth.GetTransactionReceipt(ctx, l.TransactionHash)
	if err != nil {
		p.deadletter(ctx, l, "settle-retry", fmt.Sprintf("receipt lookup: %s", err), ev.Fields)
		return
	}
	if receipt == nil {
		p.deadletter(ctx, l, "settle-retry", "receipt not found", ev.Fields)
		return
	}
	if err := apply(receipt.Status == 1); err != nil {
		p.deadletter(ctx, l, "settle-retry", fmt.Sprintf("settlement write: %s", err), ev.Fields)
	}
}

// broadcastEvent fans decoded events to the circle room. Resolution is by
// contract address; unresolvable logs stay audit-only (no room to send to).
func (p *Poller) broadcastEvent(ctx context.Context, l *arc.Log, ev *DecodedEvent) {
	if p.broadcast == nil || p.circles == nil {
		return
	}
	circleAddr, ok := ev.Fields["circle"]
	if !ok || strings.TrimSpace(circleAddr) == "" {
		return
	}
	cir, err := p.circles.FindByContractID(ctx, circleAddr)
	if err != nil || cir == nil {
		return
	}
	payload := make(map[string]any, len(ev.Fields)+3)
	for k, v := range ev.Fields {
		payload[k] = v
	}
	payload["txHash"] = l.TransactionHash
	payload["blockNumber"] = l.BlockNumber
	p.broadcast.BroadcastChainEvent(ctx, cir.ID.String(), "chain."+toSnake(ev.Schema.Name), payload)
}

func toSnake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func (p *Poller) deadletter(ctx context.Context, l *arc.Log, stage, msg string, fields map[string]string) {
	li := l.LogIndex
	d := Deadletter{
		TxHash: l.TransactionHash, LogIndex: &li,
		BlockNumber: l.BlockNumber, Stage: stage, Err: msg,
	}
	if fields != nil {
		d.Payload = fields
	}
	if err := p.repo.InsertDeadletter(ctx, d); err != nil {
		log.Warn().Err(err).Str("stage", stage).Msg("indexer: deadletter insert failed")
		return
	}
	metrics.IndexerDeadletterTotal.WithLabelValues(stage).Inc()
}
