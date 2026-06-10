# Core Concepts

This document explains the core concepts of Permission Manager and how they relate to each other.

## Overview

Permission Manager is a **"Permissions as Code"** framework that uses Kubernetes Custom Resource Definitions (CRDs) to define access roles. All permission definitions live as YAML in Git, are validated in CI, and consumed by connector-specific operators that provision access on target systems.

The source CRD schema (`permission-manager.cloudoperators.dev/v1`) is **vendor-neutral**. It defines WHAT access should exist — not HOW any specific system implements it.

## CRD Hierarchy

```
FunctionalRole (requestable bundle)
  └── contains 1..N →  ApplicationSpecificRole (atomic access grant)
                          └── references 1..N → ConnectorBinding (provider connection)
                          └── defines 1..N →    Permission (resource + capability)
```

## ApplicationSpecificRole (ASR)

The **atomic unit of access**. An ASR grants specific capabilities on specific resources through one or more connectors.

```yaml
apiVersion: permission-manager.cloudoperators.dev/v1
kind: ApplicationSpecificRole
metadata:
  name: team-alpha-k8s-monitoring
spec:
  name: TEAM_ALPHA_K8S_MONITORING_ADMIN    # ← This IS the OIDC group name
  connectorRefs:
    - k8s-connector                         # ← Which provider(s) handle this
  permissions:
    - resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu-1/monitoring"
      capability: WRITE
    - resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu-1"
      capability: READ
  tags: [prod]
  classification: restricted
```

Key fields:
- **`metadata.name`** — canonical reference key (used in FunctionalRole.roles[].name)
- **`spec.name`** — OIDC group name + display identifier (case-sensitive)
- **`connectorRefs[]`** — connectors self-select by their provider type
- **`permissions[]`** — CCRN-based resource + capability pairs
- **`deny[]`** — exclusions subtracted from the permission set

## FunctionalRole (FR)

A **bundle of ASRs** forming a requestable profile with approval workflows. End-users request FunctionalRoles; the system provisions access for all included ASRs.

```yaml
apiVersion: permission-manager.cloudoperators.dev/v1
kind: FunctionalRole
metadata:
  name: team-alpha-platform
spec:
  name: "Team Alpha - Platform"
  type: access                              # access | elevated | passport
  roles:
    - name: team-alpha-k8s-monitoring       # references ASR metadata.name
    - name: team-alpha-github-repos
  approvers:
    - userID: "admin-user-1"
      step: 10
  validity:
    default: P6M
    maximum: P12M
```

Key fields:
- **`type`** — `access` (standard), `elevated` (time-limited, sensitive), `passport` (pre-approval gate)
- **`roles[]`** — references ASRs by `metadata.name`
- **`passports[]`** — pre-approval gates (user must hold these to request this FR)

## ConnectorBinding

Defines the **connection to an external system**. This is the seam between the open-source schema and provider-specific implementation.

```yaml
apiVersion: permission-manager.cloudoperators.dev/v1
kind: ConnectorBinding
metadata:
  name: k8s-connector
spec:
  name: Kubernetes Production
  provider: kubernetes                      # ← Controller filters by this
  definition:
    capabilityMapping:
      READ: "get,list,watch"
      WRITE: "create,update,patch,delete"
      "*": "*"
```

Key fields:
- **`provider`** — connector operators filter by this (self-select)
- **`definition`** — provider-specific configuration (opaque payload)

## CCRN (Common Cloud Resource Name)

A standardized, machine-readable URN format for identifying resources across systems:

```
urn:ccrn:<resource-type>.<domain>.<api-group>/v1/<resource-path>
```

### Supported Types

| Domain | Type | Format | Use Case |
|--------|------|--------|----------|
| k8s | cluster | `.../v1/<cluster>` | Cluster-scoped RBAC |
| k8s | namespace | `.../v1/<cluster>/<namespace>` | Namespace-scoped RBAC |
| github | organization | `.../v1/<instance>/<org>` | GitHub org membership |
| github | repository | `.../v1/<instance>/<org>/<repo>` | Repository access |
| github | repository_type | `.../v1/<instance>/<org>/<visibility>` | Repo class access |

### Wildcards

Wildcards (`*`) are supported for broad resource selection:
- `urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/*` — all clusters
- `urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-*` — prefix matching
- `urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu-1/*` — all namespaces on a cluster

### Deny Rules

Deny rules subtract from the expanded permission set:

```yaml
permissions:
  - resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/*"
    capability: READ
deny:
  - resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/security-cluster"
    capability: READ
```

Result: READ on all clusters **except** `security-cluster`.

## Capability Mapping (Per-Connector)

Capabilities are abstract permission levels used in ASR `permissions[].capability` fields. **Each connector defines its own mapping** from these abstract capabilities to provider-specific actions via the `ConnectorBinding.spec.definition.capabilityMapping` field.

There is no universal capability taxonomy — each downstream system has its own permission model, and the mapping is connector-specific:

**Kubernetes connector:**

| Capability | Verbs |
|-----------|-------|
| READ | get, list, watch |
| WRITE | create, update, patch, delete |
| OPERATE | exec, port-forward, proxy |
| MANAGE | bind, escalate, impersonate |
| * | * (all verbs) |

**GitHub connector:**

| Capability | Permission |
|-----------|-----------|
| READ | pull |
| WRITE | push |
| * | admin |

**Greenhouse Teams connector:**

| Capability | TeamRole |
|-----------|----------|
| READ | cluster:view |
| WRITE | cluster:edit |
| * | cluster:admin |

When adding a new downstream connector (e.g., Vault, ArgoCD, a database), you define a new capability matrix in the ConnectorBinding that maps the abstract capabilities to that system's native permission model.

## How Connectors Self-Select

A single ASR can reference multiple ConnectorBindings with different providers:

```yaml
spec:
  connectorRefs: [k8s-connector, github-connector]
```

Each connector operator:
1. Watches all ASRs
2. Resolves `connectorRefs` → ConnectorBinding → checks if `provider` matches
3. Processes **only** matching ASRs
4. Provisions access on its target system

This means one ASR with multiple connectorRefs gets processed by multiple providers independently — no duplication of ASR definitions needed.

## OIDC Group Model

The identity model is simple and universal:
- `spec.name` = OIDC group name (exact, case-sensitive)
- Connectors create bindings for that group name
- No dependency on any specific IAM/IdP platform
- Any OIDC-compliant provider that issues group claims works

## ServiceIdentity

Technical/machine user template for service accounts:

```yaml
apiVersion: permission-manager.cloudoperators.dev/v1
kind: ServiceIdentity
metadata:
  name: monitoring-service
spec:
  name: MONITORING_SERVICE
  connectorRefs: [k8s-connector]
  roles:
    - name: team-alpha-k8s-monitoring
```
