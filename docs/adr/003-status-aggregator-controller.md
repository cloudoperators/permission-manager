# ADR-003: Status Aggregator Controller

## Status

Pending review

## Context

Each provider independently writes its reconciliation result to `status.providers[<key>]`. Users and tooling need a single top-level signal: "Is this ASR fully reconciled?" and "Is it degraded?"

Options considered:
1. **Let each provider compute holistic status** — but providers don't know about each other and can't determine if all providers are done.
2. **Compute conditions client-side** (in the UI or CLI) — pushes complexity to every consumer.
3. **Dedicated aggregator controller** — watches status changes and computes conditions.

## Decision

Implement a **separate status aggregator controller** (`internal/controller/aggregator/`) that:

1. Watches all ASRs (no predicate filtering).
2. On each reconcile, reads `status.providers` and counts how many providers report `ready: true`.
3. Sets standard `metav1.Condition` entries on the ASR:
   - **Ready** — `True` if all providers are ready, `False` otherwise, `Unknown` if no providers have reported.
   - **Degraded** — `True` if some (but not all) providers are not ready.
4. Uses `meta.SetStatusCondition` for proper condition merging.
5. Skips the status update if conditions haven't meaningfully changed (type + status + reason match) to avoid reconcile loops.

The aggregator has zero provider-specific logic — it only reads the provider map structure.

## Consequences

**Benefits:**
- Single source of truth for "is this ASR healthy?" — consumers check conditions, not the providers map.
- Provider-agnostic — adding a new provider requires zero changes to the aggregator.
- Deployable alongside any provider binary or as a standalone — the Kubernetes provider binary includes it for convenience, and a standalone `cmd/aggregator/` exists for dedicated deployment.

**Trade-offs:**
- Additional reconcile traffic — every provider status update triggers the aggregator. Mitigated by the no-change short-circuit.
- Eventual consistency — there's a brief window where providers have updated but conditions haven't been recomputed yet.
- The aggregator and provider reconcilers can race on status updates — SSA field ownership prevents conflicts, but the aggregator uses a regular status Update (it owns the `conditions` field, providers own `providers`).
