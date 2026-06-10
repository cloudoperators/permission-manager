# ADR-010: Multi-Cluster Client Factory with Caching

## Status

Pending review

## Context

The Kubernetes provider provisions RBAC on **remote clusters**. Each target cluster requires a `controller-runtime` client built from a kubeconfig stored as a Kubernetes Secret in the management cluster. Key challenges:

1. **Performance** — building a new REST client for every reconcile is expensive (TLS handshake, discovery, etc.).
2. **Namespace listing** — expanding namespace wildcards requires listing namespaces on remote clusters; this should not happen on every reconcile.
3. **Cluster discovery** — knowing which clusters exist (for wildcard matching) requires watching Greenhouse `Cluster` CRs.
4. **Failure handling** — remote clusters may be unreachable; the controller must handle this gracefully without blocking other clusters.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Fresh client per reconcile** | Build a new REST client from the kubeconfig Secret on every reconcile | Expensive — TLS handshake + discovery per reconcile; unacceptable latency with many ASRs × many clusters |
| **Use Greenhouse's `clientutil` directly** | Import Greenhouse's `NewK8sClientFromCluster` helper | `internal/` package, no caching, registers all scheme types; see ADR-011 for full analysis |
| **Shared multi-cluster manager (e.g., `liqo`, `kcp` virtual workspace)** | Use a higher-level multi-cluster framework that abstracts away individual cluster clients | Heavyweight dependency; our needs are narrow (RBAC only); introduces coupling to an external project's lifecycle |
| **Lazy-init client cache + TTL namespace cache** ✅ | Custom factory with per-cluster client caching and separate namespace cache with short TTL | Chosen — see Decision |

## Decision

Implement a **`GreenhouseClusterClientFactory`** that composes two caches with independent locking:

### Architecture

```
GreenhouseClusterClientFactory
├── ClusterClientCache    (mutex: clientMu)
│   └── map[clusterName] → cachedClient{client, restConfig}
└── NamespaceCache        (mutex: nsMu)
    └── map[clusterName] → cachedNamespaces{names []string, fetchedAt time.Time}
```

### Client Cache (`ClusterClientCache`)

- **Keyed by cluster name** — one client per remote cluster.
- **Lazy initialization** — clients are created on first access.
- **Secret-based** — reads kubeconfig from the cluster's Secret (`greenhousekubeconfig` or `kubeconfig` key).
- **No TTL** — clients persist for the controller's lifetime. If a cluster's kubeconfig rotates, the old client will fail and a new one will be created on the next attempt.
- **Scheme** — each remote client registers only `rbacv1` types (ClusterRole, ClusterRoleBinding, RoleBinding) plus core types (Namespace for listing).

### Namespace Cache (`NamespaceCache`)

- **TTL-based** — cached namespace lists expire after 60 seconds.
- **Per-cluster** — each cluster has its own cached list.
- **Injectable clock** — for deterministic testing without real time.
- **Used for wildcard expansion** — when a CCRN contains a namespace wildcard, the cache avoids repeated list calls to the remote cluster.

### Error Handling

```go
type ClusterUnreachableError struct {
    ClusterName string
    Reason      string
}
```

When a cluster is unreachable:
- The status for that cluster is set to `"Unreachable"` (not `"Error"`).
- A Kubernetes event is emitted on the ASR.
- Other clusters in the same reconcile continue normally.
- The ASR is requeued with `RequeueAfter: 30s` to retry.

### Interface Abstraction

```go
type ClusterClientFactory interface {
    ClientForCluster(ctx context.Context, clusterName string) (ClusterClient, error)
}

type NamespaceLister interface {
    NamespacesForCluster(ctx context.Context, clusterName string) ([]string, error)
}
```

Both interfaces are implemented by `GreenhouseClusterClientFactory`, enabling clean test injection with fake clients.

## Consequences

**Benefits:**
- Efficient — clients are reused across reconciles; namespace lists are cached with short TTL.
- Concurrent-safe — separate mutexes for client and namespace caches avoid cross-blocking.
- Testable — interfaces allow injecting fake clients for unit tests without real clusters.
- Graceful degradation — unreachable clusters don't block provisioning on healthy clusters.
- Clear error taxonomy — `ClusterUnreachableError` vs generic errors enables appropriate status reporting.

**Trade-offs:**
- No client cache invalidation on Secret rotation — a rotated kubeconfig requires the stale client to fail first. In practice, Greenhouse Secrets have long-lived tokens, and the error path re-creates the client.
- Namespace cache TTL (60s) means wildcard expansion can be up to 60s stale. Acceptable for RBAC provisioning where namespaces change infrequently.
- Memory grows linearly with the number of remote clusters — each client holds an HTTP connection pool. Bounded by the realistic number of clusters (low hundreds).
