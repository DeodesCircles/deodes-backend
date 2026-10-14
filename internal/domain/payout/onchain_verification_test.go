package payout_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/domain/payout"
	payoutMocks "github.com/deodes/backend/internal/domain/payout/mocks"
	"github.com/deodes/backend/pkg/apperrors"
)

// Post-purge semantics (no chain client): a txnHash without explicit
// overrides records as Pending for the Arc indexer (2.6); explicit caller
// states always win; replays stay idempotent.

// --------------------------------------------------------------------------
// Default: txnHash records as pending
// --------------------------------------------------------------------------

func TestPayoutService_Record_PendingByDefault(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()

	input := payout.RecordInput{
		CircleID:    uuid.New().String(),
		RecipientID: uuid.New().String(),
		RoundNumber: 1,
		Amount:      500.0,
		FeeAmount:   5.0,
		TxnHash:     "0xabc123",
		PayoutType:  payout.PayoutTypeFixed,
	}

	repo.On("FindByTxnHash", ctx, "0xabc123").Return(nil, apperrors.ErrNotFound)
	repo.On("FindByCircleAndRound", ctx, mock.AnythingOfType("uuid.UUID"), 1).Return(nil, apperrors.ErrNotFound)
	repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
		return p.VerifiedOnchain == false &&
			p.VerificationStatus == payout.VerificationStatusPending &&
			p.TxnHash.String == "0xabc123"
	})).Return(nil)

	p, err := svc.Record(ctx, input)

	require.NoError(t, err)
	require.NotNil(t, p)
	assert.False(t, p.VerifiedOnchain)
	assert.Equal(t, payout.VerificationStatusPending, p.VerificationStatus)

	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Explicit caller overrides win
// --------------------------------------------------------------------------

func TestPayoutService_Record_ExplicitOverrideHonored(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()

	verified := true
	status := payout.VerificationStatusVerified
	input := payout.RecordInput{
		CircleID:           uuid.New().String(),
		RecipientID:        uuid.New().String(),
		RoundNumber:        1,
		Amount:             500.0,
		TxnHash:            "0xabc123",
		PayoutType:         payout.PayoutTypeFixed,
		VerifiedOnchain:    &verified,
		VerificationStatus: &status,
	}

	repo.On("FindByTxnHash", ctx, "0xabc123").Return(nil, apperrors.ErrNotFound)
	repo.On("FindByCircleAndRound", ctx, mock.AnythingOfType("uuid.UUID"), 1).Return(nil, apperrors.ErrNotFound)
	repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
		return p.VerifiedOnchain == true &&
			p.VerificationStatus == payout.VerificationStatusVerified
	})).Return(nil)

	p, err := svc.Record(ctx, input)

	require.NoError(t, err)
	require.NotNil(t, p)
	assert.True(t, p.VerifiedOnchain)
	assert.Equal(t, payout.VerificationStatusVerified, p.VerificationStatus)

	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Replay protection — same payout txnHash submitted twice
// --------------------------------------------------------------------------

func TestPayoutService_Record_IdempotentOnReplay(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()

	existingPayout := &payout.Payout{
		ID:                 uuid.New(),
		Amount:             500.0,
		VerifiedOnchain:    true,
		VerificationStatus: payout.VerificationStatusVerified,
	}

	input := payout.RecordInput{
		CircleID:    uuid.New().String(),
		RecipientID: uuid.New().String(),
		RoundNumber: 1,
		Amount:      500.0,
		TxnHash:     "replayed-payout-txn",
		PayoutType:  payout.PayoutTypeFixed,
	}

	// FindByTxnHash returns the existing payout — replay detected.
	repo.On("FindByTxnHash", ctx, "replayed-payout-txn").Return(existingPayout, nil)

	p, err := svc.Record(ctx, input)

	require.NoError(t, err, "replay must succeed (idempotent)")
	require.NotNil(t, p)
	assert.Equal(t, existingPayout.ID, p.ID, "must return the original payout record")

	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
}
