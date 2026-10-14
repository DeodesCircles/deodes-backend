package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/deodes/backend/internal/api/middleware"
	"github.com/deodes/backend/internal/domain/contribution"
	"github.com/deodes/backend/pkg/pagination"
	"github.com/deodes/backend/pkg/response"
)

type ContributionHandler struct {
	contribService contribution.Service
	contribRepo    contribution.Repository
}

func NewContributionHandler(svc contribution.Service, repo contribution.Repository) *ContributionHandler {
	return &ContributionHandler{contribService: svc, contribRepo: repo}
}

// @Summary List contributions
// @Description Returns paginated contributions filtered by userId, circleId, or the authenticated user's own contributions.
// @Tags Contributions
// @Produce json
// @Security BearerAuth
// @Param userId query string false "Filter by user ID"
// @Param circleId query string false "Filter by circle ID"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Success 200 {object} response.Envelope{data=object{contributions=array},meta=response.PaginationMeta}
// @Failure 500 {object} response.Envelope
// @Router /contributions [get]
func (h *ContributionHandler) ListContributions(c *gin.Context) {
	// Auth: identity always comes from the bearer session. A missing userID
	// is a 401, never a 500 from a downstream UUID parse.
	authUserID := middleware.GetUserID(c)
	if authUserID == "" {
		response.Unauthorized(c, "authentication required")
		return
	}
	if _, err := uuid.Parse(authUserID); err != nil {
		response.Unauthorized(c, "invalid session identity")
		return
	}
	userIDFilter := c.Query("userId")
	circleIDFilter := c.Query("circleId")
	page, limit, _ := pagination.Parse(c)

	if circleIDFilter != "" {
		if _, err := uuid.Parse(circleIDFilter); err != nil {
			response.BadRequest(c, "invalid circleId")
			return
		}
		contribs, total, err := h.contribService.GetCircleHistory(c.Request.Context(), circleIDFilter, page, limit)
		if err != nil {
			// Invalid UUID surfaces here as well — map to 400, not 500.
			if containsInvalidID(err.Error()) {
				response.BadRequest(c, err.Error())
				return
			}
			response.InternalError(c, "failed to list contributions")
			return
		}
		if contribs == nil {
			contribs = []contribution.Contribution{}
		}
		response.OKWithMeta(c, gin.H{"contributions": contribs}, response.NewPaginationMeta(page, limit, total))
		return
	}

	if userIDFilter != "" {
		if _, err := uuid.Parse(userIDFilter); err != nil {
			response.BadRequest(c, "invalid userId")
			return
		}
		// Authz: a user may only list their own history via userId. Listing
		// another user's contributions is forbidden (prevents enumeration).
		if userIDFilter != authUserID {
			response.Forbidden(c, "cannot list another user's contributions")
			return
		}
		contribs, total, err := h.contribService.GetUserHistory(c.Request.Context(), userIDFilter, page, limit)
		if err != nil {
			if containsInvalidID(err.Error()) {
				response.BadRequest(c, err.Error())
				return
			}
			response.InternalError(c, "failed to list contributions")
			return
		}
		if contribs == nil {
			contribs = []contribution.Contribution{}
		}
		response.OKWithMeta(c, gin.H{"contributions": contribs}, response.NewPaginationMeta(page, limit, total))
		return
	}

	contribs, total, err := h.contribService.GetUserHistory(c.Request.Context(), authUserID, page, limit)
	if err != nil {
		if containsInvalidID(err.Error()) {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, "failed to list contributions")
		return
	}
	if contribs == nil {
		contribs = []contribution.Contribution{}
	}
	response.OKWithMeta(c, gin.H{"contributions": contribs}, response.NewPaginationMeta(page, limit, total))
}

func containsInvalidID(msg string) bool {
	return len(msg) >= 7 && (contains(msg, "invalid userID") || contains(msg, "invalid circleID") || contains(msg, "invalid contribution ID"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// @Summary Get a contribution
// @Description Returns a single contribution by ID.
// @Tags Contributions
// @Produce json
// @Security BearerAuth
// @Param id path string true "Contribution ID (UUID)"
// @Success 200 {object} response.Envelope{data=object{contribution=object}}
// @Failure 400 {object} response.Envelope
// @Failure 404 {object} response.Envelope
// @Router /contributions/{id} [get]
func (h *ContributionHandler) GetContribution(c *gin.Context) {
	authUserID := middleware.GetUserID(c)
	if authUserID == "" {
		response.Unauthorized(c, "authentication required")
		return
	}
	id := c.Param("id")
	uid, err := uuid.Parse(id)
	if err != nil {
		response.BadRequest(c, "invalid contribution ID")
		return
	}
	contrib, err := h.contribRepo.FindByID(c.Request.Context(), uid)
	if err != nil {
		response.NotFound(c, "contribution not found")
		return
	}
	// Authz: a contribution is visible only to its owner. Organizer-wide
	// visibility needs a circle-membership check (requires wiring
	// circle.Service into this handler — reported to the integrator); until
	// then, cross-user reads are forbidden to prevent enumeration.
	if contrib.UserID.String() != authUserID {
		response.Forbidden(c, "cannot view another user's contribution")
		return
	}
	response.OK(c, gin.H{"contribution": contrib})
}
