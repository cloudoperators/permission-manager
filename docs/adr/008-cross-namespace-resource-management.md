# ADR-008: Cross-Namespace Resource Management

## Status

Pending review

## Context

Permission Manager ASRs live in a team-specific namespace (e.g., `team-a`), but the resources they manage must be created in different namespaces:

- **Kubernetes provider** — RBAC is created on remote clusters (not the management cluster namespace at all).
- **GreenhouseTeams provider** — Team/TRB CRs must be created in the Greenhouse **organization namespace** (e.g., `greenhouse-org-foo`), which is different from the ASR's namespace.
- **GitHub provider** — GithubTeam/GithubTeamRepository CRs may target a provider-specific namespace.

The question: how does each provider know where to create its resources?

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Always use the ASR namespace** | Create all managed resources in the same namespace as the ASR | Incorrect for GreenhouseTeams (Greenhouse controllers only watch org namespaces); silently broken |
| **Hardcode target namespaces per provider** | Each provider has a fixed config for where to create resources | Inflexible; doesn't support multi-org setups or per-connector overrides |
| **Namespace annotation on the ASR** | ASR author annotates target namespace directly | Mixes provider-specific config into the ASR spec; breaks provider-agnosticism |
| **Priority chain via ConnectorBinding + advancedConfig** ✅ | Resolution follows explicit config → CB default → provider-specific fallback, with label-based ownership tracking | Chosen — see Decision |

## Decision

Target namespace resolution follows a **priority chain** specified in the ConnectorBinding and ASR:

### GreenhouseTeams Provider

```
Priority 1: advancedConfig.greenhouse-teams.targetNamespaces (explicit list)
Priority 2: ConnectorBinding.spec.definition.defaultTargetNamespace (single default)
Priority 3: ERROR — no fallback to ASR namespace
```

The decision to **not** fall back to the ASR namespace is intentional: Greenhouse CRs must live in an organization namespace for Greenhouse's controllers to discover them. Creating them in the wrong namespace silently fails.

### GitHub Provider

```
Priority 1: advancedConfig.github.targetNamespace (explicit)
Priority 2: ConnectorBinding.spec.definition.defaultTargetNamespace
Priority 3: ASR namespace (fallback)
```

### Kubernetes Provider

The kubernetes provider doesn't create resources in the management cluster — it provisions RBAC on **remote clusters** identified by CCRN cluster names. Namespace targeting within those clusters is resolved from CCRN namespace segments and expanded via the NamespaceLister.

### Ownership Tracking

All cross-namespace resources are labeled with their source ASR for reverse lookup during cleanup:

```yaml
labels:
  managed-by: greenhouse-teams          # or pm-controller, github-provider
  source-asr: my-asr-name
  source-namespace: team-a
```

During deletion, the provider uses these labels to find and delete resources even if the ConnectorBinding (which specifies target namespaces) has already been deleted:

```go
func (r *Reconciler) cleanupAllNamespaces(ctx, asrName, asrNamespace) error {
    // Label-based search across all namespaces
    labels := client.MatchingLabels{...}
    r.List(ctx, &trbList, labels)
    // delete each
}
```

## Consequences

**Benefits:**
- Clear separation between "where the ASR lives" and "where resources are created."
- Multiple target namespaces supported — an ASR can target several Greenhouse orgs simultaneously.
- Label-based cleanup is resilient to ConnectorBinding deletion — resources can always be found.
- No implicit assumptions about namespace layout.

**Trade-offs:**
- Cross-namespace operations require broad RBAC — the controller needs permissions across all potential target namespaces (typically cluster-scoped list/create/delete).
- Label-based cleanup is a full list+filter — not indexed. Acceptable at expected scale.
- The "no fallback" policy for GreenhouseTeams means misconfigured ASRs fail explicitly rather than silently creating resources in the wrong place. This is a feature for correctness but requires careful documentation.
