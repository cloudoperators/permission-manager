// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	pmv1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// patchGithubTeamStatus patches the status.teamStatus field on a GithubTeam CR
// using a merge-patch on the status subresource. This allows tests to simulate
// the external operator setting a status without actually running the operator.
func patchGithubTeamStatus(t *testing.T, namespace, name, teamStatus string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	statusPatch := fmt.Sprintf(`{"status":{"teamStatus":%q}}`, teamStatus)
	runCmd(t, "kubectl", "--context", mgmtCtx,
		"-n", namespace,
		"patch", "githubteam.repo-guard.cloudoperators.dev", name,
		"--type=merge", "--subresource=status",
		"-p", statusPatch)
}

// patchTeamRoleBindingStatus patches the status.statusConditions on a TeamRoleBinding CR
// to simulate the Greenhouse operator marking it as Ready.
func patchTeamRoleBindingStatus(t *testing.T, namespace, name string, ready bool) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	status := "True"
	if !ready {
		status = "False"
	}
	statusPatch := fmt.Sprintf(`{"status":{"statusConditions":{"conditions":[{"type":"Ready","status":%q}]}}}`, status)
	runCmd(t, "kubectl", "--context", mgmtCtx,
		"-n", namespace,
		"patch", "teamrolebinding.greenhouse.sap", name,
		"--type=merge", "--subresource=status",
		"-p", statusPatch)
}

// expectProviderTargetStatus asserts that the named ASR's providers map has
// the specified provider with a target matching targetID and expectedStatus.
// This is a generic helper for any provider (github, greenhouse-teams, kubernetes).
func expectProviderTargetStatus(t *testing.T, ctx context.Context, c client.Client, namespace, asrName, provider, targetID, expectedStatus string) {
	t.Helper()

	err := pollUntil(t, ctx, providerAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: namespace, Name: asrName}
		if err := c.Get(ctx, key, &asr); err != nil {
			return false, fmt.Errorf("failed to get ASR %s/%s: %w", namespace, asrName, err)
		}

		ps, ok := asr.Status.Providers[pmmeta.ProviderType(provider)]
		if !ok {
			return false, fmt.Errorf("ASR %s has no provider %q in status (providers: %v)", asrName, provider, mapKeys(asr.Status.Providers))
		}

		for _, entry := range ps.Targets {
			if entry.TargetID == targetID {
				if string(entry.Status) == expectedStatus {
					return true, nil
				}
				return false, fmt.Errorf("ASR %s provider %q target %q status=%q, want %q (message: %s)",
					asrName, provider, targetID, entry.Status, expectedStatus, entry.Message)
			}
		}
		return false, fmt.Errorf("ASR %s provider %q has no target %q (targets: %d)",
			asrName, provider, targetID, len(ps.Targets))
	})
	if err != nil {
		t.Fatalf("expectProviderTargetStatus(%s/%s, provider=%q, target=%q, expected=%q): %v",
			namespace, asrName, provider, targetID, expectedStatus, err)
	}
}

// mapKeys returns the keys of a map as a slice of strings (for diagnostics).
func mapKeys(m map[pmmeta.ProviderType]pmmeta.ProviderStatus) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, string(k))
	}
	return keys
}

// createGithubConnectorBinding creates the test-github ConnectorBinding with the
// standard E2E capability mapping (read->pull, write->push, admin->admin).
func createGithubConnectorBinding(t *testing.T) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	definition := map[string]interface{}{
		"capabilityMapping": map[string]string{
			"read":  "pull",
			"write": "push",
			"admin": "admin",
		},
		"defaultTargetNamespace": controllerNamespace,
	}
	defJSON, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("failed to marshal github definition: %v", err)
	}

	cbYAML := fmt.Sprintf(`apiVersion: permission-manager.cloudoperators.dev/v1
kind: ConnectorBinding
metadata:
  name: test-github
  namespace: %s
spec:
  name: test-github
  provider: github
  definition: %s`, controllerNamespace, string(defJSON))

	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(cbYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create GitHub ConnectorBinding: %v\n%s", err, out.String())
	}
	t.Log("Created ConnectorBinding test-github")
}

// createGreenhouseTeamsConnectorBinding creates the test-greenhouse-teams ConnectorBinding
// with the standard E2E capability mapping (read->cluster-viewer, write->cluster-developer, admin->cluster-admin).
func createGreenhouseTeamsConnectorBinding(t *testing.T) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	definition := map[string]interface{}{
		"capabilityMapping": map[string]string{
			"read":  "cluster-viewer",
			"write": "cluster-developer",
			"admin": "cluster-admin",
		},
		"defaultTargetNamespace": controllerNamespace,
	}
	defJSON, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("failed to marshal greenhouse-teams definition: %v", err)
	}

	cbYAML := fmt.Sprintf(`apiVersion: permission-manager.cloudoperators.dev/v1
kind: ConnectorBinding
metadata:
  name: test-greenhouse-teams
  namespace: %s
spec:
  name: test-greenhouse-teams
  provider: greenhouse-teams
  definition: %s`, controllerNamespace, string(defJSON))

	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(cbYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create greenhouse-teams ConnectorBinding: %v\n%s", err, out.String())
	}
	t.Log("Created ConnectorBinding test-greenhouse-teams")
}

// createGithubOrganization creates a pre-seeded GithubOrganization CR on the mgmt cluster.
func createGithubOrganization(t *testing.T, namespace, name string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	orgYAML := fmt.Sprintf(`apiVersion: repo-guard.cloudoperators.dev/v1
kind: GithubOrganization
metadata:
  name: %s
  namespace: %s
spec:
  organizationOwnerTeams: []
  defaultPublicRepositoryTeams: []
  defaultPrivateRepositoryTeams: []`, name, namespace)

	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(orgYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create GithubOrganization %s/%s: %v\n%s", namespace, name, err, out.String())
	}
	t.Logf("Created GithubOrganization %s/%s", namespace, name)
}

// createTeamRole creates a pre-seeded TeamRole CR on the mgmt cluster.
// TeamRoles are referenced by TeamRoleBindings via the teamRoleRef field.
func createTeamRole(t *testing.T, namespace, name string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	teamRoleYAML := fmt.Sprintf(`apiVersion: greenhouse.sap/v1alpha1
kind: TeamRole
metadata:
  name: %s
  namespace: %s
spec: {}`, name, namespace)

	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(teamRoleYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create TeamRole %s/%s: %v\n%s", namespace, name, err, out.String())
	}
	t.Logf("Created TeamRole %s/%s", namespace, name)
}
