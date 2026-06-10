# ADR-012: Finalizer-Guarded Cleanup with Orphan Tracking

## Status

Pending review

## Context

The Kubernetes provider uses a **finalizer** to ensure RBAC resources (ClusterRoles, ClusterRoleBindings, RoleBindings) are cleaned up on remote clusters when an ASR is deleted or its permissions change. The `RBACCleaner.CleanupAll` method iterates all known clusters and deletes resources by label selector.

A problem arises when a target cluster is **unreachable** at cleanup time:

1. **Transiently unreachable** — network partition, node maintenance, or temporary API server downtime.
2. **Permanently gone** — the cluster was decommissioned or its kubeconfig Secret was removed.

The permission-manager's core purpose is enforcing access control. Silently leaving stale RBAC permissions on a cluster — even temporarily — contradicts this purpose. If an ASR is being deleted, it means access should be revoked. Allowing deletion to proceed while permissions remain active on an unreachable cluster creates a security gap.

This manifested initially as E2E tests hanging when unreachable clusters blocked finalizer removal indefinitely. The naive fix (skip unreachable, remove finalizer) was rejected because it prioritizes operational convenience over security correctness.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Best-effort skip (fire-and-forget)** | Skip unreachable, remove finalizer, log and move on | Silently leaves active permissions — defeats the purpose of permission-manager |
| **Best-effort skip + orphan tracking** | Skip unreachable, remove finalizer, record orphans for later cleanup | Still allows a window where revoked permissions are active; recovery is not guaranteed |
| **Bounded retries then force-remove** | Retry N times, then remove finalizer anyway | Arbitrary timeout; still leaves permissions active after threshold |
| **Block forever (no escape hatch)** | Requeue indefinitely until cluster returns | No way out for permanently-gone clusters; requires manual kubectl to strip finalizer |
| **Finalizer-guarded + explicit exit paths** ✅ | Block until clean, with defined exit conditions | Chosen — see Decision |

## Decision

Adopt a **finalizer-guarded cleanup** strategy: the finalizer is **held** (ASR deletion is blocked) until RBAC resources are confirmed cleaned from all clusters. The controller retries periodically. Three well-defined exit paths allow the finalizer to be removed:

### Exit path 1: All clusters cleaned successfully

The normal happy path. `CleanupAll` reaches all clusters, deletes RBAC resources, returns no errors and no skipped clusters. Finalizer is removed, ASR deletion proceeds.

### Exit path 2: Cluster removed from Greenhouse

When a cluster is decommissioned via Greenhouse (Cluster CR deleted), the `ClusterInventory` no longer contains it. On the next cleanup attempt, `CleanupAll` only iterates clusters still in the inventory — the gone cluster is simply not iterated. Finalizer proceeds.

This is the correct behavior: if the cluster itself is gone, so are its RBAC resources. No cleanup needed.

### Exit path 3: Operator explicitly abandons cleanup

An operator can set the annotation `permission-manager.cloudoperators.dev/abandon-cleanup: "true"` on the ASR. This signals deliberate acceptance that RBAC resources may remain on unreachable clusters. The controller removes the finalizer on the next reconcile.

This is the escape hatch for edge cases where a cluster is unreachable but not yet removed from Greenhouse (e.g., stuck in a decommissioning process).

### Orphan tracking in ASR status

While the finalizer is held, unreachable clusters are recorded in the provider status for observability:

```yaml
status:
  providers:
    kubernetes:
      orphanedClusters:
        - clusterName: "consumer-3"
          reason: "cluster unreachable: connection refused"
          skippedAt: "2026-06-09T15:30:00Z"
```

This provides:
- **Observability** — operators can query for ASRs with stuck deletions and see which clusters are blocking.
- **Automation input** — monitoring can alert on ASRs with orphaned clusters older than a threshold.
- **Recovery tracking** — when the orphan reconciler successfully cleans a cluster, it removes the entry.

### Orphan reconciler

A separate controller watches ASRs with non-empty `orphanedClusters`:
- Periodically retries cleanup on listed clusters (every 5 minutes).
- On success: removes the entry from `orphanedClusters`, emits a `Normal` event.
- When all entries are cleared and the ASR is pending deletion: the main reconciler's next run finds no skipped clusters and removes the finalizer.

### Cleanup during live reconciliation

The same pattern applies when clusters are removed from an ASR's permissions while the ASR is still alive (`CleanupOrphans` path). If a previously-targeted cluster is unreachable during orphan cleanup, it remains in `orphanedClusters` and is retried by the orphan reconciler.

### Design principles

- **Security over convenience** — stale permissions are a security gap; blocking deletion is the safer default.
- **Explicit exit paths only** — the finalizer is removed only when cleanup is confirmed, the cluster is gone from Greenhouse, or an operator deliberately abandons.
- **Reachable errors still block** — if we *can* reach a cluster but deletion fails (authz, server error), that's also retried.
- **Observable** — every blocked cluster is tracked in status, not just logged.
- **No silent data loss** — permissions are never silently left active without explicit human decision.

## Consequences

**Benefits:**
- No window of stale permissions after ASR deletion (unless explicitly abandoned).
- Security posture matches the controller's purpose — access revocation is guaranteed.
- Observable — operators see exactly what's blocking and why.
- Clean integration with Greenhouse cluster lifecycle — decommissioned clusters don't block.
- Escape hatch exists for edge cases without requiring raw kubectl finalizer manipulation.

**Trade-offs:**
- ASR deletion is blocked while any target cluster is unreachable and still in Greenhouse inventory. This is by design but may surprise operators unfamiliar with the pattern.
- Requires operator action (abandon annotation or Greenhouse cluster removal) for permanently-unreachable clusters that haven't been decommissioned.
- Adds complexity: orphan reconciler, status field, annotation handling.
- E2E tests that delete ASRs with unreachable clusters must either fix the cluster, use the abandon annotation, or remove the cluster from inventory.

**Mitigations:**
- Clear Warning events emitted when finalizer is held due to unreachable clusters.
- Documentation of the three exit paths for operators.
- `abandon-cleanup` annotation provides a controlled escape without raw finalizer manipulation.
- Orphan reconciler automatically resolves transient failures without operator intervention.
