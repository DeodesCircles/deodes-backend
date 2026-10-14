// Package arc is the minimal Arc (EVM) client used by hosted wallets and the
// contribution keeper: USDC balance reads, USDC transfer submission, and the
// fee rules that make scheduled debits cost fractions of a cent.
//
// Arc specifics honored here (docs.arc.io): USDC uses 18 decimals natively
// and is the gas token, the mempool enforces a 20 Gwei maxFeePerGas floor,
// and transactions reach deterministic finality in under a second (no
// multi-confirmation waits).
package arc

import "math/big"

// Testnet and mainnet chain IDs (docs.arc.io/arc/references/connect-to-arc).
const (
	ChainIDMainnet uint64 = 5042
	ChainIDTestnet uint64 = 5042002
)

// Default RPC endpoints, overridable via config (arc.rpc_url).
const (
	DefaultMainnetRPC = "https://rpc.mainnet.arc.io"
	DefaultTestnetRPC = "https://rpc.testnet.arc.io"
)

// USDCDecimals is the NATIVE gas token precision (18). Contract calls below
// use ERC20Decimals instead — never interchange them (see abi.go).
const USDCDecimals = 18

// ERC20Decimals is the precision of the USDC ERC-20 interface at
// 0x3600...0000 (docs.arc.io/arc/references/contract-addresses.md), through
// which ALL balance reads and transfers in this package flow. Native (18) vs
// interface (6) confusion moves 1e12x the intended value.
const ERC20Decimals = 6

// MinMaxFeePerGasWei is the mempool maxFeePerGas floor enforced on Arc.
const MinMaxFeePerGasWei uint64 = 20_000_000_000 // 20 Gwei

// Canonical unit ladder (Phase 0.2 freeze, see docs/amount-units-freeze.md):
// native wei (18) <-> ERC-20 interface units (6). The 1e12 gap between them
// (and the 1e11 gap to legacy 7-decimal minor units in pkg/money) is the
// source of past 1e11-1e12x amount bugs. Always convert via ERC20ToWei /
// WeiToERC20Units below — never raw multiplication.
const (
	// WeiPerToken is 10^18: one whole USDC in native wei.
	WeiPerToken uint64 = 1_000_000_000_000_000_000
	// ERC20PerToken is 10^6: one whole USDC in ERC-20 interface units.
	ERC20PerToken uint64 = 1_000_000
	// WeiPerERC20Unit is 10^12: the exact gap between the two precisions.
	WeiPerERC20Unit uint64 = 1_000_000_000_000
)

// DefaultRPCForChain returns the default RPC endpoint for a chain ID.
func DefaultRPCForChain(chainID uint64) string {
	if chainID == ChainIDMainnet {
		return DefaultMainnetRPC
	}
	return DefaultTestnetRPC
}

// ERC20ToWei converts whole-token ERC-20 interface units (6 decimals) to
// native wei (18 decimals). It reports overflow instead of wrapping: callers
// must fail the transfer visibly rather than move a truncated amount.
func ERC20ToWei(units int64) (*big.Int, bool) {
	if units < 0 {
		return nil, true
	}
	wei := new(big.Int).Mul(big.NewInt(units), new(big.Int).SetUint64(WeiPerERC20Unit))
	if wei.BitLen() > 256 {
		return nil, true
	}
	return wei, false
}

// WeiToERC20Units converts native wei (18 decimals) down to ERC-20 interface
// units (6 decimals), truncating sub-unit dust. Negative input is rejected
// with ok=false so debt can never be manufactured by a sign error.
func WeiToERC20Units(wei *big.Int) (units int64, ok bool) {
	if wei == nil || wei.Sign() < 0 {
		return 0, false
	}
	q := new(big.Int).Quo(wei, new(big.Int).SetUint64(WeiPerERC20Unit))
	if !q.IsInt64() {
		return 0, false
	}
	return q.Int64(), true
}
