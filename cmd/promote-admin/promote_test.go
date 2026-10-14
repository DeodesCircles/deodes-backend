package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/domain/user"
)

func mockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		if err := db.Close(); err != nil {
			t.Logf("closing mock db: %v", err)
		}
	})
	return db, mock
}

func TestOptionsValidate(t *testing.T) {
	assert.NoError(t, Options{Wallet: "0xabc"}.Validate())
	assert.NoError(t, Options{Email: "owner@example.com"}.Validate())
	assert.Error(t, Options{}.Validate(), "neither identifier")
	assert.Error(t, Options{Wallet: "0xabc", Email: "owner@example.com"}.Validate(), "both identifiers")
	assert.Error(t, Options{Wallet: "   "}.Validate(), "blank wallet counts as missing")
}

func TestPromote_ByWallet_FirstAdmin(t *testing.T) {
	db, mock := mockDB(t)

	mock.ExpectQuery(`SELECT id, role FROM users WHERE wallet_address`).
		WithArgs("0xabc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow("user-1", "user"))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM users WHERE role`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`UPDATE users SET role`).
		WithArgs("admin", "user-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	id, already, err := Promote(context.Background(), db, Options{Wallet: "0xabc"})
	require.NoError(t, err)
	assert.Equal(t, "user-1", id)
	assert.False(t, already)
}

func TestPromote_ByEmail_HashesBeforeLookup(t *testing.T) {
	db, mock := mockDB(t)

	hashed := user.HashEmail("owner@example.com")
	mock.ExpectQuery(`SELECT id, role FROM users WHERE email`).
		WithArgs(hashed).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow("user-2", "user"))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM users WHERE role`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`UPDATE users SET role`).
		WithArgs("admin", "user-2").
		WillReturnResult(sqlmock.NewResult(0, 1))

	id, already, err := Promote(context.Background(), db, Options{Email: "owner@example.com"})
	require.NoError(t, err)
	assert.Equal(t, "user-2", id)
	assert.False(t, already)
}

func TestPromote_ByEmail_FallsBackToLowercase(t *testing.T) {
	db, mock := mockDB(t)

	mock.ExpectQuery(`SELECT id, role FROM users WHERE email`).
		WithArgs(user.HashEmail("Owner@Example.COM")).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT id, role FROM users WHERE email`).
		WithArgs(user.HashEmail("owner@example.com")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow("user-3", "user"))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM users WHERE role`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`UPDATE users SET role`).
		WithArgs("admin", "user-3").
		WillReturnResult(sqlmock.NewResult(0, 1))

	id, _, err := Promote(context.Background(), db, Options{Email: "Owner@Example.COM"})
	require.NoError(t, err)
	assert.Equal(t, "user-3", id)
}

func TestPromote_RefusesSecondAdminWithoutForce(t *testing.T) {
	db, mock := mockDB(t)

	mock.ExpectQuery(`SELECT id, role FROM users WHERE wallet_address`).
		WithArgs("0xdef").
		WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow("user-4", "user"))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM users WHERE role`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	_, _, err := Promote(context.Background(), db, Options{Wallet: "0xdef"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAdminExists))
}

func TestPromote_ForceAllowsSecondAdmin(t *testing.T) {
	db, mock := mockDB(t)

	mock.ExpectQuery(`SELECT id, role FROM users WHERE wallet_address`).
		WithArgs("0xdef").
		WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow("user-4", "user"))
	// With force there is no admin-count query at all.
	mock.ExpectExec(`UPDATE users SET role`).
		WithArgs("admin", "user-4").
		WillReturnResult(sqlmock.NewResult(0, 1))

	id, already, err := Promote(context.Background(), db, Options{Wallet: "0xdef", Force: true})
	require.NoError(t, err)
	assert.Equal(t, "user-4", id)
	assert.False(t, already)
}

func TestPromote_AlreadyAdminIsNoOp(t *testing.T) {
	db, mock := mockDB(t)

	mock.ExpectQuery(`SELECT id, role FROM users WHERE wallet_address`).
		WithArgs("0xadmin").
		WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow("admin-1", "admin"))
	// No count query and no update: nothing to change.

	id, already, err := Promote(context.Background(), db, Options{Wallet: "0xadmin"})
	require.NoError(t, err)
	assert.Equal(t, "admin-1", id)
	assert.True(t, already)
}

func TestPromote_UnknownUser(t *testing.T) {
	db, mock := mockDB(t)

	mock.ExpectQuery(`SELECT id, role FROM users WHERE wallet_address`).
		WithArgs("0xmissing").
		WillReturnError(sql.ErrNoRows)

	_, _, err := Promote(context.Background(), db, Options{Wallet: "0xmissing"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, user.ErrUserNotFound))
}
