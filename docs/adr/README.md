# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for the pm-controller.

ADRs document significant design decisions made during development. Each record captures the context, the decision, and its consequences.

## Index

| # | Title | Status | Date |
|---|-------|--------|------|
| [015](015-scope-separated-clusterroles.md) | Scope-Separated ClusterRoles for RBAC Generation | Pending review | 2026-06 |
| [017](017-resource-ccrn-types.md) | Resource-Scoped CCRN Types for Fine-Grained Kubernetes RBAC | Draft | 2026-09 |
| [018](018-capability-mapping-rework.md) | Scope-Aware Capability Mapping with Shared Aggregated ClusterRoles | Draft | 2026-09 |

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
