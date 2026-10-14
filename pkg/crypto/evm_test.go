package crypto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeSeed32_HexVsRaw(t *testing.T) {
	hexSeed := "81362d66c26500a3ffca1259323cfe1bc3b59658b65d6d534aaee0cb4eabf904"
	raw, err := DecodeSeed32(hexSeed)
	require.NoError(t, err)
	require.Len(t, raw, 32)
	// First byte must be decoded 0x81, not ASCII '8' (0x38) — the legacy bug.
	assert.Equal(t, byte(0x81), raw[0])

	_, err = DecodeSeed32("short")
	assert.Error(t, err)

	raw32 := make([]byte, 32)
	for i := range raw32 {
		raw32[i] = byte(i)
	}
	// Non-hex 32B input falls back to raw copy path via decodeSeedInput analogue:
	// DecodeSeed32 itself requires hex, so raw garbage errors (callers handle).
	_, err = DecodeSeed32(string(raw32))
	assert.Error(t, err)
}

func TestDeriveArcSeed_DomainSeparated(t *testing.T) {
	hexSeed := "81362d66c26500a3ffca1259323cfe1bc3b59658b65d6d534aaee0cb4eabf904"
	arc, err := DeriveArcSeed(hexSeed)
	require.NoError(t, err)
	// Must differ from raw legacy seed bytes.
	raw, err := DecodeSeed32(hexSeed)
	require.NoError(t, err)
	assert.NotEqual(t, raw, arc)

	// Deterministic.
	arc2, err := DeriveArcSeed(hexSeed)
	require.NoError(t, err)
	assert.Equal(t, arc, arc2)

	_, err = DeriveArcSeed("not-hex")
	assert.Error(t, err)
}

func TestEVMAddressFromSeed_FormatAndStability(t *testing.T) {
	hexSeed := "81362d66c26500a3ffca1259323cfe1bc3b59658b65d6d534aaee0cb4eabf904"
	arcSeed, err := DeriveArcSeed(hexSeed)
	require.NoError(t, err)

	addr, priv, err := EVMAddressFromSeed(arcSeed)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(addr, "0x"), "address must be 0x-prefixed")
	assert.Len(t, addr, 42, "0x + 40 hex chars")
	assert.Len(t, priv, 32)

	// Same seed → same address.
	addr2, _, err := EVMAddressFromSeed(arcSeed)
	require.NoError(t, err)
	assert.Equal(t, addr, addr2)
}
