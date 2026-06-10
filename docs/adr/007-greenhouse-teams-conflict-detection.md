# ADR-007: GreenhouseTeams Conflict Detection (Team-Only Mode)

## Status

Pending review

## Context

An ASR can reference multiple ConnectorBindings — for example, both a `kubernetes` and a `greenhouse-teams` connector. Both providers independently reconcile the ASR:

- The **Kubernetes provider** creates ClusterRole/RoleBinding on remote clusters.
- The **GreenhouseTeams provider** creates Team + TeamRoleBinding (TRB) CRs, which Greenhouse's own controller then reconciles into ClusterRole/RoleBinding.

If both run normally, the **same RBAC resources are created twice** on the target clusters — once by the Kubernetes provider directly, and once by Greenhouse via the TRB. This causes:
- Duplicate RBAC resources with different naming conventions.
- Ownership conflicts (both controllers think they own the RBAC).
- Wasted reconciliation cycles.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Let both providers reconcile fully (accept duplicates)** | Both create their own RBAC independently; rely on idempotent naming to avoid true conflicts | Different naming conventions mean actual duplicates; double the RBAC resources; confusing for auditors; wasted API calls |
| **Coordinator controller that assigns exclusive ownership** | A separate controller inspects ASR connectors and annotates which provider "wins" RBAC responsibility | Adds a new controller, introduces ordering dependencies, increases latency before any RBAC is applied |
| **User-defined priority field on ConnectorBinding** | ASR author explicitly marks which connector takes precedence for RBAC | Shifts complexity to the user; error-prone; doesn't handle dynamic connector addition/removal gracefully |
| **Team-only mode with dynamic conflict detection** ✅ | GreenhouseTeams provider detects the kubernetes connector per-reconcile and skips TRB creation | Chosen — see Decision |

## Decision

Implement **conflict detection** in the GreenhouseTeams provider with a **Team-only mode**:

```go
mode, conflict, err := DetermineMode(ctx, client, asr, log)
// mode is either ModeFullReconcile or ModeTeamOnly
```

When an ASR references both a `kubernetes` and a `greenhouse-teams` ConnectorBinding:

1. The GreenhouseTeams provider operates in **Team-only mode**:
   - `Team` CRs are still created (needed for IDP group mapping in Greenhouse).
   - `TeamRoleBinding` CRs are **skipped** (the Kubernetes provider handles RBAC directly).
2. If existing TRBs are found from a previous full-reconcile (e.g., the kubernetes connector was added later), they are cleaned up via `CleanupTRBs()`.
3. The provider status reports the conflict and its mode.

### Detection Logic

```go
func HasKubernetesProvider(ctx, client, asr) (*ConflictResult, error) {
    for each connectorRef:
        fetch ConnectorBinding
        if cb.Spec.Provider == "kubernetes":
            result.HasConflict = true
    return result
}
```

The check is performed on every reconcile (not cached) to handle dynamic changes — a kubernetes connector can be added or removed from an ASR at any time.

## Consequences

**Benefits:**
- No duplicate RBAC on target clusters when both providers are active.
- Teams are still created — Greenhouse can use them for org-level team management even without TRBs.
- Dynamic — adding/removing a kubernetes connector from an ASR immediately changes the mode.
- Provider status clearly communicates the operating mode.

**Trade-offs:**
- Per-reconcile API calls to fetch ConnectorBindings for conflict detection. Mitigated by the ConnectorBinding index (O(1) lookup for known CBs), but the conflict check fetches them fresh to verify provider type.
- Implicit coupling — the GreenhouseTeams provider "knows about" the kubernetes provider. This is contained in a single `conflict.go` file and uses only the provider string constant.
- If the kubernetes provider is deployed but not functioning, TRBs are still skipped. The conflict is based on the ConnectorBinding's existence, not the provider's operational state.
