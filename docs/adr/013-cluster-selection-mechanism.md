# ADR-013: Cluster Selection Mechanism — CCRN Patterns vs Label Selectors

## Status

Pending review

## Context

The pm-controller targets remote clusters for RBAC rollout using **CCRN URN patterns** embedded in `spec.permissions[].resource`. The cluster name (or trailing-star wildcard) is a segment of the CCRN string itself — there is no separate cluster selection field in the `ApplicationSpecificRole` spec.

Example:
```
urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/nw-a-*    → all clusters starting with "nw-a-"
urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/*/monitoring  → namespace "monitoring" on ALL clusters
```

Greenhouse's `TeamRoleBinding` takes a different approach: it has an explicit `ClusterSelector` field with two mutually exclusive modes:
1. **By name** — targets a single named cluster.
2. **By label selector** — uses `metav1.LabelSelector` (`matchLabels` / `matchExpressions`) against `Cluster` object labels.

The label selector approach is reactive — when a Cluster's labels change or a new Cluster is created with matching labels, all TeamRoleBindings are re-queued and the RBAC set dynamically expands or contracts.

The question is whether the pm-controller should adopt label-selector-based cluster selection alongside or instead of the current CCRN pattern approach.

### Key differences

| Dimension | pm-controller (CCRN patterns) | Greenhouse (Label selectors) |
|-----------|-------------------------------|------------------------------|
| Selection mechanism | Trailing-star prefix glob on cluster name | `matchLabels` / `matchExpressions` on Cluster CR labels |
| Granularity | Per-permission (each CCRN can target different clusters) | Per-binding (one selector per TeamRoleBinding) |
| Reactive expansion | Yes — new clusters trigger re-reconcile via `ClusterInventory` | Yes — label/readiness changes trigger re-reconcile via watch predicates |
| Multi-dimensional selection | No — only cluster name pattern | Yes — arbitrary label combinations (environment, region, team, tier, etc.) |
| Coupling | Coupled to naming conventions | Coupled to labeling conventions |

### Alternatives considered

| # | Approach | Description |
|---|----------|-------------|
| 1 | **Keep CCRN patterns only** | No change — cluster targeting remains a CCRN string pattern |
| 2 | **Add label selector as a separate ASR spec field** | Add an optional `ClusterSelector` field to ASR spec alongside CCRN patterns |
| 3a | **New CCRN types: `labeled-cluster` / `labeled-namespace` (single label)** | New CCRN types with one label key/value pair as path segments |
| 3b | **New CCRN types: `labeled-cluster` / `labeled-namespace` (fixed slots)** | Same as 3a but with a fixed number of label slots (e.g., 2), unused slots wildcarded |
| 3c | **New CCRN types (arbitrary labels)** | Variable-length path encoding for arbitrary label count |
| 4 | **Extend existing `cluster` CCRN with label query syntax** | Add a query-string-like suffix to the existing type |
| 5 | **Replace CCRN cluster segment with label selector entirely** | Remove name-pattern matching, require label selectors for all cluster targeting |

## Decision

**Option 1: Keep CCRN patterns only.** Do not add label-selector-based cluster selection at this time.

### Reasoning

#### Arguments FOR label-based selection (Options 2–3)

- **Multi-dimensional targeting** — label selectors allow targeting by environment (`env=prod`), region (`region=eu`), team (`team=platform`), or any combination. CCRN patterns can only match the cluster *name*.
- **Decoupled from naming** — teams don't need to encode semantics into cluster names. Clusters named `abc-12345` can still be targeted via labels.
- **Greenhouse alignment** — adopting a similar mechanism reduces cognitive load for teams already using Greenhouse TeamRoleBindings.
- **Dynamic grouping** — adding a label to a cluster automatically includes it in all matching selectors, without modifying any ASR.

#### Option 3a analysis: `labeled-cluster` / `labeled-namespace` CCRN types

Two new CCRN types using standard path segments for a single label key/value pair, mirroring the existing `cluster` / `namespace` split:

```
# Cluster-scoped access on all clusters labeled env=prod:
urn:ccrn:labeled-cluster.k8s.ccrn.cloudoperators.dev/v1/env/prod

# Namespace-scoped access in "monitoring" namespace on all clusters labeled env=prod:
urn:ccrn:labeled-namespace.k8s.ccrn.cloudoperators.dev/v1/env/prod/monitoring

# Wildcards compose naturally:
urn:ccrn:labeled-cluster.k8s.ccrn.cloudoperators.dev/v1/env/*       → all clusters with label "env" (any value)
urn:ccrn:labeled-namespace.k8s.ccrn.cloudoperators.dev/v1/env/prod/* → all namespaces on env=prod clusters
```

Path structure:
```
labeled-cluster:    /v1/<label-key>/<label-value>
labeled-namespace:  /v1/<label-key>/<label-value>/<namespace>
```

**Pros:**
- Preserves the CCRN-as-single-addressing-model principle — label-based cluster selection is still expressed as a CCRN resource, not as a side-channel field.
- Clean parsing — label key, value, and namespace are standard CCRN path segments, parsed the same way as cluster name and namespace in existing types. No micro-grammar.
- Maintains per-permission granularity — one permission can use name-patterned clusters (`cluster` type) while another uses label-selected clusters (`labeled-cluster` type) within the same ASR.
- Wildcards compose naturally on value and namespace segments.
- The `cluster`/`namespace` and `labeled-cluster`/`labeled-namespace` pairs are symmetric — same scoping semantics, different selection mechanism.
- Single label covers the dominant use case (environment-based targeting). Multiple labels can be approximated by intersecting multiple permissions.

**Cons:**
- The `ClusterInventory` would need to cache cluster labels (not just names) or issue List calls with label selectors on reconcile.
- Adds a `LabelChangedPredicate` watch on Cluster objects to trigger re-reconciliation, increasing controller event volume.
- Single label is limited — cannot express "env=prod AND region=eu" in one CCRN. Multiple permissions approximate this via intersection but are not equivalent (each permission independently expands to its cluster set; AND semantics require all matched clusters to overlap).

#### Option 3b analysis: Fixed label slots

```
# Two label slots, second unused:
urn:ccrn:labeled-cluster.k8s.ccrn.cloudoperators.dev/v1/env/prod/*/*
urn:ccrn:labeled-namespace.k8s.ccrn.cloudoperators.dev/v1/env/prod/*/*/monitoring

# Both slots used:
urn:ccrn:labeled-cluster.k8s.ccrn.cloudoperators.dev/v1/env/prod/region/eu
urn:ccrn:labeled-namespace.k8s.ccrn.cloudoperators.dev/v1/env/prod/region/eu/monitoring
```

**Additional pros over 3a:**
- Supports AND-semantics for up to N labels in a single CCRN — no approximation needed.

**Additional cons over 3a:**
- Ordering ambiguity — `env/prod/region/eu` vs `region/eu/env/prod` are different strings but semantically identical. Requires canonicalization (e.g., alphabetical key order) or accepting that both forms are valid but distinct.
- Longer, harder-to-read URNs — especially with wildcarded unused slots.
- Fixed slot count is an arbitrary limit that will eventually be insufficient or wasteful.

#### Arguments for Option 1 (chosen)

1. **Naming conventions are already the contract** — Greenhouse cluster names in the target environment follow naming conventions that encode environment and region (e.g., `nw-a-prod-eu`, `nw-b-staging-us`). The CCRN wildcard is sufficient for the current and planned use cases.

2. **Complexity budget** — the pm-controller already handles wildcard expansion, dynamic cluster onboarding, namespace wildcards, and multi-provider reconciliation. Adding a second cluster resolution path (label-based) increases the matching surface area and the test matrix without delivering value for current use cases.

3. **Scope separation** — the pm-controller manages *application-specific* roles scoped to a well-known set of clusters by naming convention. The broader "grant team X access to all prod clusters" use case is already served by Greenhouse TeamRoleBindings — there's no need to duplicate it.

4. **CCRN wildcards cover the realistic scenarios** — with trailing-star globs, the patterns `nw-a-*` (all clusters in landscape A), `*` (all clusters), and exact names cover the observed usage. No request for "all clusters labeled env=prod regardless of name" has emerged.

5. **No YAGNI** — Option 3a is a clean design and a viable future path, but adding it now without a concrete use case creates unused code paths that must be tested and maintained.

#### Why Options 2, 3c, 4, and 5 are rejected

- **Option 2** (separate spec field) — creates two parallel addressing systems. The CCRN says "which resource" but a separate `ClusterSelector` also says "which clusters" — conflicting or redundant information in the same ASR.
- **Option 3c** (arbitrary labels) — variable-length CCRN paths break the fixed-segment parsing assumption. Full `metav1.LabelSelector` semantics (`NotIn`, `Exists`) cannot be encoded as path segments.
- **Option 4** (extend existing CCRN grammar) — pollutes the existing `cluster` type parser. Adding a query suffix creates ambiguity with wildcards and breaks the clean path-segment structure.
- **Option 5** (replace patterns with selectors) — breaks all existing ASR specs, removes the CCRN's self-contained nature, and eliminates per-permission cluster scoping.

### Recommended future path

If label-based selection becomes necessary, **Option 3a (single label, two new types)** is the recommended path forward:

```
urn:ccrn:labeled-cluster.k8s.ccrn.cloudoperators.dev/v1/<key>/<value>
urn:ccrn:labeled-namespace.k8s.ccrn.cloudoperators.dev/v1/<key>/<value>/<namespace>
```

Implementation would require:
- New CCRN type constants and parser paths (`ResourceTypeLabeledCluster`, `ResourceTypeLabeledNamespace`).
- `ClusterInventory` extended to cache `map[clusterName]map[labelKey]labelValue` and expose `MatchingLabel(key, value) []string`.
- `LabelChangedPredicate` watch on Cluster objects to trigger ASR re-reconciliation.
- `buildClusterScopes` extended with a branch for labeled types calling `MatchingLabel` instead of `MatchingNames`.

### Conditions for revisiting

This decision should be revisited if:
- Cluster naming conventions break down (e.g., clusters are provisioned with opaque names and semantics live only in labels).
- Multiple ASRs are observed that need identical permission sets across a label-defined group that doesn't map to a name prefix.
- The CCRN specification evolves to support structured metadata queries.

## Consequences

**Benefits:**
- Single, consistent addressing model (CCRN name patterns) — no divergent code paths for cluster resolution.
- Simpler controller logic — `ClusterInventory.MatchingNames` remains the only cluster resolution mechanism.
- Fewer watch triggers — no need for `LabelChangedPredicate` watches on Cluster objects (only name presence/readiness matters).
- Clear scope boundary with Greenhouse: TeamRoleBindings handle team-wide access with label selectors; ASRs handle application-specific access with CCRN patterns.

**Trade-offs:**
- Cluster targeting is coupled to naming conventions — if conventions change, ASR specs must be updated.
- Cannot express "all clusters with label X" without a naming pattern that correlates with that label.
- Teams familiar with Greenhouse's label selector model need to learn the CCRN pattern approach for ASRs.

**Mitigations:**
- Document the naming convention → CCRN pattern mapping clearly for operators.
- If a label-based need emerges, Option 3a (`labeled-cluster` / `labeled-namespace` with single key/value) is a clean extension path that preserves all existing behavior.
