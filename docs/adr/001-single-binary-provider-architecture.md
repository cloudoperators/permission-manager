# ADR-001: Single-Binary Provider Architecture

## Status

Pending review

## Context

The pm-controller started as a single binary reconciling ASRs against Kubernetes clusters. As the system grew to support GitHub and GreenhouseTeams providers, we faced a choice:

1. **Multi-binary split** — each provider runs as an independent binary with its own manager, leader election, and deployment. Each binary watches the same ASR kind, filtering via ConnectorBinding predicates to decide whether to process.
2. **Single binary with provider dispatch** — one reconciler per ASR kind dispatches to a provider interface based on the referenced ConnectorBinding type.

Key concerns:
- The Kubernetes Operator SDK [best practice](https://sdk.operatorframework.io/docs/best-practices/common-recommendation/) recommends a single controller per GVK to avoid status conflicts, unnecessary reconciliations from generation changes, and split-brain concurrency issues.
- In the multi-binary model, all provider controllers wake up on every ASR change and check "is this mine?" — this is a dispatch problem solved with extra controllers rather than a switch statement.
- Connector-specific watches (e.g., watch `GitHubConnector`) can be expressed as secondary watches with `EnqueueRequestsFromMapFunc` within a single reconciler.
- A well-designed provider interface offers equivalent extensibility to separate binaries — adding a provider is still "add one file implementing the interface."

## Decision

Run a **single binary** with one reconciler for the ASR kind that dispatches to providers via a registry/strategy pattern:

```go
type Provider interface {
    Reconcile(ctx context.Context, asr *v1.ApplicationSpecificRole) ([]metav1.Condition, error)
    Cleanup(ctx context.Context, asr *v1.ApplicationSpecificRole) error
}
```

The reconciler:
1. Fetches the ASR.
2. Resolves referenced ConnectorBindings.
3. Groups ConnectorBindings by provider type.
4. Iterates over matched providers, calling `provider.Reconcile()`.
5. Aggregates errors and conditions from all providers.
6. Writes a unified status (per-provider conditions + aggregated `Ready`).

```go
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    asr := &v1.ApplicationSpecificRole{}
    if err := r.Get(ctx, req.NamespacedName, asr); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    bindings := r.resolveConnectorBindings(ctx, asr)
    var allConditions []metav1.Condition
    var errs []error

    for providerType, cbs := range groupByProvider(bindings) {
        provider, ok := r.providers[providerType]
        if !ok {
            continue // unknown provider, skip
        }
        conditions, err := provider.Reconcile(ctx, asr)
        allConditions = append(allConditions, conditions...)
        if err != nil {
            errs = append(errs, err)
        }
    }

    r.updateStatus(ctx, asr, allConditions, errs)
    return ctrl.Result{}, errors.Join(errs...)
}
```

Each provider registers secondary watches for its connector type:

```go
func (p *KubernetesProvider) SetupWithManager(mgr ctrl.Manager, builder *ctrl.Builder) {
    builder.Watches(
        &v1alpha1.KubernetesConnector{},
        handler.EnqueueRequestsFromMapFunc(p.mapConnectorToASRs),
    )
}
```

### Deployment Model

| Component | Binary | Leader Election ID |
|-----------|--------|-------------------|
| All providers + aggregation | `pm-controller` | `pm-controller.permission-manager.cloudoperators.dev` |

The single binary:
- Registers all known provider implementations via a registry at startup.
- Providers that are not configured (no ConnectorBindings of that type exist) are effectively no-ops.
- Has one `ctrl.Manager`, one set of health probes, metrics, and leader election.
- Deploys as a single Kubernetes `Deployment`.

Shared logic lives in `pkg/` (provider interface, status helpers, CCRN parser). Each provider implementation lives in its own package under `internal/provider/`.

## Consequences

**Benefits:**
- One controller per GVK — eliminates status conflicts, split-brain issues, and unnecessary reconciliations.
- Simpler operations — one Deployment, one ServiceAccount, one RBAC configuration.
- No wasted wake-ups — only the relevant provider is dispatched, not N controllers filtering by predicate.
- Equally extensible — adding a provider is implementing the `Provider` interface in a new package and registering it.
- Natural error aggregation — the reconcile loop collects all provider results and writes status once.
- Sequencing is explicit — phases (validate → dispatch → aggregate) are clear in one code path.

**Trade-offs:**
- Blast radius — a panic in one provider's code crashes the entire controller (mitigated with `recover()` in the dispatch loop).
- Memory footprint — all provider schemes are loaded even if not all are deployed (negligible in practice).
- Release coupling — all providers share a release cadence (acceptable given shared CRD ownership).
- Cannot independently scale providers (mitigated: the single reconciler already handles all provider work per-ASR atomically).

## References

- [Operator SDK: Common Recommendations](https://sdk.operatorframework.io/docs/best-practices/common-recommendation/) — one controller per GVK
