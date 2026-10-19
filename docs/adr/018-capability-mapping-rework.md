# ADR-018: Scope-Aware Capability Mapping with Shared Aggregated ClusterRoles

## Status

Draft

## Context

The current Kubernetes provider uses a ConnectorBinding `capabilityMapping` that maps symbolic
capability names (e.g. `READ`, `WRITE`, `ACCESS_CREDENTIALS`) to a list of
`(apiGroups, resources, verbs)` triples. This mapping has two compounding problems.

### Problem 1: No scope awareness

The same `capabilityMapping` is consulted for every Kubernetes CCRN type regardless of scope.
A `cluster.k8s` CCRN and a `namespace.k8s` CCRN referencing the same CB resolve `READ` to
the same `(apiGroups, resources, verbs)` bundle. This conflates two distinct things:

- For `cluster.k8s`, the capability defines read access across the whole cluster, including
  cluster-scoped resources (nodes, PersistentVolumes, CRDs, webhook configurations).
- For `namespace.k8s`, the capability defines read access within a namespace. Cluster-scoped
  resources listed in the mapping land in the Role but are silently ignored by Kubernetes
  because namespace-scoped Role bindings cannot grant access to cluster-scoped resources.

There is no way to express "for cluster scope, READ means X; for namespace scope, READ means Y."

### Problem 2: Open vocabulary is hard to reason about

The capability name is a free string. Operators can invent arbitrary names (`OPERATE`,
`ACCESS_CREDENTIALS`, `SUPER_ADMIN`) with no controller enforcement. The result is that
real deployments converge on a small practical set of roles anyway, while the spec allows
a combinatorial space that is hard to audit and easy to misconfigure.

### Problem 3: Role+RoleBinding prevents aggregation for namespace scope

`namespace.k8s` CCRNs currently produce a `Role` + `RoleBinding` in each target namespace.
Kubernetes `Role` objects have no `aggregationRule` field. This means CRD-owning teams
cannot extend namespace permissions by creating ClusterRoles with aggregation labels - a
pattern the Kubernetes built-in `admin`/`edit`/`view` roles rely on. Cluster-scope already
uses ClusterRole+ClusterRoleBinding and can participate in aggregation; namespace-scope
cannot. Switching `namespace.k8s` to ClusterRole+RoleBinding (a RoleBinding can reference
a ClusterRole and still confines access to the binding's namespace) resolves this asymmetry.

### Problem 4: excludeResources adds complexity with no production usage

The `excludeResources` deny-driven expansion feature, which enumerates cluster resources
via live discovery and subtracts a named exclusion set, has no production usage and
introduces significant controller complexity (discovery caching, per-cluster expansion,
fail-closed error handling). It is dropped in this rework.

### Use cases that cannot be expressed today

| Use case | What is needed | Gap |
|---|---|---|
| Cluster admin | Full cluster access | `cluster.k8s` + `*` works but is undifferentiated |
| Developer | Namespace admin + cluster viewer | No "cluster viewer" capability that excludes secrets |
| Operator developer | Namespace admin + targeted CRD/webhook access + cluster viewer | Mixed CCRN types have no coherent capability model |

## Decision

### 1. Closed capability vocabulary per scope

Replace the open-vocabulary flat `capabilityMapping` with a scope-nested structure. Valid
capability names are a closed set, enforced by the controller. Any capability name not in
the set for its scope is rejected as `InvalidCCRN`.

This ADR is the decision of record for the capability vocabulary; there is no prior
written taxonomy it inherits from. The names `READ`, `OPERATE`, and `MANAGE` are chosen to
read as an increasing-authority progression (view, then operate on workloads, then full
administrative control) that an ASR author can reason about without consulting the CB.
`WRITE`, added for the resource CCRN scopes, names create/update/patch/delete on a single
targeted resource type, where the finer READ/WRITE split is useful and OPERATE has no
meaning. The set is intentionally small: real deployments converge on a handful of roles,
so a closed vocabulary is easier to audit than the open string space it replaces.

| Scope | Capability | Semantics |
|---|---|---|
| `cluster.k8s` | `MANAGE` | Full cluster access - direct binding to `cluster-admin` |
| `cluster.k8s` | `READ` | Read-only cluster access, excluding secrets |
| `namespace.k8s` | `MANAGE` | Full namespace administrative access including RoleBinding management |
| `namespace.k8s` | `OPERATE` | Read and write access to workload resources |
| `namespace.k8s` | `READ` | Read-only namespace access, excluding secrets |
| `cluster-resource` | `READ` | `get`, `list`, `watch` |
| `cluster-resource` | `WRITE` | `create`, `update`, `patch`, `delete` |
| `cluster-resource` | `MANAGE` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` |
| `namespace-resource` | `READ` | `get`, `list`, `watch` |
| `namespace-resource` | `WRITE` | `create`, `update`, `patch`, `delete` |
| `namespace-resource` | `MANAGE` | `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` |

### 2. Switch namespace.k8s from Role+RoleBinding to ClusterRole+RoleBinding

`namespace.k8s` CCRNs produce a ClusterRole+RoleBinding instead of Role+RoleBinding. The
RoleBinding remains namespace-scoped, so access is still confined to the target namespace.
This is identical to how the Kubernetes built-in `admin`/`edit`/`view` roles work, and it
enables ClusterRole aggregation for namespace-scoped capabilities.

The per-namespace ClusterRole produced by this path is a shared ClusterRole owned by the
CB (see below), not a per-ASR resource.

### 3. Shared aggregated ClusterRoles per capability per CB

For all capabilities except `cluster:MANAGE`, the controller creates two ClusterRoles on
each target cluster per capability per CB:

1. **Aggregated root ClusterRole** (`pm-s-<cb-name>-<scope>-<capability>`) - has only an
   `aggregationRule`, no inline `rules`. The Kubernetes aggregation controller owns and
   overwrites the `rules` field of any ClusterRole with `aggregationRule`, so inline rules
   on the root are not durable and must not be used.

2. **Baseline child ClusterRole** (`pm-s-<cb-name>-<scope>-<capability>-baseline`) - carries
   the CB-defined baseline rules and is labelled with the CB-private
   `pm.cloudoperators.dev/aggregate-to-<cb-hash>-<scope>-<capability>: "true"` label so it is
   selected only by its own CB's aggregated root. `<cb-hash>` is `ShortHash(cb-name)`. CRD-owning
   teams extend a capability by labelling their ClusterRoles with the shared
   `pm.cloudoperators.dev/aggregate-to-<scope>-<capability>: "true"` label (see below), not the
   CB-private one.

All shared-role names are defensively wrapped so an over-long `<cb-name>` cannot exceed the 253
character Kubernetes name limit: a name over the limit is truncated and suffixed with a hash of
the full name. The `pm-s-` prefix is disjoint from the per-ASR `pm-a-` prefix (decision 12), so a
shared object and a per-ASR object can never collide on name.

Both ClusterRoles carry `pm.cloudoperators.dev/cb: <cb-name>` and
`app.kubernetes.io/managed-by: pm-controller`.

The aggregated root's `aggregationRule` combines:
- The `clusterRoleSelectors` from the CB definition (may include built-in Kubernetes
  aggregation labels such as `rbac.authorization.k8s.io/aggregate-to-admin: "true"`)
- The shared `pm.cloudoperators.dev/aggregate-to-<scope>-<capability>: "true"` selector, which
  selects any team-supplied extension ClusterRole for this scope/capability
- The CB-private `pm.cloudoperators.dev/aggregate-to-<cb-hash>-<scope>-<capability>: "true"`
  selector, which selects this CB's own baseline child. Keeping the baseline label CB-private
  stops one CB's baseline from leaking into another CB's root when both define the same
  scope/capability.

**Secrets semantics by capability:**

- `namespace:READ` - aggregates built-in `view` (excludes Secrets by design) plus baseline
  child. Secrets access is not granted.
- `namespace:OPERATE` - does NOT aggregate built-in `edit` because `edit` grants Secret
  access. The baseline child ClusterRole explicitly enumerates workload resources, omitting
  Secrets. CRD-owning teams can extend via the pm-specific label without pulling in `edit`.
- `namespace:MANAGE` - aggregates built-in `admin`, which grants Secret access. This is
  intentional: a namespace administrator needs to manage Secrets. Secret access is an
  expected and documented part of `namespace:MANAGE`.
- `cluster:READ` - aggregates built-in `view` (covers namespaced resources, excludes
  Secrets). The baseline child enumerates cluster-scoped resources only (nodes,
  PersistentVolumes, Namespaces, etc.) and must explicitly omit Secrets.

`cluster:MANAGE` is a special case: no shared ClusterRole is created. The controller emits
a direct ClusterRoleBinding to the built-in `cluster-admin` ClusterRole.

### 4. Per-ASR output shrinks to bindings for cluster.k8s and namespace.k8s

| CCRN type | Capability | Output on target cluster |
|---|---|---|
| `cluster.k8s` | `MANAGE` | ClusterRoleBinding to `cluster-admin` |
| `cluster.k8s` | `READ` | ClusterRoleBinding to `pm-s-<cb-name>-cluster-read` |
| `namespace.k8s` | `MANAGE` | RoleBinding (namespace-scoped) to `pm-s-<cb-name>-namespace-manage` |
| `namespace.k8s` | `OPERATE` | RoleBinding (namespace-scoped) to `pm-s-<cb-name>-namespace-operate` |
| `namespace.k8s` | `READ` | RoleBinding (namespace-scoped) to `pm-s-<cb-name>-namespace-read` |

No per-ASR ClusterRole or Role is created for `cluster.k8s` or `namespace.k8s` CCRNs.

These coarse bindings are named
`pm-a-<asr-name>-<cb-name>-<scope>-<capability>-<hash>`, where `<hash>` is
`ShortHash(asr-namespace, asr-name, cb-name, scope, capability)`. The hash suffix
disambiguates bindings whose readable fields collide after truncation, so two distinct
(ASR, CB, scope, capability) tuples can never resolve to the same binding name. Like all
pm object names, the result is wrapped against the 253-character limit.

### 5. Resource CCRNs unchanged

`cluster-resource` and `namespace-resource` CCRNs continue to produce a per-ASR ClusterRole
containing the compiled inline rules, plus a ClusterRoleBinding to it. The per-ASR
ClusterRole now only contains resource CCRN rules, since `cluster.k8s` and `namespace.k8s`
rules moved to shared ClusterRole bindings.

A mixed ASR (e.g. `cluster.k8s` + `READ` and a `cluster-resource` + `WRITE`) produces on
the target cluster:
- 1 ClusterRoleBinding to the shared `pm-s-<cb-name>-cluster-read`
- 1 per-ASR ClusterRole `pm-a-<asr-name>` + 1 ClusterRoleBinding to it

These are independent grants. Two ClusterRoleBindings for the same ASR on the same cluster
is intentional.

### 6. ConnectorBinding spec structure

Each capability entry in `capabilityMapping` takes one of two mutually exclusive shapes:

**Direct binding** (valid only for `cluster:MANAGE`):
```yaml
MANAGE:
  clusterRoleName: cluster-admin
```

The controller creates a ClusterRoleBinding pointing to the named ClusterRole. No shared
ClusterRole is created. `clusterRoleName` is only valid in the `cluster` scope section.

**Shared aggregated ClusterRole:**
```yaml
READ:
  clusterRoleSelectors:           # at least one selector required
  - matchLabels:
      rbac.authorization.k8s.io/aggregate-to-view: "true"
  rules:                          # optional baseline rules for the child ClusterRole
  - apiGroups: [""]
    resources: ["nodes", "namespaces", "persistentvolumes"]
    verbs: ["get", "list", "watch"]
```

The controller creates two ClusterRoles: an aggregated root with `aggregationRule` only,
and a baseline child ClusterRole carrying the `rules` content labelled with the auto-derived
CB-private `pm.cloudoperators.dev/aggregate-to-<cb-hash>-<scope>-<capability>` label (selected
only by this CB's own root). The `rules` field describes what goes into the child; the root's
`rules` field is owned by Kubernetes and must not be set directly.

**Full example:**

```yaml
apiVersion: greenhouse.sap/v1alpha1
kind: ConnectorBinding
metadata:
  name: my-k8s-cb
spec:
  name: my-k8s-cb
  provider: kubernetes
  definition:
    capabilityMapping:
      cluster:
        MANAGE:
          clusterRoleName: cluster-admin
        READ:
          clusterRoleSelectors:
          - matchLabels:
              rbac.authorization.k8s.io/aggregate-to-view: "true"
          rules:
          # cluster-scoped resources only - namespaced resources covered by view aggregation
          # secrets intentionally omitted
          - apiGroups: [""]
            resources: ["nodes", "namespaces", "persistentvolumes"]
            verbs: ["get", "list", "watch"]
          - apiGroups: ["storage.k8s.io"]
            resources: ["storageclasses", "volumeattachments"]
            verbs: ["get", "list", "watch"]
      namespace:
        MANAGE:
          # admin grants Secret access - intentional for namespace administrators
          clusterRoleSelectors:
          - matchLabels:
              rbac.authorization.k8s.io/aggregate-to-admin: "true"
          - matchLabels:
              pm.cloudoperators.dev/aggregate-to-namespace-manage: "true"
        OPERATE:
          # edit is NOT included - edit grants Secret access which OPERATE must exclude
          # baseline child enumerates workload resources explicitly
          clusterRoleSelectors:
          - matchLabels:
              pm.cloudoperators.dev/aggregate-to-namespace-operate: "true"
          rules:
          - apiGroups: ["apps"]
            resources: ["deployments", "daemonsets", "statefulsets", "replicasets"]
            verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
          - apiGroups: ["batch"]
            resources: ["jobs", "cronjobs"]
            verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
          - apiGroups: [""]
            resources: ["pods", "services", "configmaps", "serviceaccounts"]
            verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
          # secrets intentionally omitted
        READ:
          clusterRoleSelectors:
          - matchLabels:
              rbac.authorization.k8s.io/aggregate-to-view: "true"
          - matchLabels:
              pm.cloudoperators.dev/aggregate-to-namespace-read: "true"
```

### 7. Label separation between shared and per-ASR objects

| Object | Label | Managed by |
|---|---|---|
| Per-ASR ClusterRole, ClusterRoleBinding, RoleBinding | `pm.cloudoperators.dev/asr: <asr-name>` + `pm.cloudoperators.dev/source-namespace: <asr-namespace>` | ASR reconciler - per-ASR orphan cleanup |
| Shared aggregated root ClusterRoles | `pm.cloudoperators.dev/cb: <cb-name>` | ASR reconciler - shared orphan cleanup |
| Shared baseline child ClusterRoles | `pm.cloudoperators.dev/cb: <cb-name>` + CB-private `pm.cloudoperators.dev/aggregate-to-<cb-hash>-<scope>-<capability>: "true"` | ASR reconciler - shared orphan cleanup |

Per-ASR orphan cleanup (`EnsureOrphansRemoved`) only touches objects carrying the `asr`
label. It never deletes shared ClusterRoles. A separate subroutine handles shared ClusterRole
orphan cleanup.

The per-ASR `source-namespace` label makes cleanup namespace-precise: two ASRs that share a
name in different namespaces own disjoint objects, so revoking one never removes the other's
RBAC. Label values derived from a name are themselves wrapped against the 63-character label
limit with the same transform in both the builders and the delete selector, so the value stays
label-valid and the selector still matches.

**Ownership guards.** Before patching an object that already exists, the controller refuses to
overwrite one owned by a different principal: a per-ASR object whose existing `asr` /
`source-namespace` labels name a different (ASR, namespace), or a shared object whose existing
`cb` label names a different CB. A refused patch fails that cluster (surfaced via provisioning
status) rather than silently hijacking another owner's object. Combined with the disjoint
`pm-s-`/`pm-a-` prefixes, this makes name collisions across owners both unreachable and, if one
somehow occurred, non-destructive.

### 8. ASR reconciler subroutine sequence

Two new subroutines are added around the existing pipeline:

| Step | Subroutine | Change |
|---|---|---|
| 1 | `EnsureConnectorBindings` | Rejects a CB whose `spec.name` is not a DNS-1123 label (the name seeds object names and labels); when every referenced CB is rejected this way the ASR goes `InvalidConfig`/`InvalidConnectorBinding` |
| 2 | `EnsureSharedClusterRoles` | New - ensures shared ClusterRoles exist per CB per target cluster. A create/patch failure is recorded per cluster and folded into that cluster's provisioning outcome by `EnsureRbacApplied`, so a binding to a shared role that failed to create fails the cluster rather than being silently swallowed |
| 3 | `EnsureRbacCompiled` | Changed - `cluster.k8s`/`namespace.k8s` CCRNs compile to binding references, not inline rules |
| 4 | `EnsureRbacApplied` | Changed - applies bindings only for `cluster.k8s`/`namespace.k8s`; consumes recorded shared-role failures into per-cluster provisioning status |
| 5 | `EnsureOrphansRemoved` | Unchanged - per-ASR label only |
| 6 | `EnsureSharedClusterRoleOrphansRemoved` | New - per-CB label cleanup |

`EnsureSharedClusterRoles` runs before compilation so that bindings can reference
ClusterRoles that are guaranteed to exist. It is a no-op for `cluster:MANAGE` (no shared
ClusterRole to create).

`EnsureSharedClusterRoleOrphansRemoved` lists ClusterRoles on each target cluster labelled
`pm.cloudoperators.dev/cb: <cb-name>` and deletes any whose shared-role name is no longer in
the keep-set derived from the CB's current `capabilityMapping` (both the `pm-s-<cb-name>-<scope>-<capability>`
root and its `-baseline` child). This handles the case where a CB author removes a capability
(e.g. drops `cluster:READ`): the orphaned shared ClusterRoles are cleaned up on the next ASR
reconcile for any ASR referencing that CB. It sweeps the union of the clusters compiled this
reconcile and the clusters previously targeted, and runs even when the ASR is `InvalidConfig`:
dropping a capability sets `InvalidConfig`/`UnknownCapability`, yet the CB still parses, so the
per-CB keep-set is trustworthy and the stale roles must still be reclaimed. The one unsafe reason,
`MissingConnectorBinding`, never reaches this subroutine because `EnsureConnectorBindings` breaks
the chain first.

### 9. Migration: hard cut

The old flat `capabilityMapping` structure (a map of string to `[]CapabilityRule`) is
invalid after this change. CBs using the old structure fail to parse against the new schema
and immediately put all referencing ASRs into `InvalidConfig` with reason
`MissingConnectorBinding`. The new controller and updated CB definitions must be deployed
together. No backwards-compatibility shim is provided.

### 10. Drop excludeResources

The following code is removed entirely:

- `ExcludeResources` field on `CapabilityRule`
- `ResourceSelector` type
- `internal/provider/k8s/expand.go` and its test file
- `ensure_compiled_exclude_test.go`
- `rulesNeedExpansion` and `expandRulesForCluster` functions in `ensure_compiled.go`

The `discoveryFor` helper and the `RemoteDiscovery` factory field on `Phase` are
retained: the kind-vs-scope warning (ADR-017) resolves a resource's scope on the
target cluster via the same discovery client, so they outlive `excludeResources`.

`CapabilityRule` in the new model only carries `apiGroups`, `resources`, and `verbs`.

### 11. Implementation rollout in two phases

This ADR records one design, but the design decomposes into two parts of very different
weight and risk. The implementation lands them in sequence rather than in one change.

**Phase 1 - closed vocabulary and scope nesting.** The scope-nested `capabilityMapping`
structure and the closed per-scope capability vocabulary (decisions 1 and 6). This is a
schema and validation change with a small blast radius: it changes how CBs are parsed and
which capability names validate, and it is straightforward to reason about and audit.

**Phase 2 - shared aggregated ClusterRole infrastructure.** The shared root/baseline-child
ClusterRoles, the `namespace.k8s` switch to ClusterRole+RoleBinding, the two new reconciler
subroutines, and per-CB orphan cleanup (decisions 2, 3, 4, 7, 8). This is the heavy part:
new controller machinery with its own failure modes, delivered and reviewed on its own.

Phase 2 depends on Phase 1 (the shared roles are keyed by scope and capability, which Phase
1 defines), so the phases are sequenced, not independent. Keeping them as one ADR reflects
that dependency; splitting the implementation keeps the higher-risk machinery under focused
review. `excludeResources` removal (decision 10) can land with Phase 1, since it is a pure
deletion independent of the aggregation work.

### 12. Object identity: disjoint prefixes, hashes, ownership guards

Object names and label values are derived from operator-controlled strings (`asr.Name`,
`cb.Spec.Name`, scope, capability). Left naive, that derivation can collide two distinct
owners onto one name or leak one owner's rules into another's role. Four rules close those
gaps.

**Disjoint prefixes.** Per-ASR objects are prefixed `pm-a-`; shared aggregated objects are
prefixed `pm-s-`. The prefixes are disjoint, so a per-ASR object and a shared object can
never collide on name regardless of how an ASR or CB is named.

**Hash suffix on coarse bindings.** A coarse binding name
`pm-a-<asr-name>-<cb-name>-<scope>-<capability>-<hash>` ends in
`ShortHash(asr-namespace, asr-name, cb-name, scope, capability)` (8 hex chars). Without it,
two distinct tuples whose readable fields coincide after truncation could resolve to the same
binding; the hash is taken over the field-delimited tuple, so it distinguishes them even when
the readable prefix is identical. All pm names are additionally wrapped against the
253-character Kubernetes limit (truncate-and-hash when over length).

**CB-private baseline label.** A baseline child carries only the CB-private
`aggregate-to-<cb-hash>-<scope>-<capability>` label, not the shared
`aggregate-to-<scope>-<capability>` label. Two CBs defining the same scope/capability therefore
never aggregate each other's baselines; the shared label remains reserved for intentional
team extensions selected by every CB's root.

**Ownership guards + namespace-scoped labels.** Per-ASR objects carry both `asr` and
`source-namespace` labels; cleanup and the ownership guard both match on the pair, so same-named
ASRs in different namespaces own disjoint objects. Before overwriting an existing object the
controller refuses to patch across owners (different (ASR, namespace) for per-ASR objects,
different CB for shared objects), failing the cluster instead of hijacking the object.
```yaml
permissions:
- resource: urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1
  capability: MANAGE
```

**Developer (namespace admin + cluster viewer):**
```yaml
permissions:
- resource: urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/my-app
  capability: MANAGE
- resource: urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1
  capability: READ
```

**Operator developer (namespace admin + targeted cluster-scoped resources + cluster viewer):**
```yaml
permissions:
- resource: urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/my-operator-system
  capability: MANAGE
- resource: urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/apiextensions.k8s.io/customresourcedefinitions
  capability: WRITE
- resource: urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/admissionregistration.k8s.io/validatingwebhookconfigurations
  capability: WRITE
- resource: urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1
  capability: READ
```

**Read and write secrets in a namespace (replaces ACCESS_CREDENTIALS):**
```yaml
permissions:
- resource: urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/my-app/v1/secrets
  capability: MANAGE
```

**Read and write access to a custom resource group in a namespace:**
```yaml
permissions:
- resource: urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/my-app/myoperator.cloud.sap/*
  capability: MANAGE
```

### ACCESS_CREDENTIALS replacement

The previous open-vocabulary `capabilityMapping` supported an `ACCESS_CREDENTIALS`
capability that granted read access to Secrets and other credential-carrying resources.
This capability is not part of the closed vocabulary in this ADR.

Granting read or write access to Secrets (or any other credential-carrying resource) is
expressed directly via resource CCRNs using the `READ`, `WRITE`, or `MANAGE` capability:

```yaml
# Read secrets in a specific namespace
- resource: urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/my-app/v1/secrets
  capability: READ

# Read and write secrets cluster-wide
- resource: urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/v1/secrets
  capability: MANAGE

# Read service account tokens
- resource: urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/my-app/v1/serviceaccounts:token
  capability: READ
```

This is strictly more expressive than `ACCESS_CREDENTIALS`: the scope (cluster vs
namespace), the specific namespace, and the exact resource type are all explicit in the
CCRN address rather than hidden inside a CB definition. Audit reviewers can determine
exactly what credential access was granted without inspecting the CB.

## Consequences

**Benefits:**

- Closes the scope awareness gap: `cluster.k8s` and `namespace.k8s` capabilities are now
  distinct and independently configurable. An operator cannot accidentally express a
  cluster-scoped concept via a namespace CCRN or vice versa.
- Closed vocabulary is auditable and self-documenting. Reviewers and auditors can reason
  about what MANAGE/OPERATE/READ mean without reading CB definitions.
- Aggregation extensibility is now uniform across both scopes. CRD-owning teams can extend
  both cluster-read and namespace-manage/operate/read by creating ClusterRoles with the
  appropriate `pm.cloudoperators.dev/aggregate-to-*` label. This is a planned migration
  path: as CRD-owning teams begin shipping their own aggregation ClusterRoles, the CB
  baseline can be reduced.
- Namespace built-in roles (`admin`, `edit`, `view`) are reused via aggregation where safe.
  The built-ins exclude Secrets by design, so `namespace:READ` and `namespace:OPERATE`
  inherit secrets exclusion without the CB author needing to enumerate exclusions.
- Secrets exclusion is explicit for `cluster:READ`. The CB author enumerates what is
  readable; omitting secrets is a deliberate act with clear audit trail.
- Removes the `excludeResources` discovery-dependent path, reducing controller complexity
  significantly (no per-cluster discovery calls, no fail-closed expansion logic).
- Per-ASR output for `cluster.k8s` and `namespace.k8s` shrinks to bindings only. Fewer
  objects on target clusters; the ClusterRole lifecycle is decoupled from individual ASRs.

**Trade-offs:**

- Hard-cut migration: existing CBs using the old flat `capabilityMapping` must be updated
  on deploy. There is a brief window where referencing ASRs enter `InvalidConfig` if the
  CB is not updated atomically with the controller. This is acceptable given the system is
  pre-production.
- Two capability mapping semantics remain in the same system: CB-driven for
  `cluster.k8s`/`namespace.k8s`, hardcoded for resource CCRNs. This is the same interim
  state noted in ADR-017; a future ADR may unify these further.
- Shared ClusterRoles accumulate on clusters when CB capability mappings shrink.
  `EnsureSharedClusterRoleOrphansRemoved` cleans them up lazily on ASR reconcile, not
  immediately when the CB changes. Orphaned ClusterRoles are harmless (no bindings point to
  them) but add cluster-level RBAC noise until cleanup runs.
- Mixed ASRs (resource CCRNs + coarse CCRNs) produce two ClusterRoleBindings on the same
  cluster for the same ASR. This is intentional but increases binding count.
- The `cluster:READ` baseline is fully operator-managed. If the CB author omits a resource
  type, cluster-viewer subjects cannot see it. This is the correct behavior (explicit beats
  implicit) but places authoring responsibility on the CB author.

**Related:**

- Kubernetes RBAC Target Architecture - overview of the end state this ADR reaches, and how
  ADR-017 and ADR-018 sequence toward it.
- ADR-015: Scope-Separated RBAC - establishes the per-namespace Role invariant that this
  ADR extends to ClusterRole+RoleBinding.
- ADR-017: Resource-Scoped CCRN Types - this ADR keeps the resource CCRN grammar unchanged
  and extends its capability set with `MANAGE`, consistent with the closed vocabulary here.
- ADR-009: Capability Mapping via ConnectorBinding - this ADR supersedes the open-vocabulary
  capabilityMapping design in ADR-009.
- `internal/provider/k8s/definition.go` - ConnectorDefinition struct to be replaced with
  scope-nested structure.
- `internal/provider/k8s/ensure_compiled.go` - compiler to be updated to emit bindings for
  coarse CCRNs and delegate inline rules to resource CCRNs only.
- `internal/provider/k8s/ensure_bindings.go` - new subroutines `EnsureSharedClusterRoles`
  and `EnsureSharedClusterRoleOrphansRemoved` to be added.
- `internal/provider/k8s/expand.go` - to be deleted.
