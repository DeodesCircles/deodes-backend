package crypto

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compatibility vectors generated against the retired keypair implementation
// before its removal (purge program). If any of these fail, derivation
// drifted and historical wallets stop resolving — treat as P0.
func TestEd25519AddressFromSeed_Vectors(t *testing.T) {
	cases := map[string]string{
		"0000000000000000000000000000000000000000000000000000000000000000": "GA5WUJ54Z23KILLCUOUNAKTPBVZWKMQVO4O6EQ5GHLAERIMLLHNCSKYH",
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff": "GB3KCWJAISTOJ5IREZN4U45GATMQWBJJ2HPWAK7DBIM2SJLWMDI7KDMZ",
		"abababababababababababababababababababababababababababababababab": "GASIVS63V6PAKAMW3ZYEX2RNNB3Q4UMRKDIQHNMH3LRNTSWVHXMTANKE",
	}
	for seedHex, wantAddr := range cases {
		raw, err := hex.DecodeString(seedHex)
		require.NoError(t, err)
		var seed [32]byte
		copy(seed[:], raw)
		pub, addr := Ed25519AddressFromSeed(seed)
		require.Len(t, pub, 32)
		assert.Equal(t, wantAddr, addr, "seed %s", seedHex)

		// Round-trip through the parser.
		back, err := ParseAccountAddress(addr)
		require.NoError(t, err)
		assert.Equal(t, []byte(pub), []byte(back))
	}
}

func TestParseAccountAddress_RejectsGarbage(t *testing.T) {
	for _, bad := range []string{
		"",
		"0x1234",
		"GA5WUJ54Z23KILLCUOUNAKTPBVZWKMQVO4O6EQ5GHLAERIMLLHNCSKYG", // bad checksum
		"BA5WUJ54Z23KILLCUOUNAKTPBVZWKMQVO4O6EQ5GHLAERIMLLHNCSKYH", // bad version
	} {
		_, err := ParseAccountAddress(bad)
		assert.Error(t, err, "input %q", bad)
	}
}
