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
| `k8s` | `cluster` | Kubernetes cluster (cluster-scoped RBAC target) |
| `k8s` | `namespace` | Kubernetes namespace (namespace-scoped RBAC target) |
| `github` | `organization` | GitHub organization |
| `github` | `repository` | GitHub repository |
| `github` | `repository_type` | Class of repositories by visibility |
| `greenhouse` | `organization` | Greenhouse organization |

## Configuring the API Group

The default CCRN API group is `ccrn.cloudoperators.dev`. To override (e.g., for internal deployments), set `ccrn.apiGroup` in `values.yaml`:

```yaml
ccrn:
  apiGroup: ccrn.your-domain.com
```

The template variable `{{ .Values.ccrn.apiGroup }}` is used in all CRD definitions, making the domain fully configurable at deploy time.

## Deploying

These CCRN definitions are Helm templates. Render them with:

```bash
helm template my-ccrns ./ccrn-definitions/ -f ccrn-definitions/values.yaml
```

The rendered CRDs can then be applied to your cluster to enable CCRN validation via the [common-cloud-resource-names](https://github.com/cloudoperators/common-cloud-resource-names) webhook.
