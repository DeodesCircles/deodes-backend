package contribution_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/domain/contribution"
	contribMocks "github.com/deodes/backend/internal/domain/contribution/mocks"
	"github.com/deodes/backend/pkg/apperrors"
)

// Post-purge semantics (no chain client): a txnHash without explicit
// overrides records as Pending for the Arc indexer (2.6); explicit caller
// states always win; replays stay idempotent.

// --------------------------------------------------------------------------
// Default: txnHash records as pending
// --------------------------------------------------------------------------

func TestContributionService_Record_PendingByDefault(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil)
	ctx := context.Background()

	input := contribution.RecordInput{
		CircleID:    uuid.New().String(),
		UserID:      uuid.New().String(),
		RoundNumber: 1,
		Amount:      100.0,
		TxnHash:     "0xabc123",
	}

	// No prior record for this hash.
	repo.On("FindByTxnHash", ctx, "0xabc123").Return(nil, apperrors.ErrNotFound)
	// Record is persisted as pending.
	repo.On("Create", ctx, mock.MatchedBy(func(c *contribution.Contribution) bool {
		return c.VerifiedOnchain == false &&
			c.VerificationStatus == contribution.VerificationStatusPending &&
			c.TxnHash.String == "0xabc123"
	})).Return(nil)

	c, err := svc.Record(ctx, input)

	require.NoError(t, err)
	require.NotNil(t, c)
	assert.False(t, c.VerifiedOnchain, "contribution must not be marked verified without indexer confirmation")
	assert.Equal(t, contribution.VerificationStatusPending, c.VerificationStatus)

	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Non-0x hashes record as pending too (never rejected without a verifier)
// --------------------------------------------------------------------------

func TestContributionService_Record_NonArcHashPendingNotRejected(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil)
	ctx := context.Background()

	input := contribution.RecordInput{
		CircleID:    uuid.New().String(),
		UserID:      uuid.New().String(),
		RoundNumber: 1,
		Amount:      500.0,
		TxnHash:     "forged-txn-hash",
	}

	repo.On("FindByTxnHash", ctx, "forged-txn-hash").Return(nil, apperrors.ErrNotFound)
	repo.On("Create", ctx, mock.MatchedBy(func(c *contribution.Contribution) bool {
		return c.VerificationStatus == contribution.VerificationStatusPending &&
			c.VerifiedOnchain == false
	})).Return(nil)

	c, err := svc.Record(ctx, input)

	require.NoError(t, err, "without a verifier nothing may be rejected on-chain grounds")
	require.NotNil(t, c)
	assert.Equal(t, contribution.VerificationStatusPending, c.VerificationStatus)

	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Security: replay protection — same txnHash submitted twice
// --------------------------------------------------------------------------

func TestContributionService_Record_IdempotentOnReplay(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil)
	ctx := context.Background()

	existing := &contribution.Contribution{
		ID:                 uuid.New(),
		Amount:             100.0,
		TxnHash:            sql.NullString{String: "replayed-txn", Valid: true},
		VerifiedOnchain:    true,
		VerificationStatus: contribution.VerificationStatusVerified,
		Status:             contribution.StatusConfirmed,
	}

	input := contribution.RecordInput{
		CircleID:    existing.CircleID.String(),
		UserID:      existing.UserID.String(),
		RoundNumber: 1,
		Amount:      100.0,
		TxnHash:     "replayed-txn",
	}

	// FindByTxnHash returns the existing record — replay detected.
	repo.On("FindByTxnHash", ctx, "replayed-txn").Return(existing, nil)

	c, err := svc.Record(ctx, input)

	require.NoError(t, err, "replay must succeed (idempotent)")
	require.NotNil(t, c)
	assert.Equal(t, existing.ID, c.ID, "must return the existing record")

	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Caller-supplied verification state is used (indexer path)
// --------------------------------------------------------------------------

func TestContributionService_Record_UsesCallerVerificationState(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil)
	ctx := context.Background()

	verified := true
	status := contribution.VerificationStatusVerified

	input := contribution.RecordInput{
		CircleID:           uuid.New().String(),
		UserID:             uuid.New().String(),
		RoundNumber:        1,
		Amount:             200.0,
		TxnHash:            "indexer-confirmed-txn",
		VerifiedOnchain:    &verified,
		VerificationStatus: &status,
	}

	repo.On("FindByTxnHash", ctx, "indexer-confirmed-txn").Return(nil, apperrors.ErrNotFound)
	repo.On("Create", ctx, mock.MatchedBy(func(c *contribution.Contribution) bool {
		return c.VerifiedOnchain == true &&
			c.VerificationStatus == contribution.VerificationStatusVerified
	})).Return(nil)

	c, err := svc.Record(ctx, input)

	require.NoError(t, err)
	assert.True(t, c.VerifiedOnchain)
	assert.Equal(t, contribution.VerificationStatusVerified, c.VerificationStatus)
	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Conflict error on Create (race) is handled gracefully
// --------------------------------------------------------------------------

func TestContributionService_Record_HandlesCreateConflict(t *testing.T) {
	repo := new(contribMocks.Repository)
	svc := contribution.NewService(repo, nil, nil, nil)
	ctx := context.Background()

	existing := &contribution.Contribution{
		ID:     uuid.New(),
		Amount: 100.0,
	}

	input := contribution.RecordInput{
		CircleID:    uuid.New().String(),
		UserID:      uuid.New().String(),
		RoundNumber: 1,
		Amount:      100.0,
		TxnHash:     "race-condition-txn",
	}

	repo.On("FindByTxnHash", ctx, "race-condition-txn").Return(nil, apperrors.ErrNotFound).Once()
	repo.On("Create", ctx, mock.AnythingOfType("*contribution.Contribution")).Return(apperrors.ErrConflict)
	// After the conflict, FindByTxnHash returns the row created by the other request.
	repo.On("FindByTxnHash", ctx, "race-condition-txn").Return(existing, nil).Once()

	c, err := svc.Record(ctx, input)

	require.NoError(t, err)
	assert.Equal(t, existing.ID, c.ID)
	repo.AssertExpectations(t)
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// Compile-time import check to keep sql in scope.
var _ sql.NullString
