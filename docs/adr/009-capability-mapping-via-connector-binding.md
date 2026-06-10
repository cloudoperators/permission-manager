# ADR-009: Capability Mapping via ConnectorBinding

## Status

Pending review

## Context

ASR permissions use abstract capability names (e.g., `READ`, `WRITE`, `OPERATE`, `ADMIN`). Each provider interprets these differently:

- **Kubernetes** — maps to RBAC verbs (`get`, `list`, `watch`, `create`, `update`, etc.)
- **GreenhouseTeams** — maps to Greenhouse TeamRole names (e.g., `cluster-admin`, `namespace-admin`)
- **GitHub** — maps to repository permissions (`pull`, `push`, `admin`)

The question: where should this mapping live?

Options:
1. **Hardcoded per provider** — the Kubernetes provider already hardcodes `CapabilityVerbs` internally. Simple but inflexible.
2. **In the ASR spec** — clutters the ASR with provider-specific details and violates separation of concerns.
3. **In the ConnectorBinding definition** — the CB already carries provider-specific configuration; the mapping belongs there.

## Decision

The **ConnectorBinding's `spec.definition`** carries a `capabilityMapping` field that maps abstract capabilities to provider-specific values:

```json
// ConnectorBinding for greenhouse-teams
{
  "defaultTargetNamespace": "greenhouse-org-foo",
  "capabilityMapping": {
    "READ": "cluster-viewer",
    "WRITE": "namespace-admin",
    "OPERATE": "cluster-admin",
    "ADMIN": "cluster-admin"
  }
}
```

```json
// ConnectorBinding for github
{
  "instance": "github.example.com",
  "org": "my-org",
  "capabilityMapping": {
    "READ": "pull",
    "WRITE": "push",
    "ADMIN": "admin"
  }
}
```

The shared `pkg/greenhouse.CapabilityMapping` type provides a `Resolve(capability) (string, error)` method. Unmapped capabilities produce a warning (logged but don't fail the reconciliation).

### Exception: Kubernetes Provider

The Kubernetes provider uses a **hardcoded** capability-to-verbs mapping because Kubernetes RBAC verbs are standardized and there's a well-defined relationship between capabilities and verb sets. This is acceptable because:
- The mapping is stable and unlikely to change.
- Kubernetes RBAC verbs are a fixed set defined by the API server.
- Allowing customization here would create a security risk (users could escalate by mapping READ to `*`).

## Consequences

**Benefits:**
- ASRs are provider-agnostic — the same `WRITE` capability means "push" for GitHub and "namespace-admin" for Greenhouse, determined by the ConnectorBinding.
- Different ConnectorBindings for the same provider can have different mappings — e.g., a production GH connector maps WRITE→push while a sandbox connector maps WRITE→admin.
- Adding new capabilities requires only ConnectorBinding changes, not code changes.
- Unmapped capabilities are warnings, not errors — partial provisioning is possible.

**Trade-offs:**
- ConnectorBinding authors must know the provider's target values (TeamRole names, permission levels).
- No validation that mapped values are valid provider-side — a typo in a TeamRole name will only fail at reconciliation time.
- The Kubernetes provider's hardcoded mapping is inconsistent with the ConnectorBinding-based approach of other providers. Documented as an intentional exception for security.
