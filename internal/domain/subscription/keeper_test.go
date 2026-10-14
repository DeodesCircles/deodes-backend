package subscription

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/domain/circle"
	"github.com/deodes/backend/internal/domain/contribution"
	"github.com/deodes/backend/internal/domain/wallet"
)

// memRepo is an in-memory Repository honoring RecordDebit CAS semantics.
type memRepo struct {
	mu   sync.Mutex
	subs map[string]*Subscription
}

func newMemRepo(subs ...*Subscription) *memRepo {
	m := &memRepo{subs: map[string]*Subscription{}}
	for _, s := range subs {
		cp := *s
		m.subs[s.ID] = &cp
	}
	return m
}

func (m *memRepo) Create(ctx context.Context, s *Subscription) error { return nil }
func (m *memRepo) FindByID(ctx context.Context, id string) (*Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	cp := *s
	return &cp, nil
}
func (m *memRepo) FindByUserCircle(ctx context.Context, userID, circleID string) (*Subscription, error) {
	return nil, fmt.Errorf("not found")
}
func (m *memRepo) ListByUser(ctx context.Context, userID string) ([]Subscription, error) {
	return nil, nil
}
func (m *memRepo) ListDue(ctx context.Context, now time.Time, limit int) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Subscription
	for _, s := range m.subs {
		if s.Status == StatusActive && !s.NextDebitAt.After(now) {
			out = append(out, *s)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
func (m *memRepo) UpdateStatus(ctx context.Context, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	s.Status = status
	return nil
}
func (m *memRepo) Postpone(ctx context.Context, id string, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	s.NextDebitAt = next
	return nil
}
func (m *memRepo) ListRemindable(ctx context.Context, now time.Time, window time.Duration) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Subscription
	for _, s := range m.subs {
		if s.Status != StatusActive {
			continue
		}
		if s.NextDebitAt.After(now) && !s.NextDebitAt.After(now.Add(window)) {
			if s.LastRemindedAt == nil || s.LastRemindedAt.Before(s.NextDebitAt.Add(-window)) {
				out = append(out, *s)
			}
		}
	}
	return out, nil
}
func (m *memRepo) MarkReminded(ctx context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	s.LastRemindedAt = &now
	return nil
}
func (m *memRepo) RecordDebit(ctx context.Context, id string, expectedRound, round int, amount string, next time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return false, fmt.Errorf("not found")
	}
	if s.LastRound != expectedRound {
		return false, nil
	}
	s.LastRound = round
	s.LastDebitAt = &next
	s.NextDebitAt = next
	return true, nil
}

type fakeWallet struct {
	balance float64
	sends   int
	sendErr error
}

func (f *fakeWallet) GetBalance(ctx context.Context, userID string) (*wallet.Balance, error) {
	return &wallet.Balance{USDC: f.balance}, nil
}
func (f *fakeWallet) SendPayment(ctx context.Context, userID string, seed []byte, dest, asset string, amount float64, memo, ip, agent string) (string, error) {
	f.sends++
	if f.sendErr != nil {
		return "", f.sendErr
	}
	if dest == "" || asset != "USDC" || amount <= 0 {
		return "", fmt.Errorf("bad send args")
	}
	return "0xtxhash", nil
}
func (f *fakeWallet) DeleteWallet(ctx context.Context, userID, walletID string) error { return nil }

var (
	_ WalletDebiter        = (*fakeWallet)(nil)
	_ ContributionRecorder = (*fakeContrib)(nil)
	_ CircleReader         = (*fakeCircles)(nil)
)

type fakeContrib struct {
	recorded  []contribution.RecordInput
	recordErr error
}

func (f *fakeContrib) Record(ctx context.Context, in contribution.RecordInput) (*contribution.Contribution, error) {
	if f.recordErr != nil {
		return nil, f.recordErr
	}
	f.recorded = append(f.recorded, in)
	return &contribution.Contribution{}, nil
}
func (f *fakeContrib) GetUserHistory(ctx context.Context, userID string, page, limit int) ([]contribution.Contribution, int, error) {
	return nil, 0, nil
}
func (f *fakeContrib) GetCircleHistory(ctx context.Context, circleID string, page, limit int) ([]contribution.Contribution, int, error) {
	return nil, 0, nil
}
func (f *fakeContrib) UpdateVerification(ctx context.Context, id string, v bool, s contribution.VerificationStatus) error {
	return nil
}

type fakeCircles struct {
	circles map[string]*circle.Circle
	err     error
}

func (f *fakeCircles) Get(ctx context.Context, id string) (*circle.Circle, error) {
	if f.err != nil {
		return nil, f.err
	}
	c, ok := f.circles[id]
	if !ok {
		return nil, fmt.Errorf("circle not found")
	}
	return c, nil
}

func testKeeper(t *testing.T, repo Repository, w *fakeWallet, c *fakeContrib, cir *fakeCircles) (*Keeper, *[]DebitEvent) {
	t.Helper()
	var events []DebitEvent
	k, err := NewKeeper(KeeperConfig{
		Repo: repo, Wallets: w, Contrib: c, Circles: cir,
		EscrowAddress: "0x0000000000000000000000000000000000000001",
		Clock:         func() time.Time { return time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC) },
		OnEvent: func(_ context.Context, e DebitEvent) {
			events = append(events, e)
		},
	})
	require.NoError(t, err)
	return k, &events
}

func activeCircle(round int) *circle.Circle {
	return &circle.Circle{
		Status: circle.CircleStatusActive, CurrentRound: round,
		Frequency: circle.FrequencyWeekly, AutoDebit: true,
	}
}

func dueSub() *Subscription {
	return &Subscription{
		ID: "s-1", UserID: "u-1", CircleID: "c-1",
		Amount: "100.0000000", CapTotal: "1000.0000000", CollectedTotal: "0",
		Status: StatusActive, NextDebitAt: time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC),
		LastRound: 2,
	}
}

func TestKeeper_HappyPath(t *testing.T) {
	repo := newMemRepo(dueSub())
	w := &fakeWallet{balance: 500}
	c := &fakeContrib{}
	cir := &fakeCircles{circles: map[string]*circle.Circle{"c-1": activeCircle(3)}}
	k, events := testKeeper(t, repo, w, c, cir)

	sum := k.RunOnce(context.Background(), 100)
	assert.Equal(t, RunSummary{Due: 1, Debited: 1}, sum)
	require.Len(t, c.recorded, 1)
	assert.Equal(t, 3, c.recorded[0].RoundNumber)
	assert.Equal(t, "0xtxhash", c.recorded[0].TxnHash)
	assert.True(t, *c.recorded[0].VerifiedOnchain)
	require.Len(t, *events, 1)
	assert.Equal(t, OutcomeDebited, (*events)[0].Outcome)

	got, err := repo.FindByID(context.Background(), "s-1")
	require.NoError(t, err)
	assert.Equal(t, 3, got.LastRound)
	assert.True(t, got.NextDebitAt.After(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)))
}

func TestKeeper_SkipsLowBalanceWithoutDebit(t *testing.T) {
	repo := newMemRepo(dueSub())
	w := &fakeWallet{balance: 50}
	c := &fakeContrib{}
	cir := &fakeCircles{circles: map[string]*circle.Circle{"c-1": activeCircle(3)}}
	k, events := testKeeper(t, repo, w, c, cir)

	sum := k.RunOnce(context.Background(), 100)
	assert.Equal(t, 1, sum.Skipped)
	assert.Equal(t, 0, w.sends)
	assert.Empty(t, c.recorded)
	assert.Equal(t, OutcomeSkippedLowBalance, (*events)[0].Outcome)
}

func TestKeeper_CatchesUpPaidRound(t *testing.T) {
	sub := dueSub()
	sub.LastRound = 3 // already paid (e.g. manual payment)
	repo := newMemRepo(sub)
	w := &fakeWallet{balance: 500}
	c := &fakeContrib{}
	cir := &fakeCircles{circles: map[string]*circle.Circle{"c-1": activeCircle(3)}}
	k, events := testKeeper(t, repo, w, c, cir)

	sum := k.RunOnce(context.Background(), 100)
	assert.Equal(t, 1, sum.Skipped)
	assert.Equal(t, 0, w.sends)
	assert.Equal(t, OutcomeCaughtUp, (*events)[0].Outcome)
}

func TestKeeper_CompletesAtCap(t *testing.T) {
	sub := dueSub()
	sub.CollectedTotal = "950.0000000" // 950 + 100 > 1000 cap
	repo := newMemRepo(sub)
	w := &fakeWallet{balance: 5000}
	c := &fakeContrib{}
	cir := &fakeCircles{circles: map[string]*circle.Circle{"c-1": activeCircle(3)}}
	k, events := testKeeper(t, repo, w, c, cir)

	sum := k.RunOnce(context.Background(), 100)
	assert.Equal(t, 1, sum.Skipped)
	assert.Equal(t, 0, w.sends)
	assert.Equal(t, OutcomeSkippedCapped, (*events)[0].Outcome)
	got, _ := repo.FindByID(context.Background(), "s-1")
	assert.Equal(t, StatusCompleted, got.Status)
}

func TestKeeper_CompletesFinishedCircle(t *testing.T) {
	repo := newMemRepo(dueSub())
	w := &fakeWallet{balance: 500}
	c := &fakeContrib{}
	done := activeCircle(3)
	done.Status = circle.CircleStatusCompleted
	cir := &fakeCircles{circles: map[string]*circle.Circle{"c-1": done}}
	k, _ := testKeeper(t, repo, w, c, cir)

	sum := k.RunOnce(context.Background(), 100)
	assert.Equal(t, 1, sum.Skipped)
	assert.Equal(t, 0, w.sends)
	got, _ := repo.FindByID(context.Background(), "s-1")
	assert.Equal(t, StatusCompleted, got.Status)
}

func TestKeeper_SkipsFlexibleCircleWithoutCompleting(t *testing.T) {
	repo := newMemRepo(dueSub())
	w := &fakeWallet{balance: 500}
	c := &fakeContrib{}
	flex := activeCircle(3)
	flex.AutoDebit = false
	cir := &fakeCircles{circles: map[string]*circle.Circle{"c-1": flex}}
	k, events := testKeeper(t, repo, w, c, cir)

	sum := k.RunOnce(context.Background(), 100)
	assert.Equal(t, 1, sum.Skipped)
	assert.Equal(t, 0, w.sends)
	assert.Equal(t, OutcomeCaughtUp, (*events)[0].Outcome)
	// Consent row survives: flipping back to Auto resumes cleanly.
	got, _ := repo.FindByID(context.Background(), "s-1")
	assert.Equal(t, StatusActive, got.Status)
}

func TestKeeper_SendFailureDoesNotAdvance(t *testing.T) {
	repo := newMemRepo(dueSub())
	w := &fakeWallet{balance: 500, sendErr: fmt.Errorf("rpc down")}
	c := &fakeContrib{}
	cir := &fakeCircles{circles: map[string]*circle.Circle{"c-1": activeCircle(3)}}
	k, events := testKeeper(t, repo, w, c, cir)

	sum := k.RunOnce(context.Background(), 100)
	assert.Equal(t, 1, sum.Failed)
	assert.Empty(t, c.recorded)
	assert.Equal(t, OutcomeFailed, (*events)[0].Outcome)
	got, _ := repo.FindByID(context.Background(), "s-1")
	assert.Equal(t, 2, got.LastRound) // unadvanced: safe to retry, manual reconcile via event
}

func TestNewKeeper_RejectsBadEscrow(t *testing.T) {
	_, err := NewKeeper(KeeperConfig{
		Repo: newMemRepo(), Wallets: &fakeWallet{}, Contrib: &fakeContrib{},
		Circles: &fakeCircles{}, EscrowAddress: "not-an-address",
	})
	assert.Error(t, err)

	_, err = NewKeeper(KeeperConfig{EscrowAddress: "0x0000000000000000000000000000000000000001"})
	assert.Error(t, err)
}

func TestNewKeeper_RejectsEscrowEqualToTreasury(t *testing.T) {
	// Phase 0.6 freeze: fee vault and debit collector must be separate keys.
	_, err := NewKeeper(KeeperConfig{
		Repo: newMemRepo(), Wallets: &fakeWallet{}, Contrib: &fakeContrib{},
		Circles:         &fakeCircles{},
		EscrowAddress:   "0x51b45Ba8570551e0D1fe818227d8bAa8b556694D",
		TreasuryAddress: "0x51b45ba8570551e0d1fe818227d8baa8b556694d", // case differs: still equal
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must differ from protocol treasury")

	// Distinct keys pass validation.
	_, err = NewKeeper(KeeperConfig{
		Repo: newMemRepo(), Wallets: &fakeWallet{}, Contrib: &fakeContrib{},
		Circles:         &fakeCircles{},
		EscrowAddress:   "0x1111111111111111111111111111111111111111",
		TreasuryAddress: "0x2222222222222222222222222222222222222222",
	})
	require.NoError(t, err)
}

type fakeNotifier struct {
	due     int
	debited int
	failed  int
}

func (f *fakeNotifier) NotifyContributionDue(ctx context.Context, userID, circleID, amount string, dueIn time.Duration) error {
	f.due++
	return nil
}
func (f *fakeNotifier) NotifyDebited(ctx context.Context, userID, circleID, amount string, round int, txHash string) error {
	f.debited++
	return nil
}
func (f *fakeNotifier) NotifyDebitFailed(ctx context.Context, userID, circleID, amount string, round int, reason string) error {
	f.failed++
	return nil
}

func TestKeeper_RemindUpcomingOncePerDebit(t *testing.T) {
	sub := dueSub()
	sub.NextDebitAt = time.Date(2026, 10, 13, 20, 0, 0, 0, time.UTC) // +8h: inside window
	repo := newMemRepo(sub)
	k, _ := testKeeper(t, repo, &fakeWallet{}, &fakeContrib{},
		&fakeCircles{circles: map[string]*circle.Circle{"c-1": activeCircle(3)}})
	n := &fakeNotifier{}
	k.notifier = n

	sent, err := k.RemindUpcoming(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, sent)
	assert.Equal(t, 1, n.due)

	// Second sweep: already stamped, no duplicate reminder.
	sent, err = k.RemindUpcoming(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, sent)
	assert.Equal(t, 1, n.due)
}

func TestKeeper_RemindSkipsFarDebits(t *testing.T) {
	sub := dueSub()
	sub.NextDebitAt = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) // +7d: outside window
	repo := newMemRepo(sub)
	k, _ := testKeeper(t, repo, &fakeWallet{}, &fakeContrib{},
		&fakeCircles{circles: map[string]*circle.Circle{"c-1": activeCircle(3)}})
	n := &fakeNotifier{}
	k.notifier = n

	sent, err := k.RemindUpcoming(context.Background(), 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, sent)
}
