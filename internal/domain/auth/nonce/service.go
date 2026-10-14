package nonce

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/deodes/backend/pkg/apperrors"
	"github.com/deodes/backend/pkg/arc"
)

type Service interface {
	Generate(ctx context.Context, walletAddress string) (*Nonce, error)
	Verify(ctx context.Context, walletAddress, signature string) (bool, error)
}

type Nonce struct {
	WalletAddress string
	Nonce         string
	ExpiresAt     time.Time
}

type service struct {
	redis    *redis.Client
	nonceTTL time.Duration
}

func NewService(redisClient *redis.Client, nonceTTL time.Duration) Service {
	return &service{
		redis:    redisClient,
		nonceTTL: nonceTTL,
	}
}

func (s *service) Generate(ctx context.Context, walletAddress string) (*Nonce, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generating random nonce: %w", err)
	}
	nonceStr := hex.EncodeToString(b)

	// Store nonce with creation timestamp for clock skew tolerance
	now := time.Now().Unix()
	storedValue := fmt.Sprintf("%s:%d", nonceStr, now)
	key := fmt.Sprintf("nonce:%s", walletAddress)

	// Add 30s clock skew tolerance to the TTL
	ttl := s.nonceTTL + 30*time.Second
	if err := s.redis.Set(ctx, key, storedValue, ttl).Err(); err != nil {
		return nil, fmt.Errorf("storing nonce in redis: %w", err)
	}

	return &Nonce{
		WalletAddress: walletAddress,
		Nonce:         nonceStr,
		ExpiresAt:     time.Now().UTC().Add(s.nonceTTL),
	}, nil
}

func (s *service) Verify(ctx context.Context, walletAddress, signature string) (bool, error) {
	key := fmt.Sprintf("nonce:%s", walletAddress)
	stored, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return false, apperrors.ErrNonceExpired
		}
		return false, fmt.Errorf("retrieving nonce from redis: %w", err)
	}

	// Parse nonce value and creation timestamp
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false, fmt.Errorf("invalid nonce format")
	}
	nonceStr := parts[0]
	createdAt, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid nonce timestamp: %w", err)
	}

	// Check expiry with 30-second clock skew tolerance
	now := time.Now().Unix()
	skewTolerance := int64(30)
	if now > createdAt+int64(s.nonceTTL.Seconds())+skewTolerance {
		return false, apperrors.ErrNonceExpired
	}
	if now < createdAt-skewTolerance {
		return false, fmt.Errorf("nonce from the future — clock skew detected")
	}

	sigBytes, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(signature), "0x"), "0X"))
	if err == nil && len(sigBytes) == 65 && arc.ValidateAddress(walletAddress) == nil {
		// Arc EVM path: EIP-191 personal_sign of the raw nonce string.
		recovered, recErr := arc.RecoverPersonalSigner(nonceStr, signature)
		if recErr != nil {
			return false, fmt.Errorf("recovering arc signer: %w", recErr)
		}
		valid := arc.SameSigner(recovered, walletAddress)
		if valid {
			// Atomically consume the nonce (single-use enforcement): delete
			// only if it still holds the value read above. A lost race
			// means a concurrent request consumed it first.
			consumed, delErr := consumeNonce(ctx, s.redis, key, stored)
			if delErr != nil {
				// Fail safe: expire fast and reject — a replay must never
				// succeed on infrastructure doubt.
				_ = s.redis.Expire(ctx, key, 1*time.Second)
				return false, fmt.Errorf("consuming nonce: %w", delErr)
			}
			if !consumed {
				return false, apperrors.ErrNonceExpired
			}
		}
		return valid, nil
	}

	return false, fmt.Errorf("unsupported signature: want 65-byte EIP-191 personal_sign for a 0x address")
}

// consumeNonce deletes key only if it still equals expected, atomically
// (Lua compare-and-del). True means this caller consumed the single use.
func consumeNonce(ctx context.Context, rdb *redis.Client, key, expected string) (bool, error) {
	n, err := redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
else
  return 0
end`).Run(ctx, rdb, []string{key}, expected).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
