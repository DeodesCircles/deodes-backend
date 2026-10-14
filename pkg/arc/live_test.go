package arc

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Live testnet probe. Skipped unless DEODES_ARC_LIVE_PROBE=1 — CI must never
// touch the network. Run manually: DEODES_ARC_LIVE_PROBE=1 go test ./pkg/arc/ -run TestLive.
//
// What it proves against REAL Arc testnet:
//   - chain ID pins to 5042002 (testnet keys can never spend on mainnet)
//   - USDC.decimals() == 6 on-chain (the money-critical assumption)
//   - balanceOf decodes for a real address (escrow starts at 0)
func TestLiveTestnetProbe(t *testing.T) {
	if os.Getenv("DEODES_ARC_LIVE_PROBE") != "1" {
		t.Skip("set DEODES_ARC_LIVE_PROBE=1 to probe live testnet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := NewClient("", ChainIDTestnet)
	id, err := c.ChainID(ctx)
	require.NoError(t, err)
	assert.Equal(t, ChainIDTestnet, id)

	const usdc = "0x3600000000000000000000000000000000000000"
	dec, err := ReadERC20Decimals(ctx, c, usdc)
	require.NoError(t, err, "decimals() must read on-chain")
	assert.Equal(t, uint8(6), dec, "ERC-20 interface must use 6 decimals")

	bal, err := USDCBalanceOf(ctx, c, usdc, "0x51b45Ba8570551e0D1fe818227d8bAa8b556694D")
	require.NoError(t, err)
	t.Logf("escrow balance: %s USDC", bal)
}
