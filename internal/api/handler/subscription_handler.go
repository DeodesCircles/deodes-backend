package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/deodes/backend/internal/api/middleware"
	"github.com/deodes/backend/internal/domain/circle"
	"github.com/deodes/backend/internal/domain/subscription"
	"github.com/deodes/backend/pkg/money"
	"github.com/deodes/backend/pkg/response"
)

// SubscriptionHandler exposes member consent for keeper auto-debit:
// subscribe (opt in), pause/resume, cancel, and list. Subscriptions are only
// accepted for Auto circles; Flexible circles stay manual.
type SubscriptionHandler struct {
	repo    subscription.Repository
	circles circle.Service
}

func NewSubscriptionHandler(repo subscription.Repository, circles circle.Service) *SubscriptionHandler {
	return &SubscriptionHandler{repo: repo, circles: circles}
}

// @Summary Subscribe to auto-debit
// @Description Opt into keeper-pulled contributions for an Auto circle.
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /circles/{id}/subscription [post]
func (h *SubscriptionHandler) Subscribe(c *gin.Context) {
	circleID := c.Param("id")
	userID := middleware.GetUserID(c)
	var req struct {
		Amount   float64 `json:"amount" binding:"required,gt=0"`
		CapTotal float64 `json:"capTotal" binding:"required,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "amount and capTotal are required and must be positive")
		return
	}
	ctx := c.Request.Context()

	cir, err := h.circles.Get(ctx, circleID)
	if err != nil {
		response.NotFound(c, "circle not found")
		return
	}
	if cir.Status == circle.CircleStatusCompleted || cir.Status == circle.CircleStatusCancelled {
		response.BadRequest(c, "circle is no longer accepting subscriptions")
		return
	}
	if !cir.AutoDebit {
		response.BadRequest(c, "circle is Flexible (manual contributions); auto-debit is only available for Auto circles")
		return
	}
	member, err := h.circles.IsMember(ctx, circleID, userID)
	if err != nil || !member {
		response.ErrorWithCode(c, http.StatusForbidden, "not_member", "join the circle before subscribing to auto-debit")
		return
	}
	// The debit must equal the circle contribution: anything else would
	// record a wrong amount against the round. Compared in Money (exact).
	want, err := money.FromFloat64(cir.ContributionAmount)
	if err != nil {
		response.InternalError(c, "invalid circle contribution amount")
		return
	}
	got, err := money.FromFloat64(req.Amount)
	if err != nil || !got.Equal(want) {
		response.BadRequest(c, "subscription amount must equal the circle contribution amount")
		return
	}
	cap, err := money.FromFloat64(req.CapTotal)
	if err != nil || cap.Cmp(got) < 0 {
		response.BadRequest(c, "capTotal must cover at least one contribution")
		return
	}
	if _, err := h.repo.FindByUserCircle(ctx, userID, circleID); err == nil {
		response.ErrorWithCode(c, http.StatusConflict, "already_subscribed", "a subscription already exists; pause or cancel it first")
		return
	}

	sub := &subscription.Subscription{
		UserID:      userID,
		CircleID:    circleID,
		Amount:      got.String(),
		CapTotal:    cap.String(),
		NextDebitAt: time.Now().UTC(),
	}
	if err := h.repo.Create(ctx, sub); err != nil {
		response.InternalError(c, "failed to create subscription")
		return
	}
	response.Created(c, gin.H{"subscription": sub})
}

// @Summary Cancel auto-debit subscription
// @Description Revoke keeper consent. Past debits stand; future rounds are manual.
// @Tags Subscriptions
// @Security BearerAuth
// @Router /circles/{id}/subscription [delete]
func (h *SubscriptionHandler) Cancel(c *gin.Context) {
	h.setOwnStatus(c, subscription.StatusCancelled, "subscription cancelled")
}

// @Summary Pause auto-debit subscription
// @Tags Subscriptions
// @Security BearerAuth
// @Router /circles/{id}/subscription/pause [post]
func (h *SubscriptionHandler) Pause(c *gin.Context) {
	h.setOwnStatus(c, subscription.StatusPaused, "subscription paused")
}

// @Summary Resume auto-debit subscription
// @Tags Subscriptions
// @Security BearerAuth
// @Router /circles/{id}/subscription/resume [post]
func (h *SubscriptionHandler) Resume(c *gin.Context) {
	userID := middleware.GetUserID(c)
	circleID := c.Param("id")
	ctx := c.Request.Context()
	sub, err := h.repo.FindByUserCircle(ctx, userID, circleID)
	if err != nil || sub == nil {
		response.NotFound(c, "subscription not found")
		return
	}
	if err := h.repo.UpdateStatus(ctx, sub.ID, subscription.StatusActive); err != nil {
		response.InternalError(c, "failed to resume subscription")
		return
	}
	// Restart the cadence now so a long-paused row debits promptly instead
	// of waiting on a stale next_debit_at far in the past... which is
	// exactly what we want: catch up to the current round on next tick.
	if err := h.repo.Postpone(ctx, sub.ID, time.Now().UTC()); err != nil {
		response.InternalError(c, "failed to resume subscription")
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *SubscriptionHandler) setOwnStatus(c *gin.Context, status, msg string) {
	userID := middleware.GetUserID(c)
	circleID := c.Param("id")
	ctx := c.Request.Context()
	sub, err := h.repo.FindByUserCircle(ctx, userID, circleID)
	if err != nil || sub == nil {
		response.NotFound(c, "subscription not found")
		return
	}
	if err := h.repo.UpdateStatus(ctx, sub.ID, status); err != nil {
		response.InternalError(c, "failed to update subscription")
		return
	}
	response.OK(c, gin.H{"success": true, "message": msg})
}

// @Summary List my auto-debit subscriptions
// @Description Upcoming debit calendar: amounts, caps, next debit times.
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Router /users/me/subscriptions [get]
func (h *SubscriptionHandler) ListMine(c *gin.Context) {
	userID := middleware.GetUserID(c)
	subs, err := h.repo.ListByUser(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, "failed to list subscriptions")
		return
	}
	response.OK(c, gin.H{"subscriptions": subs})
}
