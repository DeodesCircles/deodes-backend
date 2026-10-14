package nonce

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/sha3"

	"github.com/deodes/backend/pkg/apperrors"
	"github.com/deodes/backend/pkg/arc"
)

// testWallet mints a fresh secp256k1 key and its 0x address (EIP-55-agnostic
// lowercase form, which SameSigner compares case-insensitively).
func testWallet(t *testing.T) (*secp256k1.PrivateKey, string) {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	require.NoError(t, err)
	uncompressed := priv.PubKey().SerializeUncompressed()
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	sum := h.Sum(nil)
	return priv, "0x" + hex.EncodeToString(sum[12:])
}

// signPersonal produces a 65-byte EIP-191 personal_sign signature (R||S||V
// with V 27/28) for the raw nonce string.
func signPersonal(t *testing.T, priv *secp256k1.PrivateKey, nonce string) string {
	t.Helper()
	sig := ecdsa.SignCompact(priv, arc.PersonalSignHash(nonce), false)
	require.Len(t, sig, 65)
	require.Contains(t, []int{27, 28}, int(sig[0]))
	out := make([]byte, 65)
	copy(out, sig[1:])
	out[64] = sig[0]
	return hex.EncodeToString(out)
}

func TestNonceReplayAttackMatrix(t *testing.T) {
	redisClient, mr := setupRedis(t)
	defer redisClient.Close()

	svc := NewService(redisClient, 5*time.Minute)
	ctx := context.Background()

	priv, walletAddress := testWallet(t)

	t.Run("single-use enforcement: nonce deleted after successful verification", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		sigHex := signPersonal(t, priv, nonce.Nonce)

		// First verification should succeed
		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		assert.NoError(t, err)
		assert.True(t, valid)

		// Second verification with same nonce should fail (replay)
		valid, err = svc.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})

	t.Run("replay attack after successful use rejected", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		sigHex := signPersonal(t, priv, nonce.Nonce)

		// Verify successfully (deletes nonce)
		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		require.NoError(t, err)
		require.True(t, valid)

		// Wait a moment then attempt replay
		time.Sleep(100 * time.Millisecond)
		valid, err = svc.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})

	t.Run("concurrent double-submit of same signature", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		sigHex := signPersonal(t, priv, nonce.Nonce)

		// Simulate concurrent double-submit (both calls in quick succession)
		results := make(chan struct {
			valid bool
			err   error
		}, 2)

		go func() {
			valid, err := svc.Verify(ctx, walletAddress, sigHex)
			results <- struct {
				valid bool
				err   error
			}{valid, err}
		}()

		go func() {
			valid, err := svc.Verify(ctx, walletAddress, sigHex)
			results <- struct {
				valid bool
				err   error
			}{valid, err}
		}()

		// Collect results
		r1 := <-results
		r2 := <-results

		// Atomic compare-and-del consumption: exactly one request wins.
		successCount := 0
		if r1.err == nil && r1.valid {
			successCount++
		}
		if r2.err == nil && r2.valid {
			successCount++
		}
		assert.Equal(t, 1, successCount, "atomic single-use: exactly one concurrent request must succeed")
	})

	t.Run("post-restart reuse of nonce fails", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		sigHex := signPersonal(t, priv, nonce.Nonce)

		// Verify successfully
		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		require.NoError(t, err)
		require.True(t, valid)

		// Simulate service restart by creating new service instance
		// (in practice, nonce is deleted from Redis so this is just validation)
		svc2 := NewService(redisClient, 5*time.Minute)

		// Attempt to use same signature after "restart"
		valid, err = svc2.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})

	t.Run("invalid signature is rejected without deleting nonce", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Sign with a DIFFERENT key: well-formed 65-byte signature that
		// recovers to another address (rejected, nonce survives).
		otherPriv, _ := secp256k1.GeneratePrivateKey()
		wrongSigHex := signPersonal(t, otherPriv, nonce.Nonce)

		valid, err := svc.Verify(ctx, walletAddress, wrongSigHex)
		assert.NoError(t, err)
		assert.False(t, valid)

		// Nonce should still exist - try with valid signature
		sigHex := signPersonal(t, priv, nonce.Nonce)

		valid, err = svc.Verify(ctx, walletAddress, sigHex)
		assert.NoError(t, err)
		assert.True(t, valid)
	})

	t.Run("malformed signature errors loudly", func(t *testing.T) {
		_, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// 64 zero bytes: well-formed hex but not a 65-byte signature.
		invalidSig := hex.EncodeToString(make([]byte, 64))
		valid, err := svc.Verify(ctx, walletAddress, invalidSig)
		assert.Error(t, err)
		assert.False(t, valid)
	})

	t.Run("nonce expiry prevents reuse", func(t *testing.T) {
		// Create service with very short TTL
		shortTTLSvc := NewService(redisClient, 100*time.Millisecond)

		// Generate nonce
		nonce, err := shortTTLSvc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Wait for nonce to expire (exceeding 30s clock skew tolerance)
		mr.FastForward(35 * time.Second)

		sigHex := signPersonal(t, priv, nonce.Nonce)

		// Verification should fail due to expiry
		valid, err := shortTTLSvc.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})
}

func TestNonceBasicFlow(t *testing.T) {
	redisClient, mr := setupRedis(t)
	defer redisClient.Close()

	svc := NewService(redisClient, 5*time.Minute)
	ctx := context.Background()

	priv, walletAddress := testWallet(t)

	t.Run("generate and verify valid nonce", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)
		assert.NotEmpty(t, nonce.Nonce)
		assert.Equal(t, walletAddress, nonce.WalletAddress)
		assert.False(t, nonce.ExpiresAt.IsZero())

		sigHex := signPersonal(t, priv, nonce.Nonce)

		// Verify signature
		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		assert.NoError(t, err)
		assert.True(t, valid)
	})

	t.Run("reject invalid signature", func(t *testing.T) {
		// Generate nonce
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)
		require.NotEmpty(t, nonce.Nonce)

		// Sign the nonce with the WRONG key: well-formed 65-byte signature
		// recovering to a different address.
		otherPriv, _ := secp256k1.GeneratePrivateKey()
		wrongSigHex := signPersonal(t, otherPriv, nonce.Nonce)

		// Verify wrong signature
		valid, err := svc.Verify(ctx, walletAddress, wrongSigHex)
		assert.NoError(t, err)
		assert.False(t, valid)
	})

	t.Run("reject expired nonce", func(t *testing.T) {
		// Create service with very short TTL
		shortTTLSvc := NewService(redisClient, 50*time.Millisecond)

		// Generate nonce
		nonce, err := shortTTLSvc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		// Wait for expiry (exceeding 30s clock skew tolerance)
		mr.FastForward(35 * time.Second)

		sigHex := signPersonal(t, priv, nonce.Nonce)

		// Verify expired nonce
		valid, err := shortTTLSvc.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})

	t.Run("reject nonexistent nonce", func(t *testing.T) {
		// Try to verify a nonce that was never generated
		otherPriv, _ := secp256k1.GeneratePrivateKey()
		sigHex := signPersonal(t, otherPriv, "0000000000000000000000000000000000000000000000000000000000000000")

		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		assert.Error(t, err)
		assert.Equal(t, apperrors.ErrNonceExpired, err)
		assert.False(t, valid)
	})

	t.Run("reject signature from another key", func(t *testing.T) {
		nonce, err := svc.Generate(ctx, walletAddress)
		require.NoError(t, err)

		otherPriv, _ := secp256k1.GeneratePrivateKey()
		sigHex := signPersonal(t, otherPriv, nonce.Nonce)

		valid, err := svc.Verify(ctx, walletAddress, sigHex)
		assert.NoError(t, err)
		assert.False(t, valid)
	})
}

func setupRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	}), mr
}
