package indexer

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/jmoiron/sqlx"
)

// CursorStore persists the poller's per-chain block cursor. The stored
// value is the last finalized PROCESSED block; polling resumes at last+1
// (absent row = fresh backfill from startBlock).
type CursorStore interface {
	// GetNextFrom returns the first block to poll: last+1, or startBlock
	// when no cursor row exists yet.
	GetNextFrom(ctx context.Context, chainID uint64, startBlock uint64) (uint64, error)
	SetLastBlock(ctx context.Context, chainID uint64, block uint64) error
}

// EventStore is the idempotent audit log of decoded events.
type EventStore interface {
	// Insert records a decoded event; a duplicate (tx_hash, log_index)
	// is a no-op returning (false, nil) — replays are safe.
	Insert(ctx context.Context, e PersistedEvent) (inserted bool, err error)
	// DeleteByTxLog removes an event row orphaned by a reorg.
	DeleteByTxLog(ctx context.Context, txHash string, logIndex uint64) error
}

// DeadletterStore parks poison items without stalling the poller.
type DeadletterStore interface {
	InsertDeadletter(ctx context.Context, d Deadletter) error
}

// PersistedEvent is one decoded log row.
type PersistedEvent struct {
	TxHash      string
	LogIndex    uint64
	BlockNumber uint64
	Address     string
	Event       string
	Payload     map[string]string
}

// Deadletter is one parked item.
type Deadletter struct {
	TxHash      string
	LogIndex    *uint64
	BlockNumber uint64
	Stage       string
	Err         string
	Payload     map[string]string
}

// Repository bundles the poller's Postgres state.
type Repository interface {
	CursorStore
	EventStore
	DeadletterStore
}

type pgRepo struct {
	db *sqlx.DB
}

// NewRepository builds the Postgres-backed poller state.
func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

func (r *pgRepo) GetNextFrom(ctx context.Context, chainID uint64, startBlock uint64) (uint64, error) {
	var last sql.NullInt64
	err := r.db.QueryRowxContext(ctx,
		`SELECT last_block FROM arc_cursor WHERE chain_id = $1`, int64(chainID)).Scan(&last)
	if err == sql.ErrNoRows || !last.Valid {
		return startBlock, nil
	}
	if err != nil {
		return 0, err
	}
	if last.Int64 < 0 {
		return startBlock, nil
	}
	next := uint64(last.Int64) + 1
	if next == 0 { // overflow saturate (last == MaxUint64)
		return uint64(last.Int64), nil
	}
	return next, nil
}

func (r *pgRepo) SetLastBlock(ctx context.Context, chainID uint64, block uint64) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO arc_cursor (chain_id, last_block, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (chain_id) DO UPDATE
		 SET last_block = GREATEST(arc_cursor.last_block, EXCLUDED.last_block),
		     updated_at = NOW()`,
		int64(chainID), int64(block))
	return err
}

func (r *pgRepo) Insert(ctx context.Context, e PersistedEvent) (bool, error) {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return false, err
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO arc_events (tx_hash, log_index, block_number, address, event, payload)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (tx_hash, log_index) DO NOTHING`,
		e.TxHash, int64(e.LogIndex), int64(e.BlockNumber), e.Address, e.Event, string(payload))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *pgRepo) DeleteByTxLog(ctx context.Context, txHash string, logIndex uint64) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM arc_events WHERE tx_hash = $1 AND log_index = $2`,
		txHash, int64(logIndex))
	return err
}

func (r *pgRepo) InsertDeadletter(ctx context.Context, d Deadletter) error {
	payload, err := json.Marshal(d.Payload)
	if err != nil {
		return err
	}
	var logIndex sql.NullInt64
	if d.LogIndex != nil {
		logIndex = sql.NullInt64{Int64: int64(*d.LogIndex), Valid: true}
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO arc_deadletter (tx_hash, log_index, block_number, stage, error, payload)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		d.TxHash, logIndex, int64(d.BlockNumber), d.Stage, d.Err, string(payload))
	return err
}
