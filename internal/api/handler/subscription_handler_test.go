package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/api/handler"
	"github.com/deodes/backend/internal/domain/circle"
	"github.com/deodes/backend/internal/domain/subscription"
)

type memSubRepo struct {
	mu   sync.Mutex
	subs map[string]*subscription.Subscription
}

func newMemSubRepo() *memSubRepo {
	return &memSubRepo{subs: map[string]*subscription.Subscription{}}
}

func (m *memSubRepo) Create(ctx context.Context, s *subscription.Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.subs {
		if e.UserID == s.UserID && e.CircleID == s.CircleID {
			return fmt.Errorf("duplicate subscription")
		}
	}
	s.ID = uuid.New().String()
	s.Status = subscription.StatusActive
	s.CollectedTotal = "0"
	s.CreatedAt = time.Now().UTC()
	s.UpdatedAt = s.CreatedAt
	cp := *s
	m.subs[s.ID] = &cp
	return nil
}
func (m *memSubRepo) FindByID(ctx context.Context, id string) (*subscription.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	cp := *s
	return &cp, nil
}
func (m *memSubRepo) FindByUserCircle(ctx context.Context, userID, circleID string) (*subscription.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.subs {
		if s.UserID == userID && s.CircleID == circleID {
			cp := *s
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (m *memSubRepo) ListByUser(ctx context.Context, userID string) ([]subscription.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []subscription.Subscription
	for _, s := range m.subs {
		if s.UserID == userID {
			out = append(out, *s)
		}
	}
	if out == nil {
		out = []subscription.Subscription{}
	}
	return out, nil
}
func (m *memSubRepo) ListDue(ctx context.Context, now time.Time, limit int) ([]subscription.Subscription, error) {
	return nil, nil
}
func (m *memSubRepo) ListRemindable(ctx context.Context, now time.Time, window time.Duration) ([]subscription.Subscription, error) {
	return nil, nil
}
func (m *memSubRepo) MarkReminded(ctx context.Context, id string, now time.Time) error {
	return nil
}
func (m *memSubRepo) UpdateStatus(ctx context.Context, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	s.Status = status
	return nil
}
func (m *memSubRepo) Postpone(ctx context.Context, id string, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	s.NextDebitAt = next
	return nil
}
func (m *memSubRepo) RecordDebit(ctx context.Context, id string, expectedRound, round int, amount string, next time.Time) (bool, error) {
	return true, nil
}

func setupSubRouter(repo subscription.Repository, circles circle.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := handler.NewSubscriptionHandler(repo, circles)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	r.POST("/circles/:id/subscription", h.Subscribe)
	r.DELETE("/circles/:id/subscription", h.Cancel)
	r.POST("/circles/:id/subscription/pause", h.Pause)
	r.POST("/circles/:id/subscription/resume", h.Resume)
	r.GET("/users/me/subscriptions", h.ListMine)
	return r
}

func autoCircle() *circle.Circle {
	return &circle.Circle{
		ID: uuid.New(), ContributionAmount: 100,
		Frequency: circle.FrequencyWeekly, Status: circle.CircleStatusActive,
		AutoDebit: true,
	}
}

func doSubRequest(t *testing.T, r *gin.Engine, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var decoded map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &decoded)
	return w.Code, decoded
}

func TestSubscription_SubscribeHappyPath(t *testing.T) {
	repo := newMemSubRepo()
	circles := &stubCircleService{
		byID:      map[string]*circle.Circle{"c-1": autoCircle()},
		memberSet: map[string]bool{"user-1|c-1": true},
	}
	r := setupSubRouter(repo, circles)

	code, _ := doSubRequest(t, r, "POST", "/circles/c-1/subscription",
		map[string]any{"amount": 100, "capTotal": 1000})
	assert.Equal(t, http.StatusCreated, code)

	got, err := repo.FindByUserCircle(context.Background(), "user-1", "c-1")
	require.NoError(t, err)
	assert.Equal(t, subscription.StatusActive, got.Status)
}

func TestSubscription_SubscribeRejected(t *testing.T) {
	flex := autoCircle()
	flex.AutoDebit = false
	done := autoCircle()
	done.Status = circle.CircleStatusCompleted

	setup := func() (*memSubRepo, *stubCircleService) {
		repo := newMemSubRepo()
		return repo, &stubCircleService{
			byID: map[string]*circle.Circle{
				"c-auto": autoCircle(), "c-flex": flex, "c-done": done,
			},
			memberSet: map[string]bool{"user-1|c-auto": true, "user-1|c-flex": true},
		}
	}

	t.Run("flexible circle", func(t *testing.T) {
		repo, circles := setup()
		code, _ := doSubRequest(t, setupSubRouter(repo, circles), "POST",
			"/circles/c-flex/subscription", map[string]any{"amount": 100, "capTotal": 1000})
		assert.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("non-member", func(t *testing.T) {
		repo, circles := setup()
		circles.memberSet = map[string]bool{}
		code, _ := doSubRequest(t, setupSubRouter(repo, circles), "POST",
			"/circles/c-auto/subscription", map[string]any{"amount": 100, "capTotal": 1000})
		assert.Equal(t, http.StatusForbidden, code)
	})

	t.Run("wrong amount", func(t *testing.T) {
		repo, circles := setup()
		code, _ := doSubRequest(t, setupSubRouter(repo, circles), "POST",
			"/circles/c-auto/subscription", map[string]any{"amount": 50, "capTotal": 1000})
		assert.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("cap below amount", func(t *testing.T) {
		repo, circles := setup()
		code, _ := doSubRequest(t, setupSubRouter(repo, circles), "POST",
			"/circles/c-auto/subscription", map[string]any{"amount": 100, "capTotal": 50})
		assert.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("duplicate", func(t *testing.T) {
		repo, circles := setup()
		r := setupSubRouter(repo, circles)
		body := map[string]any{"amount": 100, "capTotal": 1000}
		code, _ := doSubRequest(t, r, "POST", "/circles/c-auto/subscription", body)
		require.Equal(t, http.StatusCreated, code)
		code, _ = doSubRequest(t, r, "POST", "/circles/c-auto/subscription", body)
		assert.Equal(t, http.StatusConflict, code)
	})

	t.Run("completed circle", func(t *testing.T) {
		repo, circles := setup()
		code, _ := doSubRequest(t, setupSubRouter(repo, circles), "POST",
			"/circles/c-done/subscription", map[string]any{"amount": 100, "capTotal": 1000})
		assert.Equal(t, http.StatusBadRequest, code)
	})
}

func TestSubscription_PauseResumeCancelList(t *testing.T) {
	repo := newMemSubRepo()
	circles := &stubCircleService{
		byID:      map[string]*circle.Circle{"c-1": autoCircle()},
		memberSet: map[string]bool{"user-1|c-1": true},
	}
	r := setupSubRouter(repo, circles)
	body := map[string]any{"amount": 100, "capTotal": 1000}
	code, _ := doSubRequest(t, r, "POST", "/circles/c-1/subscription", body)
	require.Equal(t, http.StatusCreated, code)

	code, _ = doSubRequest(t, r, "POST", "/circles/c-1/subscription/pause", nil)
	assert.Equal(t, http.StatusOK, code)
	got, _ := repo.FindByUserCircle(context.Background(), "user-1", "c-1")
	assert.Equal(t, subscription.StatusPaused, got.Status)

	code, resp := doSubRequest(t, r, "GET", "/users/me/subscriptions", nil)
	assert.Equal(t, http.StatusOK, code)
	data := resp["data"].(map[string]any)
	assert.Len(t, data["subscriptions"].([]any), 1)

	code, _ = doSubRequest(t, r, "POST", "/circles/c-1/subscription/resume", nil)
	assert.Equal(t, http.StatusOK, code)
	got, _ = repo.FindByUserCircle(context.Background(), "user-1", "c-1")
	assert.Equal(t, subscription.StatusActive, got.Status)

	code, _ = doSubRequest(t, r, "DELETE", "/circles/c-1/subscription", nil)
	assert.Equal(t, http.StatusOK, code)
	got, _ = repo.FindByUserCircle(context.Background(), "user-1", "c-1")
	assert.Equal(t, subscription.StatusCancelled, got.Status)
}
