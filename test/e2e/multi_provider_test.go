// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// patchASRConnectorRefs patches an ASR's connectorRefs using kubectl.
func patchASRConnectorRefs(t *testing.T, asrName string, refs []string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	refsJSON := "["
	for i, r := range refs {
		if i > 0 {
			refsJSON += ","
		}
		refsJSON += fmt.Sprintf("%q", r)
	}
	refsJSON += "]"
	patch := fmt.Sprintf(`{"spec":{"connectorRefs":%s}}`, refsJSON)
	runCmd(t, "kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"patch", "applicationspecificrole", asrName,
		"--type=merge", "-p", patch)
}

// waitForASRDeleted polls until the named ASR no longer exists on the mgmt cluster.
func waitForASRDeleted(t *testing.T, ctx context.Context, c client.Client, namespace, name string, timeout time.Duration) {
	t.Helper()

	err := pollUntil(t, ctx, timeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, &asr); err != nil {
			return true, nil // NotFound = deleted
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("waitForASRDeleted(%s/%s): ASR still exists after %s: %v", namespace, name, timeout, err)
	}
}

// multiProviderPermissions returns the standard three-provider permission set used
// across multi-provider tests. It targets consumer-1 for K8s and greenhouse-teams,
// plus a GitHub repo CCRN.
func multiProviderPermissions() []map[string]string {
	return []map[string]string{
		{"resource": fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumerClusters[0].Name), "capability": "read"},
		{"resource": "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/e2e-org/test-repo", "capability": "read"},
	}
}

// multiProviderConnectorRefs returns the standard three-provider connector refs.
func multiProviderConnectorRefs() []string {
	return []string{"e2e-k8s", "test-github", "test-greenhouse-teams"}
}

// TestMultiProvider_ThreeProviderAllReportStatus verifies that an ASR referencing all
// three providers (kubernetes, github, greenhouse-teams) creates the expected CRs for
// each provider. Because both "e2e-k8s" and "test-greenhouse-teams" are referenced,
// the greenhouse-teams controller operates in team-only mode: Teams are created but
// TeamRoleBindings are suppressed (conflict with K8s RBAC). The greenhouse-teams
// provider reports ready=true automatically in team-only mode.
func TestMultiProvider_ThreeProviderAllReportStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "mp-three-prov"
	consumer1 := consumerClusters[0].Name
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	createASR(t, asrName, multiProviderConnectorRefs(), multiProviderPermissions())
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Verify: ClusterRole appears on consumer-1 (kubernetes provider)
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	// Verify: GithubTeam and GithubTeamRepository created (github provider)
	githubTeamName := githubTeamCRName("e2e-org", asrName)
	githubTeamRepoName := githubTeamRepoCRName("e2e-org", asrName, "pull")
	assertGithubTeamExists(t, ctx, mgmtClient, controllerNamespace, githubTeamName)
	assertGithubTeamRepoExists(t, ctx, mgmtClient, controllerNamespace, githubTeamRepoName)

	// Verify: Team created (greenhouse-teams provider in team-only mode)
	// TRBs are NOT created due to conflict with K8s RBAC
	teamName := "pm-" + asrName
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Mock external statuses: GithubTeam → "complete"
	// greenhouse-teams reports ready=true automatically in team-only mode (no TRB mock needed)
	patchGithubTeamStatus(t, controllerNamespace, githubTeamName, "complete")

	// Verify all three providers report ready/success
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "kubernetes", true)
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "github", true)
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "greenhouse-teams", true)

	t.Log("SUCCESS: Three-provider ASR — all providers report ready")
}

// TestMultiProvider_PartialFailure verifies that when one provider fails (github),
// the other providers (kubernetes, greenhouse-teams) are unaffected and continue to
// report success. The failing provider reports an error state.
// greenhouse-teams operates in team-only mode (no TRBs) due to conflict with K8s.
func TestMultiProvider_PartialFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "mp-partial-fail"
	consumer1 := consumerClusters[0].Name
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	createASR(t, asrName, multiProviderConnectorRefs(), multiProviderPermissions())
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for CRs to be created
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	githubTeamName := githubTeamCRName("e2e-org", asrName)
	teamName := "pm-" + asrName

	assertGithubTeamExists(t, ctx, mgmtClient, controllerNamespace, githubTeamName)
	// Team exists in team-only mode (no TRBs due to conflict)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Mock: GithubTeam status as "failed"
	// greenhouse-teams is ready automatically in team-only mode
	patchGithubTeamStatus(t, controllerNamespace, githubTeamName, "failed")

	// Verify: github provider reports error/not-ready
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "github", false)

	// Verify: greenhouse-teams and kubernetes are still ready
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "greenhouse-teams", true)
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "kubernetes", true)

	// Verify: K8s ClusterRole still exists (unaffected by github failure)
	if !clusterRoleExists(t, consumer1, "pm-"+asrName) {
		t.Fatal("ClusterRole should still exist on consumer-1 despite github provider failure")
	}

	t.Log("SUCCESS: Partial provider failure — github failed, others unaffected")
}

// TestMultiProvider_ProviderRecovery verifies that after a provider failure is fixed,
// the provider status transitions back to ready and the ASR becomes fully ready.
// greenhouse-teams operates in team-only mode (no TRBs) due to conflict with K8s.
func TestMultiProvider_ProviderRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "mp-recovery"
	consumer1 := consumerClusters[0].Name
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	createASR(t, asrName, multiProviderConnectorRefs(), multiProviderPermissions())
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for CRs to be created
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	githubTeamName := githubTeamCRName("e2e-org", asrName)
	teamName := "pm-" + asrName

	assertGithubTeamExists(t, ctx, mgmtClient, controllerNamespace, githubTeamName)
	// Team exists in team-only mode (no TRBs due to conflict)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Step 1: Simulate failure — GithubTeam status "failed"
	// greenhouse-teams is ready automatically in team-only mode
	patchGithubTeamStatus(t, controllerNamespace, githubTeamName, "failed")

	// Confirm github is not ready
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "github", false)

	// Step 2: Fix the failure — patch GithubTeam status to "complete"
	patchGithubTeamStatus(t, controllerNamespace, githubTeamName, "complete")

	// Verify all three providers now report ready
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "kubernetes", true)
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "github", true)
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "greenhouse-teams", true)

	// Verify ASR is ready overall
	expectASRReady(t, ctx, mgmtClient, controllerNamespace, asrName, true)

	t.Log("SUCCESS: Provider recovery — all providers now ready after fix")
}

// TestMultiProvider_DeletionCleansAllProviders verifies that deleting an ASR
// with multiple providers causes each provider to clean up its resources
// independently (ClusterRole, GithubTeam, GithubTeamRepository, Team).
// Note: TRBs are never created in team-only mode, so they are not asserted absent.
func TestMultiProvider_DeletionCleansAllProviders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "mp-delete-all"
	consumer1 := consumerClusters[0].Name
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	createASR(t, asrName, multiProviderConnectorRefs(), multiProviderPermissions())
	// No t.Cleanup for deleteASR since the test itself deletes it

	// Wait for all CRs to be created
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	githubTeamName := githubTeamCRName("e2e-org", asrName)
	githubTeamRepoName := githubTeamRepoCRName("e2e-org", asrName, "pull")
	teamName := "pm-" + asrName

	assertGithubTeamExists(t, ctx, mgmtClient, controllerNamespace, githubTeamName)
	assertGithubTeamRepoExists(t, ctx, mgmtClient, controllerNamespace, githubTeamRepoName)
	// Team exists in team-only mode (no TRBs due to conflict with K8s)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Delete the ASR gracefully (let controllers' finalizers handle cleanup)
	deleteASRGraceful(t, asrName)

	// Verify: ClusterRole removed from consumer-1
	waitForClusterRoleAbsent(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	// Verify: GithubTeam and GithubTeamRepository removed
	assertGithubTeamAbsent(t, ctx, mgmtClient, controllerNamespace, githubTeamName)
	assertGithubTeamRepoAbsent(t, ctx, mgmtClient, controllerNamespace, githubTeamRepoName)

	// Verify: Team removed (TRBs were never created in team-only mode)
	assertTeamAbsent(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Verify: ASR fully deleted (no stuck finalizers)
	waitForASRDeleted(t, ctx, mgmtClient, controllerNamespace, asrName, defaultAssertTimeout)

	t.Log("SUCCESS: ASR deletion cleaned up all provider resources")
}

// TestMultiProvider_ConnectorRefRemoved verifies that removing a provider's
// ConnectorBinding reference from an ASR causes that provider's CRs to be cleaned
// up while leaving the other providers' CRs intact.
// After removing github, k8s+greenhouse-teams still both exist so the conflict
// persists — TRBs still won't be created. Team still exists.
func TestMultiProvider_ConnectorRefRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "mp-cb-removed"
	consumer1 := consumerClusters[0].Name
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	createASR(t, asrName, multiProviderConnectorRefs(), multiProviderPermissions())
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for all CRs to be created
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	githubTeamName := githubTeamCRName("e2e-org", asrName)
	githubTeamRepoName := githubTeamRepoCRName("e2e-org", asrName, "pull")
	teamName := "pm-" + asrName

	assertGithubTeamExists(t, ctx, mgmtClient, controllerNamespace, githubTeamName)
	assertGithubTeamRepoExists(t, ctx, mgmtClient, controllerNamespace, githubTeamRepoName)
	// Team exists in team-only mode (no TRBs due to conflict with K8s)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Remove test-github from connectorRefs (keep only k8s + greenhouse-teams)
	patchASRConnectorRefs(t, asrName, []string{"e2e-k8s", "test-greenhouse-teams"})

	// Verify: GitHub CRs cleaned up
	assertGithubTeamAbsent(t, ctx, mgmtClient, controllerNamespace, githubTeamName)
	assertGithubTeamRepoAbsent(t, ctx, mgmtClient, controllerNamespace, githubTeamRepoName)

	// Verify: K8s ClusterRole still exists
	if !clusterRoleExists(t, consumer1, "pm-"+asrName) {
		t.Fatal("ClusterRole should still exist on consumer-1 after removing github CB ref")
	}

	// Verify: Greenhouse Team still exists (still in team-only mode since k8s+greenhouse conflict persists)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	t.Log("SUCCESS: Removing github ConnectorBinding ref cleaned up only github CRs")
}

// TestMultiProvider_AggregatedReadyOnlyWhenAllReady verifies that the ASR's
// aggregated Ready condition is only True when ALL providers report ready.
// It tests the progression from partial ready (github failed) → all ready.
// greenhouse-teams is ready immediately in team-only mode (no TRB status to wait for).
func TestMultiProvider_AggregatedReadyOnlyWhenAllReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "mp-agg-ready"
	consumer1 := consumerClusters[0].Name
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	createASR(t, asrName, multiProviderConnectorRefs(), multiProviderPermissions())
	t.Cleanup(func() { deleteASR(t, asrName) })

	githubTeamName := githubTeamCRName("e2e-org", asrName)

	// Wait for K8s ClusterRole (kubernetes becomes ready once CR is provisioned)
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	// Wait for GithubTeam to exist
	assertGithubTeamExists(t, ctx, mgmtClient, controllerNamespace, githubTeamName)

	// Team exists in team-only mode (no TRBs due to k8s+greenhouse conflict)
	teamName := "pm-" + asrName
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Force GithubTeam into "failed" state to create a deterministic not-ready state.
	patchGithubTeamStatus(t, controllerNamespace, githubTeamName, "failed")

	// kubernetes and greenhouse-teams are ready, but github should be not-ready (failed).
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "kubernetes", true)
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "greenhouse-teams", true)
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "github", false)

	// ASR should NOT be fully ready (github is failed)
	expectASRReady(t, ctx, mgmtClient, controllerNamespace, asrName, false)

	// Fix GithubTeam → "complete" → github becomes ready
	patchGithubTeamStatus(t, controllerNamespace, githubTeamName, "complete")
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "github", true)

	// Now all three providers are ready → ASR aggregated Ready=True
	expectASRReady(t, ctx, mgmtClient, controllerNamespace, asrName, true)

	t.Log("SUCCESS: Aggregated Ready is true only when all three providers are ready")
}
