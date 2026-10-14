package circle_test

import (
	"encoding/json"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/deodes/backend/internal/domain/circle"
	"github.com/deodes/backend/pkg/money"
)

func mustMoney(t *testing.T, minor int64) money.Money {
	t.Helper()
	return money.FromMinor(minor)
}

func TestSplitFee_Vectors(t *testing.T) {
	cases := []struct{ pool, fee, net int64 }{
		{0, 0, 0},
		{1, 0, 1},
		{199, 0, 199},
		{200, 1, 199},
		{201, 1, 200},
		{9999, 49, 9950},
		{10000, 50, 9950},
		{10001, 50, 9951},
		{250_000000, 1_250000, 248_750000},
		{1_000_000_000_000_000, 5_000_000_000_000, 995_000_000_000_000},
	}
	for _, c := range cases {
		fee, net, err := circle.SplitFee(mustMoney(t, c.pool))
		require.NoError(t, err)
		assert.Equal(t, c.fee, fee.Minor(), "fee for pool %d", c.pool)
		assert.Equal(t, c.net, net.Minor(), "net for pool %d", c.pool)
	}
}

// TestSplitFee_MatchesSharedVectors consumes the same fee-vectors.json the
// forge suite pins (UNIFIED-PLAN 2.8 acceptance). Skipped when the contracts
// checkout is absent (e.g. backend-only CI).
func TestSplitFee_MatchesSharedVectors(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "deodes-contracts", "test", "vectors", "fee-vectors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("shared vectors unavailable: %s", err)
	}
	var doc struct {
		Cases []struct {
			Pool string `json:"pool"`
			Fee  string `json:"fee"`
			Net  string `json:"net"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Cases)
	for _, c := range doc.Cases {
		pool, ok := new(big.Int).SetString(c.Pool, 10)
		require.True(t, ok)
		if !pool.IsInt64() {
			continue // beyond Money range; contract-only case
		}
		wantFee, ok := new(big.Int).SetString(c.Fee, 10)
		require.True(t, ok)
		wantNet, ok := new(big.Int).SetString(c.Net, 10)
		require.True(t, ok)
		fee, net, err := circle.SplitFee(money.FromMinor(pool.Int64()))
		require.NoError(t, err)
		assert.Equal(t, wantFee.Int64(), fee.Minor(), "fee for pool "+c.Pool)
		assert.Equal(t, wantNet.Int64(), net.Minor(), "net for pool "+c.Pool)
	}
}

// TestSplitFee_Property_MirrorsFuzz ports DeodesCircle.t.sol
// testFuzz_FeeIs50Bps: fee+net reconstructs the pool and the truncation
// error stays under one denominator (|fee*10000 - pool*50| < 10000).
func TestSplitFee_Property_MirrorsFuzz(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pool := rapid.Int64Range(0, math.MaxInt64/100).Draw(t, "pool")
		fee, net, err := circle.SplitFee(money.FromMinor(pool))
		require.NoError(t, err)
		// Reconstruction: no dust leaks.
		sum, err := fee.Add(net)
		require.NoError(t, err)
		assert.Equal(t, pool, sum.Minor())
		// Truncation bound: remainder of a /10000 division.
		diff := pool*50 - fee.Minor()*10000
		assert.GreaterOrEqual(t, diff, int64(0))
		assert.Less(t, diff, int64(10000))
	})
}

func TestSelectFixedRecipient_Rotates(t *testing.T) {
	m := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for round := 1; round <= 6; round++ {
		got, err := circle.SelectFixedRecipient(m, round)
		require.NoError(t, err)
		assert.Equal(t, m[(round-1)%3], got)
	}
	_, err := circle.SelectFixedRecipient(nil, 1)
	require.Error(t, err)
	_, err = circle.SelectFixedRecipient(m, 0)
	require.Error(t, err)
}

func TestSelectAuctionWinner_HighestBidDeterministicTie(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	low := a
	high := b
	if b.String() < a.String() {
		low, high = b, a
	}
	winner, winning, err := circle.SelectAuctionWinner(map[uuid.UUID]money.Money{
		a: mustMoney(t, 100),
		b: mustMoney(t, 100),
	})
	require.NoError(t, err)
	assert.Equal(t, low, winner, "ties break to smallest ID")
	_ = high
	assert.Equal(t, int64(100), winning.Minor())
	_, _, err = circle.SelectAuctionWinner(nil)
	require.Error(t, err)
}

func TestSelectVoteWinner_MajorityAndTie(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	winner, err := circle.SelectVoteWinner(map[uuid.UUID]int{a: 1, b: 3})
	require.NoError(t, err)
	assert.Equal(t, b, winner)
	low := a
	if b.String() < a.String() {
		low = b
	}
	winner, err = circle.SelectVoteWinner(map[uuid.UUID]int{a: 2, b: 2})
	require.NoError(t, err)
	assert.Equal(t, low, winner, "ties break to smallest ID")
	_, err = circle.SelectVoteWinner(map[uuid.UUID]int{})
	require.Error(t, err)
	_, err = circle.SelectVoteWinner(map[uuid.UUID]int{a: 0, b: 0})
	require.Error(t, err)
}

func testMembers(n int) []circle.CircleMember {
	m := make([]circle.CircleMember, n)
	for i := range m {
		m[i] = circle.CircleMember{UserID: uuid.New(), Status: circle.MemberStatusActive}
	}
	return m
}

func TestDistribute_AllTypes(t *testing.T) {
	members := testMembers(4)
	contrib := mustMoney(t, 100_000000)
	zero := mustMoney(t, 0)

	// Random via auditable seed: resolves an unpaid member, exact split.
	d, err := circle.Distribute(circle.PayoutTypeRandom, members, nil, nil, nil, contrib, zero, 1, circle.RotationSeed(members[0].CircleID, 1))
	require.NoError(t, err)
	assert.Equal(t, int64(400_000000), d.Gross.Minor())
	assert.Equal(t, int64(2_000000), d.Fee.Minor())
	assert.Equal(t, d.Gross.Minor(), d.Fee.Minor()+d.Net.Minor())

	// Fixed positional: round 1 → member 0, round 5 → member 0 (cycle).
	d, err = circle.Distribute(circle.PayoutTypeFixed, members, nil, nil, nil, contrib, zero, 5, 0)
	require.NoError(t, err)
	assert.Equal(t, members[0].UserID, d.RecipientID)

	// Auction: highest bid wins.
	bids := map[uuid.UUID]money.Money{
		members[0].UserID: mustMoney(t, 10),
		members[2].UserID: mustMoney(t, 50),
	}
	d, err = circle.Distribute(circle.PayoutTypeAuction, members, nil, bids, nil, contrib, zero, 1, 0)
	require.NoError(t, err)
	assert.Equal(t, members[2].UserID, d.RecipientID)

	// Vote: majority wins.
	votes := map[uuid.UUID]int{members[1].UserID: 3, members[3].UserID: 1}
	d, err = circle.Distribute(circle.PayoutTypeVote, members, nil, nil, votes, contrib, zero, 1, 0)
	require.NoError(t, err)
	assert.Equal(t, members[1].UserID, d.RecipientID)

	// Unknown type and empty roster fail.
	_, err = circle.Distribute("bogus", members, nil, nil, nil, contrib, zero, 1, 0)
	require.Error(t, err)
	_, err = circle.Distribute(circle.PayoutTypeFixed, nil, nil, nil, nil, contrib, zero, 1, 0)
	require.Error(t, err)
}
