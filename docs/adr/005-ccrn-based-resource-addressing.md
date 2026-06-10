# ADR-005: CCRN-Based Resource Addressing

## Status

Pending review

## Context

ASR permissions specify (resource, capability) pairs. The `resource` field must identify targets across multiple domains: Kubernetes clusters, Kubernetes namespaces, Git repositories, and Greenhouse clusters. We need a uniform addressing scheme that:

- Is parseable by all providers.
- Supports wildcards for pattern-based targeting (e.g., "all clusters matching `nw-a-*`").
- Is extensible to new resource types without schema changes.
- Can be validated at the CRD level.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Free-form strings with provider-specific conventions** | Each provider defines its own resource identifier format (e.g., `k8s://cluster/namespace`, `github:org/repo`) | No uniform parsing, no shared validation, every consumer must understand every format |
| **Structured sub-fields in the CRD** | Separate fields per resource type (`spec.permissions[].kubernetes.cluster`, `spec.permissions[].github.repo`) | Requires schema changes for every new resource type; ASR spec becomes provider-aware and bloated |
| **Label-selector-based resource references** | Reference resources by labels on external objects (e.g., Cluster CRs) | Loses per-permission granularity; cannot address namespaces or repos; requires API calls to resolve at reconcile time |
| **CCRN URNs with trailing-star wildcards** ✅ | Uniform URN format parseable by all providers, extensible via versioned type segments | Chosen — see Decision |

## Decision

Use **Common Cloud Resource Name (CCRN)** URNs as the resource identifier format:

```
urn:ccrn:<resource-type>.<domain>.ccrn.cloudoperators.dev/v1/<path-segments>
```

### Supported Formats

| Resource | Format | Example |
|----------|--------|---------|
| K8s Cluster | `urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/<cluster>` | `urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/nw-a-prod` |
| K8s Namespace | `urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/<cluster>/<ns>` | `urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/nw-a-prod/kube-system` |
| Git Repo | `urn:ccrn:repo.git.ccrn.cloudoperators.dev/v1/<org>/<repo>` | `urn:ccrn:repo.git.ccrn.cloudoperators.dev/v1/my-org/my-repo` |
| GH Cluster | `urn:ccrn:cluster.greenhouse.ccrn.cloudoperators.dev/v1/<cluster>` | `urn:ccrn:cluster.greenhouse.ccrn.cloudoperators.dev/v1/gh-prod` |

### Wildcard Support

Only **trailing-star prefix-glob** is supported:
- `nw-a-*` matches `nw-a-prod`, `nw-a-staging`, etc.
- `*` matches everything.
- Mid-string wildcards (e.g., `nw-*-prod`) are not supported.

The `pkg/ccrn/` package provides:
- `Parse(urn) → ParsedCCRN` — extracts type, domain, cluster name, namespace.
- `IsWildcard(s) bool` — checks for wildcard characters.
- `MatchGlob(pattern, name) bool` — performs prefix-glob matching.

### Domain Routing

The `Domain` field in `ParsedCCRN` enables providers to filter permissions relevant to them:
- Kubernetes provider processes `domain == "k8s"` CCRNs.
- GreenhouseTeams provider processes `domain == "k8s"` CCRNs (maps to TeamRoleBinding cluster selectors).
- GitHub provider processes `domain == "git"` CCRNs.

## Consequences

**Benefits:**
- Uniform resource addressing across all providers — the ASR spec doesn't need provider-specific fields.
- Wildcards enable broad targeting without enumerating every cluster/namespace.
- Versioned URN format (`/v1/`) allows future evolution without breaking existing resources.
- Clean separation of concerns — parsing logic is in a shared `pkg/ccrn/` package reusable by all providers.

**Trade-offs:**
- URN strings are verbose and somewhat hard to read for humans.
- Wildcard resolution happens at reconcile time, requiring access to the cluster inventory — patterns are not pre-expanded.
- Only trailing-star wildcards are supported; more complex matching (regex, multiple wildcards) is not available.
- The CCRN format is not a widely-adopted external standard — it's internal to this system.
