package arc

import (
	"math/big"
	"testing"
)

// Phase 0.2 freeze: the 1e12 gap between native wei (18) and the ERC-20
// interface (6) must convert exactly, and invalid input must fail visibly.
func TestHexUint_ZeroRenders0x0(t *testing.T) {
	// Regression: TrimLeft stripped every digit, producing bare "0x",
	// which nodes reject with -32602 (mainnet indexer outage, 2.6).
	if got := hexUint(big.NewInt(0)); got != "0x0" {
		t.Fatalf("hexUint(0) = %q, want 0x0", got)
	}
	if got := hexUint(nil); got != "0x0" {
		t.Fatalf("hexUint(nil) = %q, want 0x0", got)
	}
	if got := hexUint(big.NewInt(49)); got != "0x31" {
		t.Fatalf("hexUint(49) = %q, want 0x31", got)
	}
	from, to := ethBlockRange(0, 49)
	if from != "0x0" || to != "0x31" {
		t.Fatalf("ethBlockRange(0,49) = %q,%q", from, to)
	}
}

func TestUnitLadderConstants(t *testing.T) {
	if WeiPerToken != 1_000_000_000_000_000_000 {
		t.Fatalf("WeiPerToken = %d, want 10^18", WeiPerToken)
	}
	if ERC20PerToken != 1_000_000 {
		t.Fatalf("ERC20PerToken = %d, want 10^6", ERC20PerToken)
	}
	if WeiPerERC20Unit != 1_000_000_000_000 {
		t.Fatalf("WeiPerERC20Unit = %d, want 10^12", WeiPerERC20Unit)
	}
	if uint64(WeiPerToken)/uint64(ERC20PerToken) != WeiPerERC20Unit {
		t.Fatal("ladder inconsistent: 10^18 / 10^6 must equal 10^12")
	}
}

func TestERC20ToWei(t *testing.T) {
	// 10 USDC (V0 contribution) in interface units -> 10e18 wei.
	wei, overflow := ERC20ToWei(10_000_000)
	if overflow {
		t.Fatal("unexpected overflow for 10 USDC")
	}
	want, _ := new(big.Int).SetString("10000000000000000000", 10)
	if wei.Cmp(want) != 0 {
		t.Fatalf("ERC20ToWei(10e6) = %s, want %s", wei, want)
	}

	if _, overflow := ERC20ToWei(-1); !overflow {
		t.Fatal("negative input must report overflow")
	}
	// The full int64 range fits uint256 after 1e12 scaling
	// (max ~9.2e30 << 2^256 ~1.2e77), so the overflow branch guards
	// future widening, not current inputs: it must not fire here.
	maxInt64 := int64(9223372036854775807)
	if _, overflow := ERC20ToWei(maxInt64); overflow {
		t.Fatal("max int64 must convert without overflow")
	}
}

func TestWeiToERC20Units(t *testing.T) {
	tenUSDC, _ := new(big.Int).SetString("10000000000000000000", 10)
	units, ok := WeiToERC20Units(tenUSDC)
	if !ok || units != 10_000_000 {
		t.Fatalf("WeiToERC20Units(10e18) = %d,%v, want 10000000,true", units, ok)
	}

	// Sub-unit dust truncates instead of rounding up.
	dusty := new(big.Int).Add(tenUSDC, big.NewInt(999_999_999_999))
	if units, ok := WeiToERC20Units(dusty); !ok || units != 10_000_000 {
		t.Fatalf("dust must truncate: got %d,%v", units, ok)
	}

	if _, ok := WeiToERC20Units(big.NewInt(-1)); ok {
		t.Fatal("negative wei must be rejected")
	}
	if _, ok := WeiToERC20Units(nil); ok {
		t.Fatal("nil wei must be rejected")
	}
}
