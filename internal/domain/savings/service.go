package savings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/deodes/backend/pkg/apperrors"
)

type Service interface {
	CreateGoal(ctx context.Context, userID string, req CreateGoalRequest) (*SavingsGoal, error)
	GetGoal(ctx context.Context, userID, goalID string) (*SavingsGoal, error)
	ListGoals(ctx context.Context, userID string) ([]SavingsGoal, error)
	ListActiveGoals(ctx context.Context, userID string) ([]SavingsGoal, error)
	UpdateGoal(ctx context.Context, userID, goalID string, req UpdateGoalRequest) (*SavingsGoal, error)
	DeleteGoal(ctx context.Context, userID, goalID string) error
	CompleteGoal(ctx context.Context, userID, goalID string) (*SavingsGoal, error)
	GetSummary(ctx context.Context, userID string) (*GoalSummary, error)
	GetUpcomingObligations(ctx context.Context, userID string) ([]SavingsGoal, error)
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func invalidInput(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, apperrors.ErrInvalidInput)...)
}

func notFound() error {
	return fmt.Errorf("savings goal not found: %w", apperrors.ErrNotFound)
}

// mapLookupError converts a repository lookup failure into a service error.
// sql.ErrNoRows (unknown id) and ownership mismatches both surface as
// ErrNotFound so callers cannot probe for other users' goal ids; anything
// else is an internal failure the handler must report as 500.
func mapLookupError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return notFound()
	}
	return fmt.Errorf("finding goal: %w", err)
}

func (s *service) CreateGoal(ctx context.Context, userID string, req CreateGoalRequest) (*SavingsGoal, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, invalidInput("goal name is required")
	}
	if req.TargetAmount <= 0 {
		return nil, invalidInput("target amount must be greater than zero")
	}
	if _, _, err := ParseTargetDate(req.TargetDate); err != nil {
		return nil, invalidInput("target date must be a valid YYYY-MM-DD date")
	}
	g := &SavingsGoal{
		UserID:        userID,
		Name:          name,
		Description:   strings.TrimSpace(req.Description),
		TargetAmount:  req.TargetAmount,
		CurrentAmount: 0,
		TargetDate:    req.TargetDate,
		CircleID:      req.CircleID,
		AutoReserve:   req.AutoReserve,
		Status:        "active",
	}
	if err := s.repo.Create(ctx, g); err != nil {
		return nil, fmt.Errorf("creating goal: %w", err)
	}
	return g, nil
}

func (s *service) GetGoal(ctx context.Context, userID, goalID string) (*SavingsGoal, error) {
	g, err := s.repo.FindByID(ctx, goalID)
	if err != nil {
		return nil, mapLookupError(err)
	}
	if g.UserID != userID {
		return nil, notFound()
	}
	return g, nil
}

func (s *service) ListGoals(ctx context.Context, userID string) ([]SavingsGoal, error) {
	return s.repo.FindByUserID(ctx, userID)
}

func (s *service) ListActiveGoals(ctx context.Context, userID string) ([]SavingsGoal, error) {
	return s.repo.FindActiveByUserID(ctx, userID)
}

func (s *service) UpdateGoal(ctx context.Context, userID, goalID string, req UpdateGoalRequest) (*SavingsGoal, error) {
	g, err := s.repo.FindByID(ctx, goalID)
	if err != nil {
		return nil, mapLookupError(err)
	}
	if g.UserID != userID {
		return nil, notFound()
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, invalidInput("goal name must not be empty")
		}
		g.Name = name
	}
	if req.Description != nil {
		g.Description = strings.TrimSpace(*req.Description)
	}
	if req.TargetAmount != nil {
		if *req.TargetAmount <= 0 {
			return nil, invalidInput("target amount must be greater than zero")
		}
		g.TargetAmount = *req.TargetAmount
	}
	if req.CurrentAmount != nil {
		if *req.CurrentAmount < 0 {
			return nil, invalidInput("current amount must not be negative")
		}
		g.CurrentAmount = *req.CurrentAmount
	}
	if req.TargetDate != nil {
		if _, _, err := ParseTargetDate(req.TargetDate); err != nil {
			return nil, invalidInput("target date must be a valid YYYY-MM-DD date")
		}
		g.TargetDate = req.TargetDate
	}
	if req.CircleID != nil {
		g.CircleID = req.CircleID
	}
	if req.AutoReserve != nil {
		g.AutoReserve = *req.AutoReserve
	}
	if req.Status != nil {
		if !IsValidStatus(*req.Status) {
			return nil, invalidInput("status must be one of active, completed, cancelled")
		}
		g.Status = *req.Status
	}

	if err := s.repo.Update(ctx, g); err != nil {
		return nil, fmt.Errorf("updating goal: %w", err)
	}
	return g, nil
}

func (s *service) DeleteGoal(ctx context.Context, userID, goalID string) error {
	g, err := s.repo.FindByID(ctx, goalID)
	if err != nil {
		return mapLookupError(err)
	}
	if g.UserID != userID {
		return notFound()
	}
	return s.repo.Delete(ctx, goalID)
}

func (s *service) CompleteGoal(ctx context.Context, userID, goalID string) (*SavingsGoal, error) {
	g, err := s.repo.FindByID(ctx, goalID)
	if err != nil {
		return nil, mapLookupError(err)
	}
	if g.UserID != userID {
		return nil, notFound()
	}
	if g.Status == "completed" {
		return g, nil
	}
	g.Status = "completed"
	g.CurrentAmount = g.TargetAmount
	if err := s.repo.Update(ctx, g); err != nil {
		return nil, fmt.Errorf("completing goal: %w", err)
	}
	return g, nil
}

func (s *service) GetSummary(ctx context.Context, userID string) (*GoalSummary, error) {
	return s.repo.GetSummary(ctx, userID)
}

func (s *service) GetUpcomingObligations(ctx context.Context, userID string) ([]SavingsGoal, error) {
	return s.repo.GetUpcomingObligations(ctx, userID, 5)
}
