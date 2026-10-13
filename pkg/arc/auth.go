package arc

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

var arcAddressRegexp = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// ValidateAddress checks Arc EVM 0x address format.
func ValidateAddress(address string) error {
	if !arcAddressRegexp.MatchString(address) {
		return fmt.Errorf("arc address must be 0x + 40 hex chars, got %q", address)
	}
	return nil
}

// PersonalSignHash builds keccak("\x19Ethereum Signed Message:\n" + len + msg) per EIP-191.
func PersonalSignHash(message string) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	return keccak256([]byte(prefix), []byte(message))
}

// RecoverPersonalSigner recovers the 0x signer of an EIP-191 personal_sign signature.
// Signature must be 65-byte hex (0x-prefixed ok): R(32) || S(32) || V(27/28, or 0/1).
func RecoverPersonalSigner(message, sigHex string) (string, error) {
	s := strings.TrimSpace(sigHex)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	sig, err := hex.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("decoding signature hex: %w", err)
	}
	if len(sig) != 65 {
		return "", fmt.Errorf("signature must be 65 bytes, got %d", len(sig))
	}
	v := sig[64]
	// Normalize V: allow 0/1 or 27/28.
	var recID byte
	switch v {
	case 0, 1:
		recID = v + 27
	case 27, 28:
		recID = v
	default:
		return "", fmt.Errorf("invalid recovery id %d", v)
	}
	compact := make([]byte, 65)
	compact[0] = recID
	copy(compact[1:], sig[:64])

	hash := PersonalSignHash(message)
	pub, _, err := ecdsa.RecoverCompact(compact, hash)
	if err != nil {
		return "", fmt.Errorf("recovering signer: %w", err)
	}
	uncompressed := pub.SerializeUncompressed()
	digest := keccak256(uncompressed[1:])
	return "0x" + hex.EncodeToString(digest[12:]), nil
}

// SameSigner compares two 0x addresses case-insensitively.
func SameSigner(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
