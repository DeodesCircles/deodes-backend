package contribution_test

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/domain/contribution"
)

func mustTime(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func TestContribution_MarshalJSON_TxnHashAsString(t *testing.T) {
	c := contribution.Contribution{
		ID:        uuid.New(),
		CircleID:  uuid.New(),
		UserID:    uuid.New(),
		Amount:    100,
		TxnHash:   sql.NullString{String: "0xabc", Valid: true},
		Status:    contribution.StatusConfirmed,
		CreatedAt: mustTime(t),
		UpdatedAt: mustTime(t),
	}
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, "0xabc", decoded["txnHash"])
}

func TestContribution_MarshalJSON_OmitsEmptyTxnHash(t *testing.T) {
	c := contribution.Contribution{
		ID:        uuid.New(),
		Status:    contribution.StatusPending,
		CreatedAt: mustTime(t),
		UpdatedAt: mustTime(t),
	}
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	_, ok := decoded["txnHash"]
	assert.False(t, ok, "empty txnHash must be omitted, got %s", string(raw))
}
