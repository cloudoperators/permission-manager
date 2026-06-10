# ADR-015: Scope-Separated ClusterRoles for RBAC Generation

## Status

Pending review (supersedes implicit design in ADR-010)

## Context

The Kubernetes provider generates RBAC resources (ClusterRole, ClusterRoleBinding, RoleBinding) on target clusters based on ASR permissions. The original `buildRBACPlan` implementation merged **all verbs** — from both cluster-scoped and namespace-scoped permissions — into a **single ClusterRole** with wildcard rules (`apiGroups: ["*"]`, `resources: ["*"]`).

This created a privilege escalation vulnerability:

### The Verb-Merging Bug

Given an ASR with:
- `READ` on cluster CCRN (cluster-scoped) → verbs: `get, list, watch`
- `WRITE` on namespace CCRN (namespace-scoped) → verbs: `create, update, patch, delete`

The original code:
1. Merged all verbs into one ClusterRole: `[create, delete, get, list, patch, update, watch]` on `*/*`
2. Created a ClusterRoleBinding (because cluster-scoped verbs exist) referencing this merged ClusterRole
3. **Result**: The subject received `create, update, patch, delete` **cluster-wide**, even though WRITE was intended only for a specific namespace

The RoleBindings for specific namespaces were also created but were redundant — the ClusterRoleBinding already granted everything cluster-wide.

### Design Principle Violated

Kubernetes RBAC follows a **union-only, no-deny** model. The correct pattern for mixed-scope permissions is:
- Cluster-wide permissions → ClusterRoleBinding referencing a ClusterRole with **only** cluster-scoped verbs
- Namespace-scoped permissions → RoleBinding (in each namespace) referencing a ClusterRole with **only** namespace-scoped verbs

A RoleBinding referencing a ClusterRole is a standard Kubernetes pattern that grants the ClusterRole's permissions **only within the binding's namespace**.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Single merged ClusterRole (original behavior)** | Combine all verbs into one ClusterRole; bind via both ClusterRoleBinding and RoleBindings | The privilege escalation bug — namespace-scoped verbs leak to cluster-wide via the ClusterRoleBinding |
| **Namespace-scoped Roles instead of ClusterRoles** | Create `Role` resources in each target namespace rather than ClusterRoles | Roles cannot be shared across namespaces; would require N identical Role objects for N namespaces with no deduplication; ClusterRoles referenced by RoleBindings are the standard Kubernetes pattern for this |
| **Single ClusterRole with per-rule namespace restrictions** | Use a single ClusterRole but add namespace-aware rules (resourceNames or namespace conditions) | Kubernetes RBAC rules do not support namespace restrictions within a ClusterRole — scope is determined entirely by the binding type |
| **Two scope-separated ClusterRoles per ASR per cluster** ✅ | Cluster-scoped verbs in `pm-<asr>`, namespace-scoped verbs in `pm-<asr>-ns`, each with appropriate binding type | Chosen — see Decision |

## Decision

Generate **two separate ClusterRoles** per ASR per cluster, each carrying the standard `permission-manager.cloudoperators.dev/asr` label:

| Resource | Name | Contains | Bound by |
|----------|------|----------|----------|
| ClusterRole | `pm-<asr>` | Cluster-scoped verbs only | ClusterRoleBinding `pm-<asr>` |
| ClusterRole | `pm-<asr>-ns` | Namespace-scoped verbs only | RoleBinding `pm-<asr>` (one per namespace) |

### Rules

1. If the ASR has **only** cluster-scoped permissions → produce only `pm-<asr>` + ClusterRoleBinding. No `-ns` ClusterRole.
2. If the ASR has **only** namespace-scoped permissions → produce only `pm-<asr>-ns` + RoleBindings. No ClusterRoleBinding.
3. If the ASR has **both** → produce both ClusterRoles with their respective bindings.
4. On reconcile, **stale counterparts are deleted**: if the ASR no longer has cluster-scoped permissions, the old `pm-<asr>` ClusterRole and ClusterRoleBinding are cleaned up (and vice versa for `-ns`).

### Migration from Old Format

Existing clusters may have a single `pm-<asr>` ClusterRole with merged verbs. On first reconcile after deployment:
- The `pm-<asr>` ClusterRole is **patched** to contain only cluster-scoped verbs (reduced, not expanded).
- A new `pm-<asr>-ns` ClusterRole is **created** with namespace-scoped verbs.
- Existing RoleBindings must have their `roleRef` updated from `pm-<asr>` to `pm-<asr>-ns`. Since Kubernetes forbids patching `roleRef`, these are **deleted and recreated**.

### RBACCleaner Compatibility

The `RBACCleaner` uses `client.MatchingLabels{LabelASRName: asrName}` to find all RBAC resources for an ASR. Since both ClusterRoles carry this label, **no changes to the cleaner are needed** — it discovers and deletes both automatically.

## Consequences

**Benefits:**
- Eliminates the privilege escalation vulnerability — namespace-scoped verbs can never appear in a cluster-wide binding.
- Principle of least privilege: each binding grants exactly the intended scope.
- No behavioral change for ASRs with only one scope type (cluster-only or namespace-only).
- Backward-compatible cleanup via label selectors.

**Trade-offs:**
- **More resources per ASR**: mixed-scope ASRs produce 2 ClusterRoles instead of 1. For N ASRs, worst case is 2N ClusterRoles. Acceptable for the security improvement.
- **RoleBinding migration**: the first reconcile after deployment will delete and recreate all RoleBindings for mixed-scope ASRs (due to `roleRef` immutability). This causes a brief window where namespace-scoped permissions are absent. Acceptable because the reconcile is fast and self-healing.
- **Complexity**: `buildRBACPlan` and `applyRBACPlan` become moderately more complex. Mitigated by clear naming and comprehensive unit tests.

**Related ADRs:**
- ADR-009 (Capability Mapping) — defines the verb sets that flow into ClusterRoles.
- ADR-010 (Multi-Cluster Client Factory) — the pipeline that delivers clients to `applyRBACPlan`.
- ADR-012 (Best-Effort Cleanup) — the orphan tracking that handles scope transitions.
