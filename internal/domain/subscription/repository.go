package subscription

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

type Repository interface {
	Create(ctx context.Context, s *Subscription) error
	FindByID(ctx context.Context, id string) (*Subscription, error)
	FindByUserCircle(ctx context.Context, userID, circleID string) (*Subscription, error)
	ListByUser(ctx context.Context, userID string) ([]Subscription, error)
	// ListDue returns up to limit active subscriptions with next_debit_at <= now,
	// ordered oldest-first so the keeper works through backlogs fairly.
	ListDue(ctx context.Context, now time.Time, limit int) ([]Subscription, error)
	UpdateStatus(ctx context.Context, id, status string) error
	// Postpone moves next_debit_at without recording a debit (low balance,
	// failed send, circle not ready). It never touches rounds or totals.
	Postpone(ctx context.Context, id string, nextDebitAt time.Time) error
	// ListRemindable returns active subscriptions due within window that have
	// not been reminded for this debit yet: never reminded, or last reminded
	// before (next_debit_at - window), which means the debit moved since.
	ListRemindable(ctx context.Context, now time.Time, window time.Duration) ([]Subscription, error)
	// MarkReminded records when a T-24h style reminder was sent.
	MarkReminded(ctx context.Context, id string, now time.Time) error
	// RecordDebit atomically advances a subscription after a settled debit:
	// last_round and last_debit_at are set, collected_total grows by amount,
	// and next_debit_at moves to the supplied value. The update only applies
	// when last_round still equals expectedRound, so concurrent keepers can
	// never double-advance the same round — returns true when applied.
	RecordDebit(ctx context.Context, id string, expectedRound, round int, amount string, nextDebitAt time.Time) (bool, error)
}

type pgRepo struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) Repository {
	return &pgRepo{db: db}
}

const subscriptionColumns = `id, user_id, circle_id, amount, cap_total, collected_total, status, next_debit_at, last_debit_at, last_reminded_at, last_round, created_at, updated_at`

func (r *pgRepo) Create(ctx context.Context, s *Subscription) error {
	query := `INSERT INTO subscriptions (user_id, circle_id, amount, cap_total, next_debit_at)
		VALUES (:user_id, :circle_id, :amount, :cap_total, :next_debit_at)
		RETURNING id, collected_total, status, last_debit_at, last_round, created_at, updated_at`
	rows, err := r.db.NamedQueryContext(ctx, query, s)
	if err != nil {
		return fmt.Errorf("creating subscription: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.StructScan(s); err != nil {
			return fmt.Errorf("scanning subscription: %w", err)
		}
		return nil
	}
	return fmt.Errorf("creating subscription: no row returned")
}

func (r *pgRepo) FindByID(ctx context.Context, id string) (*Subscription, error) {
	var s Subscription
	err := r.db.GetContext(ctx, &s,
		`SELECT `+subscriptionColumns+` FROM subscriptions WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("finding subscription by id: %w", err)
	}
	return &s, nil
}

func (r *pgRepo) FindByUserCircle(ctx context.Context, userID, circleID string) (*Subscription, error) {
	var s Subscription
	err := r.db.GetContext(ctx, &s,
		`SELECT `+subscriptionColumns+` FROM subscriptions WHERE user_id = $1 AND circle_id = $2`, userID, circleID)
	if err != nil {
		return nil, fmt.Errorf("finding subscription by user and circle: %w", err)
	}
	return &s, nil
}

func (r *pgRepo) ListByUser(ctx context.Context, userID string) ([]Subscription, error) {
	var out []Subscription
	err := r.db.SelectContext(ctx, &out,
		`SELECT `+subscriptionColumns+` FROM subscriptions WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing subscriptions by user: %w", err)
	}
	if out == nil {
		out = []Subscription{}
	}
	return out, nil
}

func (r *pgRepo) ListDue(ctx context.Context, now time.Time, limit int) ([]Subscription, error) {
	if limit <= 0 {
		limit = 100
	}
	var out []Subscription
	err := r.db.SelectContext(ctx, &out,
		`SELECT `+subscriptionColumns+` FROM subscriptions
		 WHERE status = 'active' AND next_debit_at <= $1
		 ORDER BY next_debit_at ASC LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("listing due subscriptions: %w", err)
	}
	if out == nil {
		out = []Subscription{}
	}
	return out, nil
}

func (r *pgRepo) UpdateStatus(ctx context.Context, id, status string) error {
	switch status {
	case StatusActive, StatusPaused, StatusCancelled, StatusCompleted:
	default:
		return fmt.Errorf("invalid subscription status %q", status)
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE subscriptions SET status = $1, updated_at = NOW() WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("updating subscription status: %w", err)
	}
	return nil
}

func (r *pgRepo) Postpone(ctx context.Context, id string, nextDebitAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE subscriptions SET next_debit_at = $1, updated_at = NOW() WHERE id = $2`, nextDebitAt, id)
	if err != nil {
		return fmt.Errorf("postponing subscription debit: %w", err)
	}
	return nil
}

func (r *pgRepo) ListRemindable(ctx context.Context, now time.Time, window time.Duration) ([]Subscription, error) {
	var out []Subscription
	err := r.db.SelectContext(ctx, &out,
		`SELECT `+subscriptionColumns+` FROM subscriptions
		 WHERE status = 'active'
		   AND next_debit_at > $1
		   AND next_debit_at <= $2
		   AND (last_reminded_at IS NULL OR last_reminded_at <= next_debit_at - ($3 || ' seconds')::INTERVAL)
		 ORDER BY next_debit_at ASC`, now, now.Add(window), int(window.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("listing remindable subscriptions: %w", err)
	}
	if out == nil {
		out = []Subscription{}
	}
	return out, nil
}

func (r *pgRepo) MarkReminded(ctx context.Context, id string, now time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE subscriptions SET last_reminded_at = $1, updated_at = NOW() WHERE id = $2`, now, id)
	if err != nil {
		return fmt.Errorf("marking subscription reminded: %w", err)
	}
	return nil
}

func (r *pgRepo) RecordDebit(ctx context.Context, id string, expectedRound, round int, amount string, nextDebitAt time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE subscriptions
		SET last_round = $1,
		    last_debit_at = NOW(),
		    collected_total = collected_total + $2::NUMERIC,
		    next_debit_at = $3,
		    updated_at = NOW()
		WHERE id = $4 AND last_round = $5`, round, amount, nextDebitAt, id, expectedRound)
	if err != nil {
		return false, fmt.Errorf("recording subscription debit: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking debit update: %w", err)
	}
	return n == 1, nil
}
