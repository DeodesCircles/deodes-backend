# Indexer Extended-Outage Recovery Runbook

This runbook outlines the operational recovery procedure when the Deodes Arc event indexer encounters an extended outage or severe lag. (The Arc poller itself lands in Phase 2.6; this runbook defines its operating contract now so 2.6 builds to it.)

---

## 1. Overview & Decision Flowchart

When the indexer stops or falls behind, chain events accumulate on Arc. When restarted, the indexer must safely catch up without missing contract events or overwhelming downstream message brokers.

```
                  +---------------------------+
                  |  Indexer Outage Detected  |
                  +-------------+-------------+
                                |
                                v
                  +---------------------------+
                  |    Step 1: Assess Lag     |
                  |  (Blocks & Time Lag)      |
                  +-------------+-------------+
                                |
             +------------------+------------------+
             |                                     |
             v                                     v
     [ Lag < 5,000 blocks ]               [ Lag >= 5,000 blocks ]
     (Short outage)                       (Extended outage)
             |                                     |
             v                                     v
+--------------------------+          +--------------------------+
|  Strategy A: Live Catchup|          | Strategy B: Batched Replay|
|  - Start standard indexer|          | - Scale up batch size    |
|  - Monitor lag reduction |          | - Run catch-up profile   |
+------------+-------------+          +------------+-------------+
             |                                     |
             +------------------+------------------+
                                |
                                v
                  +---------------------------+
                  | Step 2: Cursor Continuity |
                  | Verify sequential replay  |
                  +-------------+-------------+
                                |
                                v
                  +---------------------------+
                  | Step 3: Event Audit       |
                  | Validate deduplication    |
                  +-------------+-------------+
                                |
                                v
                  +---------------------------+
                  | Step 4: All-Clear Criteria|
                  +---------------------------+
```

---

## 2. Step 1: Assess Lag & Health

### 2.1 Query the Indexer Lag Endpoint

Check the internal lag endpoint exposed by the API server:

```bash
# JSON format
curl -s http://localhost:1100/internal/indexer/lag | jq .

# Prometheus format
curl -s "http://localhost:1100/internal/indexer/lag?format=prometheus"
```

**Response Example:**
```json
{
  "ledgerLag": 2400,
  "queueDepth": 0,
  "processingRate": 10.0
}
```
(`ledgerLag` is blocks behind head; the key name is frozen for dashboard compatibility.)

### 2.2 Inspect Redis Cursor & Head Height

Compare the stored indexer cursor with the latest Arc block:

```bash
# 1. Fetch current indexer cursor from Redis
redis-cli GET indexer:cursor

# 2. Fetch current indexer lag in seconds
redis-cli GET indexer:lag_seconds

# 3. Query the Arc RPC for the latest block
cast block-number --rpc-url $DEODES_ARC_RPC_URL
```

### 2.3 Calculate Total Block Gap

$$\text{Block Gap} = \text{RPC Latest Block} - \text{Stored Cursor Block}$$

- **Estimated Time to Recover:**
  $$\text{Recovery Time (s)} = \frac{\text{Block Gap}}{\text{Processing Rate (blocks/s)}}$$

---

## 3. Step 2: Choose Recovery Strategy

### Strategy A: Live Sequential Catchup (Lag < 5,000 blocks)
The indexer's built-in `Reconciler` automatically detects gaps and catches up without special tuning.

1. Ensure RabbitMQ and Postgres connections are healthy:
   ```bash
   curl -s http://localhost:1100/health | jq .dependencies
   ```
2. Start or restart the indexer process (see Phase 2.6 for the service name).
3. Watch recovery logs for `eth_getLogs` backfill progress.

---

### Strategy B: High-Throughput Batched Backfill (Lag >= 5,000 blocks)
For extended outages, widen the `eth_getLogs` block ranges to catch up faster while preventing memory spikes:

1. Update configuration environment variables temporarily:
   ```bash
   export DEODES_INDEXER_BATCH_SIZE=200
   export DEODES_INDEXER_POLL_INTERVAL=500ms
   ```
2. Restart the indexer with the high-throughput parameters.
3. Monitor processing speed and ensure the database connection pool is not exhausted.
4. Restore the standard values after the gap closes — sustained wide ranges risk RPC rate limits.

---

## 4. Step 3: Verify Cursor Continuity & Scan for Gaps

Run the following SQL and Redis queries to confirm no blocks were skipped during the replay.

### 4.1 SQL: Check Sequence Continuity in Processed Contributions & Payouts

```sql
-- Check for unexpected gaps in recorded circle rounds
SELECT
    circle_id,
    round_number,
    COUNT(*) as total_contributions,
    MIN(created_at) as first_recorded,
    MAX(created_at) as last_recorded
FROM contributions
WHERE created_at >= NOW() - INTERVAL '24 hours'
GROUP BY circle_id, round_number
ORDER BY circle_id, round_number;

-- Verify verification status of recent payouts
SELECT
    verification_status,
    COUNT(*)
FROM payouts
WHERE created_at >= NOW() - INTERVAL '24 hours'
GROUP BY verification_status;
```

### 4.2 Deduplication Audit

Verify that the indexer deduplication cache correctly suppressed duplicate event hashes:

```bash
# Check deduplication hit metrics in Prometheus
curl -s http://localhost:1100/metrics | grep deodes_indexer
```

Dedup key is `(tx_hash, log_index)` — replays of the same range must not double-count.

---

## 5. Step 4: All-Clear Criteria

Before declaring the outage resolved, verify that all of the following conditions are met:

- [ ] **Block Lag:** `GET /internal/indexer/lag` reports `ledgerLag < 10` blocks (or lag_seconds < 60s).
- [ ] **Health Endpoint:** `GET /health` reports `status: "ok"` and `dependencies.postgres`, `dependencies.redis` are `healthy`.
- [ ] **Event Queue:** RabbitMQ queue depth for `deodes.notifications` and `deodes.events` has normalized to near zero.
- [ ] **WebSocket Broadcasts:** Real-time updates are propagating to active circle rooms without backlog.
- [ ] **No Unhandled Errors:** Indexer log output contains zero `level=error` logs over the last 15 minutes.

---

## 6. Emergency Rollback / Reseed Procedure

If the indexer cursor becomes corrupted or points beyond the valid chain height:

1. Stop the indexer immediately.
2. Reset the Redis cursor to a known valid block number:
   ```bash
   # Reset to target block (e.g. 500 blocks prior to head)
   cast block-number --rpc-url $DEODES_ARC_RPC_URL  # note head H
   redis-cli SET indexer:cursor <H-500>
   ```
3. Restart the indexer to trigger re-scan with automatic deduplication.
