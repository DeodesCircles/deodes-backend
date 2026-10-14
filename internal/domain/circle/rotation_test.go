package circle

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rotationMembers(n int) []CircleMember {
	members := make([]CircleMember, n)
	for i := range members {
		members[i] = CircleMember{
			CircleID: uuid.New(),
			UserID:   uuid.New(),
			Position: i + 1,
			Status:   MemberStatusActive,
		}
	}
	return members
}

// TestRotation_NRoundsYieldNDistinctRecipients is the core rotation property:
// simulating a full rotation by feeding each pick back into the paid set must
// produce N distinct recipients for N members — never a repeat.
func TestRotation_NRoundsYieldNDistinctRecipients(t *testing.T) {
	for _, n := range []int{2, 3, 4, 5, 8, 12} {
		members := rotationMembers(n)
		circleID := uuid.New()
		paid := make(map[uuid.UUID]bool)
		seen := make(map[uuid.UUID]int)
		for round := 1; round <= n; round++ {
			pick, err := SelectRandomRecipient(members, paid, RotationSeed(circleID, round))
			require.NoError(t, err, "n=%d round=%d", n, round)
			require.NotContains(t, seen, pick, "n=%d round=%d: recipient paid twice", n, round)
			seen[pick] = round
			paid[pick] = true
		}
		assert.Len(t, seen, n, "n=%d: full rotation must pay every member exactly once", n)

		// The (N+1)th draw has nobody left: rotation is complete, not a repeat.
		_, err := SelectRandomRecipient(members, paid, RotationSeed(circleID, n+1))
		require.ErrorIs(t, err, ErrRotationComplete, "n=%d", n)
	}
}

// TestRotation_DeterministicExclusion verifies the exclusion is deterministic:
// paid members are removed in member order regardless of map iteration, and
// the same seed always yields the same pick.
func TestRotation_DeterministicExclusion(t *testing.T) {
	members := rotationMembers(5)
	paid := map[uuid.UUID]bool{members[1].UserID: true, members[3].UserID: true}

	unpaid := UnpaidMembers(members, paid)
	require.Len(t, unpaid, 3)
	assert.Equal(t, members[0].UserID, unpaid[0].UserID)
	assert.Equal(t, members[2].UserID, unpaid[1].UserID)
	assert.Equal(t, members[4].UserID, unpaid[2].UserID)

	first, err := SelectRandomRecipient(members, paid, 42)
	require.NoError(t, err)
	second, err := SelectRandomRecipient(members, paid, 42)
	require.NoError(t, err)
	assert.Equal(t, first, second, "same seed must reproduce the same draw")
	assert.NotContains(t, paid, first, "draw must never return an already-paid member")
}

// TestRotation_SeedBindsCircleAndRound checks that the seed actually varies
// across circles and rounds (otherwise every circle would share one order)
// while staying reproducible for the same inputs.
func TestRotation_SeedBindsCircleAndRound(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	assert.Equal(t, RotationSeed(a, 1), RotationSeed(a, 1), "seed must be reproducible")
	assert.NotEqual(t, RotationSeed(a, 1), RotationSeed(a, 2), "seed must vary by round")
	assert.NotEqual(t, RotationSeed(a, 1), RotationSeed(b, 1), "seed must vary by circle")
}

// TestRotation_EmptyCircle verfies degenerate inputs fail closed, not open.
func TestRotation_EmptyCircle(t *testing.T) {
	_, err := SelectRandomRecipient(nil, nil, 1)
	require.ErrorIs(t, err, ErrRotationComplete)
	assert.Empty(t, UnpaidMembers(nil, nil))
}
