# ADR-016: Capability Taxonomy Revision (SECURE + Wildcard)

## Status

Pending review (supersedes capability entries in ADR-009)

## Context

The Kubernetes provider's `CapabilityVerbs` map had two problems:

### Problem 1: MANAGE Mapped to Privilege-Escalation Verbs

```go
// Old (broken)
"MANAGE": {"bind", "escalate", "impersonate"},
"ADMIN":  {"*"},
```

The verbs `bind`, `escalate`, and `impersonate` are **privilege-escalation verbs** in Kubernetes RBAC:
- `bind` — bind any ClusterRole to any subject, bypassing the "you can only bind roles you already have" rule
- `escalate` — edit ClusterRoles beyond your own permissions (bypasses Kubernetes' built-in escalation prevention)
- `impersonate` — act as any user, group, or service account

These verbs are fundamentally different from "managing resources" — they are security-administration operations that can grant full cluster-admin access. Mapping them to a generic "MANAGE" capability obscured their danger.

### Problem 2: ADMIN Was Redundant

`ADMIN` mapped to `*` (all verbs), which already includes `bind`, `escalate`, and `impersonate`. Having both MANAGE and ADMIN was confusing — MANAGE was effectively a subset of ADMIN with no clear semantic boundary.

### The Canonical Capability Taxonomy

The Permission Manager's capability taxonomy (documented in CONTEXT.md glossary) defines a clear progression:

```
READ < WRITE < OPERATE < MANAGE < SECURE
```

Where each level represents increasing authority. For the Kubernetes provider, the question was: what verb sets map to this taxonomy?

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Keep existing MANAGE/ADMIN mapping** | Retain `MANAGE → [bind, escalate, impersonate]` and `ADMIN → [*]` | Dangerous verbs hidden behind a generic name; MANAGE vs ADMIN distinction is confusing; no clear security boundary |
| **Hierarchical cumulative capabilities (READ ⊂ WRITE ⊂ OPERATE ⊂ ADMIN)** | Each level includes all verbs from lower levels, ADMIN at top with `*` | Forces privilege escalation verbs (bind/escalate/impersonate) into a single hierarchy; a team needing OPERATE would automatically get WRITE; no way to grant security-admin verbs independently |
| **Per-verb granular capabilities (no grouping)** | Each Kubernetes verb is its own capability (`CAP_GET`, `CAP_LIST`, `CAP_BIND`, etc.) | Too granular for the ASR abstraction layer; defeats the purpose of capability-based simplification; ASRs become verbose verb lists |
| **Corrected taxonomy with SECURE as lateral capability + wildcard** ✅ | READ, WRITE, OPERATE as independent groups; SECURE isolates privilege-escalation verbs; `*` for full access | Chosen — see Decision |

## Decision

Replace the `CapabilityVerbs` map with a corrected taxonomy that creates a clear security boundary:

```go
var CapabilityVerbs = map[string][]string{
    "READ":    {"get", "list", "watch"},
    "WRITE":   {"get", "list", "watch", "create", "update", "patch", "delete"},
    "OPERATE": {"exec", "port-forward", "proxy"},
    "SECURE":  {"bind", "escalate", "impersonate"},
    "*":       {"*"},
}
```

### Taxonomy Semantics

| Capability | K8s Verbs | Meaning |
|---|---|---|
| `READ` | get, list, watch | View resources |
| `WRITE` | get, list, watch, create, update, patch, delete | Full CRUD on resources |
| `OPERATE` | exec, port-forward, proxy | Interact with running workloads |
| `SECURE` | bind, escalate, impersonate | Privilege management (security-admin) |
| `*` | `*` (wildcard) | Everything — full cluster-admin |

### Key Design Choices

1. **SECURE is a lateral capability, not hierarchical.** It is not "higher" than MANAGE in the normal sense — it grants a specific category of dangerous verbs that are orthogonal to resource CRUD. An ASR can grant WRITE + SECURE without granting OPERATE.

2. **`*` as a capability name.** The wildcard `*` serves as the "everything" capability. It is case-insensitive safe (non-alphabetic characters pass through `strings.ToUpper` unchanged). It is the only way to grant all verbs including future ones.

3. **MANAGE and ADMIN are removed.** Using either in an ASR's `capability` field will trigger the "unmapped capability" status, alerting operators to update. No production k8s ASRs currently use MANAGE or ADMIN.

4. **WRITE subsumes READ.** WRITE includes all READ verbs (get/list/watch) plus mutation verbs. This is intentional — you can't meaningfully write without reading.

5. **OPERATE is independent.** exec/port-forward/proxy are separate from CRUD operations. A team might need to exec into pods (OPERATE) without being able to create or delete them.

## Consequences

**Benefits:**
- Clear security boundary: privilege-escalation verbs (`bind`, `escalate`, `impersonate`) are isolated under an explicitly-named "SECURE" capability that signals danger.
- Simpler mental model: `*` is the single top-level "everything" capability, no confusion between MANAGE and ADMIN.
- Consistent with the project's capability taxonomy documented in CONTEXT.md.
- Unmapped MANAGE/ADMIN surfaces in ASR status, prompting migration.

**Trade-offs:**
- **Breaking change for MANAGE/ADMIN users** — any ASR using these capabilities on k8s CCRNs will show as degraded. Current assessment: no production k8s ASRs use them.
- **SECURE capability is extremely powerful** — even namespace-scoped SECURE grants `impersonate` which can be used for escalation. This is intentional and documented — only security administrators should use it, and it requires explicit assignment.
- The `*` capability key is unusual (not a word). Considered acceptable because it mirrors the Kubernetes RBAC wildcard convention and is immediately understandable.

**Non-k8s providers unaffected:**
- GitHub and GreenhouseTeams providers use ConnectorBinding-based capability mapping (ADR-009). Their interpretation of MANAGE is determined by the ConnectorBinding's `capabilityMapping` field, not the hardcoded `CapabilityVerbs` map. This change is k8s-provider-only.

**Related ADRs:**
- ADR-009 (Capability Mapping) — documents that the k8s provider's hardcoded map is an intentional exception. This ADR updates what that map contains.
- ADR-015 (Scope-Separated ClusterRoles) — even with SECURE properly scoped, the scope-separation ensures namespace-scoped SECURE only grants those verbs within specific namespaces.
