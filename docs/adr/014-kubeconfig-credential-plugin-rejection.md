# ADR-014: Kubeconfig Credential Plugin Rejection

## Status

Pending review

## Context

The `GreenhouseClusterClientFactory` (ADR-010) reads kubeconfig Secrets from the management cluster and uses `clientcmd.RESTConfigFromKubeConfig()` to build `rest.Config` objects for remote cluster clients. The client-go library supports two dynamic credential plugin mechanisms in kubeconfigs:

1. **`exec` credential plugins** — specifies a binary command to execute for token generation (`users[].user.exec`)
2. **`authProvider` plugins** — specifies a plugin for authentication (`users[].user.auth-provider`)

Both mechanisms cause the client-go transport layer to **execute arbitrary binaries** specified in the kubeconfig when making API calls. This creates a Remote Code Execution (RCE) vulnerability: an attacker with write access to Secrets in the controller's watched namespace can craft a kubeconfig containing an exec stanza, achieving code execution inside the controller pod with the controller's service account privileges.

### Threat Model

- **Attacker access required**: `create` or `update` on Secrets in the controller's watch namespace (typically the Greenhouse organization namespace).
- **Blast radius**: Arbitrary code execution in the controller pod. The controller's service account has broad read access to CRDs, Secrets, and cluster resources across the watched scope.
- **Likelihood**: Medium — the watched namespace typically hosts resources from multiple controllers/operators, and namespace-level Secret write access is a lower privilege bar than cluster-admin.

### Legitimate Kubeconfig Formats

In the Greenhouse platform, cluster kubeconfigs are provisioned by the Greenhouse platform controller using:
- **Static tokens** (`users[].user.token`)
- **Client certificates** (`users[].user.client-certificate-data` + `client-key-data`)

No legitimate use case requires exec or authProvider plugins for controller-to-cluster authentication.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Allow all kubeconfig auth methods** | Trust that only legitimate Secrets exist in the watched namespace | Any actor with Secret write access gains RCE in the controller pod; violates defense-in-depth |
| **Sandboxed exec via seccomp/AppArmor** | Allow exec plugins but restrict what the spawned process can do via container-level security policies | Incomplete mitigation — credential plugins inherit the pod's service account token and network access; seccomp cannot prevent data exfiltration over the network |
| **Allowlist of specific exec plugin binaries** | Reject unknown exec plugins but permit a vetted set (e.g., `gke-gcloud-auth-plugin`) | No current use case; increases attack surface maintenance burden; a compromised allowed binary is still full RCE |
| **Reject exec and authProvider unconditionally** ✅ | Block both mechanisms before the transport layer ever invokes them | Chosen — see Decision |

## Decision

After successfully parsing a kubeconfig via `clientcmd.RESTConfigFromKubeConfig()`, the `ClientForCluster` method **rejects** the resulting `rest.Config` if either `restConfig.ExecProvider` or `restConfig.AuthProvider` is non-nil. Rejection returns a `ClusterUnreachableError` with a descriptive reason and logs a warning for security auditing.

```go
if restConfig.ExecProvider != nil {
    return nil, &ClusterUnreachableError{
        ClusterName: clusterName,
        Reason:      "kubeconfig uses exec credential plugin (forbidden)",
    }
}
if restConfig.AuthProvider != nil {
    return nil, &ClusterUnreachableError{
        ClusterName: clusterName,
        Reason:      "kubeconfig uses auth provider plugin (forbidden)",
    }
}
```

The validation occurs **before** building the controller-runtime client — the exec binary is never invoked.

## Consequences

**Benefits:**
- Eliminates the RCE vector via kubeconfig Secrets entirely.
- Defense-in-depth: even if an attacker gains Secret write access, they cannot achieve code execution through this path.
- No operational impact — no legitimate cluster kubeconfig in Greenhouse uses exec/authProvider.
- The rejection surfaces clearly in ASR status as "Unreachable" with an explanatory message.

**Trade-offs:**
- If a future cluster integration requires exec-based authentication (e.g., cloud provider CLI token refresh), this would need to be revisited. Current assessment: not needed, and the security risk outweighs the flexibility.
- The check is per-reconcile (on cache miss). Already-cached clients are unaffected until the Secret's `resourceVersion` changes.

**Related ADRs:**
- ADR-010 (Multi-Cluster Client Factory) — describes the caching and client-building pipeline where this check is inserted.
