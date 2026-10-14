package circle

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/google/uuid"
)

// ErrRotationComplete is returned when every member of a circle has already
// been paid in the current rotation. The rotation must be closed (or a new
// one started) before anyone can be paid again — a random draw never repeats
// a recipient within a rotation.
var ErrRotationComplete = errors.New("rotation complete: every member has already been paid")

// RotationSeed derives a deterministic, auditable seed for a circle round
// from the circle ID and round number (SHA-256, first 8 bytes, big-endian).
// Anyone with the circle ID and round can recompute the draw order, which is
// the provability property the off-chain draw offers in place of an on-chain
// VRF: the product spec names a Soroban VRF contract for random payout order,
// but no such contract is wired into this backend today — recipient selection
// below is a seeded pseudo-random shuffle (math/rand, see RandomOrder), and
// the rotation invariant (no repeats until every member is paid) is enforced
// at record time in the payout domain. When the VRF contract lands, its
// output should replace this seed while keeping the unpaid-pool exclusion.
func RotationSeed(circleID uuid.UUID, round int) int64 {
	var buf [16]byte
	copy(buf[:], circleID[:])
	var roundBuf [8]byte
	binary.BigEndian.PutUint64(roundBuf[:], uint64(round))
	h := sha256.New()
	_, _ = h.Write(buf[:])
	_, _ = h.Write(roundBuf[:])
	sum := h.Sum(nil)
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// UnpaidMembers returns the members that have not yet been paid in the
// current rotation, preserving the repository's member order so draws and
// displays are stable. Membership in paid is by user ID.
func UnpaidMembers(members []CircleMember, paid map[uuid.UUID]bool) []CircleMember {
	unpaid := make([]CircleMember, 0, len(members))
	for _, m := range members {
		if !paid[m.UserID] {
			unpaid = append(unpaid, m)
		}
	}
	return unpaid
}

// UnpaidIDs returns the user IDs of members with no payout yet in the
// current rotation, in member order.
func UnpaidIDs(members []CircleMember, paid map[uuid.UUID]bool) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(members))
	for _, m := range members {
		if !paid[m.UserID] {
			ids = append(ids, m.UserID)
		}
	}
	return ids
}

// SelectRandomRecipient picks the next random-payout recipient from the
// unpaid pool: the unpaid members are shuffled with the given seed and the
// first entry wins. Already-paid members are deterministically excluded, so a
// full N-member rotation always yields N distinct recipients; once the pool
// is empty the caller must close the rotation (reshuffle only happens after
// a full rotation) instead of paying anyone twice.
func SelectRandomRecipient(members []CircleMember, paid map[uuid.UUID]bool, seed int64) (uuid.UUID, error) {
	unpaid := UnpaidIDs(members, paid)
	if len(unpaid) == 0 {
		return uuid.Nil, ErrRotationComplete
	}
	order := RandomOrder(seed, len(unpaid))
	return unpaid[order[0]], nil
}
