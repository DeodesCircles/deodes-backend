package subscription

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockRepo(t *testing.T) (Repository, sqlmock.Sqlmock, func()) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	db := sqlx.NewDb(mockDB, "sqlmock")
	cleanup := func() {
		require.NoError(t, mock.ExpectationsWereMet())
		mockDB.Close()
	}
	return NewRepository(db), mock, cleanup
}

func TestRepository_ListDue_OnlyActiveAndOverdue(t *testing.T) {
	repo, mock, cleanup := newMockRepo(t)
	defer cleanup()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "user_id", "circle_id", "amount", "cap_total",
		"collected_total", "status", "next_debit_at", "last_debit_at", "last_reminded_at", "last_round",
		"created_at", "updated_at"}).
		AddRow("s-1", "u-1", "c-1", "100.0000000", "1000.0000000", "0",
			"active", now.Add(-time.Hour), nil, nil, 3, now, now)
	mock.ExpectQuery("SELECT id, user_id, circle_id").
		WithArgs(sqlmock.AnyArg(), 50).
		WillReturnRows(rows)

	got, err := repo.ListDue(context.Background(), now, 50)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "s-1", got[0].ID)
	assert.Equal(t, 3, got[0].LastRound)
}

func TestRepository_ListDue_DefaultLimit(t *testing.T) {
	repo, mock, cleanup := newMockRepo(t)
	defer cleanup()

	mock.ExpectQuery("SELECT id, user_id, circle_id").
		WithArgs(sqlmock.AnyArg(), 100).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "circle_id", "amount",
			"cap_total", "collected_total", "status", "next_debit_at", "last_debit_at", "last_reminded_at",
			"last_round", "created_at", "updated_at"}))

	got, err := repo.ListDue(context.Background(), time.Now().UTC(), 0)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestRepository_RecordDebit_AppliedOnlyOnce(t *testing.T) {
	repo, mock, cleanup := newMockRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE subscriptions").
		WithArgs(4, "100.0000000", sqlmock.AnyArg(), "s-1", 3).
		WillReturnResult(sqlmock.NewResult(0, 1))

	applied, err := repo.RecordDebit(context.Background(), "s-1", 3, 4, "100.0000000", time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, applied)

	// Concurrent keeper with a stale expected round affects zero rows.
	mock.ExpectExec("UPDATE subscriptions").
		WithArgs(4, "100.0000000", sqlmock.AnyArg(), "s-1", 3).
		WillReturnResult(sqlmock.NewResult(0, 0))

	applied, err = repo.RecordDebit(context.Background(), "s-1", 3, 4, "100.0000000", time.Now().UTC())
	require.NoError(t, err)
	assert.False(t, applied)
}

func TestRepository_UpdateStatus_RejectsInvalid(t *testing.T) {
	repo, _, cleanup := newMockRepo(t)
	defer cleanup()

	err := repo.UpdateStatus(context.Background(), "s-1", "archived")
	assert.ErrorContains(t, err, "invalid subscription status")
}

func TestRepository_UpdateStatus_Persists(t *testing.T) {
	repo, mock, cleanup := newMockRepo(t)
	defer cleanup()

	mock.ExpectExec("UPDATE subscriptions SET status").
		WithArgs("paused", "s-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.UpdateStatus(context.Background(), "s-1", "paused"))
}

func TestRepository_ListRemindable_WindowAndStamp(t *testing.T) {
	repo, mock, cleanup := newMockRepo(t)
	defer cleanup()

	now := time.Now().UTC()
	cols := []string{"id", "user_id", "circle_id", "amount", "cap_total",
		"collected_total", "status", "next_debit_at", "last_debit_at", "last_reminded_at",
		"last_round", "created_at", "updated_at"}
	rows := sqlmock.NewRows(cols).
		AddRow("s-1", "u-1", "c-1", "100.0000000", "1000.0000000", "0",
			"active", now.Add(8*time.Hour), nil, nil, 2, now, now)
	mock.ExpectQuery("FROM subscriptions").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), 86400).
		WillReturnRows(rows)

	got, err := repo.ListRemindable(context.Background(), now, 24*time.Hour)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "s-1", got[0].ID)
}

func TestRepository_MarkReminded_Persists(t *testing.T) {
	repo, mock, cleanup := newMockRepo(t)
	defer cleanup()

	now := time.Now().UTC()
	mock.ExpectExec("UPDATE subscriptions SET last_reminded_at").
		WithArgs(now, "s-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.MarkReminded(context.Background(), "s-1", now))
}
