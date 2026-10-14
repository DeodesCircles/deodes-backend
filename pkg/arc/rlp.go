package arc

import (
	"bytes"
	"math/big"
)

// rlpEncode encodes one RLP item: a byte string, or a list of already-encoded
// items when list is true. Only the short/long string and list forms are
// needed for transactions.
func rlpEncode(payload []byte, list bool) []byte {
	// Single byte below 0x80 is its own encoding.
	if !list && len(payload) == 1 && payload[0] < 0x80 {
		return []byte{payload[0]}
	}
	base := byte(0x80)
	if list {
		base = 0xc0
	}
	if len(payload) <= 55 {
		return append([]byte{base + byte(len(payload))}, payload...)
	}
	lenBytes := big.NewInt(int64(len(payload))).Bytes()
	out := []byte{base + 55 + byte(len(lenBytes))}
	out = append(out, lenBytes...)
	return append(out, payload...)
}

// rlpString encodes a byte string, stripping leading zeroes for integers.
func rlpUint(v *big.Int) []byte {
	if v == nil || v.Sign() == 0 {
		return []byte{0x80}
	}
	b := bytes.TrimLeft(v.Bytes(), "\x00")
	return rlpEncode(b, false)
}

// rlpBytes encodes arbitrary bytes (empty = 0x80).
func rlpBytes(b []byte) []byte {
	if len(b) == 0 {
		return []byte{0x80}
	}
	return rlpEncode(b, false)
}

// rlpAddress encodes a 20-byte address (never empty).
func rlpAddress(a [20]byte) []byte {
	return rlpEncode(a[:], false)
}
