# CCRN Definitions

This directory contains **Common Cloud Resource Name (CCRN)** type definitions used by the Permission Manager controller to resolve resources referenced in `ApplicationSpecificRole` permissions.

## What is a CCRN?

A CCRN is a URN-formatted, machine-readable identifier for resources across heterogeneous systems:

```
urn:ccrn:<resource-type>.<domain>.<api-group>/v1/<resource-path>
```

Examples:
- `urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu-1`
- `urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu-1/monitoring`
- `urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.com/my-org/my-repo`

## Included Resource Types

| Domain | Type | Description |
|--------|------|-------------|
| `k8s` | `cluster` | Kubernetes cluster (cluster-scoped RBAC target) — **bundled with the permission-manager chart** |
| `k8s` | `namespace` | Kubernetes namespace (namespace-scoped RBAC target) — **bundled with the permission-manager chart** |
| `github` | `organization` | GitHub organization |
| `github` | `repository` | GitHub repository |
| `github` | `repository_type` | Class of repositories by visibility |
| `greenhouse` | `organization` | Greenhouse organization |

> **Note:** The `k8s` CCRN CRDs (`K8sCluster`, `K8sNamespace`) have been moved into the
> `charts/permission-manager` Helm chart and are installed automatically when `ccrn.enabled=true`
> (the default). They are no longer present in this directory.

## Configuring the API Group

The default CCRN API group is `ccrn.cloudoperators.dev`. To override (e.g., for internal deployments), set `ccrn.apiGroup` in this directory's `values.yaml`:

```yaml
ccrn:
  apiGroup: ccrn.your-domain.com
```

The template variable `{{ .Values.ccrn.apiGroup }}` is used in the `github` and `greenhouse` CRD definitions in this directory, making the domain configurable at deploy time.

> **Note:** The `k8s` CRDs are installed via the permission-manager chart and use a
> separate `ccrn.apiGroups` **list** value (one CRD per group). See
> `charts/permission-manager/values.yaml`.

## Deploying

The `github` and `greenhouse` CCRN definitions in this directory are Helm templates. Render them with:

```bash
helm template my-ccrns ./ccrn-definitions/ -f ccrn-definitions/values.yaml
```

The `k8s` CRDs are installed via the permission-manager chart — use `ccrn.enabled=true` (default) and set `ccrn.apiGroups` there instead.
