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
- **`deny[]`**: present in the schema but **not enforced by the Kubernetes controller**. Exclusions are expressed on the ConnectorBinding capability rule via `excludeResources` (see [Deny-driven rules](#deny-driven-rules-excluderesources)), not here.

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
      READ:
        - apiGroups: ["*"]
          resources: ["*"]
          verbs: [get, list, watch]
      WRITE:
        - apiGroups: ["*"]
          resources: ["*"]
          verbs: [create, update, patch, delete]
```

Each capability maps to a list of rules mirroring the native Kubernetes
`PolicyRule` shape (`apiGroups` + `resources` + `verbs`). All three fields are
required per rule — there are no implicit wildcards, so operators must be
explicit about what they grant.

Key fields:
- **`provider`** — connector operators filter by this (self-select)
- **`definition`** — provider-specific configuration (opaque payload)

#### Deny-driven rules (`excludeResources`)

Kubernetes RBAC is additive: there is no native "allow everything **except**
secrets". To avoid forcing operators to hand-enumerate every apiGroup/resource
across a fleet, a rule may declare `excludeResources` and let the controller
compose the grant from the target cluster's discovered API groups, subtracting
only the denied resources:

```yaml
capabilityMapping:
  READ:
    - apiGroups: ["*"]
      resources: ["*"]
      excludeResources:
        - apiGroups: [""]
          resources: [secrets]
        - apiGroups: ["core.gardener.cloud"]
          resources: [internalsecrets]
      verbs: [get, list, watch]
```

At compile time, for each target cluster the controller reads the cluster's
discovered API groups (`Cluster.status.discoveryCache.groups`) and splits them
into *denied* groups (named in `excludeResources`) and *the rest*. It emits one
wildcard rule (`resources: ["*"]`) for the rest, and for each denied group it
**live-enumerates** that group's resources (via the cluster's discovery API) and
grants them minus the denied ones. Verbs are copied onto every emitted rule.

Rules and constraints:
- `excludeResources` is only valid alongside `resources: ["*"]` — subtracting
  from an explicit resource list is rejected as a configuration error.
- Each selector must name non-empty `apiGroups` and `resources`; selectors carry
  no verbs (they inherit the parent rule's).
- Subresources are handled: excluding `secrets` also drops `secrets/status`.
- The per-group resource lookup is **live** (not read from the discovery cache,
  which holds group names only) so a newly-added secret-like resource cannot be
  silently granted.
- **Fail-closed**: if a target cluster's discovery is unavailable (no
  `discoveryCache`, or the discovery API is unreachable), the ASR is marked
  `InvalidConfig=True` / `Ready=False` with reason `DiscoveryUnavailable` and
  **nothing is applied** — the controller never emits a grant that could leak a
  denied resource. The ASR reconciles automatically once discovery recovers.

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

Wildcards (`*`) are supported for broad resource selection:
- `urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/*` — all clusters
- `urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-*` — prefix matching
- `urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu-1/*` — all namespaces on a cluster

### Exclusions

The schema carries an ASR-level `deny[]` field, but the Kubernetes controller
does not act on it: there is no code path that subtracts denied CCRNs from the
expanded permission set. Exclusions that the controller enforces are declared on
the ConnectorBinding capability rule via `excludeResources` (see
[Deny-driven rules](#deny-driven-rules-excluderesources)), which subtracts
specific resources (for example, secrets) from an otherwise-wildcard grant.

## Capability Mapping (Per-Connector)

Capabilities are abstract permission levels used in ASR `permissions[].capability` fields. **Each connector defines its own mapping** from these abstract capabilities to provider-specific actions via the `ConnectorBinding.spec.definition.capabilityMapping` field.

There is no universal capability taxonomy and none is built into the controller: the capability names in an ASR (`READ`, `WRITE`, or anything else) mean only what the referenced ConnectorBinding's `capabilityMapping` says they mean. The tables below are **illustrative examples** of how a binding might map capabilities, not a fixed set the controller enforces.

**Kubernetes connector (example mapping):**

| Capability | Maps to (verbs on the rule's resources) |
|-----------|------------------------------------------|
| READ | get, list, watch |
| WRITE | create, update, patch, delete |
| * | * (all verbs) |

Each capability maps to one or more rules in the `PolicyRule` shape (`apiGroups` + `resources` + `verbs`), optionally with `excludeResources` (see [Deny-driven rules](#deny-driven-rules-excluderesources)). A binding defines whatever capability names it needs; a real binding may expose more or fewer than the three shown here.

**GitHub connector (example mapping):**

| Capability | Permission |
|-----------|-----------|
| READ | pull |
| WRITE | push |
| * | admin |

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
