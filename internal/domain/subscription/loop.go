package subscription

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	deodesredis "github.com/deodes/backend/pkg/redis"
)

// DefaultKeeperInterval is how often the loop looks for due debits.
// Debits are exact to next_debit_at, not to the tick: a 15-minute loop
// debits a weekly round within 15 minutes of its due time.
const DefaultKeeperInterval = 15 * time.Minute

// DefaultReminderWindow looks this far ahead for T-24h style reminders.
const DefaultReminderWindow = 24 * time.Hour

// DefaultKeeperLockTTL bounds a keeper pass; a crashed holder stops
// blocking after the TTL so another replica takes over.
const DefaultKeeperLockTTL = 10 * time.Minute

const keeperLockKey = "locks:subscription-keeper"

// Loop runs Keeper.RunOnce on an interval with a Redis single-flight lock,
// so exactly one replica debits even with several API servers up. It mirrors
// the mobile-money reconciler's loop shape.
type Loop struct {
	keeper   *Keeper
	interval time.Duration
	jitter   time.Duration
	lock     *deodesredis.Lock
	batch    int

	mu       sync.Mutex
	started  bool
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

type LoopConfig struct {
	Keeper   *Keeper
	Redis    *redis.Client
	Interval time.Duration
	Jitter   time.Duration
	// Batch caps debits per pass so one slow RPC cannot stall the loop.
	Batch int
}

func NewLoop(cfg LoopConfig) *Loop {
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultKeeperInterval
	}
	batch := cfg.Batch
	if batch <= 0 {
		batch = 100
	}
	var lock *deodesredis.Lock
	if cfg.Redis != nil {
		lock = deodesredis.NewLock(cfg.Redis, keeperLockKey, DefaultKeeperLockTTL)
	}
	return &Loop{
		keeper:   cfg.Keeper,
		interval: interval,
		jitter:   cfg.Jitter,
		lock:     lock,
		batch:    batch,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start launches the loop in the background. A nil keeper is a no-op so
// deployments without an escrow address simply don't debit.
func (l *Loop) Start(ctx context.Context) {
	l.mu.Lock()
	if l.started || l.keeper == nil {
		l.mu.Unlock()
		return
	}
	l.started = true
	l.mu.Unlock()
	go l.run(ctx)
}

// Stop terminates the loop after any in-flight pass finishes.
func (l *Loop) Stop() {
	l.stopOnce.Do(func() { close(l.stopCh) })
	l.mu.Lock()
	started := l.started
	l.mu.Unlock()
	if !started {
		return
	}
	select {
	case <-l.doneCh:
	case <-time.After(l.interval):
		log.Warn().Msg("subscription keeper did not stop within one interval")
	}
}

func (l *Loop) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-l.stopCh:
		return false
	case <-time.After(d):
		return true
	}
}

func (l *Loop) run(ctx context.Context) {
	defer close(l.doneCh)
	log.Info().Dur("interval", l.interval).Msg("subscription keeper started")
	for {
		if !l.sleep(ctx, l.jitter) {
			return
		}
		l.pass(ctx)
		if !l.sleep(ctx, l.interval) {
			return
		}
	}
}

func (l *Loop) pass(ctx context.Context) {
	if l.lock == nil {
		l.unguardedPass(ctx)
		return
	}
	held, err := l.lock.With(ctx, func(ctx context.Context) error {
		l.unguardedPass(ctx)
		return nil
	})
	if err != nil {
		log.Error().Err(err).Msg("subscription keeper pass failed")
		return
	}
	if !held {
		log.Debug().Msg("subscription keeper skipped: another replica holds the lock")
	}
}

// unguardedPass sends T-24h reminders first (cheap query), then debits.
func (l *Loop) unguardedPass(ctx context.Context) {
	if n, err := l.keeper.RemindUpcoming(ctx, DefaultReminderWindow); err != nil {
		log.Error().Err(err).Msg("subscription keeper reminders failed")
	} else if n > 0 {
		log.Info().Int("reminded", n).Msg("subscription keeper sent debit reminders")
	}
	sum := l.keeper.RunOnce(ctx, l.batch)
	logPass(sum)
}

func logPass(sum RunSummary) {
	if sum.Due == 0 {
		log.Debug().Msg("subscription keeper pass: nothing due")
		return
	}
	log.Info().
		Int("due", sum.Due).
		Int("debited", sum.Debited).
		Int("skipped", sum.Skipped).
		Int("failed", sum.Failed).
		Msg("subscription keeper pass completed")
}
