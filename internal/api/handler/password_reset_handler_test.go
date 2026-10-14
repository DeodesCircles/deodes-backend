package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/api/handler"
	"github.com/deodes/backend/internal/domain/auth"
	"github.com/deodes/backend/internal/domain/user"
	"github.com/deodes/backend/internal/domain/verification"
)

type fakeResetUserSvc struct {
	user.Service
	users   map[string]*user.User
	updated []*user.User
}

func (f *fakeResetUserSvc) GetByEmail(_ context.Context, email string) (*user.User, error) {
	if u, ok := f.users[email]; ok {
		return u, nil
	}
	return nil, nil
}

func (f *fakeResetUserSvc) Update(_ context.Context, u *user.User) error {
	f.updated = append(f.updated, u)
	for email, existing := range f.users {
		if existing.ID == u.ID {
			f.users[email] = u
		}
	}
	return nil
}

type fakeResetAuthSvc struct {
	auth.Service
	revoked []string
}

func (f *fakeResetAuthSvc) RevokeAllSessions(_ context.Context, userID, _ string) error {
	f.revoked = append(f.revoked, userID)
	return nil
}

type resetFixture struct {
	handler *handler.PasswordResetHandler
	users   *fakeResetUserSvc
	auth    *fakeResetAuthSvc
	sent    *string
}

func newResetFixture(t *testing.T) *resetFixture {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = rdb.Close()
		mr.Close()
	})
	usvc := &fakeResetUserSvc{users: map[string]*user.User{}}
	asvc := &fakeResetAuthSvc{}
	vsvc := verification.NewService(rdb)
	var sent string
	vsvc.WithEmailSender(func(_ string, code string) error {
		sent = code
		return nil
	}, nil)
	return &resetFixture{
		handler: handler.NewPasswordResetHandler(asvc, usvc, vsvc),
		users:   usvc,
		auth:    asvc,
		sent:    &sent,
	}
}

func resetUser(email string) *user.User {
	return &user.User{ID: uuid.New(), Email: &email}
}

func postResetJSON(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	req, err := http.NewRequest("POST", path, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, req)
	return w
}

func resetRouter(h *handler.PasswordResetHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/forgot", h.ForgotPassword)
	r.POST("/reset", h.ResetPassword)
	return r
}

func TestPasswordReset_ForgotUnknownEmailStill200(t *testing.T) {
	f := newResetFixture(t)
	r := resetRouter(f.handler)

	w := postResetJSON(t, r, "/forgot", map[string]string{"email": "nobody@example.com"})
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "success")
	assert.Empty(t, *f.sent, "nothing sent for unknown emails")
}

func TestPasswordReset_ResetWrongCode422(t *testing.T) {
	f := newResetFixture(t)
	f.users.users["amina@example.com"] = resetUser("amina@example.com")
	r := resetRouter(f.handler)

	w := postResetJSON(t, r, "/reset", map[string]string{
		"email": "amina@example.com", "code": "000000", "newPassword": "newpassword1",
	})
	assert.Equal(t, 422, w.Code)
}

func TestPasswordReset_FullRoundTrip(t *testing.T) {
	f := newResetFixture(t)
	uid := uuid.New()
	f.users.users["amina@example.com"] = &user.User{ID: uid, Email: resetUser("amina@example.com").Email}
	r := resetRouter(f.handler)

	w := postResetJSON(t, r, "/forgot", map[string]string{"email": "amina@example.com"})
	require.Equal(t, 200, w.Code)
	require.Len(t, *f.sent, 6, "code delivered via sender hook (email in prod)")

	w = postResetJSON(t, r, "/reset", map[string]string{
		"email": "amina@example.com", "code": *f.sent, "newPassword": "newpassword1",
	})
	require.Equal(t, 200, w.Code)
	require.Len(t, f.users.updated, 1)
	require.True(t, f.users.updated[0].PasswordHash.Valid)
	require.NotEmpty(t, f.users.updated[0].PasswordHash.String)
	require.Equal(t, []string{uid.String()}, f.auth.revoked)

	// Code is single-use: replay fails.
	w = postResetJSON(t, r, "/reset", map[string]string{
		"email": "amina@example.com", "code": *f.sent, "newPassword": "newpassword2",
	})
	assert.Equal(t, 422, w.Code)
}
