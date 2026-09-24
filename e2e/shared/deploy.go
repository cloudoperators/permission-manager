// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package shared

import (
	"fmt"
	"strings"
)

// externalCRDNames are the stub CRDs the suite installs to simulate Greenhouse
// and the repo-guard github provider. Waited on for Established before use.
var externalCRDNames = []string{
	"clusters.greenhouse.sap",
	"githubteams.repo-guard.cloudoperators.dev",
	"githubteamrepositories.repo-guard.cloudoperators.dev",
	"githuborganizations.repo-guard.cloudoperators.dev",
}

// InstallExternalCRDs applies the greenhouse + repo-guard CRD stubs from
// crdDir and waits until each is Established. The Permission Manager CRDs are
// installed by the Helm chart (--include-crds) in DeployController.
func InstallExternalCRDs(cluster, crdDir string) error {
	ctx := kindContext(cluster)
	for _, f := range []string{"greenhouse-cluster.yaml", "github-provider.yaml"} {
		if _, err := run("kubectl", "--context", ctx, "apply", "-f", crdDir+"/"+f); err != nil {
			return fmt.Errorf("apply %s: %w", f, err)
		}
	}
	for _, name := range externalCRDNames {
		if _, err := run("kubectl", "--context", ctx,
			"wait", "--for=condition=Established", "crd/"+name, "--timeout=60s"); err != nil {
			return fmt.Errorf("wait crd %s: %w", name, err)
		}
	}
	return nil
}

// DeployController templates the Helm chart and applies it to the cluster,
// installing the PM CRDs, RBAC, provider ConfigMap, and the controller
// Deployment with both providers enabled. It then waits for the rollout.
func DeployController(cluster, chartDir string) error {
	ctx := kindContext(cluster)

	// Namespace first (idempotent via apply of a rendered manifest).
	nsManifest, err := run("kubectl", "--context", ctx,
		"create", "namespace", ControllerNamespace, "--dry-run=client", "-o", "yaml")
	if err != nil {
		return fmt.Errorf("render namespace: %w", err)
	}
	if err := runInput(nsManifest, "kubectl", "--context", ctx, "apply", "-f", "-"); err != nil {
		return fmt.Errorf("apply namespace: %w", err)
	}

	manifests, err := run("helm", "template", "permission-manager", chartDir,
		"--namespace", ControllerNamespace,
		"--include-crds",
		"--set", "manager.image.repository="+ControllerImageRepo,
		"--set", "manager.image.tag="+ControllerImageTag,
		"--set", "manager.image.pullPolicy=Never",
		"--set", "providers.kubernetes=true",
		"--set", "providers.github=true",
	)
	if err != nil {
		return fmt.Errorf("helm template: %w", err)
	}
	if err := runInput(manifests, "kubectl", "--context", ctx,
		"apply", "--server-side", "--force-conflicts", "-f", "-"); err != nil {
		return fmt.Errorf("apply chart: %w", err)
	}

	return waitForControllerRollout(ctx)
}

// waitForControllerRollout discovers the controller Deployment by name (it is
// templated by the chart, not fixed) and waits for the rollout to complete.
func waitForControllerRollout(ctx string) error {
	out, err := run("kubectl", "--context", ctx, "-n", ControllerNamespace,
		"get", "deploy", "-o", "name")
	if err != nil {
		return fmt.Errorf("list deployments: %w", err)
	}
	deploy := strings.TrimSpace(out)
	if deploy == "" {
		return fmt.Errorf("no controller Deployment found in namespace %s", ControllerNamespace)
	}
	// A namespace should hold exactly the one manager Deployment; take the first.
	deploy = strings.Split(deploy, "\n")[0]
	if _, err := run("kubectl", "--context", ctx, "-n", ControllerNamespace,
		"rollout", "status", deploy, "--timeout=180s"); err != nil {
		return fmt.Errorf("rollout status %s: %w", deploy, err)
	}
	return nil
}
