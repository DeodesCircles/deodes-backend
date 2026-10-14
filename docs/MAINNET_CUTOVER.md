# Mainnet Cutover Checklist

This checklist guides the transition from testnet to mainnet. Follow each step carefully to avoid sending testnet configuration to production.

## Pre-Cutover: Staging Environment Dry Run

Run this entire checklist on a staging environment first to validate the process.

- [ ] Deploy all Soroban contracts to testnet
- [ ] Record testnet contract IDs
- [ ] Verify contract functionality with smoke tests
- [ ] Switch staging config to testnet (use testnet values from defaults)
- [ ] Fund staging master account with sufficient stroops
- [ ] Run full smoke test suite against testnet endpoint
- [ ] Verify all DNS and CORS configurations work on staging
- [ ] Document any issues or deviations

## Mainnet Cutover Steps

### 1. Contract Deployment (Run on Mainnet)

- [ ] Deploy circle factory contract to mainnet Soroban
- [ ] Deploy circle contract to mainnet Soroban
- [ ] Deploy reputation registry contract to mainnet Soroban
- [ ] Deploy governance token contract to mainnet Soroban
- [ ] Deploy treasury contract to mainnet Soroban
- [ ] Record all mainnet contract IDs

### 2. Configuration Update

Update `config/config.yaml` with mainnet values:

- [ ] Set `stellar.network: "mainnet"`
- [ ] Set `stellar.horizon_url: "https://horizon.stellar.org"`
- [ ] Set `stellar.soroban_rpc_url: "https://soroban-mainnet.stellar.org"` (or your RPC provider)
- [ ] Set `stellar.network_passphrase: "Public Global Stellar Network ; September 2015"`
- [ ] Set `stellar.usdc_issuer: "GBUQWP3BOUZX34ULNQG23RQ6F4YUSXHTQSXUSMIQ375YRKBY2S27425"` (mainnet USDC issuer)
- [ ] Update contract IDs in `stellar.*_contract_id` to mainnet values
- [ ] Verify config file does not contain any testnet values

### 2b. Arc / Keeper Configuration (ADDED — Arc is the primary chain)

The Stellar section above is retained for legacy modules. New deployments
must ALSO complete this section — the startup guard rejects mainnet boot
without it.

- [ ] Set `arc.chain_id: 5042` (mainnet; the `5042002` default is Arc testnet)
- [ ] Set `arc.rpc_url: "https://rpc.mainnet.arc.io"` (or your RPC provider —
      must NOT contain `testnet`)
- [ ] Set `arc.usdc_contract` to the mainnet USDC contract address from
      `docs.arc.io/arc/references/contract-addresses.md` (has no safe default)
- [ ] Set `keeper.escrow_address` to the mainnet operator collection address
      whenever the keeper is enabled (`keeper.interval_minutes != 0`, default
      `15`). To run mainnet with NO auto-debits, set
      `keeper.interval_minutes: 0` instead of configuring an escrow address
- [ ] Set every `contracts.*` entry (`circle_factory`, `circle`,
      `reputation_registry`, `governance_token`, `treasury`) to mainnet values
      — any of the git-tracked testnet `C...` IDs left in place fails startup.
      Prefer env overrides (`DEODES_CONTRACT_FACTORY`,
      `DEODES_CONTRACT_CIRCLE`, `DEODES_CONTRACT_REP_REGISTRY`,
      `DEODES_CONTRACT_GOV_TOKEN`, `DEODES_CONTRACT_TREASURY`) over editing
      the tracked `config.yaml`. NOTE: `contracts.circle` is a deployed
      instance ID, NOT the circle WASM hash (`CONTRACT_CIRCLE_HASH`)

### 3. Master Account Funding

- [ ] Fund mainnet master account (`MOISTELLO_STELLAR_MASTER_PUBLIC_KEY`) with:
  - Minimum 100 stroops for operational reserves
  - Additional stroops for transaction fees (estimate based on expected throughput)
  - Test with small amounts first, scale up after validation

### 4. Environment Variable Update

- [ ] Set `MOISTELLO_STELLAR_MASTER_SECRET_KEY` to mainnet master secret (from secure vault, never hardcoded)
- [ ] Set `MOISTELLO_STELLAR_MASTER_PUBLIC_KEY` to mainnet master public key
- [ ] Verify no environment variable still contains testnet keys

(ADDED — Arc/Keeper; the Stellar lines above still apply.)
- [ ] Set `DEODES_ARC_RPC_URL` to the mainnet Arc RPC endpoint
- [ ] Set `DEODES_ARC_CHAIN_ID` to `5042`
- [ ] Set `DEODES_ARC_USDC_CONTRACT` to the mainnet USDC contract address
- [ ] Set `DEODES_KEEPER_ESCROW_ADDRESS` to the mainnet collection address
      (or `DEODES_KEEPER_INTERVAL_MINUTES=0` to disable the keeper)
- [ ] Set `DEODES_CONTRACT_FACTORY` / `DEODES_CONTRACT_CIRCLE` /
      `DEODES_CONTRACT_REP_REGISTRY` / `DEODES_CONTRACT_GOV_TOKEN` /
      `DEODES_CONTRACT_TREASURY` to mainnet contract IDs
- [ ] Frontend build vars (`17-build-frontend.sh` writes these from secrets):
      `NEXT_PUBLIC_ARC_NETWORK=mainnet`, `NEXT_PUBLIC_ARC_CHAIN_ID=5042`,
      `NEXT_PUBLIC_ARC_RPC_URL`, `NEXT_PUBLIC_ARC_EXPLORER_URL`,
      `NEXT_PUBLIC_DEODES_CONTRACT_ID`, `NEXT_PUBLIC_ONRAMP_ENABLED`,
      `NEXT_PUBLIC_EXPLORE_SOURCE`, `NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID`
- [ ] Frontend server-only vars (`frontend/.env.server`, loaded via the
      `18-systemd.sh` `EnvironmentFile`): `WALLET_HMAC_KEY`, `CIRCLE_ENV`
      (`sandbox` vs `production`), `CIRCLE_API_KEY` / `CIRCLE_TEST_API_KEY`,
      `ONRAMP_REFERRER_DOMAIN`, `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET`,
      `PASSKEY_SERVER_PEPPER`

### 5. Smoke Testing with Real Stroops

Before full launch, run production-like smoke tests:

- [ ] Test circle creation with real stroops
- [ ] Test contribution recording on mainnet
- [ ] Test payout execution on mainnet
- [ ] Verify transactions appear on mainnet block explorer
- [ ] Check account balances and fees
- [ ] Monitor for any rate limiting or RPC issues

### 6. DNS and CORS Configuration

- [ ] Update DNS records to point to mainnet infrastructure
- [ ] Update `cors.allowed_origins` in config for mainnet domain(s)
- [ ] Verify CORS headers are correct for mainnet endpoints
- [ ] Test API calls from mainnet frontend domain

### 7. Startup Guard Validation

The system includes an automatic guard that refuses startup if mainnet mode is detected with any testnet values:

- [ ] The application startup will validate mainnet config
- [ ] If any testnet defaults are detected, startup will fail with clear error
- [ ] Example error: "mainnet cutover guard: mainnet mode requires all fields to differ from testnet defaults"
- [ ] Fix all reported issues before attempting restart

(ADDED — extended guard behavior. Stellar checks are unchanged; Arc/Keeper/
contracts checks below are new.)

Mainnet mode is ON when `stellar.network == "mainnet"` OR `environment` is
`production`/`mainnet` (case-insensitive). Guard behavior matrix:

| Condition (in mainnet mode)              | Result  |
|------------------------------------------|---------|
| `stellar.*` still testnet values         | REJECT (unchanged legacy check; only evaluated when `stellar.network == "mainnet"`) |
| `arc.chain_id != 5042`                   | REJECT  |
| `arc.rpc_url` empty                      | REJECT  |
| `arc.rpc_url` contains `testnet`         | REJECT  |
| `arc.usdc_contract` empty                | REJECT  |
| `keeper.escrow_address` empty AND `keeper.interval_minutes != 0` | REJECT |
| `keeper.escrow_address` empty AND `keeper.interval_minutes == 0` (keeper disabled) | PASS (documented opt-out) |
| any `contracts.*` still a tracked testnet `C...` ID | REJECT |
| `development`/`staging` with testnet-shaped Arc/Keeper/contracts | PASS (guard off) |

### 8. Deploy Pipeline Homes (ADDED)

| Vars | Home |
|------|------|
| `ARC_RPC_URL`, `ARC_CHAIN_ID`, `ARC_USDC_CONTRACT`, `DEODES_CONTRACT_ID`, `KEEPER_ESCROW_ADDRESS`, `KEEPER_INTERVAL_MINUTES`, `WALLET_HMAC_KEY` (generated), `WALLETCONNECT_PROJECT_ID`, `CIRCLE_*`, `ONRAMP_*`, `GOOGLE_*`, `PASSKEY_SERVER_PEPPER`, `EXPLORE_SOURCE` | `08-secrets.sh` → `$APP_DIR/secrets/.env` |
| `DEODES_ARC_RPC_URL/CHAIN_ID/USDC_CONTRACT`, `DEODES_KEEPER_ESCROW_ADDRESS/INTERVAL_MINUTES`, `MOISTELLO_CONTRACT_*` (+ new `MOISTELLO_CONTRACT_CIRCLE`) | `12-backend-env.sh` → backend `.env`, with a mainnet pre-gate mirroring the backend guard |
| `NEXT_PUBLIC_ARC_*`, `NEXT_PUBLIC_DEODES_CONTRACT_ID`, `NEXT_PUBLIC_ONRAMP_ENABLED`, `NEXT_PUBLIC_EXPLORE_SOURCE`, `NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID`, `NEXT_PUBLIC_API/WS_URL` | `17-build-frontend.sh` → `frontend/.env.local` (build-time) |
| `WALLET_HMAC_KEY`, `CIRCLE_*`, `ONRAMP_*`, `GOOGLE_*`, `PASSKEY_*`, `REDIS_*` | `17-build-frontend.sh` → `frontend/.env.server` (runtime) → `18-systemd.sh` `EnvironmentFile` on `moistello-frontend.service` |

## Post-Cutover Verification

- [ ] Monitor application logs for errors
- [ ] Verify rate limiting is working correctly
- [ ] Check transaction processing latency
- [ ] Monitor account balance for unexpected fees
- [ ] Verify all user-facing endpoints work correctly
- [ ] Check for any webhook delivery issues
- [ ] Monitor database performance with mainnet data volume
- [ ] Have rollback plan ready if critical issues emerge

## Rollback Plan (If Needed)

If critical issues emerge:

- [ ] Revert config to testnet values
- [ ] Update environment variables back to testnet keys
- [ ] Restart application (will pass startup guard)
- [ ] Update DNS to route back to testnet or previous version
- [ ] Investigate root cause
- [ ] Fix issues on staging before next cutover attempt

## Verification Commands

```bash
# Verify mainnet network configuration
gh repo view stellar/js-stellar-sdk
stellar account info <MASTER_PUBLIC_KEY> --network mainnet

# Check application configuration (when running)
curl http://localhost:1100/health | jq '.config.stellar.network'

# Monitor logs for startup guard
grep "mainnet cutover guard" logs/*.log

# Verify contract IDs are mainnet (not testnet)
curl https://soroban-mainnet.stellar.org/<contract-id>/info
```

## Support

If issues arise during cutover, ensure you have:
- Access to secure key vault
- Database backups
- Previous deployment artifacts
- Stellar support channel access
- Team communication channels ready
