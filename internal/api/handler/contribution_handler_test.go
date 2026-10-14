package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/api/handler"
	"github.com/deodes/backend/internal/domain/contribution"
	contribMocks "github.com/deodes/backend/internal/domain/contribution/mocks"
	"github.com/deodes/backend/pkg/apperrors"
	"github.com/stretchr/testify/mock"
)

// stubListService serves GetUserHistory/GetCircleHistory for list tests.
type stubListService struct {
	contribution.Service
	userContribs   []contribution.Contribution
	userTotal      int
	userErr        error
	circleContribs []contribution.Contribution
	circleTotal    int
	circleErr      error
}

func (s *stubListService) GetUserHistory(_ context.Context, _ string, _, _ int) ([]contribution.Contribution, int, error) {
	return s.userContribs, s.userTotal, s.userErr
}

func (s *stubListService) GetCircleHistory(_ context.Context, _ string, _, _ int) ([]contribution.Contribution, int, error) {
	return s.circleContribs, s.circleTotal, s.circleErr
}

func newContributionRouter(svc contribution.Service, repo contribution.Repository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := handler.NewContributionHandler(svc, repo)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", c.GetHeader("X-Test-User"))
		c.Next()
	})
	r.GET("/contributions", h.ListContributions)
	r.GET("/contributions/:id", h.GetContribution)
	return r
}

func TestListContributions_OwnHistory(t *testing.T) {
	userID := uuid.New()
	svc := &stubListService{
		userContribs: []contribution.Contribution{{ID: uuid.New(), UserID: userID, Amount: 100}},
		userTotal:    1,
	}
	r := newContributionRouter(svc, nil)
	req := httptest.NewRequest(http.MethodGet, "/contributions", nil)
	req.Header.Set("X-Test-User", userID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "contributions")
}

func TestListContributions_RejectsUnauthenticated(t *testing.T) {
	r := newContributionRouter(&stubListService{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/contributions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListContributions_RejectsOtherUserFilter(t *testing.T) {
	userID := uuid.New()
	otherID := uuid.New()
	r := newContributionRouter(&stubListService{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/contributions?userId="+otherID.String(), nil)
	req.Header.Set("X-Test-User", userID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListContributions_RejectsInvalidCircleID(t *testing.T) {
	userID := uuid.New()
	r := newContributionRouter(&stubListService{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/contributions?circleId=not-a-uuid", nil)
	req.Header.Set("X-Test-User", userID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListContributions_EmptyReturnsEmptyArray(t *testing.T) {
	userID := uuid.New()
	svc := &stubListService{userContribs: nil, userTotal: 0}
	r := newContributionRouter(svc, nil)
	req := httptest.NewRequest(http.MethodGet, "/contributions", nil)
	req.Header.Set("X-Test-User", userID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"contributions":[]`)
}

func TestGetContribution_OwnerOK(t *testing.T) {
	userID := uuid.New()
	contribID := uuid.New()
	repo := new(contribMocks.Repository)
	repo.On("FindByID", mock.Anything, contribID).Return(&contribution.Contribution{ID: contribID, UserID: userID}, nil)
	r := newContributionRouter(&stubListService{}, repo)
	req := httptest.NewRequest(http.MethodGet, "/contributions/"+contribID.String(), nil)
	req.Header.Set("X-Test-User", userID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	repo.AssertExpectations(t)
}

func TestGetContribution_ForbiddenForOtherUser(t *testing.T) {
	ownerID := uuid.New()
	callerID := uuid.New()
	contribID := uuid.New()
	repo := new(contribMocks.Repository)
	repo.On("FindByID", mock.Anything, contribID).Return(&contribution.Contribution{ID: contribID, UserID: ownerID}, nil)
	r := newContributionRouter(&stubListService{}, repo)
	req := httptest.NewRequest(http.MethodGet, "/contributions/"+contribID.String(), nil)
	req.Header.Set("X-Test-User", callerID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetContribution_InvalidID(t *testing.T) {
	r := newContributionRouter(&stubListService{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/contributions/not-a-uuid", nil)
	req.Header.Set("X-Test-User", uuid.New().String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetContribution_NotFound(t *testing.T) {
	contribID := uuid.New()
	repo := new(contribMocks.Repository)
	repo.On("FindByID", mock.Anything, contribID).Return(nil, apperrors.ErrNotFound)
	r := newContributionRouter(&stubListService{}, repo)
	req := httptest.NewRequest(http.MethodGet, "/contributions/"+contribID.String(), nil)
	req.Header.Set("X-Test-User", uuid.New().String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
