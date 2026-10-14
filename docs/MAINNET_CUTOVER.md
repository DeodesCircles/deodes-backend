# Mainnet Cutover Checklist (Arc)

This checklist guides the transition from testnet to mainnet. Follow each step carefully to avoid sending testnet configuration to production.

## Pre-Cutover: Staging Environment Dry Run

Run this entire checklist on a staging environment first to validate the process.

- [ ] Deploy all V1 contracts to Arc testnet (`deodes-contracts/CEREMONY.md` on testnet)
- [ ] Record testnet contract addresses in `deployments.testnet.json`
- [ ] Verify contract functionality with smoke tests
- [ ] Switch staging config to testnet (use testnet values from defaults)
- [ ] Fund staging operator address with sufficient USDC for gas
- [ ] Run full smoke test suite against testnet endpoint
- [ ] Verify all DNS and CORS configurations work on staging
- [ ] Document any issues or deviations

## Mainnet Cutover Steps

### 1. Contract Deployment (Run on Mainnet)

Per `deodes-contracts/CEREMONY.md` (two operators, fresh keys, hardware wallets):

- [ ] Deploy treasury, DEO token, registry, factory, governance to Arc mainnet
- [ ] Verify every contract on the explorer (green check before proceeding)
- [ ] Record all mainnet addresses in `deployments.mainnet.json`
- [ ] Transfer ownerships to the multisig; record ceremony tx hashes

### 2. Configuration Update

Update `config/config.yaml` with mainnet values:

- [ ] Set `arc.chain_id: 5042` (mainnet; the `5042002` default is Arc testnet)
- [ ] Set `arc.rpc_url: "https://rpc.mainnet.arc.io"` (or your RPC provider — must NOT contain `testnet`)
- [ ] Set `arc.usdc_contract` to the mainnet USDC contract address from
      `docs.arc.io/arc/references/contract-addresses.md` (has no safe default)
- [ ] Set `arc.circle_address` / `arc.treasury_address` from `deployments.mainnet.json`
- [ ] Set `keeper.escrow_address` to the mainnet operator collection address
      whenever the keeper is enabled (`keeper.interval_minutes != 0`, default
      `15`). To run mainnet with NO auto-debits, set
      `keeper.interval_minutes: 0` instead of configuring an escrow address.
      The escrow MUST differ from `arc.treasury_address` (startup guard rejects it).
- [ ] Verify config file does not contain any testnet values

### 3. Operator Funding

- [ ] Fund mainnet operator address (`DEODES_KEEPER_ESCROW_ADDRESS`) with
  - Minimum USDC for gas reserves
  - Additional USDC for transaction fees (estimate based on expected throughput)
  - Test with small amounts first, scale up after validation

### 4. Environment Variable Update

- [ ] Set `DEODES_ARC_RPC_URL` to the mainnet Arc RPC endpoint
- [ ] Set `DEODES_ARC_CHAIN_ID` to `5042`
- [ ] Set `DEODES_ARC_USDC_CONTRACT` to the mainnet USDC contract address
- [ ] Set `DEODES_ARC_CIRCLE_ADDRESS` / `DEODES_TREASURY_ADDRESS` from `deployments.mainnet.json`
- [ ] Set `DEODES_KEEPER_ESCROW_ADDRESS` to the mainnet collection address
      (or `DEODES_KEEPER_INTERVAL_MINUTES=0` to disable the keeper)
- [ ] Verify no environment variable still contains testnet values
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

### 5. Smoke Testing with Real Funds

Before full launch, run production-like smoke tests:

- [ ] Test circle creation with real USDC
- [ ] Test contribution recording on mainnet
- [ ] Test payout execution on mainnet
- [ ] Verify transactions appear on the Arc mainnet explorer
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
- [ ] Fix all reported issues before attempting restart

Mainnet mode is ON when `environment` is `production`/`mainnet`
(case-insensitive). Guard behavior matrix:

| Condition (in mainnet mode)              | Result  |
|------------------------------------------|---------|
| `arc.chain_id != 5042`                   | REJECT  |
| `arc.rpc_url` empty                      | REJECT  |
| `arc.rpc_url` contains `testnet`         | REJECT  |
| `arc.usdc_contract` empty                | REJECT  |
| `keeper.escrow_address` empty AND `keeper.interval_minutes != 0` | REJECT |
| `keeper.escrow_address` empty AND `keeper.interval_minutes == 0` (keeper disabled) | PASS (documented opt-out) |
| `keeper.escrow_address` equals `arc.treasury_address` | REJECT |
| `development`/`staging` with testnet-shaped Arc/Keeper | PASS (guard out of scope) |

### 8. Deploy Pipeline Homes

| Vars | Home |
|------|------|
| `ARC_RPC_URL`, `ARC_CHAIN_ID`, `ARC_USDC_CONTRACT`, `DEODES_CONTRACT_ID`, `KEEPER_ESCROW_ADDRESS`, `KEEPER_INTERVAL_MINUTES`, `WALLET_HMAC_KEY` (generated), `WALLETCONNECT_PROJECT_ID`, `CIRCLE_*`, `ONRAMP_*`, `GOOGLE_*`, `PASSKEY_SERVER_PEPPER`, `EXPLORE_SOURCE` | `08-secrets.sh` → `$APP_DIR/secrets/.env` |
| `DEODES_ARC_RPC_URL/CHAIN_ID/USDC_CONTRACT/CIRCLE_ADDRESS/TREASURY_ADDRESS`, `DEODES_KEEPER_ESCROW_ADDRESS/INTERVAL_MINUTES` | `12-backend-env.sh` → backend `.env`, with a mainnet pre-gate mirroring the backend guard |
| `NEXT_PUBLIC_ARC_*`, `NEXT_PUBLIC_DEODES_CONTRACT_ID`, `NEXT_PUBLIC_ONRAMP_ENABLED`, `NEXT_PUBLIC_EXPLORE_SOURCE`, `NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID`, `NEXT_PUBLIC_API/WS_URL` | `17-build-frontend.sh` → `frontend/.env.local` (build-time) |
| `WALLET_HMAC_KEY`, `CIRCLE_*`, `ONRAMP_*`, `GOOGLE_*`, `PASSKEY_*`, `REDIS_*` | `17-build-frontend.sh` → `frontend/.env.server` (runtime) → `18-systemd.sh` `EnvironmentFile` on `deodes-frontend.service` |

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
# Verify Arc chain + contract state (when running)
cast chain-id --rpc-url $DEODES_ARC_RPC_URL
cast call $DEODES_CIRCLE_ADDRESS "memberCount()(uint256)" --rpc-url $DEODES_ARC_RPC_URL

# Check application configuration (when running)
curl http://localhost:1100/health | jq '.dependencies'

# Monitor logs for startup guard
grep "mainnet" logs/*.log

# Verify contract addresses are mainnet (not testnet)
# (compare against deodes-contracts/deployments.mainnet.json)
```

## Support

If issues arise during cutover, ensure you have:
- Access to secure key vault
- Database backups
- Previous deployment artifacts
- Arc RPC provider support channel access
- Team communication channels ready
