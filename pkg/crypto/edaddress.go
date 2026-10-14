package crypto

import (
	"crypto/ed25519"
	"encoding/base32"
	"fmt"
)

// This file replaces the retired Ed25519 keypair helper for deterministic
// wallet derivation (purge program). The encoding below is byte-identical to
// the legacy account-address format (version byte 6<<3, 32-byte Ed25519 key,
// CRC16-XMODEM little-endian, base32 without padding), so every address
// derived before the purge keeps resolving to the same wallet. New wallets
// are Arc (0x) natives; this helper exists only so historical addresses
// keep working. See edaddress_test.go vectors (generated against the legacy
// implementation before its removal).

// accountVersionByte is the StrKey version prefix for account addresses.
const accountVersionByte = 6 << 3

var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// Ed25519AddressFromSeed derives the Ed25519 public key for a 32-byte seed
// and returns it with its legacy account-address encoding.
func Ed25519AddressFromSeed(seed [32]byte) (ed25519.PublicKey, string) {
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub := priv.Public().(ed25519.PublicKey)
	return pub, EncodeAccountAddress(pub)
}

// EncodeAccountAddress renders a 32-byte Ed25519 public key in the legacy
// account-address text format.
func EncodeAccountAddress(pub ed25519.PublicKey) string {
	if len(pub) != 32 {
		return ""
	}
	payload := make([]byte, 0, 35)
	payload = append(payload, accountVersionByte)
	payload = append(payload, pub...)
	sum := crc16XModem(payload)
	payload = append(payload, byte(sum&0xff), byte(sum>>8))
	return base32NoPad.EncodeToString(payload)
}

// crc16XModem computes CRC-16/XMODEM (poly 0x1021, init 0x0000).
func crc16XModem(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// ParseAccountAddress decodes a legacy account address back to its key.
// It rejects malformed input (wrong length, version, charset, checksum).
func ParseAccountAddress(addr string) (ed25519.PublicKey, error) {
	raw, err := base32NoPad.DecodeString(addr)
	if err != nil {
		return nil, fmt.Errorf("decoding account address: %w", err)
	}
	if len(raw) != 35 || raw[0] != accountVersionByte {
		return nil, fmt.Errorf("invalid account address shape")
	}
	if got, want := uint16(raw[33])|uint16(raw[34])<<8, crc16XModem(raw[:33]); got != want {
		return nil, fmt.Errorf("invalid account address checksum")
	}
	out := make(ed25519.PublicKey, 32)
	copy(out, raw[1:33])
	return out, nil
}
