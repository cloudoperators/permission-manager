# ADR-015: Scope-Separated RBAC — ClusterRoles and Namespace Roles

## Status

Pending review

## Context

The Kubernetes provider generates RBAC resources (ClusterRole, ClusterRoleBinding, Role, RoleBinding) on target clusters based on ASR permissions. The original `buildRBACPlan` implementation merged **all verbs** — from both cluster-scoped and namespace-scoped permissions — into a **single ClusterRole** with wildcard rules (`apiGroups: ["*"]`, `resources: ["*"]`).

This created two distinct privilege escalation vulnerabilities:

### Bug 1: Scope Leakage (Cluster vs Namespace)

Given an ASR with:
- `READ` on cluster CCRN (cluster-scoped) → verbs: `get, list, watch`
- `WRITE` on namespace CCRN (namespace-scoped) → verbs: `create, update, patch, delete`

The original code:
1. Merged all verbs into one ClusterRole: `[create, delete, get, list, patch, update, watch]` on `*/*`
2. Created a ClusterRoleBinding (because cluster-scoped verbs exist) referencing this merged ClusterRole
3. **Result**: The subject received `create, update, patch, delete` **cluster-wide**, even though WRITE was intended only for a specific namespace

### Bug 2: Verb Union Leakage (Between Namespaces)

Given an ASR with:
- `WRITE` on namespace `apps` → verbs: `create, delete, deletecollection, get, list, patch, update`
- `READ` on namespace `monitoring` → verbs: `get, list, watch`

An intermediate fix (scope-separated ClusterRoles) still:
1. Created a single `pm-<asr>-ns` ClusterRole with the **union** of all namespace verbs (7 verbs)
2. Created RoleBindings in both namespaces referencing this shared ClusterRole
3. **Result**: `monitoring` received write verbs (`create, delete, patch, update`) even though it was only granted READ

### Design Principles Violated

Kubernetes RBAC follows a **union-only, no-deny** model. The correct patterns are:
- Cluster-wide permissions → ClusterRoleBinding referencing a ClusterRole with **only** cluster-scoped verbs
- Namespace-scoped permissions → A **Role per namespace** with that namespace's exact verb set, bound via a RoleBinding

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Single merged ClusterRole (original)** | Combine all verbs into one ClusterRole; bind via both ClusterRoleBinding and RoleBindings | Bug 1 — namespace-scoped verbs leak cluster-wide via the ClusterRoleBinding |
| **Two scope-separated ClusterRoles (intermediate)** | Cluster verbs in `pm-<asr>`, namespace verbs in `pm-<asr>-ns`; RoleBindings reference the shared `-ns` ClusterRole | Bug 2 — verb union leakage between namespaces; `monitoring` gets WRITE verbs when only granted READ |
| **Single ClusterRole with per-rule namespace restrictions** | Use a single ClusterRole but add namespace-aware rules | Kubernetes RBAC rules do not support namespace restrictions within a ClusterRole — scope is determined entirely by the binding type |
| **Per-namespace Roles** ✅ | ClusterRole for cluster-scoped verbs; a separate `Role` in each namespace with that namespace's exact verb set | Chosen — see Decision |

## Decision

Generate **scope-appropriate RBAC resources** per ASR per cluster:

| Resource | Name | Namespace | Contains | Bound by |
|----------|------|-----------|----------|----------|
| ClusterRole | `pm-<asr>` | — | Cluster-scoped verbs only | ClusterRoleBinding `pm-<asr>` |
| Role | `pm-<asr>` | each target ns | That namespace's exact verb set | RoleBinding `pm-<asr>` in same ns |

### Rules

1. If the ASR has **only** cluster-scoped permissions → produce only `pm-<asr>` ClusterRole + ClusterRoleBinding. No namespace Roles.
2. If the ASR has **only** namespace-scoped permissions → produce only per-namespace Roles + RoleBindings. No ClusterRole or ClusterRoleBinding.
3. If the ASR has **both** → produce the ClusterRole + ClusterRoleBinding AND per-namespace Roles + RoleBindings.
4. Each namespace gets a Role with **exactly** the verbs granted to it — no union across namespaces.
5. On reconcile, **stale resources are cleaned up**: if the ASR no longer targets a namespace, the old Role and RoleBinding in that namespace are deleted. If it no longer has cluster-scoped permissions, the ClusterRole and ClusterRoleBinding are deleted.

### Namespace Wildcard Handling

> **Superseded.** This section described namespace-wildcard expansion as
> originally planned. The implementation does not do this. Namespace wildcards
> are now **rejected** as invalid CCRNs in `EnsureASRValidity`
> (`internal/provider/k8s/ensure_validity.go`): a CCRN with a wildcard namespace
> is added to `invalidCCRNs` and the ASR is stamped `InvalidConfig=True` /
> reason `InvalidCCRN` rather than expanded or promoted to cluster scope. The
> scope-separation decision above is unchanged; only wildcard handling differs
> from what this section proposed. The original text is kept below for the
> historical record.

- Bare wildcard `"*"` → promotes verbs to `clusterVerbs` (scope promotion via ClusterRoleBinding)
- Prefix wildcards (e.g., `"kube-*"`) → expanded against live namespace list; each matched namespace gets its own Role with the wildcard's verbs
- Overlapping patterns (e.g., `apps-*` READ + `apps-prod` WRITE) → verbs merge **per namespace** during expansion, so `apps-prod` gets READ+WRITE while `apps-staging` gets only READ

### Migration from Old Format

Existing clusters may have a legacy `pm-<asr>-ns` ClusterRole from the intermediate design. On first reconcile after deployment:
- Per-namespace `Role` objects are created with the correct verb sets.
- RoleBindings must have their `roleRef` updated from `ClusterRole/pm-<asr>-ns` to `Role/pm-<asr>`. Since Kubernetes forbids patching `roleRef`, these are **deleted and recreated**.
- The legacy `pm-<asr>-ns` ClusterRole is **deleted** as part of migration cleanup.

### Cleaner Compatibility

RBAC for an ASR is discovered by the `permission-manager.cloudoperators.dev/asr` label, so cleanup finds all of it. `EnsureOrphansCleanedUp` (`internal/provider/k8s/ensure_orphans_removed.go`) removes label-matched RBAC that is no longer in the compiled set (including scope transitions), and `EnsureRbacRemoved` removes all of it on deletion. It cleans up ClusterRoles, ClusterRoleBindings, Roles, and RoleBindings, all of which carry the ASR label. **No orphan resources are left behind.**

## Consequences

**Benefits:**
- Eliminates both privilege escalation vulnerabilities:
  - Namespace-scoped verbs never appear in a cluster-wide binding (Bug 1).
  - Each namespace gets exactly the verbs it was granted — no verb union leakage (Bug 2).
- Principle of least privilege: each Role/binding grants the minimum required scope and verbs.
- Namespace Roles are invisible outside their namespace (reduced cluster-level RBAC noise).
- No behavioral change for ASRs with only cluster-scoped permissions (cluster-only).

**Trade-offs:**
- **More resources per ASR**: an ASR targeting N namespaces produces N Roles + N RoleBindings instead of 1 ClusterRole + N RoleBindings. Acceptable for the security improvement.
- **RoleBinding migration**: the first reconcile after deployment will delete and recreate all RoleBindings for namespace-scoped ASRs (due to `roleRef` Kind change from ClusterRole to Role). This causes a brief window where namespace-scoped permissions are absent. Acceptable because the reconcile is fast and self-healing.
- **No cross-namespace deduplication**: if multiple namespaces happen to share the same verb set, each still gets its own Role. The cost is minimal (Roles are lightweight objects) and the design remains simpler.

**Related:**
- Capability mapping: the ConnectorBinding `capabilityMapping` defines the verb sets that flow into Roles/ClusterRoles (`internal/provider/k8s/ensure_compiled.go`, `UnionRulesForCapability`).
- RBAC apply: `EnsureRbacApplied` (`internal/provider/k8s/ensure_applied.go`) applies the compiled Roles/ClusterRoles to each target cluster.
- Orphan tracking: `EnsureOrphansCleanedUp` (`internal/provider/k8s/ensure_orphans_removed.go`) handles scope transitions.
