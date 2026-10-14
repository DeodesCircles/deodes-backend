package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deodes/backend/config"
)

func TestNormalizeEnvironment(t *testing.T) {
	cases := map[string]string{
		"development": config.EnvironmentDevelopment,
		"dev":         config.EnvironmentDevelopment,
		"":            config.EnvironmentDevelopment,
		"staging":     config.EnvironmentStaging,
		"STAGE":       config.EnvironmentStaging,
		"stg":         config.EnvironmentStaging,
		"production":  config.EnvironmentProduction,
		" prod ":      config.EnvironmentProduction,
	}
	for input, want := range cases {
		assert.Equal(t, want, config.NormalizeEnvironment(input), "input %q", input)
	}
}

// The per-environment defaults are the point of #348: staging and production
// must never inherit a localhost origin.
func TestDefaultCORSOrigins_PerEnvironment(t *testing.T) {
	dev := config.DefaultCORSOrigins(config.EnvironmentDevelopment)
	assert.Contains(t, dev, "http://localhost:1110")
	assert.Contains(t, dev, "http://127.0.0.1:1110")

	staging := config.DefaultCORSOrigins(config.EnvironmentStaging)
	assert.Equal(t, []string{"https://staging.moistello.io"}, staging)

	prod := config.DefaultCORSOrigins(config.EnvironmentProduction)
	assert.Contains(t, prod, "https://app.moistello.io")
	assert.Contains(t, prod, "https://www.moistello.io")
	for _, origin := range append(staging, prod...) {
		assert.NotContains(t, origin, "localhost", "non-development default must not be a loopback origin")
	}
}

func TestSplitOrigins(t *testing.T) {
	got := config.SplitOrigins(" https://a.example , https://b.example ,,https://a.example ")
	assert.Equal(t, []string{"https://a.example", "https://b.example"}, got)

	assert.Empty(t, config.SplitOrigins("   "))
	assert.Empty(t, config.SplitOrigins(""))
}

func TestResolveCORSAllowedOrigins_EnvOverridesConfig(t *testing.T) {
	t.Setenv("MOISTELLO_CORS_ALLOWED_ORIGINS", "https://from-moistello.example,https://second.example")
	t.Setenv("ALLOWED_ORIGINS", "https://from-legacy.example")

	got := config.ResolveCORSAllowedOrigins([]string{"https://from-file.example"}, config.EnvironmentProduction)
	assert.Equal(t, []string{"https://from-moistello.example", "https://second.example"}, got,
		"MOISTELLO_CORS_ALLOWED_ORIGINS must win over ALLOWED_ORIGINS and the config file")
}

func TestResolveCORSAllowedOrigins_LegacyEnvUsedWhenCanonicalUnset(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "https://from-legacy.example")

	got := config.ResolveCORSAllowedOrigins(nil, config.EnvironmentProduction)
	assert.Equal(t, []string{"https://from-legacy.example"}, got)
}

func TestResolveCORSAllowedOrigins_ConfigFileBeatsDefault(t *testing.T) {
	got := config.ResolveCORSAllowedOrigins([]string{"https://from-file.example"}, config.EnvironmentProduction)
	assert.Equal(t, []string{"https://from-file.example"}, got)
}

func TestResolveCORSAllowedOrigins_FallsBackToEnvironmentDefault(t *testing.T) {
	got := config.ResolveCORSAllowedOrigins(nil, config.EnvironmentProduction)
	assert.Equal(t, config.DefaultCORSOrigins(config.EnvironmentProduction), got)
}

// A blank ALLOWED_ORIGINS must not be mistaken for an explicit override that
// resolves to zero origins.
func TestResolveCORSAllowedOrigins_BlankEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "   ")

	got := config.ResolveCORSAllowedOrigins(nil, config.EnvironmentStaging)
	assert.Equal(t, []string{"https://staging.moistello.io"}, got)
}

// setRequiredEnv provides the secrets config.Load insists on, so the CORS
// assertions below fail on the CORS check rather than on startup validation.
// The database URL uses sslmode=require because the production environment
// asserts below would otherwise trip the earlier sslmode check.
// Mainnet-shaped Arc/Keeper/contracts values are included so the production
// cases satisfy the mainnet cutover guard (which rejects testnet Arc and
// contract defaults); they are inert in the development cases.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MOISTELLO_DATABASE_URL", "postgres://localhost:5432/db?sslmode=require")
	t.Setenv("MOISTELLO_STELLAR_MASTER_SECRET_KEY", "SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_STELLAR_MASTER_PUBLIC_KEY", "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	t.Setenv("MOISTELLO_WALLET_PEPPER", "wallet-pepper")
	t.Setenv("ENCRYPTION_KEY", "3132333435363738393031323334353637383930313233343536373839303132")
	t.Setenv("JWT_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALs=\n-----END RSA PRIVATE KEY-----")
	t.Setenv("JWT_PUBLIC_KEY", "-----BEGIN RSA PUBLIC KEY-----\nMFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALs=\n-----END RSA PUBLIC KEY-----")
	t.Setenv("DEODES_ARC_RPC_URL", "https://rpc.mainnet.arc.io")
	t.Setenv("DEODES_ARC_CHAIN_ID", "5042")
	t.Setenv("DEODES_ARC_USDC_CONTRACT", "0x3600000000000000000000000000000000000000")
	t.Setenv("DEODES_KEEPER_ESCROW_ADDRESS", "0x1111111111111111111111111111111111111111")
	t.Setenv("DEODES_CONTRACT_FACTORY", "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA1")
	t.Setenv("DEODES_CONTRACT_CIRCLE", "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA2")
	t.Setenv("DEODES_CONTRACT_REP_REGISTRY", "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA3")
	t.Setenv("DEODES_CONTRACT_GOV_TOKEN", "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA4")
	t.Setenv("DEODES_CONTRACT_TREASURY", "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA5")
}

func TestLoad_CORSOriginsResolvedPerEnvironment(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MOISTELLO_ENVIRONMENT", "production")

	cfg, err := config.Load("")
	require.NoError(t, err)
	assert.Equal(t, config.DefaultCORSOrigins(config.EnvironmentProduction), cfg.CORS.AllowedOrigins)
}

func TestLoad_CORSWildcardRejectedOutsideDevelopment(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MOISTELLO_ENVIRONMENT", "production")
	t.Setenv("ALLOWED_ORIGINS", "*")

	cfg, err := config.Load("")
	require.Error(t, err, "a CORS misconfiguration must be reported as an error, not a panic")
	require.Nil(t, cfg)
	require.Contains(t, err.Error(), "cors.allowed_origins")
}

func TestLoad_CORSWildcardWithCredentialsRejectedInDevelopment(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MOISTELLO_ENVIRONMENT", "development")
	t.Setenv("ALLOWED_ORIGINS", "*")

	cfg, err := config.Load("")
	require.Error(t, err, "a CORS misconfiguration must be reported as an error, not a panic")
	require.Nil(t, cfg)
	require.Contains(t, err.Error(), "cors.allowed_origins")
}

func TestLoad_CORSLoopbackOriginRejectedInProduction(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MOISTELLO_ENVIRONMENT", "production")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:1110")

	cfg, err := config.Load("")
	require.Error(t, err, "a CORS misconfiguration must be reported as an error, not a panic")
	require.Nil(t, cfg)
	require.Contains(t, err.Error(), "cors.allowed_origins")
}

func TestLoad_CORSNonOriginEntryRejected(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MOISTELLO_ENVIRONMENT", "production")
	t.Setenv("ALLOWED_ORIGINS", "app.moistello.io")

	cfg, err := config.Load("")
	require.Error(t, err, "a CORS misconfiguration must be reported as an error, not a panic")
	require.Nil(t, cfg)
	require.Contains(t, err.Error(), "cors.allowed_origins")
}

// cors.max_age was previously ignored because the middleware hardcoded 12h.
func TestLoad_CORSMaxAgeComesFromConfig(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MOISTELLO_ENVIRONMENT", "development")
	t.Setenv("MOISTELLO_CORS_MAX_AGE", "30m")

	cfg, err := config.Load("")
	require.NoError(t, err)
	assert.Equal(t, 30*time.Minute, cfg.CORS.MaxAge)
}
