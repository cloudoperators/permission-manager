# ADR-017: Migrate to Kubebuilder Project Layout

## Status

Accepted

## Context

The permission-manager-controller was initially developed as a standalone controller-runtime project with a hand-crafted project layout:

```
controller/
├── cmd/controller/main.go
├── pkg/
│   ├── reconciler/
│   ├── greenhouse/
│   └── ...
├── test/e2e/
├── Dockerfile
└── Dockerfile.*.e2e
```

The greenhouse team — the primary consumers and contributors to this project — uses kubebuilder v4 scaffolding extensively across their repositories (`cloudoperators/greenhouse`, `cloudoperators/greenhouse-extensions`). Their established conventions include:

- **Layout**: `go.kubebuilder.io/v4` with `multigroup: true`
- **Controllers**: `internal/controller/<kind>/` — one sub-package per Kind
- **Registration**: `knownControllers` map in `cmd/` with `SetupWithManager(name string, mgr ctrl.Manager) error` signature
- **CRD generation**: `+kubebuilder:` markers on types → `controller-gen crd` → CRDs land in `charts/` Helm chart
- **RBAC generation**: `+kubebuilder:rbac:` markers on reconcilers → `controller-gen rbac` → helmified into chart templates
- **Testing**: shared envtest harness via `internal/test/` with `RegisterController()` pattern
- **Feature flags**: `internal/features/` package, injected into controller constructors (not global state)
- **Lifecycle**: `EnsureCreated` / `EnsureDeleted` pattern via `pkg/lifecycle`
- **Tooling**: Makefile with `controller-gen` v0.21.0, `envtest` K8s 1.36.0, `kustomize` 5.8.1

Our current project diverges from these conventions, which creates friction:

1. **Onboarding**: greenhouse developers contributing to permission-manager must learn a different layout
2. **CRD maintenance**: CRDs are hand-maintained YAML rather than generated from markers — error-prone and unsynchronized with Go types
3. **RBAC maintenance**: RBAC manifests are hand-maintained rather than derived from controller code
4. **No envtest**: unit-level controller tests don't exist; only e2e tests against kind clusters (slow feedback loop)
5. **Code review**: reviewers familiar with greenhouse conventions struggle to navigate the unfamiliar structure

## Decision

We will migrate the permission-manager-controller to a kubebuilder v4 project layout, adopting greenhouse team conventions.

### Target Layout

```
permission-manager-controller/
├── PROJECT                              # kubebuilder project file
├── Makefile                             # controller-gen, envtest, build targets
├── api/
│   └── v1/                              # existing types + kubebuilder markers
│       ├── applicationspecificrole_types.go
│       ├── connectorbinding_types.go
│       ├── doc.go                       # +kubebuilder:object:generate=true
│       ├── groupversion_info.go
│       └── zz_generated.deepcopy.go
├── cmd/
│   ├── controller/main.go              # manager bootstrap
│   ├── github-provider/main.go
│   ├── greenhouse-teams-provider/main.go
│   └── aggregator/main.go
├── internal/
│   ├── controller/
│   │   ├── kubernetes/                  # kubernetes provider reconciler
│   │   ├── github/                      # github provider reconciler
│   │   ├── greenhouseteams/             # greenhouse-teams provider reconciler
│   │   └── aggregator/                  # status aggregator
│   ├── features/                        # feature flag package
│   ├── test/                            # shared envtest harness
│   └── ...                              # ccrn, clientfactory, etc.
├── charts/
│   └── permission-manager/              # existing Helm chart (CRDs generated here)
├── hack/
│   ├── boilerplate.go.txt
│   └── ...
├── e2e/                                 # existing e2e tests (kind-based, unchanged)
└── docs/
```

### Migration Steps

1. **Add `PROJECT` file** — declare domain (`cloudoperators.dev`), layout (`go.kubebuilder.io/v4`), resources
2. **Add kubebuilder markers to types** — `+kubebuilder:object:root=true`, `+kubebuilder:subresource:status`, `+kubebuilder:printcolumn:`, field validation markers
3. **Generate CRDs from markers** — replace hand-maintained CRD YAML with `controller-gen crd` output landing in `charts/`
4. **Add RBAC markers to reconcilers** — `+kubebuilder:rbac:` annotations; generate RBAC manifests into chart
5. **Restructure to `internal/controller/<kind>/`** — one sub-package per provider/controller
6. **Adopt `SetupWithManager(name, mgr)` signature** — align with greenhouse's controller registration pattern
7. **Add `knownControllers` map** — controller registration in `cmd/` matching greenhouse pattern
8. **Add envtest harness** — `internal/test/` with shared setup for unit-level controller tests
9. **Align Makefile** — `generate`, `manifests`, `test`, `build-*` targets matching greenhouse conventions
10. **Move e2e tests** — from `controller/test/e2e/` to top-level `e2e/` (greenhouse convention)

### What Stays the Same

- **Helm chart** remains the production deployment mechanism (greenhouse uses Helm too, not raw kustomize)
- **E2E tests** against kind clusters remain — they test what envtest cannot (multi-cluster, real RBAC)
- **Multi-binary architecture** (ADR-001) is preserved — separate binaries per provider
- **API group and version** (`permission-manager.cloudoperators.dev/v1`) unchanged
- **All existing functionality** — this is a structural refactor, not a behavioral change

## Consequences

### Positive

- **Consistent with greenhouse**: developers move between repos without learning a new layout
- **Generated CRDs**: types are the single source of truth; CRD YAML is always in sync
- **Generated RBAC**: no risk of forgetting to update RBAC when adding a new resource watch
- **Envtest coverage**: fast unit tests for reconciler logic (seconds, not minutes)
- **Kubebuilder tooling**: `kubebuilder create api`, `kubebuilder create webhook` work for future additions
- **Code review velocity**: greenhouse reviewers know exactly where to look

### Negative

- **Large structural PR**: significant file moves, import path changes — high churn, low functional change
- **Parallel test maintenance**: envtest tests complement but don't replace e2e; two test suites to maintain
- **Learning curve**: contributors unfamiliar with kubebuilder must understand the conventions (mitigated by greenhouse team already knowing them)
- **Dead scaffolding risk**: kubebuilder generates `config/` by default; we must actively keep it minimal (only `config/samples/` like greenhouse does)

### Risks and Mitigations

| Risk | Mitigation |
|------|-----------|
| Large PR is hard to review | Split into sequential PRs: (1) markers + codegen, (2) restructure, (3) envtest |
| Import paths break downstream | No known downstream consumers; internal module paths only |
| E2E tests break during move | E2E tests are self-contained; update `go test` path in Makefile/CI |
| Makefile complexity increases | Copy greenhouse's Makefile patterns verbatim — proven, maintained |

## References

- [cloudoperators/greenhouse](https://github.com/cloudoperators/greenhouse) — reference implementation
- [Kubebuilder Book](https://book.kubebuilder.io/) — v4 layout documentation
- [controller-gen markers](https://book.kubebuilder.io/reference/markers) — marker reference
