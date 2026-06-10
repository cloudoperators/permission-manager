# ADR-004: ConnectorBinding Indexer Pattern

## Status

Pending review

## Context

Each reconciliation of an ASR must determine whether it references a ConnectorBinding belonging to the current provider. A naive approach would fetch and inspect every ConnectorBinding on every reconcile. With hundreds of ASRs reconciling frequently, this creates significant API server load.

Additionally, when a ConnectorBinding is created, updated, or deleted, all ASRs referencing it must be re-reconciled.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Fetch-on-reconcile** | Each reconcile issues a List call for ConnectorBindings and filters in-memory | O(N) API calls per reconcile; unacceptable at scale with hundreds of ASRs |
| **Controller-runtime cache with field selector** | Use the shared informer cache with a server-side field selector | Field selectors are limited to metadata fields; cannot filter by nested spec fields without a custom index, and cross-object filtering (CB→ASR) still requires manual reverse mapping |
| **Shared external cache (Redis / etcd sidecar)** | Store the CB index in an external data store shared across provider binaries | Adds operational complexity; the index is small enough to fit in-memory per-process; provider isolation is a feature, not a bug |
| **In-memory index with event-driven updates** ✅ | Watch-based index per binary with O(1) lookup and reverse-enqueue | Chosen — see Decision |

## Decision

Each provider binary maintains an **in-memory index** of ConnectorBindings it handles:

```go
type ConnectorBindingIndexer struct {
    // maps ConnectorBinding name → ConnectorBindingSpec
    bindings map[string]pmv1.ConnectorBindingSpec
    mu       sync.RWMutex
}
```

The index provides:
- **Fast lookup** — `HasAnyKubernetesRef(connectorRefs []string) bool` checks if any ref points to a known provider CB.
- **Event-driven updates** — watches ConnectorBinding events and updates the index in-memory.
- **Reverse mapping** — `EnqueueASRsForConnectorBinding(client)` returns an `EventHandler` that maps CB changes to ASR reconcile requests.
- **Startup sync** — on controller start, lists all existing ConnectorBindings to populate the index before processing ASR events.

The startup sync is registered as a `manager.Runnable` that executes after the cache is synced but before reconcilers start processing:

```go
mgr.Add(&startupSyncRunnable{syncer: syncer})
```

## Consequences

**Benefits:**
- O(1) provider-routing per reconcile — no API calls needed to check if an ASR is relevant.
- Enables efficient event-driven reconciliation — CB changes immediately trigger re-reconciliation of affected ASRs.
- Decouples the predicate filter from API server calls — predicates run synchronously in the event loop and must be fast.

**Trade-offs:**
- Memory overhead — the entire set of ConnectorBindings for the provider is held in memory. Acceptable for the expected scale (tens to low hundreds of CBs per namespace).
- Eventual consistency — between a CB being created and the watch event arriving, an ASR reconcile might miss it. Mitigated by the startup sync and the watch-based re-enqueue.
- Each provider binary has its own index — no shared state between processes. This is intentional (provider isolation) but means CB data is duplicated.
