package circle_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/domain/circle"
	circleMocks "github.com/deodes/backend/internal/domain/circle/mocks"
)

func enforcementCircle(start time.Time, validStart bool, maxStrikes int) *circle.Circle {
	return &circle.Circle{
		ID:                 uuid.New(),
		Status:             circle.CircleStatusActive,
		ContributionAmount: 100,
		LateFeePercent:     10,
		GracePeriodHours:   48,
		MaxStrikes:         maxStrikes,
		Frequency:          circle.FrequencyDaily,
		StartDate:          sql.NullTime{Time: start, Valid: validStart},
	}
}

func TestProcessMissed_SkipsMembersInsideGrace(t *testing.T) {
	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})
	ctx := context.Background()

	// Round 1 due = start (now); 48h grace still open.
	c := enforcementCircle(time.Now(), true, 3)
	uid := uuid.New()
	repo.On("FindByID", ctx, c.ID).Return(c, nil)
	repo.On("GetMembers", ctx, c.ID).Return([]circle.CircleMember{
		{CircleID: c.ID, UserID: uid, Status: circle.MemberStatusActive},
	}, nil)
	repo.On("GetContributionsByCircleAndRound", ctx, c.ID, 1).Return([]uuid.UUID{}, nil)

	require.NoError(t, svc.ProcessMissedContributions(ctx, c.ID.String(), 1))
	repo.AssertNotCalled(t, "UpdateMemberStrikes", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	repo.AssertNotCalled(t, "CreatePenalty", mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
}

func TestProcessMissed_PersistsStrikesAndRemovesAtMax(t *testing.T) {
	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})
	ctx := context.Background()

	// Round 1 due 10 days ago; grace long closed.
	c := enforcementCircle(time.Now().Add(-240*time.Hour), true, 2)
	uid := uuid.New()
	repo.On("FindByID", ctx, c.ID).Return(c, nil)
	repo.On("GetMembers", ctx, c.ID).Return([]circle.CircleMember{
		{CircleID: c.ID, UserID: uid, Status: circle.MemberStatusActive, Strikes: 1},
	}, nil)
	repo.On("GetContributionsByCircleAndRound", ctx, c.ID, 1).Return([]uuid.UUID{}, nil)
	repo.On("UpdateMemberStrikes", ctx, c.ID, uid, 2).Return(nil)
	repo.On("UpdateMemberStatus", ctx, c.ID, uid, circle.MemberStatusRemoved).Return(nil)
	repo.On("CreatePenalty", ctx, mock.MatchedBy(func(p *circle.Penalty) bool {
		return p.UserID == uid && p.PenaltyType == circle.PenaltyTypeDefault && p.StrikesApplied == 2
	})).Return(nil)

	require.NoError(t, svc.ProcessMissedContributions(ctx, c.ID.String(), 1))
	repo.AssertExpectations(t)
}

func TestProcessMissed_LatePenaltyWithoutRemoval(t *testing.T) {
	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})
	ctx := context.Background()

	c := enforcementCircle(time.Now().Add(-240*time.Hour), true, 3)
	uid := uuid.New()
	repo.On("FindByID", ctx, c.ID).Return(c, nil)
	repo.On("GetMembers", ctx, c.ID).Return([]circle.CircleMember{
		{CircleID: c.ID, UserID: uid, Status: circle.MemberStatusActive, Strikes: 0},
	}, nil)
	repo.On("GetContributionsByCircleAndRound", ctx, c.ID, 1).Return([]uuid.UUID{}, nil)
	repo.On("UpdateMemberStrikes", ctx, c.ID, uid, 1).Return(nil)
	repo.On("CreatePenalty", ctx, mock.MatchedBy(func(p *circle.Penalty) bool {
		return p.UserID == uid && p.PenaltyType == circle.PenaltyTypeLate && p.Amount == 10.0
	})).Return(nil)

	require.NoError(t, svc.ProcessMissedContributions(ctx, c.ID.String(), 1))
	repo.AssertNotCalled(t, "UpdateMemberStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	repo.AssertExpectations(t)
}

func TestExit_RecordsEarlyExitPenalty(t *testing.T) {
	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})
	ctx := context.Background()

	cid := uuid.New()
	uid := uuid.New()
	c := &circle.Circle{
		ID: cid, Status: circle.CircleStatusActive, OrganizerID: uuid.New(),
		ContributionAmount: 100, CollateralPercent: 10, TotalContributions: 0,
		CurrentRound: 2,
	}
	repo.On("FindByID", ctx, cid).Return(c, nil)
	repo.On("FindMemberByCircleAndUser", ctx, cid, uid).Return(&circle.CircleMember{
		CircleID: cid, UserID: uid, Status: circle.MemberStatusActive,
	}, nil)
	// Collateral forfeit = 100 * 10% = 10 (nothing contributed yet).
	repo.On("CreatePenalty", ctx, mock.MatchedBy(func(p *circle.Penalty) bool {
		return p.UserID == uid && p.PenaltyType == circle.PenaltyTypeEarlyExit && p.Amount == 10.0
	})).Return(nil)
	repo.On("UpdateMemberStatus", ctx, cid, uid, circle.MemberStatusExited).Return(nil)

	require.NoError(t, svc.Exit(ctx, cid.String(), uid.String()))
	repo.AssertExpectations(t)
}
