package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/deodes/backend/internal/api/handler"
	"github.com/deodes/backend/internal/domain/circle"
	circleMocks "github.com/deodes/backend/internal/domain/circle/mocks"
	"github.com/deodes/backend/internal/domain/savings"
	savingsMocks "github.com/deodes/backend/internal/domain/savings/mocks"
)

func TestCircleHandler_CreateCircle_ServiceValidation_MapsTo400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})
	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", uuid.New().String())
		c.Next()
	})
	r.POST("/circles", h.CreateCircle)

	// Passes the handler's struct validator (all tagged fields valid) but
	// fails service validation: a past startDate has no validator tag.
	past := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	body, _ := json.Marshal(map[string]interface{}{
		"name":               "Test Circle",
		"circleType":         "public",
		"payoutType":         "random",
		"contributionAmount": 100,
		"currency":           "USDC",
		"frequency":          "weekly",
		"maxMembers":         10,
		"lateFeePercent":     5,
		"gracePeriodHours":   24,
		"maxStrikes":         3,
		"startDate":          past,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestCircleHandler_CreateCircle_XlmCurrency_MapsTo400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})
	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", uuid.New().String())
		c.Next()
	})
	r.POST("/circles", h.CreateCircle)

	body, _ := json.Marshal(map[string]interface{}{
		"name":               "Test Circle",
		"circleType":         "public",
		"payoutType":         "random",
		"contributionAmount": 100,
		"currency":           "XLM",
		"frequency":          "weekly",
		"maxMembers":         10,
		"maxStrikes":         3,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/circles", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestCircleHandler_UpdateCircle_InvalidInput_MapsTo400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(circleMocks.Repository)
	svc := circle.NewService(repo, nil, circle.Dependencies{})
	cid := uuid.New()
	orgID := uuid.New()
	repo.On("FindByID", mock.Anything, cid).Return(&circle.Circle{
		ID: cid, Name: "Test", Status: circle.CircleStatusPending,
		OrganizerID: orgID, MaxMembers: 10,
	}, nil)

	h := handler.NewCircleHandler(svc, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", orgID.String())
		c.Next()
	})
	r.PATCH("/circles/:id", h.UpdateCircle)

	body, _ := json.Marshal(map[string]interface{}{"maxMembers": 1})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PATCH", "/circles/"+cid.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
	repo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

func TestSavingsHandler_Create_PastDeadline_MapsTo400ValidationError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(savingsMocks.Repository)
	svc := savings.NewService(repo)
	h := handler.NewSavingsGoalHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	r.POST("/savings/goals", h.Create)

	past := time.Now().Add(-24 * time.Hour).Format("2006-01-02")
	body, _ := json.Marshal(map[string]interface{}{
		"name": "Valid name", "targetAmount": 2000, "targetDate": past,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/savings/goals", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestSavingsHandler_Update_PastDeadline_MapsTo400ValidationError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := new(savingsMocks.Repository)
	svc := savings.NewService(repo)
	repo.On("FindByID", mock.Anything, "goal-1").Return(&savings.SavingsGoal{ID: "goal-1", UserID: "user-1", Name: "Trip"}, nil)

	h := handler.NewSavingsGoalHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	r.PATCH("/savings/goals/:id", h.Update)

	past := time.Now().Add(-24 * time.Hour).Format("2006-01-02")
	body, _ := json.Marshal(map[string]interface{}{"targetDate": past})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PATCH", "/savings/goals/goal-1", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
	repo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}
