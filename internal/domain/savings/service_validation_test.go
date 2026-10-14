package savings_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/deodes/backend/internal/domain/savings"
	savingsMocks "github.com/deodes/backend/internal/domain/savings/mocks"
	"github.com/deodes/backend/pkg/apperrors"
)

func futureDate() *string {
	s := time.Now().Add(30 * 24 * time.Hour).Format("2006-01-02")
	return &s
}

func pastDate() *string {
	s := time.Now().Add(-24 * time.Hour).Format("2006-01-02")
	return &s
}

func TestSavingsService_CreateGoal_BoundsValidation(t *testing.T) {
	cases := []struct {
		name string
		req  savings.CreateGoalRequest
	}{
		{"short name", savings.CreateGoalRequest{Name: "AB", TargetAmount: 10}},
		{"long name", savings.CreateGoalRequest{Name: strings.Repeat("a", 101), TargetAmount: 10}},
		{"target over max", savings.CreateGoalRequest{Name: "Valid name", TargetAmount: 1_000_001}},
		{"long note", savings.CreateGoalRequest{Name: "Valid name", TargetAmount: 10, Description: strings.Repeat("a", 281)}},
		{"past deadline", savings.CreateGoalRequest{Name: "Valid name", TargetAmount: 10, TargetDate: pastDate()}},
		{"today deadline", savings.CreateGoalRequest{Name: "Valid name", TargetAmount: 10, TargetDate: func() *string {
			s := time.Now().Format("2006-01-02")
			return &s
		}()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := new(savingsMocks.Repository)
			svc := savings.NewService(repo)

			goal, err := svc.CreateGoal(ctx(), "user-1", tc.req)

			assert.Error(t, err)
			assert.ErrorIs(t, err, apperrors.ErrInvalidInput)
			assert.Nil(t, goal)
			repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
		})
	}
}

func TestSavingsService_CreateGoal_AcceptsBounds(t *testing.T) {
	repo := new(savingsMocks.Repository)
	svc := savings.NewService(repo)

	repo.On("Create", mock.Anything, mock.AnythingOfType("*savings.SavingsGoal")).Return(nil).Run(func(args mock.Arguments) {
		g := args.Get(1).(*savings.SavingsGoal)
		g.ID = "goal-1"
	})

	goal, err := svc.CreateGoal(ctx(), "user-1", savings.CreateGoalRequest{
		Name:         "ABC",
		Description:  strings.Repeat("a", 280),
		TargetAmount: 1_000_000,
		TargetDate:   futureDate(),
	})

	assert.NoError(t, err)
	assert.NotNil(t, goal)
	repo.AssertExpectations(t)
}

func TestSavingsService_UpdateGoal_BoundsValidation(t *testing.T) {
	shortName := "AB"
	longName := strings.Repeat("a", 101)
	overMax := 1_000_000.01
	longDesc := strings.Repeat("a", 281)
	past := pastDate()
	cases := []struct {
		name string
		req  savings.UpdateGoalRequest
	}{
		{"short name", savings.UpdateGoalRequest{Name: &shortName}},
		{"long name", savings.UpdateGoalRequest{Name: &longName}},
		{"target over max", savings.UpdateGoalRequest{TargetAmount: &overMax}},
		{"long note", savings.UpdateGoalRequest{Description: &longDesc}},
		{"past deadline", savings.UpdateGoalRequest{TargetDate: past}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := new(savingsMocks.Repository)
			svc := savings.NewService(repo)
			repo.On("FindByID", mock.Anything, "goal-1").Return(newGoal("goal-1", "user-1"), nil)

			goal, err := svc.UpdateGoal(ctx(), "user-1", "goal-1", tc.req)

			assert.Error(t, err)
			assert.ErrorIs(t, err, apperrors.ErrInvalidInput)
			assert.Nil(t, goal)
			repo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
		})
	}
}
