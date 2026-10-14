package arc

import (
	"fmt"
	"math/big"
)

// erc20TransferSelector is keccak("transfer(address,uint256)")[:4].
var erc20TransferSelector = functionSelector("transfer(address,uint256)")

// erc20BalanceOfSelector is keccak("balanceOf(address)")[:4].
var erc20BalanceOfSelector = functionSelector("balanceOf(address)")

// erc20DecimalsSelector is keccak("decimals()")[:4].
var erc20DecimalsSelector = functionSelector("decimals()")

func functionSelector(sig string) [4]byte {
	var out [4]byte
	copy(out[:], keccak256([]byte(sig))[:4])
	return out
}

// encodeTransfer builds ERC-20 transfer(to, amount) calldata.
func encodeTransfer(to [20]byte, amount *big.Int) []byte {
	call := make([]byte, 0, 4+32+32)
	call = append(call, erc20TransferSelector[:]...)
	call = append(call, leftPad(to[:], 32)...)
	call = append(call, leftPad(uint256Bytes(amount), 32)...)
	return call
}

// encodeBalanceOf builds ERC-20 balanceOf(holder) calldata.
func encodeBalanceOf(holder [20]byte) []byte {
	call := make([]byte, 0, 4+32)
	call = append(call, erc20BalanceOfSelector[:]...)
	call = append(call, leftPad(holder[:], 32)...)
	return call
}

// encodeDecimals builds ERC-20 decimals() calldata.
func encodeDecimals() []byte {
	call := make([]byte, 0, 4)
	call = append(call, erc20DecimalsSelector[:]...)
	return call
}

// decodeUint256 parses a 32-byte big-endian uint256 response.
func decodeUint256(raw []byte) (*big.Int, error) {
	if len(raw) != 32 {
		return nil, fmt.Errorf("uint256 response must be 32 bytes, got %d", len(raw))
	}
	return new(big.Int).SetBytes(raw), nil
}

func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b[len(b)-n:]
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}

func uint256Bytes(v *big.Int) []byte {
	if v == nil || v.Sign() < 0 {
		return make([]byte, 32)
	}
	return leftPad(v.Bytes(), 32)
}
