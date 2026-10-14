package payout_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/deodes/backend/internal/domain/payout"
	payoutMocks "github.com/deodes/backend/internal/domain/payout/mocks"
	"github.com/deodes/backend/pkg/apperrors"
)

func TestPayoutService_Record_Success(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()

	input := payout.RecordInput{
		CircleID:    uuid.New().String(),
		RecipientID: uuid.New().String(),
		RoundNumber: 1,
		Amount:      500.0,
		FeeAmount:   5.0,
		TxnHash:     "txn-payout-123",
		PayoutType:  payout.PayoutTypeFixed,
	}

	repo.On("FindByTxnHash", ctx, "txn-payout-123").Return(nil, apperrors.ErrNotFound)
	repo.On("FindByCircleAndRound", ctx, mock.AnythingOfType("uuid.UUID"), 1).Return(nil, apperrors.ErrNotFound)

	repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
		return p.Amount == 500.0 &&
			p.PayoutType == payout.PayoutTypeFixed &&
			p.VerifiedOnchain == false &&
			p.VerificationStatus == payout.VerificationStatusPending
	})).Return(nil)

	p, err := svc.Record(ctx, input)

	assert.NoError(t, err)
	assert.NotNil(t, p)
	assert.False(t, p.VerifiedOnchain)
	assert.Equal(t, payout.VerificationStatusPending, p.VerificationStatus)
	assert.Equal(t, 500.0, p.Amount)
	repo.AssertExpectations(t)
}

func TestPayoutService_Record_VerifiedOnchain(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()

	verifiedTrue := true
	customStatus := payout.VerificationStatusVerified

	input := payout.RecordInput{
		CircleID:           uuid.New().String(),
		RecipientID:        uuid.New().String(),
		RoundNumber:        1,
		Amount:             500.0,
		FeeAmount:          5.0,
		TxnHash:            "txn-payout-456",
		PayoutType:         payout.PayoutTypeAuction,
		VerifiedOnchain:    &verifiedTrue,
		VerificationStatus: &customStatus,
	}

	repo.On("FindByTxnHash", ctx, "txn-payout-456").Return(nil, apperrors.ErrNotFound)
	repo.On("FindByCircleAndRound", ctx, mock.AnythingOfType("uuid.UUID"), 1).Return(nil, apperrors.ErrNotFound)

	repo.On("Create", ctx, mock.MatchedBy(func(p *payout.Payout) bool {
		return p.VerifiedOnchain == true && p.VerificationStatus == payout.VerificationStatusVerified
	})).Return(nil)

	p, err := svc.Record(ctx, input)

	assert.NoError(t, err)
	assert.NotNil(t, p)
	assert.True(t, p.VerifiedOnchain)
	assert.Equal(t, payout.VerificationStatusVerified, p.VerificationStatus)
	repo.AssertExpectations(t)
}

func TestPayoutService_UpdateVerification(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()
	payoutID := uuid.New()

	repo.On("UpdateVerificationStatus", ctx, payoutID, true, payout.VerificationStatusVerified).Return(nil)

	err := svc.UpdateVerification(ctx, payoutID.String(), true, payout.VerificationStatusVerified)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestPayoutService_GetUserHistory(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()
	userID := uuid.New()

	payouts := []payout.Payout{
		{ID: uuid.New(), Amount: 500.0, VerifiedOnchain: true, VerificationStatus: payout.VerificationStatusVerified},
	}
	repo.On("ListByUser", ctx, userID, 1, 10).Return(payouts, 1, nil)

	res, total, err := svc.GetUserHistory(ctx, userID.String(), 1, 10)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, 1, total)
	repo.AssertExpectations(t)
}

func TestPayoutService_GetCircleHistory(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()
	circleID := uuid.New()

	payouts := []payout.Payout{
		{ID: uuid.New(), Amount: 1000.0, VerifiedOnchain: false, VerificationStatus: payout.VerificationStatusUnverified},
	}
	repo.On("ListByCircle", ctx, circleID, 1, 10).Return(payouts, 1, nil)

	res, total, err := svc.GetCircleHistory(ctx, circleID.String(), 1, 10)
	assert.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, 1, total)
	repo.AssertExpectations(t)
}

func TestPayoutService_Record_RejectsSecondPayoutForSameRound(t *testing.T) {
	repo := new(payoutMocks.Repository)
	svc := payout.NewService(repo, nil, nil)
	ctx := context.Background()

	circleID := uuid.New()
	firstRecipient := uuid.New()
	existing := &payout.Payout{
		ID: uuid.New(), CircleID: circleID, RecipientID: firstRecipient,
		RoundNumber: 1, Amount: 500.0,
	}

	// A different recipient attempts a second distribution for round 1 with a
	// fresh on-chain hash: the round — not the txn — is already paid.
	input := payout.RecordInput{
		CircleID:    circleID.String(),
		RecipientID: uuid.New().String(),
		RoundNumber: 1,
		Amount:      500.0,
		FeeAmount:   2.5,
		TxnHash:     "second-txn-same-round",
		PayoutType:  payout.PayoutTypeFixed,
	}

	repo.On("FindByTxnHash", ctx, "second-txn-same-round").Return(nil, apperrors.ErrNotFound)
	repo.On("FindByCircleAndRound", ctx, circleID, 1).Return(existing, nil)

	got, err := svc.Record(ctx, input)

	assert.ErrorIs(t, err, payout.ErrRoundAlreadyPaid)
	assert.Equal(t, existing.ID, got.ID, "must return the already-recorded round payout")
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
}

func TestPayoutService_Record_RejectsInvalidAmounts(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name  string
		input payout.RecordInput
	}{
		{"zero round", payout.RecordInput{CircleID: uuid.New().String(), RecipientID: uuid.New().String(), RoundNumber: 0, Amount: 100, TxnHash: "h", PayoutType: payout.PayoutTypeFixed}},
		{"zero amount", payout.RecordInput{CircleID: uuid.New().String(), RecipientID: uuid.New().String(), RoundNumber: 1, Amount: 0, TxnHash: "h", PayoutType: payout.PayoutTypeFixed}},
		{"negative amount", payout.RecordInput{CircleID: uuid.New().String(), RecipientID: uuid.New().String(), RoundNumber: 1, Amount: -5, TxnHash: "h", PayoutType: payout.PayoutTypeFixed}},
		{"negative fee", payout.RecordInput{CircleID: uuid.New().String(), RecipientID: uuid.New().String(), RoundNumber: 1, Amount: 100, FeeAmount: -1, TxnHash: "h", PayoutType: payout.PayoutTypeFixed}},
		{"fee exceeds amount", payout.RecordInput{CircleID: uuid.New().String(), RecipientID: uuid.New().String(), RoundNumber: 1, Amount: 100, FeeAmount: 101, TxnHash: "h", PayoutType: payout.PayoutTypeFixed}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := new(payoutMocks.Repository)
			svc := payout.NewService(repo, nil, nil)
			got, err := svc.Record(ctx, tc.input)
			assert.Error(t, err)
			assert.Nil(t, got)
			repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
		})
	}
}

func TestPayoutService_GetRoundPayout(t *testing.T) {
	ctx := context.Background()
	circleID := uuid.New()

	t.Run("returns existing round payout", func(t *testing.T) {
		repo := new(payoutMocks.Repository)
		svc := payout.NewService(repo, nil, nil)
		existing := &payout.Payout{ID: uuid.New(), CircleID: circleID, RoundNumber: 2}
		repo.On("FindByCircleAndRound", ctx, circleID, 2).Return(existing, nil)

		got, err := svc.GetRoundPayout(ctx, circleID.String(), 2)
		assert.NoError(t, err)
		assert.Equal(t, existing.ID, got.ID)
		repo.AssertExpectations(t)
	})

	t.Run("returns nil when round is unpaid", func(t *testing.T) {
		repo := new(payoutMocks.Repository)
		svc := payout.NewService(repo, nil, nil)
		repo.On("FindByCircleAndRound", ctx, circleID, 3).Return(nil, apperrors.ErrNotFound)

		got, err := svc.GetRoundPayout(ctx, circleID.String(), 3)
		assert.NoError(t, err)
		assert.Nil(t, got)
		repo.AssertExpectations(t)
	})

	t.Run("rejects invalid circle id", func(t *testing.T) {
		repo := new(payoutMocks.Repository)
		svc := payout.NewService(repo, nil, nil)
		got, err := svc.GetRoundPayout(ctx, "not-a-uuid", 1)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
}
