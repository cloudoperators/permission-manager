# Permission Manager

[![REUSE status](https://api.reuse.software/badge/github.com/cloudoperators/permission-manager)](https://api.reuse.software/info/github.com/cloudoperators/permission-manager)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

## About this project

**Permission Manager is NOT an Identity Provider.** It is a **configuration framework** for aligning:

1. **Requestable permissions** in your Identity Provider (what users can ask for)
2. **Application-specific permissions** in downstream systems (what access actually gets provisioned)

It bridges the gap between "a user requests access to role X" and "system Y enforces the corresponding permissions" — expressed as code, versioned in Git, and reconciled by Kubernetes controllers.

### What It Does

Permission Manager uses Kubernetes CRDs to define access roles declaratively. Controllers watch these CRDs and provision the actual permissions on target systems:

- **Kubernetes RBAC** — ClusterRoles and RoleBindings on remote clusters
- **GitHub team permissions** — GithubTeam and GithubTeamRepository resources
- **Greenhouse team bindings** — Team and TeamRoleBinding resources

The framework separates **intent** ("team X needs read access to namespace Y") from **implementation** (creating the right ClusterRole, RoleBinding, and OIDC group mapping).

### How It Fits Together

```
┌─────────────────────────────────────────────────────────────────────┐
│                     Identity Provider (IDP)                          │
│         (Keycloak, Entra ID, Okta, SAP IAS, ...)                   │
│                                                                     │
│  Users request FunctionalRoles → IDP assigns OIDC group claims     │
└────────────────────────────────────┬────────────────────────────────┘
                                     │ OIDC groups
                                     ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    Permission Manager (this repo)                    │
│                                                                     │
│  FunctionalRole ──contains──▶ ApplicationSpecificRole(s)           │
│                                     │                               │
│                              permissions[] + connectorRefs[]         │
│                                     │                               │
│                    ┌────────────────┼────────────────┐              │
│                    ▼                ▼                ▼              │
│             ┌──────────┐    ┌──────────┐    ┌──────────────┐       │
│             │ k8s RBAC │    │  GitHub  │    │  Greenhouse  │       │
│             │controller│    │controller│    │  controller  │       │
│             └─────┬────┘    └─────┬────┘    └──────┬───────┘       │
└───────────────────┼───────────────┼────────────────┼───────────────┘
                    ▼               ▼                ▼
              ClusterRole      GithubTeam         Team +
              RoleBinding      GithubTeamRepo     TeamRoleBinding
```

**Key insight:** Permission Manager does not authenticate users or issue tokens. It configures what permissions exist on downstream systems, keyed by OIDC group names. The IDP handles authentication and group assignment; Permission Manager handles the downstream provisioning.

### Core Concepts

| CRD | Purpose |
|-----|---------|
| `ApplicationSpecificRole` (ASR) | Atomic access grant: capabilities on resources via connectors |
| `FunctionalRole` (FR) | Bundle of ASRs forming a requestable profile with approvers |
| `ConnectorBinding` | Connection config linking ASRs to provider implementations |
| `ServiceIdentity` | Technical user / service account template |

Resources are identified using [CCRN](https://github.com/cloudoperators/common-cloud-resource-names) URNs:

```
urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu-1
urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu-1/monitoring
urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.com/my-org/my-repo
```

Every ASR's `spec.name` IS the OIDC group name (case-sensitive). Controllers provision access for that group — any OIDC-compliant provider works.

### Architecture

The controller is split into **four independent binaries**:

| Binary | What it does |
|--------|-------------|
| `kubernetes` | Provisions ClusterRole/RoleBinding on remote clusters via Greenhouse kubeconfigs |
| `github` | Creates GithubTeam/GithubTeamRepository CRDs (consumed by [repo-guard](https://github.com/cloudoperators/repo-guard)) |
| `greenhouse-teams` | Creates Team/TeamRoleBinding CRDs |
| `aggregator` | Computes top-level Ready/Degraded conditions from per-provider status |

Capability mapping is per-connector — each downstream system defines its own matrix from abstract capabilities to provider-specific actions via `ConnectorBinding.spec.definition.capabilityMapping`. The Kubernetes connector also supports deny-driven rules (`excludeResources`) to grant "everything except" a set of resources without hand-enumerating API groups — see [Concepts](docs/concepts.md#deny-driven-rules-excluderesources).

### Roadmap

| Component | Description | Status |
|-----------|-------------|--------|
| **Greenhouse Extension** | Deploy Permission Manager as a Greenhouse Plugin with UI for browsing/requesting roles | Planned |
| **IDP Sync Operator** | Controller that syncs FunctionalRoles to an open-source IDP (e.g., Keycloak, Zitadel, Dex) as requestable roles with approval workflows | In discussion |
| **Vault Controller** | Provisions Vault policies and secret engine ACLs from ASR permissions | In discussion |
| **Additional tool connectors** | Extensible provider model — any system with an RBAC API can get a connector | Community-driven |

## Requirements and Setup

### Runtime Dependencies

- **[Greenhouse](https://github.com/cloudoperators/greenhouse)** cluster (required) — provides `Cluster` CRDs for inventory, kubeconfig Secrets for remote cluster access, and Plugin system for deployment
- **[repo-guard](https://github.com/cloudoperators/repo-guard)** (required for GitHub provider) — reconciles `GithubTeam`/`GithubTeamRepository` CRDs into actual GitHub organization permissions
- **OIDC provider** — must issue group claims to your clusters; the controller binds RBAC to these group names
- **IDP Sync Operator** (not yet included) — FunctionalRoles require an operator to sync them to your IDP as requestable roles. The controller itself only needs OIDC group claims to be present in tokens.

### Installation

```bash
# Install CRDs
kubectl apply -f crds/

# Deploy via Helm
helm install pm-controller chart/pm-controller/ \
  --namespace permission-manager \
  --create-namespace
```

### Development

```bash
# Build all binaries
cd controller && make build

# Run tests
cd controller && make test

# Run a specific provider locally
cd controller && go run ./cmd/kubernetes/ --leader-elect=false
```

### Repository Structure

```
├── api/                    Go module: CRD type definitions
├── controller/             Go module: controller implementation
│   ├── cmd/                Binary entry points (4 providers)
│   ├── internal/           Provider reconciler implementations
│   ├── pkg/                Shared packages (CCRN parser, audit, metrics)
│   └── test/e2e/           End-to-end test suite
├── chart/pm-controller/    Helm chart for deployment
├── crds/                   Generated CRD YAML manifests
├── ccrn-definitions/       CCRN resource type schemas (Helm templates)
├── docs/                   Documentation
└── examples/               Example YAML manifests
```

### Further Documentation

- [Architecture](docs/ARCHITECTURE.md) — full system design
- [Concepts](docs/concepts.md) — FunctionalRole, ASR, CCRN, ConnectorBinding explained
- [ADRs](docs/adr/) — Architecture Decision Records
- [Reconciler Flows](docs/reconciler-flows.md)
- [Threat Model](docs/threat-model.md)
- [E2E Test Catalog](docs/e2e-test-catalog.md)

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/cloudoperators/permission-manager/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](CONTRIBUTING.md).

## Security / Disclosure

If you find any bug that may be a security problem, please follow our instructions at in our [security policy](https://github.com/cloudoperators/permission-manager/security/policy) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/SAP/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2025 SAP SE or an SAP affiliate company and Permission Manager contributors. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available via the [REUSE tool](https://api.reuse.software/info/github.com/cloudoperators/permission-manager).
