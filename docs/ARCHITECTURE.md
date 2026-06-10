# Architecture

## What Permission Manager Is (and Isn't)

**Permission Manager is a configuration framework**, not an Identity Provider. It does not:
- Authenticate users
- Issue tokens or credentials
- Manage user directories
- Implement approval workflows itself

What it DOES do:
- Defines access roles as code (Kubernetes CRDs in Git)
- Reconciles those definitions into actual permissions on downstream systems
- Maps OIDC group names to provider-specific access controls

The system has two halves:

1. **Downstream provisioning** (this repo) — controllers that watch ASR CRDs and create the actual RBAC/team/binding resources on target systems
2. **Upstream IDP sync** (not yet open-sourced) — an operator that reads FunctionalRole CRDs and syncs them to your Identity Provider as requestable roles

## Greenhouse Dependency

The controller is designed to run on a [Greenhouse](https://github.com/cloudoperators/greenhouse) management cluster. It relies on:

| Greenhouse Resource | Used For |
|--------------------|----------|
| `Cluster` CRD (greenhouse.sap/v1alpha1) | Cluster inventory — wildcard CCRN expansion, knowing which clusters exist |
| Kubeconfig `Secrets` | Remote cluster access — provisioning RBAC on consumer clusters |
| Plugin system | Deployment vehicle — installs the controller via Helm |

The kubernetes provider discovers target clusters by watching Greenhouse `Cluster` objects. When an ASR uses a wildcard CCRN like `urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/*`, it expands against this inventory. Remote cluster access is via kubeconfig Secrets that Greenhouse manages.

## Overview

Permission Manager Controller is a multi-provider Kubernetes controller that watches `ApplicationSpecificRole` (ASR) CRDs and provisions access on target systems through pluggable connectors.

```
Source CRDs (vendor-neutral, in Git)
  │
  ├── kubernetes provider  → ClusterRole / RoleBinding on remote clusters
  ├── github provider      → GithubTeam / GithubTeamRepository CRDs
  ├── greenhouse-teams     → Team / TeamRoleBinding CRDs
  └── (future connectors)  → Any system with an RBAC model
```

## Design Principles

1. **Source CRDs are vendor-neutral** — They define WHAT access should exist, not HOW
2. **Connectors self-select** — Each connector filters ASRs by `provider` field in ConnectorBinding
3. **OIDC groups as universal identity** — Group name = `spec.name`, no IAM dependency
4. **Provider isolation** — Each provider binary deploys, scales, and upgrades independently
5. **Shared status model** — All providers write to `status.providers[<key>]` via Server-Side Apply

## Multi-Binary Architecture

```
┌─────────────────────────────────────────────────────┐
│                 Management Cluster                    │
│                                                       │
│  ┌──────────┐ ┌──────────┐ ┌────────────────┐       │
│  │kubernetes│ │  github  │ │greenhouse-teams│       │
│  └─────┬────┘ └─────┬────┘ └───────┬────────┘       │
│        │             │              │                 │
│  ┌─────┴─────────────┴──────────────┴─────┐         │
│  │         status-aggregator               │         │
│  │  (computes Ready/Degraded conditions)   │         │
│  └─────────────────────────────────────────┘         │
└───────────────────────┬─────────────────────────────┘
                        │
            ┌───────────┼───────────┐
            │           │           │
      ┌─────┴──┐  ┌────┴───┐  ┌───┴────┐
      │prod-eu1│  │prod-us1│  │staging-*│    ← Consumer Clusters
      └────────┘  └────────┘  └────────┘
```

## ConnectorBinding Routing

An in-memory index maps ConnectorBinding names to their provider type. When reconciling an ASR:

1. Resolve each `connectorRef` → ConnectorBinding
2. Check `spec.provider` field
3. Only process if provider matches this binary's type

This means one ASR can have `connectorRefs: [k8s, github]` and both providers process it independently.

## Kubernetes Provider

### Reconciliation Flow

```
ASR created/modified
  → Parse permissions[] CCRNs
  → Expand wildcards (from Greenhouse Cluster inventory)
  → Subtract deny[] entries
  → For each target cluster:
      → Generate ClusterRole (verbs from capability mapping)
      → Generate ClusterRoleBinding (cluster-scope CCRNs)
      → Generate RoleBindings (namespace-scope CCRNs)
      → Apply via create-or-patch
  → Update status.providers["kubernetes"]
```

### CCRN → Binding Scope

| CCRN Type | Output |
|-----------|--------|
| `cluster.k8s.*` | ClusterRoleBinding |
| `namespace.k8s.*` | RoleBinding in specified namespace |

### Wildcard Expansion

- `cluster.k8s.*/v1/*` → all clusters in the Greenhouse Cluster inventory
- `cluster.k8s.*/v1/prod-*` → prefix matching against cluster names
- `namespace.k8s.*/v1/prod-eu-1/*` → all namespaces on that cluster

New clusters/namespaces automatically expand existing wildcards.

### Reconciliation Triggers

1. ASR created/modified/deleted
2. ConnectorBinding modified → re-reconcile all referencing ASRs
3. New cluster onboarded → expand wildcards
4. New namespace created → expand namespace wildcards

## GitHub Provider

### Flow

```
ASR with github ConnectorBinding
  → Parse github CCRNs (repository, organization, repository_type)
  → Map capability → permission (via ConnectorBinding capabilityMapping)
  → Group repositories by org + permission level
  → Create/update GithubTeam CRD (one per org)
  → Create/update GithubTeamRepository CRD (grouped repos + permission)
```

## Greenhouse Teams Provider

### Flow

```
ASR with greenhouse-teams ConnectorBinding
  → Parse k8s CCRNs
  → Map capability → TeamRole (via capabilityMapping: READ→cluster:view, etc.)
  → Create Team CRD (mappedIdPGroup = spec.name)
  → Create TeamRoleBinding CRDs (one per role+cluster, merging namespaces)
```

### Conflict Detection

When an ASR references both `kubernetes` and `greenhouse-teams` ConnectorBindings, the greenhouse-teams provider operates in **Team-only mode**: creates the Team CR but skips TeamRoleBindings (since the kubernetes provider handles RBAC directly with higher fidelity).

## Status Aggregation

Each provider writes to `status.providers["<name>"]` using Server-Side Apply with distinct field managers. The status-aggregator controller watches for changes and computes:

- **`Ready`** condition — all providers report success
- **`Degraded`** condition — partial failures across providers

## Remote Cluster Access

The kubernetes provider accesses remote clusters via kubeconfig Secrets stored in the management cluster. It maintains a client cache keyed by cluster name, refreshing when Secrets change.

## Namespace Caching

For wildcard namespace expansion, the controller maintains a per-cluster namespace cache. Watches are only established on clusters with active namespace-wildcard ASRs (efficient — no unnecessary watches).

## CCRN Domain Configuration

The CCRN domain (`ccrn.cloudoperators.dev` by default) is configurable:

- In Go code: set `ccrn.DefaultDomain` before starting the controller
- In CCRN definition Helm templates: set `ccrn.apiGroup` in values.yaml

This allows deployments to use custom CCRN registries while maintaining the same controller logic.
