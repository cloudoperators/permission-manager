# ADR-017: Resource-Scoped CCRN Types for Fine-Grained Kubernetes RBAC

## Status

Draft

## Context

The current Kubernetes permission model offers two CCRN types:

| CCRN type | RBAC produced | Granularity |
|---|---|---|
| `cluster.k8s` | ClusterRole + ClusterRoleBinding | entire cluster |
| `namespace.k8s` | Role + RoleBinding | all resources in one namespace |

The capability (`READ`, `WRITE`, `*`) maps to a verb set via the ConnectorBinding's
`capabilityMapping`, which also hardcodes the allowed `apiGroups` and `resources`. This means:

- `cluster.k8s` + `READ` gives `get/list/watch` on a broad, operator-curated allowlist of
  resource types across the whole cluster.
- `cluster.k8s` + `*` is effectively cluster-admin.
- `namespace.k8s` cannot express access to cluster-scoped resources at all (nodes,
  PersistentVolumes, CustomResourceDefinitions, webhook configurations, etc.).

This is sufficient for coarse access tiers (VIEWER, USER, ADMIN), but is too blunt for
operator developers who need targeted access to specific cluster-scoped resource types.
For example:

- A platform team needs `create/update` on `customresourcedefinitions` and
  `validatingwebhookconfigurations` cluster-wide, but not cluster-admin.
- A team that owns a CRD group (`myoperator.cloud.sap`) needs write access to all resources
  in that group inside their namespace, without granting write on unrelated resources.

Neither case can be expressed today without resorting to `cluster.k8s` + `*`, which
massively overprovisions.

The root problem is that `cluster.k8s` and `namespace.k8s` CCRNs address a scope (cluster
or namespace) but carry no resource-type information. The resource target is baked into the
ConnectorBinding, which is global across all CCRNs that reference it. There is no way for
an ASR author to say "grant WRITE specifically on this resource type."

## Decision

Introduce two new CCRN types that embed the resource target directly in the CCRN address:

```
urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/<cluster>/<apigroup>/<resource>[:<subresource>]
urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/<cluster>/<namespace>/<apigroup>/<resource>[:<subresource>]
```

### Segment grammar

| Position | Segment | Wildcards allowed | Notes |
|---|---|---|---|
| 2 | `<cluster>` | glob (`*`, `s-eu-de-1-*`) | same rules as existing CCRNs |
| 3 (namespace-resource only) | `<namespace>` | none | wildcards rejected as `InvalidCCRN` |
| 3 / 4 | `<apigroup>` | none | use `v1` for the core API group (maps to `apiGroups: [""]` in RBAC) |
| 4 / 5 | `<resource>` | glob (`*`) | `*` means all resources in the group |
| optional suffix | `:<subresource>` | none | `:` delimiter; e.g. `pods:log`, `pods:exec` |

The `v1` token for the core group is used because `v1` is the actual preferred GroupVersion
returned by Kubernetes discovery for that group, and is what `kubectl api-resources` shows.
The parser translates `v1` to the empty string `""` when building RBAC `apiGroups`.

The `:` subresource delimiter is a CCRN-level encoding choice. The parser translates
`resource:subresource` to `resource/subresource` when populating `PolicyRule.resources`,
which is the slash form Kubernetes RBAC requires.

Subresource wildcards (e.g. `*:log`) are rejected as `InvalidCCRN`.

### Capability semantics

For `cluster-resource` and `namespace-resource` CCRNs, the ConnectorBinding's
`capabilityMapping` is not consulted. Only two capabilities are valid, with hardcoded
verb sets:

| Capability | Verbs |
|---|---|
| `READ` | `get`, `list`, `watch` |
| `WRITE` | `create`, `update`, `patch`, `delete`, `deletecollection` |

Any other capability value (including `*`) is rejected as `InvalidCCRN`. `deletecollection`
is included in WRITE for parity with the built-in `edit`/`admin` ClusterRoles; it is a
distinct RBAC verb that authorizes deleting a whole collection in one request.

> Note: ADR-018 extends the resource CCRN vocabulary with a third capability, `MANAGE`
> (the union of the READ and WRITE verb sets), as part of the closed per-scope vocabulary.
> The READ/WRITE-only validation rule stated here is superseded by ADR-018 once that ADR
> lands; the hardcoded, CB-independent verb model described in this section is unchanged.

This is a deliberate break from the existing CCRN types where the CB owns the
verb-to-resource mapping. For resource CCRNs the resource target is already in the CCRN
address, so the CB has nothing to contribute beyond verbs, and hardcoding READ/WRITE is
simpler and less error-prone than adding a new CB convention. A future ADR will revisit
the capability mapping design more broadly.

### ConnectorBinding requirement

A kubernetes ConnectorBinding (`provider: kubernetes`) is still required in `connectorRefs`.
It acts as the activation gate that tells the k8s provider subroutine that it owns this ASR.
The CB's `capabilityMapping` content is not used for `resource.k8s` permissions.

The current `EnsureConnectorBindings` gate rejects a CB with an empty or unparseable
`capabilityMapping` as unusable. For an ASR whose permissions are exclusively
`cluster-resource` or `namespace-resource` CCRNs, this check must be relaxed: the CB is
still required to exist and have `provider: kubernetes`, but an empty `capabilityMapping`
is acceptable because the capability mapping is not consulted for these CCRN types. Mixed
ASRs (some `cluster.k8s`/`namespace.k8s` + some resource CCRNs) must still satisfy the
existing non-empty mapping requirement for the CB to serve the coarse-grained permissions.

### Compiler behavior

`EnsureRbacCompiled` treats resource CCRNs as follows:

- `cluster-resource` rules are merged into the same ClusterRole that `cluster.k8s` rules
  produce for the same cluster. The CCRN's `<apigroup>` and `<resource>` fields populate
  the `apiGroups` and `resources` fields of the RBAC PolicyRule; the capability determines
  the verbs.
- `namespace-resource` rules are merged into the per-namespace Role that `namespace.k8s`
  rules produce for the same (cluster, namespace) pair.
- The existing `policyRuleSet` verb-union logic handles merging: rules with the same
  `(apiGroups, resources)` key have their verbs unioned, so a `READ` and a `WRITE` on the
  same resource type collapse to a single PolicyRule.
- One ClusterRole and one Role per namespace per ASR per cluster, same as today (ADR-015).

> Note: ADR-018 moves `cluster.k8s` and `namespace.k8s` from per-ASR inline rules to
> bindings against shared ClusterRoles. Once that lands there is no per-ASR `cluster.k8s`
> ClusterRole to merge into, so resource CCRN rules compile into their own per-ASR
> ClusterRole instead. The `policyRuleSet` verb-union and per-CCRN parsing described here
> are unchanged; only the ClusterRole the rules land in differs.

The CCRN type (`cluster-resource` vs `namespace-resource`) determines binding scope, not
the native Kubernetes scope of the resource kind. Using `cluster-resource` for a namespaced
kind (e.g. `deployments`) produces a ClusterRoleBinding and grants access across the whole
cluster. Using `namespace-resource` for a cluster-scoped kind (e.g. `nodes`) produces a
RoleBinding, which Kubernetes will silently ignore because cluster-scoped resources are not
accessible via namespace-bound roles. Authors are responsible for matching the CCRN type to
the Kubernetes scope of the resource; the controller surfaces a non-fatal warning for this
case (see Non-fatal warnings below) but does not reject it.

### Validation errors (reason `InvalidCCRN`)

The following are hard validation failures, consistent with the existing fail-closed policy:

- Namespace wildcard on `namespace-resource` (e.g. `*/v1/secrets`)
- Wildcard in the `<apigroup>` segment
- Subresource on a wildcard resource (e.g. `*:log`)
- Empty `<apigroup>` or `<resource>` segment
- Capability other than `READ` or `WRITE`

A literal `/` inside the `<resource>` segment is rejected (it overflows the URN segment
count); use `:` for subresources.

### Non-fatal warnings

The controller surfaces configuration warnings via an informational `ConfigWarning` status
condition and a Kubernetes Warning Event, without failing the ASR:

- **Kind-vs-scope:** a `namespace-resource` naming a cluster-scoped resource can never be
  granted by a RoleBinding. Scope is a target-cluster fact, resolved per matched cluster via
  that cluster's discovery client. Resources whose scope cannot be resolved (group not
  discovered, discovery unavailable, or resource unknown) fail open — no warning. The
  controller still does not hard-reject scope mismatches.

### Examples

Operator developer needing targeted cluster-wide write access:

```yaml
permissions:
- resource: urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/apiextensions.k8s.io/customresourcedefinitions
  capability: WRITE
- resource: urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/admissionregistration.k8s.io/validatingwebhookconfigurations
  capability: WRITE
- resource: urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/admissionregistration.k8s.io/mutatingwebhookconfigurations
  capability: WRITE
```

Team owning a CRD group, needing write access in their namespace across a fleet:

```yaml
permissions:
- resource: urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1-*/myoperator-system/myoperator.cloud.sap/*
  capability: WRITE
```

Reading pod logs in a specific namespace:

```yaml
permissions:
- resource: urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/myapp/v1/pods:log
  capability: READ
```

Mixed ASR combining existing and new CCRN types:

```yaml
permissions:
- resource: urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/myapp
  capability: READ
- resource: urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/s-eu-de-1/apiextensions.k8s.io/customresourcedefinitions
  capability: WRITE
```

## Consequences

**Benefits:**

- Fills the gap between "access to one namespace" and "cluster-admin": operators can now
  grant targeted cluster-scoped resource access without overprovisioning.
- The ASR author states exactly what is granted; no implicit resource lists hidden in the CB.
- Backwards compatible: existing `cluster.k8s` and `namespace.k8s` CCRNs are unchanged.
  Mixed ASRs work naturally.
- Hardcoded READ/WRITE verbs eliminate an entire class of CB misconfiguration for these
  CCRNs.

**Trade-offs:**

- Two capability mapping semantics now exist in the same system: CB-driven for
  `cluster.k8s`/`namespace.k8s`, hardcoded for `cluster-resource`/`namespace-resource`.
  This is an intentional interim state pending a broader capability mapping rework.
- ASR authors writing `resource.k8s` permissions must know Kubernetes API group names and
  resource names. This is unavoidable given the goal: precise resource targeting requires
  precise resource identification.
- The `v1` token for the core group introduces a visual ambiguity where the CCRN version
  segment and the API group segment both show `v1` (e.g. `.../v1/s-eu-de-1/v1/secrets`).
  The positions are unambiguous to the parser; the visual oddness is accepted as the lesser
  evil compared to an informal alias like `core` that has no Kubernetes API backing.

**Related:**

- Kubernetes RBAC Target Architecture - overview of the end state and how ADR-017 and
  ADR-018 sequence toward it.
- ADR-015: Scope-Separated RBAC - establishes the one-Role-per-namespace-per-ASR invariant
  that resource CCRNs inherit.
- ADR-009: Capability Mapping via ConnectorBinding - the design this ADR partially bypasses
  for resource CCRNs; ADR-018 addresses the capability mapping model more broadly.
- ADR-018: Scope-Aware Capability Mapping - reworks the capability mapping into a closed
  per-scope vocabulary, adds `MANAGE` to the resource CCRN capabilities, and moves
  `cluster.k8s`/`namespace.k8s` to shared ClusterRole bindings (see the notes above).
- `internal/ccrn/parser.go` - parser to be extended with `cluster-resource` and
  `namespace-resource` type registrations.
- `internal/provider/k8s/ensure_compiled.go` - compiler to be extended to handle the new
  CCRN types and hardcoded verb sets.
- `internal/provider/k8s/ensure_validity.go` - validator to be extended with the new
  `InvalidCCRN` rules above.
