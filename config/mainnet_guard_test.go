package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mainnetCleanConfig returns a config that must pass the mainnet guard: Arc
// mainnet values, keeper escrow set, non-testnet contracts, Stellar mainnet.
func mainnetCleanConfig() *Config {
	return &Config{
		Environment: "production",
		Stellar: StellarConfig{
			Network:           "mainnet",
			HorizonURL:        "https://horizon.stellar.org",
			SorobanRPCURL:     "https://soroban-mainnet.stellar.org",
			NetworkPassphrase: "Public Global Stellar Network ; September 2015",
			USDCIssuer:        "GBUQWP3BOUZX34ULNQG23RQ6F4YUSXHTQSXUSMIQ375YRKBY2S27425",
		},
		Arc: ArcConfig{
			RPCURL:       "https://rpc.mainnet.arc.io",
			ChainID:      5042,
			USDCContract: "0x3600000000000000000000000000000000000000",
		},
		Keeper: KeeperConfig{
			EscrowAddress: "0x1111111111111111111111111111111111111111",
			IntervalMin:   15,
		},
		Contracts: map[string]string{
			"circle_factory": "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		},
	}
}

// devTestnetConfig mirrors the shipped defaults: nothing mainnet-shaped, so
// the guard must stay silent even with an empty keeper escrow.
func devTestnetConfig() *Config {
	return &Config{
		Environment: "development",
		Stellar: StellarConfig{
			Network:           "testnet",
			HorizonURL:        "https://horizon-testnet.stellar.org",
			SorobanRPCURL:     "https://soroban-testnet.stellar.org",
			NetworkPassphrase: "Test SDF Network ; September 2015",
			USDCIssuer:        "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC",
		},
		Arc: ArcConfig{
			RPCURL:       "",
			ChainID:      5042002,
			USDCContract: "",
		},
		Keeper: KeeperConfig{
			EscrowAddress: "",
			IntervalMin:   15,
		},
		Contracts: map[string]string{
			"circle_factory": "CADZWDGER6PYEWVHZDZ5HMEV6A5TKFVD4EY4DSPUTEKAM2KDPEABGJQU",
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

func TestCheckMainnetIssues_StellarMainnetFlagsTestnetRemnants(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Stellar.HorizonURL = "https://horizon-testnet.stellar.org"
	cfg.Stellar.SorobanRPCURL = "https://soroban-testnet.stellar.org"
	cfg.Stellar.NetworkPassphrase = "Test SDF Network ; September 2015"
	cfg.Stellar.USDCIssuer = "GAX23V3WWDPPR5WRER3KTEUTDLSCGZYMSJY5FDRRKKCIQ4JADF5T27RC"

	issues := checkMainnetIssues(cfg)
	require.Contains(t, issues, "horizon_url still set to testnet")
	require.Contains(t, issues, "soroban_rpc_url still set to testnet")
	require.Contains(t, issues, "network_passphrase still set to testnet")
	require.Contains(t, issues, "usdc_issuer still set to testnet")
}

func TestCheckMainnetIssues_ProductionEnvTriggersWithoutStellarMainnet(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Environment = "production"
	cfg.Stellar.Network = "testnet" // legacy Stellar untouched: no stellar issues...
	issues := checkMainnetIssues(cfg)
	require.Empty(t, issues) // ...and Arc/Keeper/contracts are mainnet-clean.

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

func TestCheckMainnetIssues_TestnetContractsRejected(t *testing.T) {
	cfg := mainnetCleanConfig()
	cfg.Contracts = map[string]string{
		"circle_factory":      "CADZWDGER6PYEWVHZDZ5HMEV6A5TKFVD4EY4DSPUTEKAM2KDPEABGJQU",
		"governance_token":    "CDOBQSJZJD4RIE2V5Q2IOJ6PBPSE2FT46BCD5WXW64ZN4KEUMYGBX3CS",
		"treasury":            "CMAINNETTREASURYVALUEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE",
		"reputation_registry": "  CCEEJKQCYO7CXBFZBSGHS6AK2CCBU2HY2HK4VXQDPZ3XX3X3KEDGXQ7K  ",
	}
	issues := checkMainnetIssues(cfg)
	require.Len(t, issues, 3)
	require.Contains(t, issues, "contracts.circle_factory still set to testnet value")
	require.Contains(t, issues, "contracts.governance_token still set to testnet value")
	require.Contains(t, issues, "contracts.reputation_registry still set to testnet value")
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
