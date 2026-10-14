package arc

import (
	"context"
	"fmt"
	"math/big"
	"strings"
)

// priorityFeeWei is the tip attached to keeper debits. Arc's fee market runs
// on minimal priority fees; 1 Gwei confirms promptly without bidding wars.
var priorityFeeWei = big.NewInt(1_000_000_000)

// FeeQuote is a priced EIP-1559 fee pair honoring the 20 Gwei floor.
type FeeQuote struct {
	MaxPriorityFeePerGas *big.Int
	MaxFeePerGas         *big.Int
}

// QuoteFees reads the pending base fee and returns maxFee = max(2*base +
// priority, 20 Gwei floor). The floor is enforced even if the node reports a
// lower base, so txs are never stuck below mempool policy.
func QuoteFees(ctx context.Context, c *Client) (*FeeQuote, error) {
	base, err := c.LatestBaseFee(ctx)
	if err != nil {
		return nil, err
	}
	maxFee := new(big.Int).Add(new(big.Int).Mul(base, big.NewInt(2)), priorityFeeWei)
	floor := new(big.Int).SetUint64(MinMaxFeePerGasWei)
	if maxFee.Cmp(floor) < 0 {
		maxFee = floor
	}
	return &FeeQuote{
		MaxPriorityFeePerGas: new(big.Int).Set(priorityFeeWei),
		MaxFeePerGas:         maxFee,
	}, nil
}

// USDCBalanceOf returns a holder's USDC balance in human units ("1.50").
func USDCBalanceOf(ctx context.Context, c *Client, usdcContract, holder string) (string, error) {
	if strings.TrimSpace(usdcContract) == "" {
		return "", fmt.Errorf("arc usdc contract address is not configured")
	}
	if _, err := MustHexToAddress(usdcContract); err != nil {
		return "", err
	}
	holderAddr, err := MustHexToAddress(holder)
	if err != nil {
		return "", err
	}
	raw, err := c.Call(ctx, usdcContract, encodeBalanceOf(holderAddr))
	if err != nil {
		return "", err
	}
	v, err := decodeUint256(raw)
	if err != nil {
		return "", err
	}
	return FormatUSDC(v), nil
}

// ERC20Decimals reads the ERC-20 decimals() on-chain. The docs insist on
// this over hardcoding: native gas uses 18, the interface uses 6, and only
// the contract is authoritative.
func ReadERC20Decimals(ctx context.Context, c *Client, usdcContract string) (uint8, error) {
	if strings.TrimSpace(usdcContract) == "" {
		return 0, fmt.Errorf("arc usdc contract address is not configured")
	}
	raw, err := c.Call(ctx, usdcContract, encodeDecimals())
	if err != nil {
		return 0, err
	}
	v, err := decodeUint256(raw)
	if err != nil {
		return 0, err
	}
	if !v.IsUint64() || v.Uint64() > 255 {
		return 0, fmt.Errorf("implausible decimals value %s", v.String())
	}
	return uint8(v.Uint64()), nil
}

// USDCTransfer sends amountHuman USDC from privKey to `to`, returning the tx
// hash. It verifies chain ID first (testnet keys can never spend on mainnet),
// uses pending-nonce, estimated gas, and floored fees. Amounts parse exactly;
// no floats cross this boundary.
func USDCTransfer(ctx context.Context, c *Client, usdcContract string, privKey []byte, to, amountHuman string) (string, error) {
	if strings.TrimSpace(usdcContract) == "" {
		return "", fmt.Errorf("arc usdc contract address is not configured")
	}
	if _, err := c.ChainID(ctx); err != nil {
		return "", err
	}
	toAddr, err := MustHexToAddress(to)
	if err != nil {
		return "", err
	}
	amount, err := ParseUSDC(amountHuman)
	if err != nil {
		return "", err
	}
	contractAddr, err := MustHexToAddress(usdcContract)
	if err != nil {
		return "", err
	}
	data := encodeTransfer(toAddr, amount)

	sender, err := addressFromPrivKey(privKey)
	if err != nil {
		return "", err
	}
	nonce, err := c.PendingNonce(ctx, sender)
	if err != nil {
		return "", err
	}
	gas, err := c.EstimateGas(ctx, sender, usdcContract, data)
	if err != nil {
		return "", err
	}
	// 20% headroom on the estimate; ERC-20 transfers are deterministic.
	gas = gas + gas/5
	fees, err := QuoteFees(ctx, c)
	if err != nil {
		return "", err
	}
	tx := &EIP1559Tx{
		ChainID:              new(big.Int).SetUint64(c.chainID),
		Nonce:                nonce,
		MaxPriorityFeePerGas: fees.MaxPriorityFeePerGas,
		MaxFeePerGas:         fees.MaxFeePerGas,
		GasLimit:             gas,
		To:                   contractAddr,
		Value:                big.NewInt(0),
		Data:                 data,
	}
	rawTx, _, err := tx.Sign(privKey)
	if err != nil {
		return "", err
	}
	return c.SendRawTransaction(ctx, rawTx)
}
