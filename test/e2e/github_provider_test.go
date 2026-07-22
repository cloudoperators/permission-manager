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
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ---------------------------------------------------------------------------
// Local helpers for GitHub provider E2E tests
// ---------------------------------------------------------------------------

// createASRWithAdvancedConfig creates an ApplicationSpecificRole with an advancedConfig field.
func createASRWithAdvancedConfig(t *testing.T, name string, connectorRefs []string, permissions []map[string]string, advancedConfig string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	permsJSON := "["
	for i, p := range permissions {
		if i > 0 {
			permsJSON += ","
		}
		permsJSON += fmt.Sprintf(`{"resource":%q,"capability":%q}`, p["resource"], p["capability"])
	}
	permsJSON += "]"

	refsJSON := "["
	for i, r := range connectorRefs {
		if i > 0 {
			refsJSON += ","
		}
		refsJSON += fmt.Sprintf("%q", r)
	}
	refsJSON += "]"

	asrYAML := fmt.Sprintf(`apiVersion: permission-manager.cloudoperators.dev/v1
kind: ApplicationSpecificRole
metadata:
  name: %s
  namespace: %s
spec:
  name: %s
  connectorRefs: %s
  permissions: %s
  advancedConfig: %s`, name, controllerNamespace, name, refsJSON, permsJSON, advancedConfig)

	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(asrYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create ASR %s with advancedConfig: %v\n%s", name, err, out.String())
	}
	t.Logf("Created ASR %q with advancedConfig", name)
}

// patchASRPermissions patches the permissions field of an existing ASR using kubectl apply.
func patchASRPermissions(t *testing.T, name string, connectorRefs []string, permissions []map[string]string) {
	t.Helper()
	// Re-create with updated permissions (apply is idempotent)
	createASR(t, name, connectorRefs, permissions)
}

// ensureNamespace creates a namespace on the management cluster if it doesn't already exist.
func ensureNamespace(t *testing.T, ns string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx, "create", "namespace", ns)
}

// deleteNamespace deletes a namespace on the management cluster.
func deleteNamespace(t *testing.T, ns string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx, "delete", "namespace", ns, "--ignore-not-found")
}

// deleteConnectorBinding deletes a ConnectorBinding from the management cluster.
func deleteConnectorBinding(t *testing.T, name string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"delete", "connectorbinding", name, "--ignore-not-found", "--timeout=15s")
}

// assertGithubOrgHasOwnerTeam polls until the GithubOrganization has the specified team
// in its organizationOwnerTeams.
func assertGithubOrgHasOwnerTeam(t *testing.T, ctx context.Context, c client.Client, namespace, orgName, teamName string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubOrganization",
		})
		key := types.NamespacedName{Namespace: namespace, Name: orgName}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, fmt.Errorf("failed to get GithubOrganization %s/%s: %v", namespace, orgName, err)
		}

		teams, found, err := unstructured.NestedStringSlice(obj.Object, "spec", "organizationOwnerTeams")
		if err != nil || !found {
			return false, fmt.Errorf("GithubOrganization %s/%s missing spec.organizationOwnerTeams", namespace, orgName)
		}
		for _, t := range teams {
			if t == teamName {
				return true, nil
			}
		}
		return false, fmt.Errorf("GithubOrganization %s/%s ownerTeams=%v, missing %q", namespace, orgName, teams, teamName)
	})
	if err != nil {
		t.Fatalf("assertGithubOrgHasOwnerTeam(%s/%s, %q): %v", namespace, orgName, teamName, err)
	}
}

// assertGithubOrgLacksOwnerTeam verifies the team is NOT in organizationOwnerTeams.
func assertGithubOrgLacksOwnerTeam(t *testing.T, ctx context.Context, c client.Client, namespace, orgName, teamName string) {
	t.Helper()

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "repo-guard.cloudoperators.dev",
		Version: "v1",
		Kind:    "GithubOrganization",
	})
	key := types.NamespacedName{Namespace: namespace, Name: orgName}
	if err := c.Get(ctx, key, obj); err != nil {
		t.Fatalf("failed to get GithubOrganization %s/%s: %v", namespace, orgName, err)
	}

	teams, _, _ := unstructured.NestedStringSlice(obj.Object, "spec", "organizationOwnerTeams")
	for _, team := range teams {
		if team == teamName {
			t.Fatalf("GithubOrganization %s/%s should NOT have owner team %q but does (teams=%v)", namespace, orgName, teamName, teams)
		}
	}
}

// assertGithubOrgHasPublicTeam polls until GithubOrganization has the team in defaultPublicRepositoryTeams.
func assertGithubOrgHasPublicTeam(t *testing.T, ctx context.Context, c client.Client, namespace, orgName, teamName, permission string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubOrganization",
		})
		key := types.NamespacedName{Namespace: namespace, Name: orgName}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, fmt.Errorf("failed to get GithubOrganization: %v", err)
		}

		teams, found, err := unstructured.NestedSlice(obj.Object, "spec", "defaultPublicRepositoryTeams")
		if err != nil || !found {
			return false, fmt.Errorf("missing spec.defaultPublicRepositoryTeams")
		}
		for _, entry := range teams {
			m, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			if m["name"] == teamName && m["permission"] == permission {
				return true, nil
			}
		}
		return false, fmt.Errorf("defaultPublicRepositoryTeams does not contain {name:%q, permission:%q}", teamName, permission)
	})
	if err != nil {
		t.Fatalf("assertGithubOrgHasPublicTeam(%s/%s, team=%q, perm=%q): %v", namespace, orgName, teamName, permission, err)
	}
}

// assertGithubOrgHasPrivateTeam polls until GithubOrganization has the team in defaultPrivateRepositoryTeams.
func assertGithubOrgHasPrivateTeam(t *testing.T, ctx context.Context, c client.Client, namespace, orgName, teamName, permission string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubOrganization",
		})
		key := types.NamespacedName{Namespace: namespace, Name: orgName}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, fmt.Errorf("failed to get GithubOrganization: %v", err)
		}

		teams, found, err := unstructured.NestedSlice(obj.Object, "spec", "defaultPrivateRepositoryTeams")
		if err != nil || !found {
			return false, fmt.Errorf("missing spec.defaultPrivateRepositoryTeams")
		}
		for _, entry := range teams {
			m, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			if m["name"] == teamName && m["permission"] == permission {
				return true, nil
			}
		}
		return false, fmt.Errorf("defaultPrivateRepositoryTeams does not contain {name:%q, permission:%q}", teamName, permission)
	})
	if err != nil {
		t.Fatalf("assertGithubOrgHasPrivateTeam(%s/%s, team=%q, perm=%q): %v", namespace, orgName, teamName, permission, err)
	}
}

// assertGithubOrgLacksPublicTeam verifies the team is NOT in defaultPublicRepositoryTeams.
func assertGithubOrgLacksPublicTeam(t *testing.T, ctx context.Context, c client.Client, namespace, orgName, teamName string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubOrganization",
		})
		key := types.NamespacedName{Namespace: namespace, Name: orgName}
		if err := c.Get(ctx, key, obj); err != nil {
			t.Fatalf("failed to get GithubOrganization %s/%s: %v", namespace, orgName, err)
		}

		found := false
		teams, _, _ := unstructured.NestedSlice(obj.Object, "spec", "defaultPublicRepositoryTeams")
		for _, entry := range teams {
			m, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			if m["name"] == teamName {
				found = true
				break
			}
		}
		if !found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("GithubOrganization %s/%s should NOT have public team %q but still does after 30s", namespace, orgName, teamName)
		}
		time.Sleep(2 * time.Second)
	}
}

// assertGithubOrgLacksPrivateTeam verifies the team is NOT in defaultPrivateRepositoryTeams.
func assertGithubOrgLacksPrivateTeam(t *testing.T, ctx context.Context, c client.Client, namespace, orgName, teamName string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubOrganization",
		})
		key := types.NamespacedName{Namespace: namespace, Name: orgName}
		if err := c.Get(ctx, key, obj); err != nil {
			t.Fatalf("failed to get GithubOrganization %s/%s: %v", namespace, orgName, err)
		}

		found := false
		teams, _, _ := unstructured.NestedSlice(obj.Object, "spec", "defaultPrivateRepositoryTeams")
		for _, entry := range teams {
			m, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			if m["name"] == teamName {
				found = true
				break
			}
		}
		if !found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("GithubOrganization %s/%s should NOT have private team %q but still does after 30s", namespace, orgName, teamName)
		}
		time.Sleep(2 * time.Second)
	}
}

// assertGithubTeamRepoRepoCount polls until the GithubTeamRepository has exactly the expected
// number of repositories in its spec.repositories list.
func assertGithubTeamRepoRepoCount(t *testing.T, ctx context.Context, c client.Client, namespace, name string, expectedCount int) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubTeamRepository",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}

		repos, found, err := unstructured.NestedStringSlice(obj.Object, "spec", "repositories")
		if err != nil || !found {
			return false, fmt.Errorf("missing spec.repositories")
		}
		if len(repos) == expectedCount {
			return true, nil
		}
		return false, fmt.Errorf("GithubTeamRepository %s/%s has %d repos, want %d (repos=%v)", namespace, name, len(repos), expectedCount, repos)
	})
	if err != nil {
		t.Fatalf("assertGithubTeamRepoRepoCount(%s/%s, count=%d): %v", namespace, name, expectedCount, err)
	}
}

// deleteGithubOrganization deletes a GithubOrganization CR.
func deleteGithubOrganization(t *testing.T, namespace, name string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", namespace,
		"delete", "githuborganization.repo-guard.cloudoperators.dev", name, "--ignore-not-found")
}

// createConnectorBindingWithDef creates a ConnectorBinding with a given definition JSON.
func createConnectorBindingWithDef(t *testing.T, name, provider string, definition map[string]interface{}) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	defJSON, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("failed to marshal definition: %v", err)
	}

	cbYAML := fmt.Sprintf(`apiVersion: permission-manager.cloudoperators.dev/v1
kind: ConnectorBinding
metadata:
  name: %s
  namespace: %s
spec:
  name: %s
  provider: %s
  definition: %s`, name, controllerNamespace, name, provider, string(defJSON))

	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(cbYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create ConnectorBinding %s: %v\n%s", name, err, out.String())
	}
	t.Logf("Created ConnectorBinding %q (provider=%s)", name, provider)
}

// ---------------------------------------------------------------------------
// GitHub instance and org constants for tests
// ---------------------------------------------------------------------------

const (
	// testGithubInstance is the GitHub Enterprise instance used in tests.
	testGithubInstance = "github.example.com"
	// testGithubInstanceSanitized is the sanitized form of testGithubInstance.
	testGithubInstanceSanitized = "github-example-com"
	// testGithubOrg is the pre-seeded organization name.
	testGithubOrg = "e2e-org"
)

// ---------------------------------------------------------------------------
// Basic Operations (1-4)
// ---------------------------------------------------------------------------

// TestGithub_Basic_TeamAndRepoCreation verifies that creating an ASR with one
// repository CCRN produces both a GithubTeam and a GithubTeamRepository CR.
func TestGithub_Basic_TeamAndRepoCreation(t *testing.T) {
	t.Parallel()
	const asrName = "gh-basic-1"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/my-repo", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Expected CR names
	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)
	teamRepoCRName := fmt.Sprintf("asr-%s-%s-%s-pull", asrName, testGithubInstanceSanitized, testGithubOrg)

	assertGithubTeamExists(t, ctx, c, controllerNamespace, teamCRName)
	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, teamRepoCRName)
	assertGithubTeamRepoPermission(t, ctx, c, controllerNamespace, teamRepoCRName, "pull")
	assertGithubTeamRepoContains(t, ctx, c, controllerNamespace, teamRepoCRName, "my-repo")
}

// TestGithub_Basic_TwoReposSameCapabilityAggregated verifies that two repositories in
// the same org with the same capability are aggregated into one GithubTeamRepository.
func TestGithub_Basic_TwoReposSameCapabilityAggregated(t *testing.T) {
	t.Parallel()
	const asrName = "gh-basic-2"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource1 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-a", testGithubInstance, testGithubOrg)
	resource2 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-b", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource1, "capability": "write"},
		{"resource": resource2, "capability": "write"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamRepoCRName := fmt.Sprintf("asr-%s-%s-%s-push", asrName, testGithubInstanceSanitized, testGithubOrg)

	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, teamRepoCRName)
	assertGithubTeamRepoContains(t, ctx, c, controllerNamespace, teamRepoCRName, "repo-a")
	assertGithubTeamRepoContains(t, ctx, c, controllerNamespace, teamRepoCRName, "repo-b")
	assertGithubTeamRepoRepoCount(t, ctx, c, controllerNamespace, teamRepoCRName, 2)
}

// TestGithub_Basic_TwoReposDifferentCapabilities verifies that two repos with different
// capabilities produce two separate GithubTeamRepository CRs.
func TestGithub_Basic_TwoReposDifferentCapabilities(t *testing.T) {
	t.Parallel()
	const asrName = "gh-basic-3"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource1 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-x", testGithubInstance, testGithubOrg)
	resource2 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-y", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource1, "capability": "read"},
		{"resource": resource2, "capability": "write"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	pullCRName := fmt.Sprintf("asr-%s-%s-%s-pull", asrName, testGithubInstanceSanitized, testGithubOrg)
	pushCRName := fmt.Sprintf("asr-%s-%s-%s-push", asrName, testGithubInstanceSanitized, testGithubOrg)

	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, pullCRName)
	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, pushCRName)
	assertGithubTeamRepoPermission(t, ctx, c, controllerNamespace, pullCRName, "pull")
	assertGithubTeamRepoPermission(t, ctx, c, controllerNamespace, pushCRName, "push")
}

// TestGithub_Basic_TwoOrgs verifies that permissions in two different organizations
// produce two separate GithubTeam CRs.
func TestGithub_Basic_TwoOrgs(t *testing.T) {
	t.Parallel()
	const asrName = "gh-basic-4"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	// Create a second org
	createGithubOrganization(t, controllerNamespace, "e2e-org-2")
	t.Cleanup(func() { deleteGithubOrganization(t, controllerNamespace, "e2e-org-2") })

	resource1 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-1", testGithubInstance, testGithubOrg)
	resource2 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/e2e-org-2/repo-2", testGithubInstance)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource1, "capability": "read"},
		{"resource": resource2, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	team1CRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)
	team2CRName := fmt.Sprintf("asr-%s-%s-e2e-org-2", asrName, testGithubInstanceSanitized)

	assertGithubTeamExists(t, ctx, c, controllerNamespace, team1CRName)
	assertGithubTeamExists(t, ctx, c, controllerNamespace, team2CRName)
}

// ---------------------------------------------------------------------------
// Wildcard & Organization (5-9)
// ---------------------------------------------------------------------------

// TestGithub_Wildcard_PublicRepoType verifies that a repository_type CCRN with
// visibility=public patches the GithubOrg defaultPublicRepositoryTeams.
func TestGithub_Wildcard_PublicRepoType(t *testing.T) {
	t.Parallel()
	const asrName = "gh-wildcard-pub"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository_type.github.ccrn.cloudoperators.dev/v1/%s/%s/public", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	assertGithubOrgHasPublicTeam(t, ctx, c, controllerNamespace, testGithubOrg, asrName, "pull")
}

// TestGithub_Wildcard_PrivateRepoType verifies that a repository_type CCRN with
// visibility=private patches the GithubOrg defaultPrivateRepositoryTeams.
func TestGithub_Wildcard_PrivateRepoType(t *testing.T) {
	t.Parallel()
	const asrName = "gh-wildcard-priv"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository_type.github.ccrn.cloudoperators.dev/v1/%s/%s/private", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "write"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	assertGithubOrgHasPrivateTeam(t, ctx, c, controllerNamespace, testGithubOrg, asrName, "push")
}

// TestGithub_Wildcard_AllVisibility verifies that a repository_type CCRN with
// visibility=* patches both public and private repository teams.
func TestGithub_Wildcard_AllVisibility(t *testing.T) {
	t.Parallel()
	const asrName = "gh-wildcard-all"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository_type.github.ccrn.cloudoperators.dev/v1/%s/%s/*", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "admin"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	assertGithubOrgHasPublicTeam(t, ctx, c, controllerNamespace, testGithubOrg, asrName, "admin")
	assertGithubOrgHasPrivateTeam(t, ctx, c, controllerNamespace, testGithubOrg, asrName, "admin")
}

// TestGithub_Org_AdminCapability verifies that an organization CCRN with admin
// capability adds the team to organizationOwnerTeams.
func TestGithub_Org_AdminCapability(t *testing.T) {
	t.Parallel()
	const asrName = "gh-org-admin"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("organization.github.ccrn.cloudoperators.dev/v1/%s/%s", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "admin"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	assertGithubOrgHasOwnerTeam(t, ctx, c, controllerNamespace, testGithubOrg, asrName)
}

// TestGithub_Org_NonAdminCapability verifies that an organization CCRN with a
// non-admin capability does NOT add the team to organizationOwnerTeams.
func TestGithub_Org_NonAdminCapability(t *testing.T) {
	t.Parallel()
	const asrName = "gh-org-nonadmin"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("organization.github.ccrn.cloudoperators.dev/v1/%s/%s", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait long enough for the controller to process the ASR
	time.Sleep(15 * time.Second)

	// Verify the team was NOT added to owner teams
	assertGithubOrgLacksOwnerTeam(t, ctx, c, controllerNamespace, testGithubOrg, asrName)
}

// ---------------------------------------------------------------------------
// Updates (10-12)
// ---------------------------------------------------------------------------

// TestGithub_Update_AddRepoPermission verifies that adding a second repo
// permission to an existing ASR grows the GithubTeamRepository repos array.
func TestGithub_Update_AddRepoPermission(t *testing.T) {
	t.Parallel()
	const asrName = "gh-update-add"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource1 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-first", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource1, "capability": "write"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamRepoCRName := fmt.Sprintf("asr-%s-%s-%s-push", asrName, testGithubInstanceSanitized, testGithubOrg)
	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, teamRepoCRName)
	assertGithubTeamRepoContains(t, ctx, c, controllerNamespace, teamRepoCRName, "repo-first")

	// Add a second repo with the same capability
	resource2 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-second", testGithubInstance, testGithubOrg)
	patchASRPermissions(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource1, "capability": "write"},
		{"resource": resource2, "capability": "write"},
	})

	assertGithubTeamRepoContains(t, ctx, c, controllerNamespace, teamRepoCRName, "repo-second")
	assertGithubTeamRepoRepoCount(t, ctx, c, controllerNamespace, teamRepoCRName, 2)
}

// TestGithub_Update_RemoveRepoPermission verifies that removing a repo
// permission shrinks the repos array or deletes the GithubTeamRepository.
func TestGithub_Update_RemoveRepoPermission(t *testing.T) {
	t.Parallel()
	const asrName = "gh-update-rm"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource1 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-keep", testGithubInstance, testGithubOrg)
	resource2 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-remove", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource1, "capability": "write"},
		{"resource": resource2, "capability": "write"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamRepoCRName := fmt.Sprintf("asr-%s-%s-%s-push", asrName, testGithubInstanceSanitized, testGithubOrg)
	assertGithubTeamRepoRepoCount(t, ctx, c, controllerNamespace, teamRepoCRName, 2)

	// Remove the second repo
	patchASRPermissions(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource1, "capability": "write"},
	})

	assertGithubTeamRepoRepoCount(t, ctx, c, controllerNamespace, teamRepoCRName, 1)
	assertGithubTeamRepoContains(t, ctx, c, controllerNamespace, teamRepoCRName, "repo-keep")
}

// TestGithub_Update_ChangeCapability verifies that changing a permission's
// capability creates a new GithubTeamRepository and removes/updates the old one.
func TestGithub_Update_ChangeCapability(t *testing.T) {
	t.Parallel()
	const asrName = "gh-update-cap"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-cap", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	pullCRName := fmt.Sprintf("asr-%s-%s-%s-pull", asrName, testGithubInstanceSanitized, testGithubOrg)
	pushCRName := fmt.Sprintf("asr-%s-%s-%s-push", asrName, testGithubInstanceSanitized, testGithubOrg)

	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, pullCRName)

	// Change from read to write
	patchASRPermissions(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "write"},
	})

	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, pushCRName)
	assertGithubTeamRepoAbsent(t, ctx, c, controllerNamespace, pullCRName)
}

// ---------------------------------------------------------------------------
// Deletion (13-14)
// ---------------------------------------------------------------------------

// TestGithub_Deletion_ASRDeleteCleansUpCRs verifies that deleting an ASR
// removes all associated GithubTeam and GithubTeamRepository CRs.
func TestGithub_Deletion_ASRDeleteCleansUpCRs(t *testing.T) {
	t.Parallel()
	const asrName = "gh-delete-crs"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-del", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "write"},
	})

	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)
	teamRepoCRName := fmt.Sprintf("asr-%s-%s-%s-push", asrName, testGithubInstanceSanitized, testGithubOrg)

	// Wait for CRs to appear
	assertGithubTeamExists(t, ctx, c, controllerNamespace, teamCRName)
	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, teamRepoCRName)

	// Delete the ASR gracefully (let controller run finalizer cleanup)
	deleteASRGraceful(t, asrName)

	// Verify cleanup
	assertGithubTeamAbsent(t, ctx, c, controllerNamespace, teamCRName)
	assertGithubTeamRepoAbsent(t, ctx, c, controllerNamespace, teamRepoCRName)
}

// TestGithub_Deletion_ASRDeleteCleansUpOrgPatching verifies that deleting an ASR
// with org-level patching cleans up the GithubOrganization entries.
func TestGithub_Deletion_ASRDeleteCleansUpOrgPatching(t *testing.T) {
	t.Parallel()
	const asrName = "gh-delete-org"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository_type.github.ccrn.cloudoperators.dev/v1/%s/%s/public", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})

	// Wait for org patching to take effect
	assertGithubOrgHasPublicTeam(t, ctx, c, controllerNamespace, testGithubOrg, asrName, "pull")

	// Delete the ASR gracefully (let controller run finalizer cleanup)
	deleteASRGraceful(t, asrName)

	// Wait for the controller to process deletion and clean up org entries
	assertGithubOrgLacksPublicTeam(t, ctx, c, controllerNamespace, testGithubOrg, asrName)
}

// ---------------------------------------------------------------------------
// ConnectorBinding Lifecycle (15-17)
// ---------------------------------------------------------------------------

// TestGithub_CB_NonGithubCBOnly verifies that an ASR with only a non-github
// ConnectorBinding does not produce any GitHub CRs.
func TestGithub_CB_NonGithubCBOnly(t *testing.T) {
	t.Parallel()
	const asrName = "gh-cb-nongithub"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	// ASR only references the kubernetes CB
	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-noop", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)

	// Wait long enough for controller to have acted
	time.Sleep(15 * time.Second)
	assertGithubTeamAbsent(t, ctx, c, controllerNamespace, teamCRName)
}

// TestGithub_CB_AddGithubCBLater verifies that when a GitHub CB is added to an
// ASR that previously had no GitHub CB, the CRs appear.
func TestGithub_CB_AddGithubCBLater(t *testing.T) {
	t.Parallel()
	const asrName = "gh-cb-addlater"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-later", testGithubInstance, testGithubOrg)

	// Create ASR with only the k8s connector first
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)

	// Clean up any stale GithubTeam from a prior run (deleteASR bypasses controller cleanup).
	mgmtCtx := "kind-" + mgmtClusterName
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"delete", "githubteam", teamCRName, "--ignore-not-found")

	// Verify no GitHub CRs created initially (ASR only references e2e-k8s)
	time.Sleep(10 * time.Second)
	assertGithubTeamAbsent(t, ctx, c, controllerNamespace, teamCRName)

	// Update ASR to also reference the github CB
	patchASRPermissions(t, asrName, []string{"e2e-k8s", "test-github"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})

	// Now GitHub CRs should appear
	assertGithubTeamExists(t, ctx, c, controllerNamespace, teamCRName)
}

// TestGithub_CB_RemoveGithubCB verifies that removing the GitHub CB from an ASR
// causes cleanup of GitHub CRs.
func TestGithub_CB_RemoveGithubCB(t *testing.T) {
	t.Parallel()
	const asrName = "gh-cb-remove"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-remove-cb", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github", "e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)

	// Wait for CRs to appear
	assertGithubTeamExists(t, ctx, c, controllerNamespace, teamCRName)

	// Remove the github connector ref (keep only k8s)
	patchASRPermissions(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})

	// CRs should be cleaned up
	assertGithubTeamAbsent(t, ctx, c, controllerNamespace, teamCRName)
}

// ---------------------------------------------------------------------------
// Error Handling & Status (18-22)
// ---------------------------------------------------------------------------

// TestGithub_Status_MissingOrgThenCreate verifies that an ASR targeting an org
// without a GithubOrganization CR enters Pending, then transitions to Success
// once the GithubOrganization is created.
func TestGithub_Status_MissingOrgThenCreate(t *testing.T) {
	t.Parallel()
	const asrName = "gh-status-org"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	// Use a non-existent org name
	orgName := "e2e-org-missing"
	resource := fmt.Sprintf("repository_type.github.ccrn.cloudoperators.dev/v1/%s/%s/public", testGithubInstance, orgName)

	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() {
		deleteASR(t, asrName)
		deleteGithubOrganization(t, controllerNamespace, orgName)
	})

	// Expect Pending since org doesn't exist (give it time to reconcile)
	time.Sleep(10 * time.Second)

	// Now create the org
	createGithubOrganization(t, controllerNamespace, orgName)

	// After org is created, the controller should requeue and succeed
	assertGithubOrgHasPublicTeam(t, ctx, c, controllerNamespace, orgName, asrName, "pull")
}

// TestGithub_Status_TeamComplete verifies that a GithubTeam with status "complete"
// maps to provider status Success.
func TestGithub_Status_TeamComplete(t *testing.T) {
	t.Parallel()
	const asrName = "gh-status-ok"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-status-ok", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)
	assertGithubTeamExists(t, ctx, c, controllerNamespace, teamCRName)

	// Patch the GithubTeam status to "complete"
	patchGithubTeamStatus(t, controllerNamespace, teamCRName, "complete")

	// Verify provider status shows Success
	targetID := testGithubInstanceSanitized + "/" + testGithubOrg
	expectProviderTargetStatus(t, ctx, c, controllerNamespace, asrName, "github", targetID, "Success")
}

// TestGithub_Status_TeamFailed verifies that a GithubTeam with status "failed"
// maps to provider status Error.
func TestGithub_Status_TeamFailed(t *testing.T) {
	t.Parallel()
	const asrName = "gh-status-fail"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-status-fail", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "write"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)
	assertGithubTeamExists(t, ctx, c, controllerNamespace, teamCRName)

	// Patch the GithubTeam status to "failed"
	patchGithubTeamStatus(t, controllerNamespace, teamCRName, "failed")

	// Verify provider status shows Error
	targetID := testGithubInstanceSanitized + "/" + testGithubOrg
	expectProviderTargetStatus(t, ctx, c, controllerNamespace, asrName, "github", targetID, "Error")
}

// TestGithub_Status_TeamRatelimited verifies that a GithubTeam with status "ratelimited"
// maps to provider status Pending.
func TestGithub_Status_TeamRatelimited(t *testing.T) {
	t.Parallel()
	const asrName = "gh-status-rl"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-status-rl", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "admin"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)
	assertGithubTeamExists(t, ctx, c, controllerNamespace, teamCRName)

	// Patch the GithubTeam status to "ratelimited"
	patchGithubTeamStatus(t, controllerNamespace, teamCRName, "ratelimited")

	// Verify provider status shows Pending
	targetID := testGithubInstanceSanitized + "/" + testGithubOrg
	expectProviderTargetStatus(t, ctx, c, controllerNamespace, asrName, "github", targetID, "Pending")
}

// TestGithub_Status_UnmappedCapability verifies that using a capability that
// doesn't exist in the ConnectorBinding's capabilityMapping results in provider not-ready.
func TestGithub_Status_UnmappedCapability(t *testing.T) {
	t.Parallel()
	const asrName = "gh-status-unmap"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	// "delete" capability doesn't exist in the test-github capabilityMapping
	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-unmap", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "delete"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// The unmapped capability means no GithubTeamRepository is created, but
	// there's also no valid desired state, so the provider has no targets.
	// Wait for reconciliation to complete
	time.Sleep(10 * time.Second)

	teamRepoCRName := fmt.Sprintf("asr-%s-%s-%s-", asrName, testGithubInstanceSanitized, testGithubOrg)
	// No team repo should exist with any permission suffix
	assertGithubTeamRepoAbsent(t, ctx, c, controllerNamespace, teamRepoCRName+"delete")

	// Since the capability is unmapped, no team will be created either (repo CCRN requires
	// a valid mapped capability to contribute to team creation via desired state)
	// The provider should report ready with "no targets" since there's nothing to do
	expectProviderReady(t, ctx, c, controllerNamespace, asrName, "github", false)
}

// ---------------------------------------------------------------------------
// Cross-Namespace (23-25)
// ---------------------------------------------------------------------------

// TestGithub_Namespace_AdvancedConfigOverride verifies that advancedConfig.github.targetNamespace
// overrides the default target namespace so CRs are created in the specified namespace.
func TestGithub_Namespace_AdvancedConfigOverride(t *testing.T) {
	t.Parallel()
	const asrName = "gh-ns-advanced"
	const targetNS = "gh-test-ns-advanced"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	ensureNamespace(t, targetNS)
	t.Cleanup(func() { deleteNamespace(t, targetNS) })

	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-ns", testGithubInstance, testGithubOrg)
	advancedConfig := `{"github":{"targetNamespace":"` + targetNS + `"}}`
	createASRWithAdvancedConfig(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	}, advancedConfig)
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)
	assertGithubTeamExists(t, ctx, c, targetNS, teamCRName)
}

// TestGithub_Namespace_CBDefault verifies that the ConnectorBinding's defaultTargetNamespace
// is used when no advancedConfig is specified.
func TestGithub_Namespace_CBDefault(t *testing.T) {
	t.Parallel()
	const asrName = "gh-ns-cbdef"
	const cbName = "test-github-ns"
	const targetNS = "gh-test-ns-cbdef"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	ensureNamespace(t, targetNS)
	t.Cleanup(func() { deleteNamespace(t, targetNS) })

	// Create a ConnectorBinding with a custom defaultTargetNamespace
	createConnectorBindingWithDef(t, cbName, "github", map[string]interface{}{
		"capabilityMapping": map[string]string{
			"read":  "pull",
			"write": "push",
			"admin": "admin",
		},
		"defaultTargetNamespace": targetNS,
	})
	t.Cleanup(func() { deleteConnectorBinding(t, cbName) })

	// Allow time for the CB indexer to pick it up
	time.Sleep(5 * time.Second)

	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-cbdef", testGithubInstance, testGithubOrg)
	createASR(t, asrName, []string{cbName}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)
	assertGithubTeamExists(t, ctx, c, targetNS, teamCRName)
}

// TestGithub_Namespace_AdvancedConfigOverridesCBDefault verifies that
// advancedConfig.github.targetNamespace takes priority over the CB's defaultTargetNamespace.
func TestGithub_Namespace_AdvancedConfigOverridesCBDefault(t *testing.T) {
	t.Parallel()
	const asrName = "gh-ns-override"
	const cbName = "test-github-nsov"
	const cbDefaultNS = "gh-test-ns-cbov"
	const advNS = "gh-test-ns-advov"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	ensureNamespace(t, cbDefaultNS)
	ensureNamespace(t, advNS)
	t.Cleanup(func() {
		deleteNamespace(t, cbDefaultNS)
		deleteNamespace(t, advNS)
	})

	// Create CB with one namespace
	createConnectorBindingWithDef(t, cbName, "github", map[string]interface{}{
		"capabilityMapping": map[string]string{
			"read":  "pull",
			"write": "push",
			"admin": "admin",
		},
		"defaultTargetNamespace": cbDefaultNS,
	})
	t.Cleanup(func() { deleteConnectorBinding(t, cbName) })

	// Allow time for the CB indexer to pick it up
	time.Sleep(5 * time.Second)

	// Create ASR with advancedConfig overriding the CB default
	resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-ovrd", testGithubInstance, testGithubOrg)
	advancedConfig := `{"github":{"targetNamespace":"` + advNS + `"}}`
	createASRWithAdvancedConfig(t, asrName, []string{cbName}, []map[string]string{
		{"resource": resource, "capability": "read"},
	}, advancedConfig)
	t.Cleanup(func() { deleteASR(t, asrName) })

	teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)

	// Should be in the advancedConfig namespace, NOT the CB default
	assertGithubTeamExists(t, ctx, c, advNS, teamCRName)

	// Verify NOT in CB default namespace
	time.Sleep(5 * time.Second)
	assertGithubTeamAbsent(t, ctx, c, cbDefaultNS, teamCRName)
}

// ---------------------------------------------------------------------------
// Multi-ASR (26)
// ---------------------------------------------------------------------------

// TestGithub_MultiASR_SameOrgSeparateCRs verifies that two ASRs targeting the
// same org produce separate GithubTeam CRs and org annotations track each one independently.
func TestGithub_MultiASR_SameOrgSeparateCRs(t *testing.T) {
	t.Parallel()
	const asrName1 = "gh-multi-1"
	const asrName2 = "gh-multi-2"
	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	resource1 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-m1", testGithubInstance, testGithubOrg)
	resource2 := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/repo-m2", testGithubInstance, testGithubOrg)

	createASR(t, asrName1, []string{"test-github"}, []map[string]string{
		{"resource": resource1, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName1) })

	createASR(t, asrName2, []string{"test-github"}, []map[string]string{
		{"resource": resource2, "capability": "write"},
	})
	t.Cleanup(func() { deleteASR(t, asrName2) })

	// Each ASR gets its own GithubTeam CR
	team1CRName := fmt.Sprintf("asr-%s-%s-%s", asrName1, testGithubInstanceSanitized, testGithubOrg)
	team2CRName := fmt.Sprintf("asr-%s-%s-%s", asrName2, testGithubInstanceSanitized, testGithubOrg)

	assertGithubTeamExists(t, ctx, c, controllerNamespace, team1CRName)
	assertGithubTeamExists(t, ctx, c, controllerNamespace, team2CRName)

	// Each ASR gets its own GithubTeamRepository
	teamRepo1CRName := fmt.Sprintf("asr-%s-%s-%s-pull", asrName1, testGithubInstanceSanitized, testGithubOrg)
	teamRepo2CRName := fmt.Sprintf("asr-%s-%s-%s-push", asrName2, testGithubInstanceSanitized, testGithubOrg)

	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, teamRepo1CRName)
	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, teamRepo2CRName)

	// Delete one ASR and verify only its CRs are removed
	deleteASRGraceful(t, asrName1)
	assertGithubTeamAbsent(t, ctx, c, controllerNamespace, team1CRName)
	assertGithubTeamRepoAbsent(t, ctx, c, controllerNamespace, teamRepo1CRName)

	// The other ASR's CRs should still exist
	assertGithubTeamExists(t, ctx, c, controllerNamespace, team2CRName)
	assertGithubTeamRepoExists(t, ctx, c, controllerNamespace, teamRepo2CRName)
}
