package subscription

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/internal/domain/circle"
)

func TestLoop_NilKeeperIsNoOp(t *testing.T) {
	l := NewLoop(LoopConfig{Redis: nil})
	l.Start(context.Background())
	l.Stop()
}

func TestLoop_DebitsDueThenStops(t *testing.T) {
	repo := newMemRepo(dueSub())
	w := &fakeWallet{balance: 500}
	c := &fakeContrib{}
	cir := &fakeCircles{circles: map[string]*circle.Circle{"c-1": activeCircle(3)}}
	k, err := NewKeeper(KeeperConfig{
		Repo: repo, Wallets: w, Contrib: c, Circles: cir,
		EscrowAddress: "0x0000000000000000000000000000000000000001",
		Clock:         func() time.Time { return time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)

	l := NewLoop(LoopConfig{Keeper: k, Redis: nil, Interval: 20 * time.Millisecond})
	l.Start(context.Background())
	require.Eventually(t, func() bool { return w.sends >= 1 }, 5*time.Second, 10*time.Millisecond)
	l.Stop()

	got, err := repo.FindByID(context.Background(), "s-1")
	require.NoError(t, err)
	assert.Equal(t, 3, got.LastRound)
}
