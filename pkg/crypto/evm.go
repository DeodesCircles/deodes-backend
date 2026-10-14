// Package crypto — EVM (Arc) key helpers.
//
// Arc is EVM-compatible with USDC (18 decimals) as the native gas token.
// Hosted per-user Arc wallets ("wallet address under us") are secp256k1
// keypairs derived deterministically from the same Argon2id seed as legacy
// Ed25519 wallets, but domain-separated by chain so a legacy seed never
// equals an Arc key. Addresses follow standard Ethereum rules: keccak256(uncompressed
// pubkey[1:])[12:] rendered as 0x-hex.
package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"
)

// DeriveArcSeed derives a 32-byte Arc seed from a base hex seed (as returned
// by DeriveWalletSeed) with chain separation ("arc-v1"). The base seed is
// hex-decoded first — callers must never copy ASCII hex into a 32-byte array.
func DeriveArcSeed(baseHexSeed string) ([32]byte, error) {
	var out [32]byte
	raw, err := hex.DecodeString(baseHexSeed)
	if err != nil {
		return out, fmt.Errorf("decoding base seed: %w", err)
	}
	if len(raw) != 32 {
		return out, fmt.Errorf("base seed must be 32 bytes, got %d", len(raw))
	}
	// Domain separation: legacy wallets use raw directly; Arc hashes with context.
	sum := sha256.Sum256(append([]byte("deodes-arc-v1:"), raw...))
	copy(out[:], sum[:])
	return out, nil
}

// EVMAddressFromSeed returns the 0x address and the 32-byte private key for
// an Arc seed. The private key is returned for immediate encryption — callers
// must never log or persist it in plaintext.
func EVMAddressFromSeed(seed [32]byte) (address string, privKey []byte, err error) {
	priv := secp256k1.PrivKeyFromBytes(seed[:])
	if priv == nil {
		return "", nil, fmt.Errorf("invalid secp256k1 private key")
	}
	pub := priv.PubKey()
	uncompressed := pub.SerializeUncompressed() // 0x04 || X || Y
	if len(uncompressed) != 65 || uncompressed[0] != 0x04 {
		return "", nil, fmt.Errorf("unexpected uncompressed pubkey length %d", len(uncompressed))
	}
	h := sha3.NewLegacyKeccak256()
	if _, err := h.Write(uncompressed[1:]); err != nil {
		return "", nil, fmt.Errorf("hashing pubkey: %w", err)
	}
	sum := h.Sum(nil)
	return "0x" + hex.EncodeToString(sum[12:]), seed[:], nil
}

// DecodeSeed32 hex-decodes a 64-char seed string into 32 raw bytes.
// This is the fix for the legacy bug where ASCII hex was copied directly
// into a [32]byte legacy raw seed.
func DecodeSeed32(hexSeed string) ([32]byte, error) {
	var out [32]byte
	raw, err := hex.DecodeString(hexSeed)
	if err != nil {
		return out, fmt.Errorf("decoding seed: %w", err)
	}
	if len(raw) != 32 {
		return out, fmt.Errorf("seed must be 32 bytes, got %d", len(raw))
	}
	copy(out[:], raw)
	return out, nil
}
