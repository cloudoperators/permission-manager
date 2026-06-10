# ADR-001: Multi-Binary Provider Architecture

## Status

Pending review

## Context

The pm-controller started as a single binary reconciling ASRs against Kubernetes clusters. As the system grew to support GitHub and GreenhouseTeams providers, we faced a choice:

1. **Monolithic controller** — all providers compiled and run in a single binary with a shared manager.
2. **Multi-binary split** — each provider runs as an independent binary with its own manager, leader election, and deployment.

Key concerns:
- Provider teams may have different release cadences.
- A bug in one provider's reconciler should not crash or block others.
- Resource consumption (memory for caches, API watches) differs wildly between a provider watching remote cluster Secrets and one only managing local CRs.
- Some providers need additional scheme registrations (Greenhouse types, GitHub CRDs) that are irrelevant to others.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Monolithic controller** | All providers compiled and run in a single binary with a shared manager | A bug in one provider crashes all; shared scheme bloat; uniform scaling ignores differing resource needs; coupled release cadences |
| **Plugin-based architecture (shared binary, dynamic loading)** | Single binary loads provider logic as Go plugins (`.so` files) at runtime | Go plugins are fragile (exact build-flag match required), hard to debug, limited ecosystem support; operational complexity without isolation benefits |
| **CRD-per-provider with a single orchestrator** | One controller binary dispatches reconcile work to provider-specific sub-reconcilers within the same process | Still single failure domain; providers share memory/goroutines; no independent scaling or deployment |
| **Multi-binary split** ✅ | Each provider runs as an independent binary with its own manager, leader election, and deployment | Chosen — see Decision |

## Decision

Split the controller into **four independent binaries**:

| Binary | Package | Leader Election ID |
|--------|---------|-------------------|
| `pm-controller-kubernetes` | `controller/cmd/kubernetes/` | `pm-controller-kubernetes.permission-manager.cloudoperators.dev` |
| `github-provider` | `controller/cmd/github/` | `github-provider.permission-manager.cloudoperators.dev` |
| `greenhouse-teams` | `controller/cmd/greenhouse-teams/` | `greenhouse-teams.permission-manager.cloudoperators.dev` |
| `pm-status-aggregator` | `controller/cmd/aggregator/` | `pm-status-aggregator.permission-manager.cloudoperators.dev` |

Each binary:
- Registers only the schemes it needs.
- Has its own `ctrl.Manager`, health probes, metrics, and leader election.
- Deploys as a separate Kubernetes `Deployment` with its own `ServiceAccount` and RBAC.
- Filters ASRs via a `predicate` that checks the ConnectorBinding index before reconciling.

Shared logic lives in `pkg/` (provider constants, status helpers, CCRN parser) and is imported by all binaries.

## Consequences

**Benefits:**
- Independent scaling — the kubernetes provider (which manages remote cluster clients) can run more replicas independently of the lightweight GitHub provider.
- Blast-radius isolation — a panic in the GitHub provider does not affect Kubernetes RBAC provisioning.
- Selective deployment — operators can deploy only the providers they need.
- Clean dependency boundaries — the GitHub binary does not carry Greenhouse scheme types and vice versa.

**Trade-offs:**
- Operational overhead — four deployments to manage instead of one.
- Shared state requires coordination — the status aggregator must watch status changes from all providers.
- ConnectorBinding index is duplicated per binary (not shared across processes).
- Helm chart is more complex with per-provider template files.
