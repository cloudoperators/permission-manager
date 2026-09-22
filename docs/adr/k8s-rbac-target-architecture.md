# Kubernetes RBAC Target Architecture

## Purpose

This document states the target architecture for the Kubernetes provider's RBAC model and
frames ADR-017 and ADR-018 as sequenced steps toward it. It is an overview, not a decision
record; the decisions live in the two ADRs it references.

ADR-017 and ADR-018 are tightly coupled and are best read together. This document exists so
a reader can understand the end state in one place before reading either ADR in detail.

## End state

The Kubernetes provider grants access through four CCRN types and a closed capability
vocabulary:

- **`cluster.k8s`** and **`namespace.k8s`** address a scope (whole cluster, or one
  namespace) and carry no resource-type information. Their capabilities resolve through the
  ConnectorBinding.
- **`cluster-resource.k8s`** and **`namespace-resource.k8s`** embed the API group and
  resource type directly in the CCRN address, for targeted grants that the coarse scopes
  cannot express (a specific CRD, webhook configuration, or Secret).

The capability vocabulary is a closed, per-scope set enforced by the controller. There is no
open string space of capability names.

| CCRN type | Capabilities |
|---|---|
| `cluster.k8s` | `MANAGE`, `READ` |
| `namespace.k8s` | `MANAGE`, `OPERATE`, `READ` |
| `cluster-resource.k8s` | `READ`, `WRITE`, `MANAGE` |
| `namespace-resource.k8s` | `READ`, `WRITE`, `MANAGE` |

RBAC output in the end state:

- `cluster.k8s` and `namespace.k8s` compile to bindings against **shared aggregated
  ClusterRoles owned by the ConnectorBinding**, not per-ASR inline rules. `namespace.k8s`
  uses a ClusterRole + namespace-scoped RoleBinding so it can participate in aggregation.
- `cluster-resource` and `namespace-resource` compile to **per-ASR ClusterRoles** carrying
  the inline rules parsed from the CCRN address.
- Secrets are never granted implicitly. `namespace:READ`/`cluster:READ` inherit the
  built-in `view` role's Secret exclusion; `namespace:MANAGE` grants Secrets deliberately
  via `admin`; all other Secret access is expressed explicitly through resource CCRNs.

## Sequenced steps

| Step | ADR | Introduces |
|---|---|---|
| 1 | [ADR-017](017-resource-ccrn-types.md) | The two resource CCRN types with hardcoded, CB-independent verb sets |
| 2 | [ADR-018](018-capability-mapping-rework.md) | The closed per-scope vocabulary, the shared aggregated ClusterRole model, and the `namespace.k8s` binding change |

ADR-018 supersedes some statements in ADR-017 (it adds `MANAGE` to the resource CCRN
capabilities, and moves the coarse CCRNs off per-ASR inline rules). ADR-017 carries forward
notes at the affected points. Read ADR-017 first for the resource CCRN grammar, then ADR-018
for the capability model that ties everything together.
