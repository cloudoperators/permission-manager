# Contributing to Permission Manager Controller

Thank you for your interest in contributing! This document provides guidelines for contributing to the Permission Manager Controller project.

## Getting Started

### Prerequisites

- Go 1.22+
- Docker (for container builds)
- [kind](https://kind.sigs.k8s.io/) (for E2E tests)
- [Helm](https://helm.sh/) (for chart rendering)
- kubectl

### Development Setup

```bash
# Clone the repository
git clone https://github.com/cloudoperators/permission-manager.git
cd permission-manager

# Build
cd controller && make build

# Run tests
cd controller && make test

# Run E2E tests (creates kind clusters)
cd controller && make e2e
```

## How to Contribute

### Reporting Issues

- Use GitHub Issues for bug reports and feature requests
- Include reproduction steps, expected vs actual behavior, and environment details

### Pull Requests

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes
4. Ensure tests pass: `cd controller && make test`
5. Submit a pull request

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Write tests for new functionality
- Update documentation for user-facing changes
- Add ADRs for architectural decisions (see `docs/adr/`)

### Commit Messages

Use conventional commits:
- `feat:` — new feature
- `fix:` — bug fix
- `docs:` — documentation
- `refactor:` — code change that neither fixes a bug nor adds a feature
- `test:` — adding or updating tests
- `chore:` — maintenance

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the system design. Key areas:

- **`api/`** — CRD type definitions (shared module)
- **`controller/internal/controller/`** — Provider reconciler implementations
- **`controller/pkg/`** — Shared packages (CCRN parser, audit, metrics)

## Testing

- **Unit tests**: `go test ./pkg/... ./internal/...`
- **E2E tests**: `make e2e` (creates multi-cluster kind environment)
- New providers should include both unit and E2E test coverage

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
