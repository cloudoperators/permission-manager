# ADR-002: Server-Side Apply for Provider Status

## Status

Pending review

## Context

With multiple provider controllers running independently, each needs to write its reconciliation status to the ASR's `status.providers` map without conflicts. The status map looks like:

```yaml
status:
  providers:
    kubernetes:
      ready: true
      message: "All 3 clusters provisioned successfully"
      targets: [...]
      lastReconciled: "2026-06-08T14:00:00Z"
    github:
      ready: true
      message: "2 targets reconciled"
      targets: [...]
      lastReconciled: "2026-06-08T14:01:00Z"
```

Traditional approaches:
- **Read-Modify-Write** — prone to conflicts when two providers reconcile concurrently.
- **Strategic Merge Patch** — doesn't clearly express ownership of map keys.
- **JSON Patch** — fragile if the path doesn't exist yet.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Read-Modify-Write** | Fetch current status, modify in-memory, write back the full status subresource | Conflict-prone when multiple providers reconcile concurrently; requires retries and optimistic locking |
| **Strategic Merge Patch** | Patch only the provider's key in the status map using a strategic merge patch | Ownership semantics are unclear — no server-side tracking of which controller owns which key; accidental overwrites possible |
| **JSON Patch** | Use JSON Patch operations (`add`/`replace` at specific paths) | Fragile — fails if intermediate paths don't exist; requires knowing current state to construct correct operations |
| **Server-Side Apply with per-provider field managers** ✅ | Each provider SSA-patches only its own status slice with a distinct field manager | Chosen — see Decision |

## Decision

Use **Server-Side Apply (SSA)** with distinct field managers per provider:

```go
// Field manager: "pm-controller-kubernetes", "pm-controller-github", etc.
fieldManager := "pm-controller-" + providerKey

c.Status().Patch(ctx, patchObj, client.Apply,
    client.FieldOwner(fieldManager),
    client.ForceOwnership,
)
```

The shared helper lives in `pkg/provider/status.go`:
- `PatchProviderStatus(ctx, client, asr, providerKey, status)` — SSA-patches a single provider's status entry.
- `RemoveProviderStatus(ctx, client, asr, providerKey)` — removes a provider's entry via MergePatch (SSA cannot remove map keys).

The patch object is an `Unstructured` containing only the fields the provider owns — no other status fields are touched.

## Consequences

**Benefits:**
- Zero conflicts — each provider owns its own slice of the status map; concurrent writes never conflict.
- Declarative ownership — the API server tracks which field manager owns each key.
- No read-before-write — the provider doesn't need to fetch the current status before patching.
- Clean removal — `ForceOwnership` means a provider can reclaim its fields even if another actor accidentally touches them.

**Trade-offs:**
- SSA with `Unstructured` requires manual construction of the patch object (no type safety from the generated types).
- `lastReconciled` timestamps change on every reconcile, which can trigger the aggregator. Providers skip the patch if meaningful content (ready, message, targets) hasn't changed to avoid infinite loops.
- Removing a provider key requires a separate MergePatch call since SSA doesn't support map key deletion.
