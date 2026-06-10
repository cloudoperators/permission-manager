# ADR-006: Per-Provider Finalizers

## Status

Pending review

## Context

When an ASR is deleted, each provider must clean up its managed resources before the object is removed from etcd. The original implementation used a single shared finalizer (`permission-manager.cloudoperators.dev/rbac-cleanup`) managed by the Kubernetes provider. With multiple independent providers, this creates problems:

- If one provider fails cleanup, it blocks all other providers from completing.
- Providers can't tell if the finalizer is "theirs" to remove.
- Adding a new provider requires no changes, but removing a provider leaves orphaned finalizers.

### Alternatives considered

| Approach | Description | Rejected because |
|----------|-------------|-----------------|
| **Single shared finalizer** | One finalizer (`rbac-cleanup`) managed by a coordinator or the first provider | One provider's failure blocks all; ownership ambiguity; removing a provider leaves a dangling finalizer no one manages |
| **No finalizers (background garbage collection)** | Use a periodic GC controller that discovers and deletes orphaned resources by label | No deletion guarantee — GC lag creates a window where revoked access remains active; violates security-first design |
| **Owner references with cascading delete** | Set the ASR as ownerReference on managed resources so Kubernetes GC handles deletion | Cross-cluster resources (ClusterRoles on remote clusters) cannot use owner references; only works for same-cluster resources |
| **Per-provider finalizers** ✅ | Each provider adds/removes its own finalizer independently | Chosen — see Decision |

## Decision

Each provider adds and manages its own finalizer using a deterministic naming convention:

```
permission-manager.cloudoperators.dev/<provider-id>
```

Examples:
- `permission-manager.cloudoperators.dev/kubernetes`
- `permission-manager.cloudoperators.dev/github`
- `permission-manager.cloudoperators.dev/greenhouse-teams`

The shared helper `pkg/provider.FinalizerName(providerID)` generates the finalizer string.

### Lifecycle

1. **Add** — on first successful reconcile, the provider adds its finalizer if not present.
2. **Remove** — when `deletionTimestamp` is set, the provider runs cleanup and removes only its own finalizer.
3. **Migration** — the Kubernetes provider transparently migrates the legacy `rbac-cleanup` finalizer to the new per-provider format on first reconcile.

### Deletion Flow

```
ASR deleted (deletionTimestamp set)
  ├── Kubernetes provider sees it → cleanup RBAC → remove k8s finalizer
  ├── GitHub provider sees it → cleanup GithubTeam/TeamRepo → remove github finalizer
  └── GreenhouseTeams provider sees it → cleanup Team/TRB → remove greenhouse-teams finalizer
       └── All finalizers removed → API server deletes the object
```

## Consequences

**Benefits:**
- Independent cleanup — each provider manages its own lifecycle without blocking others.
- Crash resilience — if one provider is down, the ASR enters a finalizer-blocked state only for that provider; other providers complete normally (once it restarts, it resumes cleanup).
- Clear ownership — inspecting finalizers on an ASR immediately shows which providers have claimed it.
- Safe provider removal — uninstalling a provider leaves its finalizer; an admin can manually remove it if needed.

**Trade-offs:**
- Multiple finalizers per object — an ASR with three providers has three finalizers. This is standard Kubernetes practice but can look noisy.
- Migration logic — the Kubernetes provider must detect and migrate the legacy `rbac-cleanup` finalizer during a transition period.
- If a provider is permanently removed without cleaning up, finalizers will block ASR deletion. Operators must manually strip orphaned finalizers.
