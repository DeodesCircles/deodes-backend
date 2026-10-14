package circle

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/google/uuid"

	"github.com/deodes/backend/pkg/money"
)

// Exact-money distribution (2.8). All math runs in integer minor units so
// the backend reproduces the on-chain formula bit-for-bit; floats never
// touch fee or pool computation. Converters in/out live in pkg/money
// (0.2): FromFloat64/FromString in, Float64/String out at the edges.

const (
	// FeeBps is the immutable protocol fee: 50 basis points (0.5%),
	// mirroring DeodesCircle.FEE_BPS. A change here must change the
	// contract too — the fee-vectors test fails otherwise.
	FeeBps = 50
	// FeeDenominator is the basis-points denominator (10_000).
	FeeDenominator = 10_000
)

// SplitFee divides pool into protocol fee and recipient net exactly as
// DeodesCircle.payout does: fee = floor(pool * 50 / 10000), net = pool -
// fee. fee + net always reconstructs pool (no dust leaks); pools below 200
// minor units carry a zero fee. Truncation (not rounding) is load-bearing:
// money.MulDiv rounds, so the quotient is computed here with big.Int Quo,
// which truncates toward zero (= floor for non-negative pools).
func SplitFee(pool money.Money) (fee, net money.Money, err error) {
	if pool.IsNegative() {
		return money.Money{}, money.Money{}, fmt.Errorf("cannot split negative pool %s", pool.String())
	}
	prod := new(big.Int).Mul(big.NewInt(pool.Minor()), big.NewInt(FeeBps))
	q := new(big.Int).Quo(prod, big.NewInt(FeeDenominator))
	if !q.IsInt64() {
		return money.Money{}, money.Money{}, fmt.Errorf("fee overflows int64 for pool %s", pool.String())
	}
	fee = money.FromMinor(q.Int64())
	net, err = pool.Sub(fee)
	if err != nil {
		return money.Money{}, money.Money{}, fmt.Errorf("splitting net: %w", err)
	}
	return fee, net, nil
}

// PoolForRound is the exact distributable pool for a round: every member's
// contribution plus collected late penalties. Thin wrapper over PayoutPool
// kept beside SplitFee so distributors have one import.
func PoolForRound(contribution money.Money, memberCount int, latePenalties money.Money) (money.Money, error) {
	if memberCount < 1 {
		return money.Money{}, fmt.Errorf("pool needs at least 1 member, got %d", memberCount)
	}
	return PayoutPool(contribution, memberCount, latePenalties)
}

// SelectFixedRecipient resolves positional rotation: round N pays member
// (N-1) mod len(members). Repeats are allowed across cycles (fixed order
// restarts), unlike random rotation which forbids repeats within a cycle.
func SelectFixedRecipient(members []uuid.UUID, round int) (uuid.UUID, error) {
	if len(members) == 0 {
		return uuid.Nil, fmt.Errorf("no members to pay")
	}
	if round < 1 {
		return uuid.Nil, fmt.Errorf("invalid round number %d: must be at least 1", round)
	}
	return members[(round-1)%len(members)], nil
}

// SelectAuctionWinner returns the highest bidder. Ties break toward the
// smallest user ID so the outcome is deterministic across replicas — the
// legacy float AuctionWinner keeps first-seen (map order) and must not be
// used for settlement.
func SelectAuctionWinner(bids map[uuid.UUID]money.Money) (uuid.UUID, money.Money, error) {
	if len(bids) == 0 {
		return uuid.Nil, money.Money{}, fmt.Errorf("no bids to settle")
	}
	ids := make([]uuid.UUID, 0, len(bids))
	for id := range bids {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	winner := ids[0]
	winning := bids[winner]
	for _, id := range ids[1:] {
		if bids[id].Cmp(winning) > 0 {
			winner, winning = id, bids[id]
		}
	}
	return winner, winning, nil
}

// SelectVoteWinner returns the most-voted recipient. Ties break toward the
// smallest user ID (deterministic); zero votes for everyone is an error,
// not a coin flip.
func SelectVoteWinner(votes map[uuid.UUID]int) (uuid.UUID, error) {
	if len(votes) == 0 {
		return uuid.Nil, fmt.Errorf("no votes to tally")
	}
	ids := make([]uuid.UUID, 0, len(votes))
	for id := range votes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	winner := uuid.Nil
	best := 0
	for _, id := range ids {
		if votes[id] > best {
			winner, best = id, votes[id]
		}
	}
	if winner == uuid.Nil {
		return uuid.Nil, fmt.Errorf("no votes cast for any recipient")
	}
	return winner, nil
}

// Distribution is the settled outcome for one round: who is paid and the
// exact gross/fee/net split.
type Distribution struct {
	RecipientID uuid.UUID
	Gross       money.Money
	Fee         money.Money
	Net         money.Money
}

// Distribute resolves one round for any payout type: recipient selection
// per type plus the exact fee split. members is position order; paid marks
// rotation payouts already made; bids/votes feed auction/vote; contribution
// is the per-member round amount; seed drives the random ("VRF") draw —
// pass RotationSeed(circleID, round) for the auditable default.
func Distribute(
	payoutType PayoutType,
	members []CircleMember,
	paid map[uuid.UUID]bool,
	bids map[uuid.UUID]money.Money,
	votes map[uuid.UUID]int,
	contribution money.Money,
	latePenalties money.Money,
	round int,
	seed int64,
) (Distribution, error) {
	if len(members) == 0 {
		return Distribution{}, fmt.Errorf("no members to distribute to")
	}
	var recipient uuid.UUID
	var err error
	switch payoutType {
	case PayoutTypeRandom:
		recipient, err = SelectRandomRecipient(members, paid, seed)
	case PayoutTypeFixed:
		// Fixed order cycles positionally over the full roster (repeats
		// across cycles are the point of fixed order); no paid-exclusion.
		ids := make([]uuid.UUID, 0, len(members))
		for _, m := range members {
			ids = append(ids, m.UserID)
		}
		recipient, err = SelectFixedRecipient(ids, round)
	case PayoutTypeAuction:
		recipient, _, err = SelectAuctionWinner(bids)
	case PayoutTypeVote:
		recipient, err = SelectVoteWinner(votes)
	default:
		return Distribution{}, fmt.Errorf("unknown payout type %q", payoutType)
	}
	if err != nil {
		return Distribution{}, err
	}
	pool, err := PoolForRound(contribution, len(members), latePenalties)
	if err != nil {
		return Distribution{}, err
	}
	fee, net, err := SplitFee(pool)
	if err != nil {
		return Distribution{}, err
	}
	return Distribution{RecipientID: recipient, Gross: pool, Fee: fee, Net: net}, nil
}
