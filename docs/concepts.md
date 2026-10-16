# Core Concepts

This document explains the core concepts of Permission Manager and how they relate to each other.

## Overview

Permission Manager is a **"Permissions as Code"** framework that uses Kubernetes Custom Resource Definitions (CRDs) to define access roles. All permission definitions live as YAML in Git, are validated in CI, and consumed by connector-specific operators that provision access on target systems.

The source CRD schema (`permission-manager.cloudoperators.dev/v1alpha1`) is **vendor-neutral**. It defines WHAT access should exist — not HOW any specific system implements it.

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
apiVersion: permission-manager.cloudoperators.dev/v1alpha1
kind: ApplicationSpecificRole
metadata:
  name: team-alpha-k8s-monitoring
spec:
  name: TEAM_ALPHA_K8S_MONITORING_ADMIN    # ← This IS the OIDC group name
  connectorRefs:
    - k8s-connector                         # ← Which provider(s) handle this
  permissions:
    - resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu-1/monitoring"
      capability: OPERATE
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
- **`deny[]`**: present in the schema but **not enforced by the Kubernetes controller** — there is no code path that subtracts denied CCRNs from the expanded permission set.

## FunctionalRole (FR)

A **bundle of ASRs** forming a requestable profile with approval workflows. End-users request FunctionalRoles; the system provisions access for all included ASRs.

```yaml
apiVersion: permission-manager.cloudoperators.dev/v1alpha1
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
apiVersion: permission-manager.cloudoperators.dev/v1alpha1
kind: ConnectorBinding
metadata:
  name: k8s-connector
spec:
  name: k8s-connector                       # ← embedded verbatim into shared role names
  provider: kubernetes                      # ← Controller filters by this
  definition:
    capabilityMapping:
      cluster:                              # whole-cluster CCRNs
        MANAGE:
          clusterRoleName: cluster-admin    # bind directly to a named ClusterRole
        READ:
          clusterRoleSelectors:
            - matchLabels:
                rbac.authorization.k8s.io/aggregate-to-view: "true"
      namespace:                            # namespace CCRNs
        MANAGE:
          clusterRoleSelectors:
            - matchLabels:
                rbac.authorization.k8s.io/aggregate-to-admin: "true"
        READ:
          clusterRoleSelectors:
            - matchLabels:
                rbac.authorization.k8s.io/aggregate-to-view: "true"
```

For the **kubernetes** provider, `capabilityMapping` is nested by scope. Each
scope maps a **closed** set of capability names to the RBAC the controller
provisions; a name outside its scope's set makes the ConnectorBinding invalid:

- `cluster` — `MANAGE`, `READ`
- `namespace` — `MANAGE`, `OPERATE`, `READ`

Each capability entry takes one of two mutually exclusive shapes:

- **`clusterRoleName`** — bind directly to an existing named ClusterRole. Valid
  only for cluster `MANAGE` (binding to `cluster-admin`).
- **`clusterRoleSelectors`** (+ optional `rules`) — the controller builds a
  shared aggregated root ClusterRole from the selectors, plus a baseline child
  ClusterRole from the rules that the root also aggregates.

Other providers define their own `definition` shape: the **github** provider, for
example, maps each capability to a string permission (`pull` / `push` / `admin`).

Key fields:
- **`provider`** — connector operators filter by this (self-select)
- **`definition`** — provider-specific configuration (opaque payload)

## Partial Provisioning & Cluster Readiness

An ASR often targets many clusters (e.g. a `prod-*` wildcard). When only some of
those clusters are usable, the kubernetes provider applies RBAC to the usable
ones instead of failing the whole ASR:

- **Ready clusters** — RBAC is applied; the target reports `Ready`.
- **Missing clusters** — a named cluster (or pattern) that resolves to nothing in
  the inventory reports `Error` ("cluster not found"). If the cluster previously
  had RBAC and has since vanished from the inventory, its stale status entry is
  **pruned** rather than retried forever (there is no remote left to reach).
- **Not-Ready clusters** — a cluster that exists but is not `Ready=True` is left
  untouched and reports `Pending` ("cluster not Ready"). Any RBAC already applied
  is preserved across the blip, and the target is retried once the cluster
  recovers.

As long as at least one target compiles, top-level `Ready=False` with reason
`PartialProvisioning` — access is live where it can be, and the partial state is
visible in `status.providers.kubernetes.targets`.

This is distinct from **hard-blocking** configuration errors: invalid CCRN syntax
or an unknown capability marks the whole ASR `InvalidConfig=True` and applies
nothing, because the intent itself cannot be resolved. A missing or not-Ready
cluster is an environmental condition, not a bad spec, so it degrades rather than
blocks.

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

Cluster-name wildcards are supported for broad selection and matched against the Greenhouse `Cluster` inventory:
- `urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/*` (all clusters)
- `urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-*` (prefix matching)

A **wildcard in the namespace segment is not supported**: the Kubernetes provider rejects it as an invalid CCRN (`EnsureASRValidity`, reason `InvalidCCRN`) rather than expanding it to every namespace on the cluster. Target namespaces must be named explicitly.

### Exclusions

The schema carries an ASR-level `deny[]` field, but the Kubernetes controller
does not act on it: there is no code path that subtracts denied CCRNs from the
expanded permission set. Scope the grant instead — map each capability to only
the ClusterRoles (via `clusterRoleSelectors`) or rules it should include.

## Capability Mapping (Per-Connector)

Capabilities are abstract permission levels used in ASR `permissions[].capability` fields. **Each connector defines its own mapping** from these abstract capabilities to provider-specific actions via the `ConnectorBinding.spec.definition.capabilityMapping` field.

**Kubernetes connector.** The capability vocabulary is **closed** and nested by
the CCRN's scope. The controller enforces it: a capability outside its scope's
set, or mapped with the wrong entry shape, makes the ConnectorBinding invalid.

| Scope | Capabilities | Maps to |
|-------|--------------|---------|
| cluster | `MANAGE`, `READ` | `MANAGE` → bind to `cluster-admin` (via `clusterRoleName`); `READ` → aggregated ClusterRole from `clusterRoleSelectors` (+ optional `rules`) |
| namespace | `MANAGE`, `OPERATE`, `READ` | aggregated ClusterRole from `clusterRoleSelectors` (+ optional `rules`), bound namespaced |

A binding maps only the capabilities it needs; an ASR referencing an unmapped
capability is reported as a configuration error rather than silently granted.

**GitHub connector (example mapping):** other providers define their own
free-form mapping. The github provider, for example, maps each capability to a
string permission:

| Capability | Permission |
|-----------|-----------|
| READ | pull |
| WRITE | push |
| ADMIN | admin |

When adding a new downstream connector (e.g., Vault, ArgoCD, a database), you define its capability mapping in the ConnectorBinding, mapping the abstract capability names to that system's native permission model.

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
apiVersion: permission-manager.cloudoperators.dev/v1alpha1
kind: ServiceIdentity
metadata:
  name: monitoring-service
spec:
  name: MONITORING_SERVICE
  connectorRefs: [k8s-connector]
  roles:
    - name: team-alpha-k8s-monitoring
```
