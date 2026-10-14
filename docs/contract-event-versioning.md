# Contract Event Versioning

The indexer writes every decoded EVM event to `contract_events`, an
append-only audit log. Each row records the **contract version** that emitted
it: the runtime bytecode hash (`eth_getCode` keccak) of the contract at the
time the event was recorded.

## Why it exists

An Arc contract address is stable across proxy upgrades. The address keeps
serving while the code behind it can change, so the address alone cannot tell
you which implementation produced a given event. That makes historical events
ambiguous across deployments: after an upgrade, a payload recorded by v1 and
one recorded by v2 look identical in the log.

Recording the code hash alongside each event keeps them distinguishable, so
events can be queried and interpreted per implementation.

## What is recorded

`contract_events.contract_version` holds the hex-encoded 32-byte runtime
code hash, or the literal string `unknown`.

The column is `NOT NULL`. `unknown` is used instead of `NULL` deliberately:

- It is a first-class, queryable value, so "show me the events whose version I
  could not determine" is a normal filter.
- A non-null column keeps a single equality comparison working, instead of
  quietly returning a different set of rows through a forgotten `IS NULL` branch.

Rows written before the column existed are backfilled to `unknown` by migration
`046_add_contract_event_version`. Their original version is not recoverable: the
code that produced them cannot be read back from the chain after the fact.

## How the version is resolved

The version is **not** in the event logs. A log carries topics and data, never
the implementation that emitted it, so it cannot be decoded from the receipt
alongside the event.

It is read from the chain instead: `eth_getCode` returns the deployed
bytecode, whose keccak is the version. The Arc poller (Phase 2.6) resolves it
per emitting address.

Lookups are cached per contract address (15 minutes), because the resolver is
consulted once per event and a contract's code only changes on upgrade.
Failures are cached for a much shorter window, so a broken or misconfigured
contract cannot cause a chain read per event while a transient network error
still clears quickly.

For deployments where versions are known ahead of time, a static map
(address → code hash, recorded at deploy time in `deployments.*.json`)
resolves without any chain read and has no staleness window at all.

## Known limitation

The hash is read from the contract's code **as of the block the RPC node
currently serves**. It is therefore the version the contract runs *now*, not a
guaranteed record of the version at the event's historical block. If a contract
is upgraded between an event being emitted and its version being resolved, that
event is recorded against the newer version.

This is why the value is recorded rather than derived on read: recording it
still pins the answer at index time, which is strictly better than resolving it
later, and a read-time resolution would drift for every historical row.

## Failure behaviour

Resolution never fails the write. An event that cannot be attributed to a
version is still recorded, as `unknown`, because dropping the audit row loses
more than the version does. Every such event increments
`deodes_indexer_contract_version_unknown_total` and is logged, so a
persistently broken resolver is visible on `/metrics` rather than silent.

## Querying

`internal/domain/contractevent` reads the log. `ListFilter.ContractVersion`
filters on the version, which is the point of the column:

```go
events, total, err := repo.List(ctx, contractevent.ListFilter{
    ContractID:      "0x70A7…",
    ContractVersion: "9f2c…",
}, 1, 50)
```

`ContractVersion: "unknown"` selects exactly the events with no resolved
version. Omitting the field does not filter on version at all — the two are
deliberately different.

`ListFilter` also filters by `TxHash`, `ContractID`, `EventType`, and an
inclusive block range. Results are ordered newest block first with a stable
tie-break, and the total matching count is returned alongside the page.
