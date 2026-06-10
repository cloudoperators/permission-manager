# ADR-011: Own Cluster Client Implementation vs. Greenhouse clientutil

## Status

Pending review

## Context

Greenhouse provides a shared `clientutil` package (`internal/clientutil/`) with helpers for connecting to remote clusters:

- `NewK8sClientFromCluster(ctx, client, cluster)` — high-level one-liner
- `NewRestClientGetterFromSecret(secret, namespace)` — builds a `RESTClientGetter` from a kubeconfig Secret
- `NewK8sClientFromRestClientGetter(getter)` — converts getter to `client.Client`

These follow a well-established pattern: the kubeconfig is stored in a Secret whose name matches the Cluster CR name, with key `greenhousekubeconfig` (preferred) falling back to `kubeconfig`.

The pm-controller needs to connect to remote clusters to provision RBAC. The question is whether to use Greenhouse's `clientutil` or maintain an independent implementation.

### Constraints

1. **`clientutil` is an `internal/` package** — Go's visibility rules prevent importing it from an external module. The pm-controller lives in a separate repository/module (`permission-manager/pm-controller`), so a direct import is not possible without Greenhouse exporting the package.

2. **Performance requirements differ** — Greenhouse's `clientutil` creates a new client on every call (`NewK8sClientFromCluster` has no caching). The pm-controller reconciles many ASRs across many clusters and cannot afford a fresh TLS handshake + discovery call per reconcile.

3. **Scheme requirements differ** — Greenhouse registers all known API types into each remote client's scheme. The pm-controller only needs `core/v1` (Namespaces) and `rbac/v1` (RoleBindings, ClusterRoleBindings) on remote clusters, keeping the client lightweight.

4. **Cache invalidation semantics** — The pm-controller watches `Cluster` CRs and proactively invalidates its client cache when the `KubeConfigValid` condition changes. Greenhouse's pattern relies on the caller rebuilding the client as needed.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Import Greenhouse's `clientutil` directly** | Use Go import to reference `internal/clientutil` functions | Impossible — `internal/` visibility prevents cross-module import; would require Greenhouse to export the package |
| **Vendor/copy Greenhouse's clientutil source** | Copy the clientutil source files into pm-controller's tree | Creates a maintenance fork; no caching; no scheme minimization; divergence accumulates silently |
| **Wrap Greenhouse's client via gRPC/REST proxy** | Run a sidecar that exposes Greenhouse's client-building logic as a service | Over-engineered; adds latency, failure modes, and operational complexity for ~50 lines of logic |
| **Independent implementation, semantically compatible** ✅ | Maintain own `GreenhouseClusterClientFactory` that honours the same conventions but adds caching and minimal scheme | Chosen — see Decision |

## Decision

Maintain an independent `GreenhouseClusterClientFactory` implementation in the pm-controller that is **semantically compatible** with Greenhouse's cluster connectivity conventions but **structurally independent** of the `clientutil` package.

### Compatibility guarantees

The implementation honours the following Greenhouse conventions exactly:

| Convention | Implementation |
|---|---|
| Secret name = Cluster CR name | `Secret{Name: clusterName}` lookup |
| Preferred key: `greenhousekubeconfig` | Checked first in `ClientForCluster` |
| Fallback key: `kubeconfig` | Used when preferred key is absent |
| `KubeConfigValid` condition gate | `ClusterReconciler` checks condition before adding cluster to inventory |
| Secret lives in the org namespace | Scoped via `--watch-namespace` flag |

### Enhancements over Greenhouse's clientutil

| Enhancement | Rationale |
|---|---|
| `resourceVersion`-based client cache | Avoids rebuilding clients when the Secret hasn't changed |
| Namespace cache with 60s TTL | Avoids repeated `List Namespaces` calls to remote clusters during wildcard expansion |
| Proactive cache invalidation on Cluster CR changes | Ensures stale credentials are never used after rotation |
| Minimal scheme (`core/v1` + `rbac/v1`) | Reduces memory and startup cost per remote client |

## Consequences

**Benefits:**

- No dependency on Greenhouse's internal package structure — resilient to refactors in Greenhouse.
- Optimised for the pm-controller's access pattern (many reconciles, few distinct clusters, namespace wildcards).
- Full control over error handling and cache lifecycle.
- Still fully interoperable with the Greenhouse platform — consumes the same Secrets, respects the same conditions.

**Trade-offs:**

- Must track Greenhouse convention changes manually. If the Secret key names or Cluster CR semantics change upstream, the pm-controller must update accordingly.
- Duplicates ~50 lines of kubeconfig-from-Secret extraction logic that exists in Greenhouse's `clientutil`.
- If Greenhouse ever exports `clientutil` as a public package, we should evaluate adopting it to reduce maintenance surface (see "Revisit triggers" below).

**Revisit triggers:**

- Greenhouse moves `clientutil` to an exported `pkg/` path — evaluate importing it and wrapping with a caching layer.
- Greenhouse adds a client-caching layer upstream — evaluate adopting it directly.
- A third connectivity mode is added (beyond `kubeconfig` / `oidc`) — ensure the pm-controller handles it.
