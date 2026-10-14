package handler

import (
	"errors"

	"github.com/deodes/backend/internal/api/middleware"
	"github.com/deodes/backend/internal/domain/savings"
	"github.com/deodes/backend/pkg/apperrors"
	"github.com/deodes/backend/pkg/response"
	"github.com/gin-gonic/gin"
)

type SavingsGoalHandler struct {
	svc savings.Service
}

func NewSavingsGoalHandler(svc savings.Service) *SavingsGoalHandler {
	return &SavingsGoalHandler{svc: svc}
}

// requireUserID guards against an empty subject when the handler is mounted
// outside the authenticated route group. The savings routes are registered
// under `authenticated`, so this only fires on misconfiguration — but without
// it a goal would be created with a blank user_id.
func requireUserID(c *gin.Context) (string, bool) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "authentication required")
		return "", false
	}
	return userID, true
}

// writeSavingsError maps service failures to status codes: caller mistakes
// (validation) are 400, unknown ids and other users' goals are both 404 so
// ownership cannot be probed, and only genuine infrastructure failures are 500.
func writeSavingsError(c *gin.Context, err error, action string) {
	switch {
	case errors.Is(err, apperrors.ErrInvalidInput):
		response.ValidationError(c, err.Error(), nil)
	case errors.Is(err, apperrors.ErrNotFound):
		response.NotFound(c, "savings goal not found")
	default:
		response.InternalError(c, action)
	}
}

func (h *SavingsGoalHandler) Create(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req savings.CreateGoalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	goal, err := h.svc.CreateGoal(c.Request.Context(), userID, req)
	if err != nil {
		writeSavingsError(c, err, "failed to create savings goal")
		return
	}
	response.Created(c, gin.H{"goal": goal})
}

func (h *SavingsGoalHandler) List(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	goals, err := h.svc.ListGoals(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to list savings goals")
		return
	}
	response.OK(c, gin.H{"goals": goals})
}

func (h *SavingsGoalHandler) ListActive(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	goals, err := h.svc.ListActiveGoals(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to list active savings goals")
		return
	}
	response.OK(c, gin.H{"goals": goals})
}

func (h *SavingsGoalHandler) Get(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	goalID := c.Param("id")
	goal, err := h.svc.GetGoal(c.Request.Context(), userID, goalID)
	if err != nil {
		writeSavingsError(c, err, "failed to get savings goal")
		return
	}
	response.OK(c, gin.H{"goal": goal})
}

func (h *SavingsGoalHandler) Update(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	goalID := c.Param("id")
	var req savings.UpdateGoalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	goal, err := h.svc.UpdateGoal(c.Request.Context(), userID, goalID, req)
	if err != nil {
		writeSavingsError(c, err, "failed to update savings goal")
		return
	}
	response.OK(c, gin.H{"goal": goal})
}

func (h *SavingsGoalHandler) Delete(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	goalID := c.Param("id")
	if err := h.svc.DeleteGoal(c.Request.Context(), userID, goalID); err != nil {
		writeSavingsError(c, err, "failed to delete savings goal")
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *SavingsGoalHandler) Complete(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	goalID := c.Param("id")
	goal, err := h.svc.CompleteGoal(c.Request.Context(), userID, goalID)
	if err != nil {
		writeSavingsError(c, err, "failed to complete savings goal")
		return
	}
	response.OK(c, gin.H{"goal": goal})
}

func (h *SavingsGoalHandler) Summary(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	summary, err := h.svc.GetSummary(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to get savings summary")
		return
	}
	response.OK(c, gin.H{"summary": summary})
}

func (h *SavingsGoalHandler) UpcomingObligations(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	goals, err := h.svc.GetUpcomingObligations(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to get upcoming obligations")
		return
	}
	response.OK(c, gin.H{"goals": goals})
}
