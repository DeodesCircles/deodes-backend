# Business Metrics Documentation

Deodes exports Prometheus business metrics for monitoring contributions, payouts, user registrations, and active users.

## Metrics Catalog

- `deodes_contributions_total{status, currency, method}`: Counter of contribution transactions.
- `deodes_contribution_volume_total{currency}`: Counter of total contribution volume.
- `deodes_payouts_total{status, currency, method}`: Counter of payout transactions.
- `deodes_payout_volume_total{currency}`: Counter of total payout volume.
- `deodes_user_registrations_total{method}`: Counter of user sign-ups.
- `deodes_active_users_current`: Gauge representing active platform users.

## Database Pool Metrics

- `deodes_db_pool_utilization{type}`: Gauge of pool state (`open`, `in_use`, `idle`, `max_open`, `saturation`).
- `deodes_db_pool_wait_total`: Counter of callers that had to wait for a free connection.
- `deodes_db_pool_wait_seconds_total`: Counter of time spent waiting to acquire a connection.
- `deodes_db_idle_in_transaction_connections`: Gauge of connections idle in a transaction for over 60s (leak indicator).
- `deodes_db_pool_alerts_total{kind}`: Counter of alerts raised (`pool_saturated`, `idle_in_transaction`).

Alerts are also logged at warn level with the offending pool numbers.

## Indexer Metrics & Lag Endpoint

The indexer lag endpoint (`/internal/indexer/lag`) exposes current ledger lag, queue depth, and event processing rate.

- `deodes_indexer_ledger_lag`: Gauge representing the difference in ledgers from head.
- `deodes_indexer_queue_depth`: Gauge of pending unprocessed events in queue.
- `deodes_indexer_processing_rate`: Gauge of events processed per second.

For recovery procedures during an extended indexer outage, refer to the [Indexer Extended-Outage Recovery Runbook](ops/INDEXER-OUTAGE-RECOVERY.md).

## Useful Prometheus Queries

- Contribution Rate (per sec):
  `rate(deodes_contributions_total[5m])`
- Payout Volume (sum by currency):
  `sum(rate(deodes_payout_volume_total[1h])) by (currency)`
- Indexer Lag:
  `deodes_indexer_ledger_lag`

