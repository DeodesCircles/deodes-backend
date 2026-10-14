package payout_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/domain/circle"
	"github.com/deodes/backend/internal/domain/payout"
	payoutMocks "github.com/deodes/backend/internal/domain/payout/mocks"
	"github.com/deodes/backend/pkg/apperrors"
)

const (
	testConfiguredCircle = "0x70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4"
	testOtherCircle      = "0x51b45Ba8570551e0D1fe818227d8bAa8b556694D"
)

// contractCircleService returns a circle carrying the given contract_id.
type contractCircleService struct {
	circle.Service
	circ *circle.Circle
}

func (s contractCircleService) Get(_ context.Context, _ string) (*circle.Circle, error) {
	return s.circ, nil
}

func (s contractCircleService) IsMember(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

func contractBoundCircle(contractID string) *circle.Circle {
	c := rotationTestCircle()
	c.ContractID = sql.NullString{String: contractID, Valid: contractID != ""}
	return c
}

func TestPayoutService_SetArcCircleAddress_Validation(t *testing.T) {
	svc := payout.NewService(new(payoutMocks.Repository), nil, nil)
	cwc, ok := svc.(payout.ServiceWithCircle)
	require.True(t, ok, "service must expose ServiceWithCircle")

	require.Error(t, cwc.SetArcCircleAddress("not-an-address"))
	require.Error(t, cwc.SetArcCircleAddress("0x1234"))
	require.NoError(t, cwc.SetArcCircleAddress(""))
	require.NoError(t, cwc.SetArcCircleAddress(testConfiguredCircle))
}

func TestPayoutService_Record_RejectsForeignCircleContract(t *testing.T) {
	repo := new(payoutMocks.Repository)
	ctx := context.Background()
	circ := contractBoundCircle(testOtherCircle)
	svc := payout.NewService(repo, nil, contractCircleService{circ: circ})
	require.NoError(t, svc.(payout.ServiceWithCircle).SetArcCircleAddress(testConfiguredCircle))

	repo.On("FindByTxnHash", ctx, "foreign-txn").Return(nil, apperrors.ErrNotFound)

	got, err := svc.Record(ctx, payout.RecordInput{
		CircleID:    circ.ID.String(),
		RecipientID: uuid.New().String(),
		RoundNumber: 1,
		Amount:      400.0,
		TxnHash:     "foreign-txn",
		PayoutType:  payout.PayoutTypeFixed,
	})

	require.ErrorIs(t, err, apperrors.ErrCircleContractMismatch)
	assert.Nil(t, got)
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestPayoutService_Record_AcceptsMatchingCircleContract(t *testing.T) {
	repo := new(payoutMocks.Repository)
	ctx := context.Background()
	circ := contractBoundCircle(testConfiguredCircle)
	svc := payout.NewService(repo, nil, contractCircleService{circ: circ})
	require.NoError(t, svc.(payout.ServiceWithCircle).SetArcCircleAddress(testConfiguredCircle))

	repo.On("FindByTxnHash", ctx, "matching-txn").Return(nil, apperrors.ErrNotFound)
	repo.On("FindByCircleAndRound", ctx, mock.AnythingOfType("uuid.UUID"), 1).Return(nil, apperrors.ErrNotFound)
	repo.On("Create", ctx, mock.AnythingOfType("*payout.Payout")).Return(nil)

	got, err := svc.Record(ctx, payout.RecordInput{
		CircleID:    circ.ID.String(),
		RecipientID: uuid.New().String(),
		RoundNumber: 1,
		Amount:      400.0,
		TxnHash:     "matching-txn",
		PayoutType:  payout.PayoutTypeFixed,
	})

	require.NoError(t, err)
	require.NotNil(t, got)
	repo.AssertExpectations(t)
}
