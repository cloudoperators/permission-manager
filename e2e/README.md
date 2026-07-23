# E2E Tests

Smoke tests: apply an ApplicationSpecificRole, expect the downstream resources
to show up. One spec file per provider under `e2e/`:

- `k8s_test.go`:
  - a cluster CCRN creates a ClusterRole and ClusterRoleBinding on the remote
    cluster
  - a namespace CCRN creates a Role and RoleBinding
  - deleting the ASR removes the remote RBAC through the finalizer
  - narrowing a cluster CCRN to a namespace CCRN cleans up the orphaned
    cluster-scoped RBAC
- `github_test.go`:
  - a github connector creates the GithubTeam and GithubTeamRepository CRs in
    the org namespace

All files are behind the `e2e` build tag, so `go test ./...` skips them.

## Environment

The tests run against the shared dev environment from `hack/setup.mk`, the
same one used for local development: kind clusters `greenhouse-admin` (Flux,
greenhouse, repo-guard, permission-manager) and `greenhouse-remote`, onboarded
into the `demo` Organization through the real OIDC flow. The suite never
creates or deletes clusters. Its setup verifies the environment is up and both
providers are enabled, creates the `apps-prod` namespace on the remote
cluster, applies the ConnectorBinding and GithubOrganization fixtures, and
sweeps stale ASRs plus anything they left behind on either cluster. When the
suite fails, the controller pod logs are printed at the end of the report.

## Running

```sh
make setup-e2e    # clusters + demo onboarding + docker-build + kind-load + deploy
make e2e          # run the tests
make clean-e2e    # delete both kind clusters
```

`make e2e` derives the kube contexts from `ADMIN_CLUSTER` and `REMOTE_CLUSTER`,
runs `E2E_PROCS` (default 4) parallel processes and writes a JSON report to
`bin/e2e-report.json`.

When invoking ginkgo directly, contexts and names come from env vars, so the
same specs can run against any pair of clusters: `E2E_ADMIN_CONTEXT`,
`E2E_REMOTE_CONTEXT`, `E2E_ORG`, `E2E_REMOTE_CLUSTER`,
`E2E_CONTROLLER_NAMESPACE`, `E2E_CONTROLLER_DEPLOYMENT`,
`E2E_PROVIDER_CONFIGMAP`.

## Relation to the previous suite

The previous e2e suite is untouched and still lives in `test/e2e/` with its
own targets (`setup-test-e2e`, `test-e2e`, `cleanup-test-e2e`) and its own
kind cluster bootstrap. The two suites are independent; consolidating them is
a separate cleanup task.
