package config_test

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/config"
)

func TestLoad_SucceedsWithRequiredConfig(t *testing.T) {
	t.Setenv("DEODES_DATABASE_URL", "postgres://localhost:5432/db")
	t.Setenv("DEODES_WALLET_PEPPER", "wallet-pepper")
	t.Setenv("ENCRYPTION_KEY", hex.EncodeToString([]byte("12345678901234567890123456789012")))
	t.Setenv("JWT_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PUBLIC_KEY", "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----")

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "postgres://localhost:5432/db", cfg.Database.URL)
	require.Equal(t, "wallet-pepper", cfg.Security.WalletPepper)
	require.NotEmpty(t, cfg.Auth.JWTPrivateKeyPEM)
	require.NotEmpty(t, cfg.Auth.JWTPublicKeyPEM)
	require.Equal(t, 30*time.Second, cfg.Server.ShutdownTimeout)
	require.Equal(t, time.Duration(0), cfg.Server.ShutdownDelay)
}

func TestLoad_ShutdownTimeoutIsConfigurable(t *testing.T) {
	t.Setenv("DEODES_DATABASE_URL", "postgres://localhost:5432/db")
	t.Setenv("DEODES_WALLET_PEPPER", "wallet-pepper")
	t.Setenv("ENCRYPTION_KEY", hex.EncodeToString([]byte("12345678901234567890123456789012")))
	t.Setenv("JWT_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PUBLIC_KEY", "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----")
	t.Setenv("DEODES_SERVER_SHUTDOWN_TIMEOUT", "5s")
	t.Setenv("DEODES_SERVER_SHUTDOWN_DELAY", "2s")

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, cfg.Server.ShutdownTimeout)
	require.Equal(t, 2*time.Second, cfg.Server.ShutdownDelay)
}

// Phase 2.1 freeze: the shipped config.yaml must carry the short-lived 15m
// access TTL (the former 240m widened the stolen-token window 16x). TTLs have
// no env override, so this pins the file value itself — and, through
// cmd/api-server wiring (cfg.Auth.AccessTokenTTL → auth service → JWT exp),
// the lifetime of every issued access token.
func TestLoad_ShippedConfigUses15mAccessTTL(t *testing.T) {
	setRequiredConfigEnv(t)

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Equal(t, 15*time.Minute, cfg.Auth.AccessTokenTTL)
	require.Equal(t, 168*time.Hour, cfg.Auth.RefreshTokenTTL)
}

// setRequiredConfigEnv provides the secrets config.Load insists on, so an
// assertion about a single setting fails on that setting rather than on
// unrelated startup validation.
func setRequiredConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DEODES_DATABASE_URL", "postgres://localhost:5432/db")
	t.Setenv("DEODES_WALLET_PEPPER", "wallet-pepper")
	t.Setenv("ENCRYPTION_KEY", hex.EncodeToString([]byte("12345678901234567890123456789012")))
	t.Setenv("JWT_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PUBLIC_KEY", "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----")
}

// (#346). It must be settable and must have a safe default.
func TestLoad_ReorgWindowIsConfigurable(t *testing.T) {
	setRequiredConfigEnv(t)
	t.Setenv("DEODES_INDEXER_REORG_WINDOW", "25")

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Equal(t, 25, cfg.Indexer.ReorgWindow)
}

func TestLoad_ReorgWindowHasDefault(t *testing.T) {
	setRequiredConfigEnv(t)

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Equal(t, 10, cfg.Indexer.ReorgWindow)
}

func TestLoad_ReportsAllMissingErrors(t *testing.T) {
	t.Setenv("DEODES_DATABASE_URL", "")
	t.Setenv("DEODES_WALLET_PEPPER", "")
	t.Setenv("ENCRYPTION_KEY", "")
	t.Setenv("JWT_PRIVATE_KEY", "")
	t.Setenv("JWT_PUBLIC_KEY", "")

	cfg, err := config.Load("")
	require.Error(t, err)
	require.Nil(t, cfg)
	errStr := err.Error()
	require.Contains(t, errStr, "database.url is required")
	require.Contains(t, errStr, "security.wallet_pepper is required")
	require.Contains(t, errStr, "security.encryption_key is required")
	require.Contains(t, errStr, "jwt_private_key_pem")
	require.Contains(t, errStr, "jwt_public_key_pem")
}

func TestValidateOffline_Succeeds(t *testing.T) {
	hexKey := hex.EncodeToString([]byte("12345678901234567890123456789012"))
	t.Setenv("DEODES_DATABASE_URL", "postgres://localhost:5432/db")
	t.Setenv("DEODES_WALLET_PEPPER", hexKey)
	t.Setenv("ENCRYPTION_KEY", hexKey)
	t.Setenv("ADMIN_API_KEY", hexKey)
	t.Setenv("REDIS_PASSWORD", "redis-password-123456")
	t.Setenv("JWT_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PUBLIC_KEY", "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----")

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.NotNil(t, cfg)

	err = cfg.ValidateOffline()
	require.NoError(t, err)
}

// Phase 2.5: a misconfigured EVM address fails boot with an actionable error.
func TestLoad_RejectsMalformedArcAddresses(t *testing.T) {
	setRequiredConfigEnv(t)

	cases := map[string]string{
		"DEODES_ARC_USDC_CONTRACT":     "not-an-address",
		"DEODES_CIRCLE_ADDRESS":        "0x1234",
		"DEODES_TREASURY_ADDRESS":      "70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4",
		"DEODES_KEEPER_ESCROW_ADDRESS": "0xZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ",
	}
	for env, bad := range cases {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, bad)
			cfg, err := config.Load("")
			require.Error(t, err)
			require.Nil(t, cfg)
			require.Contains(t, err.Error(), "must be 0x + 40 hex chars")
		})
	}
}

// Phase 2.5: empty addresses stay unset (chain features dormant in dev) and
// well-formed addresses load through.
func TestLoad_AcceptsEmptyAndWellFormedArcAddresses(t *testing.T) {
	setRequiredConfigEnv(t)
	t.Setenv("DEODES_CIRCLE_ADDRESS", "0x70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4")
	t.Setenv("DEODES_TREASURY_ADDRESS", "0x51b45Ba8570551e0D1fe818227d8bAa8b556694D")

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "0x70A7CCB7b5fe856384Aab3aAF553E09A10AF14A4", cfg.Arc.CircleAddress)
	require.Equal(t, "", cfg.Arc.USDCContract)
}
