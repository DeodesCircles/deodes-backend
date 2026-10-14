package payout

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	FindByID(ctx context.Context, id uuid.UUID) (*Payout, error)
	// FindByTxnHash returns the existing payout for a given on-chain
	// transaction hash. Used for idempotent replay detection.
	FindByTxnHash(ctx context.Context, txnHash string) (*Payout, error)
	// FindByCircleAndRound returns the payout recorded for a circle round, if
	// any. A ROSCA round distributes exactly once, so this is the authoritative
	// double-payout guard (unlike a bounded ListByCircle scan, it cannot miss
	// rows past the page limit).
	FindByCircleAndRound(ctx context.Context, circleID uuid.UUID, round int) (*Payout, error)
	Create(ctx context.Context, p *Payout) error
	UpdateVerificationStatus(ctx context.Context, id uuid.UUID, verifiedOnchain bool, status VerificationStatus) error
	ListByUser(ctx context.Context, userID uuid.UUID, page, limit int) ([]Payout, int, error)
	ListByCircle(ctx context.Context, circleID uuid.UUID, page, limit int) ([]Payout, int, error)
}
