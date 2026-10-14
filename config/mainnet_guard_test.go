package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mainnetCleanConfig returns a config that must pass the mainnet guard: Arc
// mainnet values with keeper escrow set and distinct from the treasury.
func mainnetCleanConfig() *Config {
	return &Config{
		Environment: "production",
		Arc: ArcConfig{
			RPCURL:          "https://rpc.mainnet.arc.io",
			ChainID:         5042,
			USDCContract:    "0x3600000000000000000000000000000000000000",
			TreasuryAddress: "0x2222222222222222222222222222222222222222",
		},
		Keeper: KeeperConfig{
			EscrowAddress: "0x1111111111111111111111111111111111111111",
			IntervalMin:   15,
		},
	}
}

// devTestnetConfig mirrors the shipped defaults: nothing mainnet-shaped, so
// the guard must stay silent even with an empty keeper escrow.
func devTestnetConfig() *Config {
	return &Config{
		Environment: "development",
		Arc: ArcConfig{
			RPCURL:       "",
			ChainID:      5042002,
			USDCContract: "",
		},
		Keeper: KeeperConfig{
			EscrowAddress: "",
			IntervalMin:   15,
		},
	}
}

func TestCheckMainnetIssues_DevTestnetDefaultsPass(t *testing.T) {
	require.Empty(t, checkMainnetIssues(devTestnetConfig()))
}

func TestCheckMainnetIssues_StagingDoesNotTrigger(t *testing.T) {
	cfg := devTestnetConfig()
	cfg.Environment = "staging"
	require.Empty(t, checkMainnetIssues(cfg))
}

func TestCheckMainnetIssues_FullMainnetCleanPasses(t *testing.T) {
	require.Empty(t, checkMainnetIssues(mainnetCleanConfig()))
}

func TestCheckMainnetIssues_ProductionEnvTriggers(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Environment = "production"
	issues := checkMainnetIssues(cfg)
	require.Empty(t, issues) // Arc/Keeper clean.

	cfg.Arc.ChainID = 5042002 // ...but a testnet Arc chain ID must fail.
	issues = checkMainnetIssues(cfg)
	require.NotEmpty(t, issues)
	require.Contains(t, issues[0], "arc.chain_id")
}

func TestCheckMainnetIssues_MainnetEnvNameTriggers(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Environment = "mainnet"
	cfg.Arc.ChainID = 5042002
	require.NotEmpty(t, checkMainnetIssues(cfg))
}

func TestCheckMainnetIssues_EnvironmentMatchIsCaseInsensitive(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Arc.ChainID = 5042002
	for _, env := range []string{"Production", "PRODUCTION", " Mainnet "} {
		cfg.Environment = env
		require.NotEmpty(t, checkMainnetIssues(cfg), "env %q should trigger guard", env)
	}
}

func TestCheckMainnetIssues_ArcChainIDMustBe5042(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Arc.ChainID = 5042002
	issues := checkMainnetIssues(cfg)
	require.Len(t, issues, 1)
	require.Contains(t, issues[0], "arc.chain_id must be 5042 for mainnet")
	require.Contains(t, issues[0], "DEODES_ARC_CHAIN_ID")
}

func TestCheckMainnetIssues_ArcRPCEmptyRejected(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Arc.RPCURL = "   "
	issues := checkMainnetIssues(cfg)
	require.Len(t, issues, 1)
	require.Contains(t, issues[0], "arc.rpc_url must be set")
}

func TestCheckMainnetIssues_ArcRPCTestnetRejected(t *testing.T) {
	cfg := mainnetCleanConfig()
	for _, url := range []string{
		"https://rpc.testnet.arc.io",
		"https://RPC.TESTNET.ARC.IO",
	} {
		cfg.Arc.RPCURL = url
		issues := checkMainnetIssues(cfg)
		require.Len(t, issues, 1, "url %q", url)
		require.Contains(t, issues[0], "arc.rpc_url still points to testnet")
	}
}

func TestCheckMainnetIssues_ArcUSDCEmptyRejected(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Arc.USDCContract = ""
	issues := checkMainnetIssues(cfg)
	require.Len(t, issues, 1)
	require.Contains(t, issues[0], "arc.usdc_contract must be set")
	require.Contains(t, issues[0], "DEODES_ARC_USDC_CONTRACT")
}

func TestCheckMainnetIssues_KeeperEscrowRequiredWhenEnabled(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Keeper.EscrowAddress = ""
	cfg.Keeper.IntervalMin = 15
	issues := checkMainnetIssues(cfg)
	require.Len(t, issues, 1)
	require.Contains(t, issues[0], "keeper.escrow_address must be set")
	require.Contains(t, issues[0], "DEODES_KEEPER_ESCROW_ADDRESS")
}

func TestCheckMainnetIssues_KeeperEscrowOptionalWhenDisabled(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Keeper.EscrowAddress = ""
	cfg.Keeper.IntervalMin = 0 // keeper disabled: empty escrow is intentional.
	require.Empty(t, checkMainnetIssues(cfg))
}

func TestCheckMainnetIssues_KeeperEscrowMustDifferFromTreasury(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Keeper.EscrowAddress = cfg.Arc.TreasuryAddress
	issues := checkMainnetIssues(cfg)
	require.Len(t, issues, 1)
	require.Contains(t, issues[0], "keeper.escrow_address must differ from arc.treasury_address")
}

func TestCheckMainnetIssues_NoStaleContractIDs(t *testing.T) {
	// Purged rail: no contract-ID registry remains to check. This test pins
	// that no Contracts map can sneak back in unnoticed.
	cfg := mainnetCleanConfig()
	require.Empty(t, checkMainnetIssues(cfg))
}

func TestCheckMainnetIssues_MultipleArcIssuesAllReported(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Arc.ChainID = 5042002
	cfg.Arc.RPCURL = ""
	cfg.Arc.USDCContract = ""
	issues := checkMainnetIssues(cfg)
	require.Len(t, issues, 3)
}

func TestValidateMainnetConfig_PanicsOnIssues(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Arc.ChainID = 5042002
	require.Panics(t, func() { validateMainnetConfig(cfg) })
}

func TestValidateMainnetConfig_NoPanicWhenClean(t *testing.T) {
	require.NotPanics(t, func() { validateMainnetConfig(mainnetCleanConfig()) })
	require.NotPanics(t, func() { validateMainnetConfig(devTestnetConfig()) })
}
