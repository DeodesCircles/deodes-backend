package indexer_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/domain/circle"
	"github.com/deodes/backend/internal/domain/contribution"
	"github.com/deodes/backend/internal/domain/payout"
	"github.com/deodes/backend/internal/indexer"
	"github.com/deodes/backend/pkg/arc"
)

func encodeAddr(a string) string {
	raw, _ := hex.DecodeString(strings.TrimPrefix(a, "0x"))
	padded := append(make([]byte, 12), raw...)
	return "0x" + hex.EncodeToString(padded)
}

func encodeU256(v uint64) string {
	var word [32]byte
	for i := 0; i < 8; i++ {
		word[31-i] = byte(v >> (8 * i))
	}
	return "0x" + hex.EncodeToString(word[:])
}

func topic0Of(name string) string {
	for _, s := range indexer.Schemas {
		if s.Name == name {
			return s.Topic0
		}
	}
	return ""
}

func TestSchemas_TwelveFrozenEvents(t *testing.T) {
	require.Len(t, indexer.Schemas, 12)
	seen := map[string]bool{}
	for _, s := range indexer.Schemas {
		require.NotEmpty(t, s.Signature)
		require.True(t, strings.HasPrefix(s.Topic0, "0x") && len(s.Topic0) == 66, s.Name)
		require.False(t, seen[s.Topic0], "duplicate topic0 for %s", s.Name)
		seen[s.Topic0] = true
		// Contract: circle indexed first everywhere (single-topic filter).
		require.GreaterOrEqual(t, len(s.Params), 1)
		require.Equal(t, "circle", s.Params[0].Name, s.Name)
		require.True(t, s.Params[0].Indexed, s.Name)
	}
}

// Topic0 goldens cross-checked with `cast keccak` (independent impl).
// A signature change here means IDeodesEvents.sol drifted — fix the schema,
// never the golden.
func TestSchemas_Topic0Goldens(t *testing.T) {
	goldens := map[string]string{
		"CircleCreated":        "0xd8d91ef7205e69709b41b7d04828958b77ad420e3ddb36ce51b7e94c3b08d849",
		"ContributionReceived": "0xe482e8d853bcf23b76d7656a12af7c09cc6820a4683d6c48851261c2b12323b3",
		"PayoutExecuted":       "0x102ce461c824a1021c2d1b210c32a2de5a8de51f34656b969bf730d789112b48",
	}
	for _, s := range indexer.Schemas {
		if want, ok := goldens[s.Name]; ok {
			assert.Equal(t, want, s.Topic0, s.Name)
		}
	}
}

func TestDecodeLog_ContributionReceived(t *testing.T) {
	circleAddr := "0x70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4"
	member := "0x51b45Ba8570551e0D1fe818227d8bAa8b556694D"
	ev, ok, err := indexer.DecodeLog(
		[]string{topic0Of("ContributionReceived"), encodeAddr(circleAddr), encodeAddr(member)},
		encodeU256(250_000000),
	)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "ContributionReceived", ev.Schema.Name)
	// Chain topics are lowercase hex; decoded addresses compare case-insensitively.
	assert.True(t, strings.EqualFold(circleAddr, ev.Fields["circle"]))
	assert.True(t, strings.EqualFold(member, ev.Fields["member"]))
	assert.Equal(t, "250000000", ev.Fields["amount"])
}

func TestDecodeLog_PayoutExecutedAndVote(t *testing.T) {
	ev, ok, err := indexer.DecodeLog(
		[]string{topic0Of("PayoutExecuted"), encodeAddr("0x70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4"), encodeAddr("0x51b45Ba8570551e0D1fe818227d8bAa8b556694D")},
		encodeU256(1000)+strings.TrimPrefix(encodeU256(50), "0x"),
	)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "1000", ev.Fields["net"])
	assert.Equal(t, "50", ev.Fields["fee"])

	ev, ok, err = indexer.DecodeLog(
		[]string{topic0Of("VoteCast"), encodeAddr("0x70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4"), encodeAddr("0x51b45Ba8570551e0D1fe818227d8bAa8b556694D")},
		encodeU256(1),
	)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "true", ev.Fields["support"])
}

func TestDecodeLog_UnknownAndMalformed(t *testing.T) {
	_, ok, err := indexer.DecodeLog([]string{"0x" + strings.Repeat("ab", 32)}, "0x")
	require.NoError(t, err)
	require.False(t, ok, "foreign topic0 must be skipped, not deadlettered")

	_, ok, err = indexer.DecodeLog(nil, "0x")
	require.NoError(t, err)
	require.False(t, ok)

	// Wrong indexed-topic count for a known event → decode error.
	_, _, err = indexer.DecodeLog([]string{topic0Of("MemberJoined")}, "0x")
	require.Error(t, err)
}

// fakeEth scripts chain responses.
type fakeEth struct {
	latest   uint64
	logs     []arc.Log
	receipts map[string]*arc.Receipt
	ranges   [][2]uint64
}

func (f *fakeEth) BlockNumber(_ context.Context) (uint64, error) { return f.latest, nil }

func (f *fakeEth) GetLogs(_ context.Context, from, to uint64, _ []string, _ [][]string) ([]arc.Log, error) {
	f.ranges = append(f.ranges, [2]uint64{from, to})
	var out []arc.Log
	for _, l := range f.logs {
		if l.BlockNumber >= from && l.BlockNumber <= to {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeEth) GetTransactionReceipt(_ context.Context, tx string) (*arc.Receipt, error) {
	if r, ok := f.receipts[tx]; ok {
		return r, nil
	}
	return &arc.Receipt{TransactionHash: tx, Status: 1, Found: true}, nil
}

// fakeRepo is an in-memory Repository with real dedup semantics.
type fakeRepo struct {
	cursor    map[uint64]uint64
	events    map[string]indexer.PersistedEvent
	dead      []indexer.Deadletter
	deleted   [][2]any
	inserts   int
	insertErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{cursor: map[uint64]uint64{}, events: map[string]indexer.PersistedEvent{}}
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func (f *fakeRepo) GetNextFrom(_ context.Context, chainID uint64, start uint64) (uint64, error) {
	if last, ok := f.cursor[chainID]; ok {
		return last + 1, nil
	}
	return start, nil
}

func (f *fakeRepo) SetLastBlock(_ context.Context, chainID uint64, block uint64) error {
	if cur, ok := f.cursor[chainID]; !ok || block > cur {
		f.cursor[chainID] = block
	}
	return nil
}

func (f *fakeRepo) Insert(_ context.Context, e indexer.PersistedEvent) (bool, error) {
	if f.insertErr != nil {
		return false, f.insertErr
	}
	k := e.TxHash + "/" + itoa(e.LogIndex)
	if _, ok := f.events[k]; ok {
		return false, nil
	}
	f.events[k] = e
	f.inserts++
	return true, nil
}

func (f *fakeRepo) DeleteByTxLog(_ context.Context, tx string, li uint64) error {
	k := tx + "/" + itoa(li)
	delete(f.events, k)
	f.deleted = append(f.deleted, [2]any{tx, li})
	return nil
}

func (f *fakeRepo) InsertDeadletter(_ context.Context, d indexer.Deadletter) error {
	f.dead = append(f.dead, d)
	return nil
}

type fakeContribs struct {
	contribution.Repository
	rows     map[string]*contribution.Contribution
	verified []uuid.UUID
	failed   []uuid.UUID
	status   map[uuid.UUID]contribution.ContributionStatus
}

func (f *fakeContribs) FindByTxnHash(_ context.Context, tx string) (*contribution.Contribution, error) {
	if c, ok := f.rows[tx]; ok {
		return c, nil
	}
	return nil, sql.ErrNoRows
}

func (f *fakeContribs) UpdateVerificationStatus(_ context.Context, id uuid.UUID, ok bool, _ contribution.VerificationStatus) error {
	if ok {
		f.verified = append(f.verified, id)
	} else {
		f.failed = append(f.failed, id)
	}
	return nil
}

func (f *fakeContribs) UpdateStatus(_ context.Context, id uuid.UUID, st contribution.ContributionStatus, _ string) error {
	if f.status == nil {
		f.status = map[uuid.UUID]contribution.ContributionStatus{}
	}
	f.status[id] = st
	return nil
}

type fakePayouts struct {
	payout.Repository
	rows     map[string]*payout.Payout
	verified []uuid.UUID
}

func (f *fakePayouts) FindByTxnHash(_ context.Context, tx string) (*payout.Payout, error) {
	if p, ok := f.rows[tx]; ok {
		return p, nil
	}
	return nil, sql.ErrNoRows
}

func (f *fakePayouts) UpdateVerificationStatus(_ context.Context, id uuid.UUID, ok bool, _ payout.VerificationStatus) error {
	if ok {
		f.verified = append(f.verified, id)
	}
	return nil
}

type fakeCircles struct {
	circle.Repository
	byContract map[string]*circle.Circle
}

func (f *fakeCircles) FindByContractID(_ context.Context, addr string) (*circle.Circle, error) {
	for k, c := range f.byContract {
		if strings.EqualFold(k, addr) {
			return c, nil
		}
	}
	return nil, sql.ErrNoRows
}

type fakeCast struct {
	calls []string
}

func (f *fakeCast) BroadcastChainEvent(_ context.Context, circleID, eventType string, _ map[string]any) {
	f.calls = append(f.calls, circleID+"/"+eventType)
}

func testLog(block uint64, tx string, li uint64, topics []string, data string) arc.Log {
	return arc.Log{
		Address: "0x70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4",
		Topics:  topics, Data: data, BlockNumber: block,
		TransactionHash: tx, LogIndex: li,
	}
}

func contribTopics() []string {
	return []string{topic0Of("ContributionReceived"),
		encodeAddr("0x70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4"),
		encodeAddr("0x51b45Ba8570551e0D1fe818227d8bAa8b556694D")}
}

func testPoller(eth *fakeEth, repo *fakeRepo, fc *fakeContribs, fp *fakePayouts, fcir *fakeCircles, cast *fakeCast) *indexer.Poller {
	return indexer.NewPoller(eth, repo, fc, fp, fcir, cast, indexer.Config{
		ChainID: 5042002,
		Addresses: []string{
			"0x70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4",
		},
		BatchBlocks: 10, MaxBatchesPerTick: 100, ReorgWindow: 2, PollInterval: time.Second,
	})
}

func TestPoller_BackfillPersistsAndAdvancesCursor(t *testing.T) {
	ctx := context.Background()
	eth := &fakeEth{latest: 100}
	eth.logs = []arc.Log{
		testLog(90, "0xtx1", 0, contribTopics(), encodeU256(250_000000)),
		testLog(95, "0xtx2", 3, contribTopics(), encodeU256(100)),
	}
	repo := newFakeRepo()
	cid := uuid.New()
	fc := &fakeContribs{rows: map[string]*contribution.Contribution{
		"0xtx1": {ID: uuid.New()}, "0xtx2": {ID: uuid.New()},
	}}
	fcir := &fakeCircles{byContract: map[string]*circle.Circle{
		"0x70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4": {ID: cid},
	}}
	cast := &fakeCast{}
	p := testPoller(eth, repo, fc, &fakePayouts{}, fcir, cast)

	require.NoError(t, p.RunOnce(ctx))
	assert.Equal(t, 2, repo.inserts)
	// Cursor stops at latest - reorgWindow.
	assert.Equal(t, uint64(98), repo.cursor[5042002])
	assert.Len(t, fc.verified, 2)
	// Broadcast resolved the circle UUID.
	assert.Contains(t, cast.calls, cid.String()+"/chain.contribution_received")
}

func TestPoller_DuplicateDeliveryDedupsSideEffects(t *testing.T) {
	ctx := context.Background()
	eth := &fakeEth{latest: 50}
	eth.logs = []arc.Log{testLog(40, "0xtx9", 0, contribTopics(), encodeU256(5))}
	repo := newFakeRepo()
	fc := &fakeContribs{rows: map[string]*contribution.Contribution{"0xtx9": {ID: uuid.New()}}}
	p := testPoller(eth, repo, fc, &fakePayouts{}, &fakeCircles{}, &fakeCast{})

	require.NoError(t, p.RunOnce(ctx))
	require.NoError(t, p.RunOnce(ctx))
	assert.Equal(t, 1, repo.inserts, "second delivery must be a dedup no-op")
	assert.Len(t, fc.verified, 1, "settlement must run exactly once")
}

func TestPoller_RemovedLogRollsBack(t *testing.T) {
	ctx := context.Background()
	eth := &fakeEth{latest: 50}
	good := testLog(40, "0xtxR", 1, contribTopics(), encodeU256(5))
	eth.logs = []arc.Log{good}
	repo := newFakeRepo()
	fc := &fakeContribs{rows: map[string]*contribution.Contribution{"0xtxR": {ID: uuid.New()}}}
	p := testPoller(eth, repo, fc, &fakePayouts{}, &fakeCircles{}, &fakeCast{})

	require.NoError(t, p.RunOnce(ctx))
	require.Equal(t, 1, repo.inserts)

	gone := good
	gone.Removed = true
	eth.logs = []arc.Log{gone}
	// Force re-poll of block 40 by rewinding the cursor.
	repo.cursor[5042002] = 39
	require.NoError(t, p.RunOnce(ctx))
	assert.Len(t, repo.deleted, 1)
	assert.Equal(t, "0xtxR", repo.deleted[0][0])
	require.NotEmpty(t, repo.dead)
	assert.Equal(t, "reorg", repo.dead[len(repo.dead)-1].Stage)
}

func TestPoller_RevertedTxMarksFailed(t *testing.T) {
	ctx := context.Background()
	eth := &fakeEth{latest: 50, receipts: map[string]*arc.Receipt{
		"0xtxBad": {TransactionHash: "0xtxBad", Status: 0, Found: true},
	}}
	eth.logs = []arc.Log{testLog(40, "0xtxBad", 0, contribTopics(), encodeU256(5))}
	repo := newFakeRepo()
	id := uuid.New()
	fc := &fakeContribs{rows: map[string]*contribution.Contribution{"0xtxBad": {ID: id}}}
	p := testPoller(eth, repo, fc, &fakePayouts{}, &fakeCircles{}, &fakeCast{})

	require.NoError(t, p.RunOnce(ctx))
	assert.Empty(t, fc.verified)
	require.Len(t, fc.failed, 1)
}

func TestPoller_UnknownSkippedMalformedDeadlettered(t *testing.T) {
	ctx := context.Background()
	eth := &fakeEth{latest: 50}
	eth.logs = []arc.Log{
		testLog(40, "0xforeign", 0, []string{"0x" + strings.Repeat("ab", 32)}, "0x"),
		testLog(41, "0xbad", 0, []string{topic0Of("MemberJoined")}, "0x"),
	}
	repo := newFakeRepo()
	p := testPoller(eth, repo, &fakeContribs{}, &fakePayouts{}, &fakeCircles{}, &fakeCast{})

	require.NoError(t, p.RunOnce(ctx))
	assert.Equal(t, 0, repo.inserts)
	require.Len(t, repo.dead, 1)
	assert.Equal(t, "decode", repo.dead[0].Stage)
}
