# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for the pm-controller.

ADRs document significant design decisions made during development. Each record captures the context, the decision, and its consequences.

## Index

| # | Title | Status | Date |
|---|-------|--------|------|
| [001](001-multi-binary-provider-architecture.md) | Multi-Binary Provider Architecture | Pending review | 2026-06 |
| [002](002-server-side-apply-for-provider-status.md) | Server-Side Apply for Provider Status | Pending review | 2026-06 |
| [003](003-status-aggregator-controller.md) | Status Aggregator Controller | Pending review | 2026-06 |
| [004](004-connector-binding-indexer-pattern.md) | ConnectorBinding Indexer Pattern | Pending review | 2026-06 |
| [005](005-ccrn-based-resource-addressing.md) | CCRN-Based Resource Addressing | Pending review | 2026-06 |
| [006](006-per-provider-finalizers.md) | Per-Provider Finalizers | Pending review | 2026-06 |
| [007](007-greenhouse-teams-conflict-detection.md) | GreenhouseTeams Conflict Detection (Team-Only Mode) | Pending review | 2026-06 |
| [008](008-cross-namespace-resource-management.md) | Cross-Namespace Resource Management | Pending review | 2026-06 |
| [009](009-capability-mapping-via-connector-binding.md) | Capability Mapping via ConnectorBinding | Pending review | 2026-06 |
| [010](010-multi-cluster-client-factory.md) | Multi-Cluster Client Factory with Caching | Pending review | 2026-06 |
| [011](011-greenhouse-clientutil-divergence.md) | Own Cluster Client Implementation vs. Greenhouse clientutil | Pending review | 2026-06 |
| [012](012-finalizer-guarded-cleanup-with-orphan-tracking.md) | Finalizer-Guarded Cleanup with Orphan Tracking | Pending review | 2026-06 |
| [013](013-cluster-selection-mechanism.md) | Cluster Selection Mechanism — CCRN Patterns vs Label Selectors | Pending review | 2026-06 |
| [014](014-kubeconfig-credential-plugin-rejection.md) | Kubeconfig Credential Plugin Rejection | Pending review | 2026-06 |
| [015](015-scope-separated-clusterroles.md) | Scope-Separated ClusterRoles for RBAC Generation | Pending review | 2026-06 |
| [016](016-capability-taxonomy-revision.md) | Capability Taxonomy Revision (SECURE + Wildcard) | Pending review | 2026-06 |
| [017](017-resource-ccrn-types.md) | Resource-Scoped CCRN Types for Fine-Grained Kubernetes RBAC | Draft | 2026-09 |

## Format

Each ADR follows this structure:

```markdown
# ADR-NNN: Title

## Status
Accepted | Superseded | Deprecated

## Context
Why was a decision needed?

## Decision
What was decided?

## Consequences
What are the trade-offs?
```
