// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestConflict_BothProviders_TeamCreated_NoTRBs verifies that when both
// kubernetes and greenhouse-teams ConnectorBindings are referenced by the same ASR,
// greenhouse-teams operates in Team-only mode: Teams ARE created but TRBs are suppressed.
func TestConflict_BothProviders_TeamCreated_NoTRBs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "conflict-dual-1"
	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1

	t.Cleanup(func() { deleteASR(t, asrName) })

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	createASR(t, asrName, []string{"e2e-k8s", "test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})

	// Expected resource names
	teamName := "pm-" + asrName
	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	crName := "pm-" + asrName

	// Verify: ClusterRole appears on consumer-1 (kubernetes works normally)
	waitForClusterRole(t, consumer1, crName, resourcePropagationTimeout)

	// Verify: Team CR is created (greenhouse-teams still creates teams)
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Verify: NO TeamRoleBinding created (suppressed due to K8s conflict)
	// Wait a bit to give the controller time to NOT create it, then assert absence
	time.Sleep(15 * time.Second)
	assertTeamRoleBindingAbsent(t, ctx, mgmtClient, controllerNamespace, trbName)

	t.Log("SUCCESS: Team created, TRB suppressed, ClusterRole provisioned in conflict mode")
}

// TestConflict_BothProviders_K8sRBACNormal verifies that in conflict mode the
// kubernetes provider still provisions RBAC normally with write verbs, and the
// greenhouse-teams Team has correct mappedIdPGroup while TRBs remain suppressed.
func TestConflict_BothProviders_K8sRBACNormal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "conflict-dual-2"
	consumer1 := consumerClusters[0].Name

	t.Cleanup(func() { deleteASR(t, asrName) })

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	createASR(t, asrName, []string{"e2e-k8s", "test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "write"},
	})

	teamName := "pm-" + asrName
	trbName := fmt.Sprintf("pm-%s-cluster-developer-%s", asrName, consumer1)
	crName := "pm-" + asrName

	// Verify: ClusterRole on consumer-1 with write verbs
	waitForClusterRole(t, consumer1, crName, resourcePropagationTimeout)

	consumerClient := crClientForCluster(t, consumer1)
	// Write capability maps to: create, update, patch, delete, get, list, watch
	assertVerbsInClusterRole(t, ctx, consumerClient, crName,
		[]string{"get", "list", "watch", "create", "update", "patch", "delete"})

	// Verify: Team exists with correct mappedIdPGroup
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)
	assertTeamMappedIDPGroup(t, ctx, mgmtClient, controllerNamespace, teamName, asrName)

	// Verify: TRB absent (conflict mode suppresses it)
	time.Sleep(15 * time.Second)
	assertTeamRoleBindingAbsent(t, ctx, mgmtClient, controllerNamespace, trbName)

	t.Log("SUCCESS: K8s RBAC provisioned with write verbs, Team with correct mappedIdPGroup, TRB suppressed")
}

// TestConflict_K8sAdded_TRBsRemoved verifies that when a kubernetes CB is added
// to an existing greenhouse-only ASR, TRBs are removed (conflict mode activates).
func TestConflict_K8sAdded_TRBsRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "conflict-k8s-added"
	consumer1 := consumerClusters[0].Name

	t.Cleanup(func() { deleteASR(t, asrName) })

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// Step 1: Create ASR with ONLY greenhouse-teams → Team + TRB should be created
	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})

	teamName := "pm-" + asrName
	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	crName := "pm-" + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)

	// Wait for Team and TRB to appear (greenhouse-only mode)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)
	assertTeamRoleBindingExists(t, ctx, mgmtClient, controllerNamespace, trbName)

	t.Log("Phase 1 complete: greenhouse-only ASR has Team + TRB")

	// Step 2: Add kubernetes CB to the ASR's connectorRefs
	patchASRConnectorRefs(t, asrName, []string{"e2e-k8s", "test-greenhouse-teams"})

	// Verify: Team still exists
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Verify: TRB removed (conflict mode activated)
	assertTeamRoleBindingAbsent(t, ctx, mgmtClient, controllerNamespace, trbName)

	// Verify: ClusterRole appears on consumer-1 (kubernetes takes over)
	waitForClusterRole(t, consumer1, crName, resourcePropagationTimeout)

	t.Log("SUCCESS: Adding kubernetes CB removed TRBs and provisioned ClusterRole")
}

// TestConflict_K8sRemoved_TRBsRecreated verifies that when a kubernetes CB is
// removed from a dual-provider ASR, TRBs are re-created (conflict resolved).
func TestConflict_K8sRemoved_TRBsRecreated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "conflict-k8s-removed"
	consumer1 := consumerClusters[0].Name

	t.Cleanup(func() { deleteASR(t, asrName) })

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// Step 1: Create dual-provider ASR (Team exists, no TRBs due to conflict)
	createASR(t, asrName, []string{"e2e-k8s", "test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})

	teamName := "pm-" + asrName
	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	crName := "pm-" + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)

	// Wait for ClusterRole to confirm dual-provider reconciliation happened
	waitForClusterRole(t, consumer1, crName, resourcePropagationTimeout)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Confirm no TRB in conflict mode
	time.Sleep(15 * time.Second)
	assertTeamRoleBindingAbsent(t, ctx, mgmtClient, controllerNamespace, trbName)

	t.Log("Phase 1 complete: dual-provider ASR has Team but no TRB")

	// Step 2: Remove kubernetes CB (patch to only greenhouse-teams)
	patchASRConnectorRefs(t, asrName, []string{"test-greenhouse-teams"})

	// Verify: TRBs re-created (conflict resolved, back to full mode)
	assertTeamRoleBindingExists(t, ctx, mgmtClient, controllerNamespace, trbName)

	// Verify: Team still exists
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)

	// Verify: ClusterRole removed from consumer-1 (kubernetes no longer active)
	waitForClusterRoleAbsent(t, consumer1, crName, resourcePropagationTimeout)

	t.Log("SUCCESS: Removing kubernetes CB re-created TRBs and removed ClusterRole")
}

// TestConflict_RoundTrip_K8sAddedThenRemoved verifies the full round-trip:
// greenhouse-only → add kubernetes (conflict) → remove kubernetes (resolved).
// Final state must match the initial greenhouse-only state.
func TestConflict_RoundTrip_K8sAddedThenRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "conflict-roundtrip"
	consumer1 := consumerClusters[0].Name

	t.Cleanup(func() { deleteASR(t, asrName) })

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	teamName := "pm-" + asrName
	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	crName := "pm-" + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)

	// Phase 1: Start greenhouse-only → Team + TRB exist
	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})

	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)
	assertTeamRoleBindingExists(t, ctx, mgmtClient, controllerNamespace, trbName)
	t.Log("Phase 1: greenhouse-only ASR has Team + TRB")

	// Phase 2: Add kubernetes → TRBs gone, ClusterRole appears
	patchASRConnectorRefs(t, asrName, []string{"e2e-k8s", "test-greenhouse-teams"})

	waitForClusterRole(t, consumer1, crName, resourcePropagationTimeout)
	assertTeamRoleBindingAbsent(t, ctx, mgmtClient, controllerNamespace, trbName)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)
	t.Log("Phase 2: conflict mode - TRBs removed, ClusterRole provisioned")

	// Phase 3: Remove kubernetes → TRBs back, ClusterRole gone
	patchASRConnectorRefs(t, asrName, []string{"test-greenhouse-teams"})

	assertTeamRoleBindingExists(t, ctx, mgmtClient, controllerNamespace, trbName)
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, teamName)
	waitForClusterRoleAbsent(t, consumer1, crName, resourcePropagationTimeout)
	t.Log("Phase 3: conflict resolved - TRBs re-created, ClusterRole removed")

	t.Log("SUCCESS: Round-trip verified - final state matches initial greenhouse-only state")
}

// TestConflict_StatusReporting verifies that in conflict mode both providers
// report their own status independently and both show as ready.
func TestConflict_StatusReporting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "conflict-status"
	consumer1 := consumerClusters[0].Name

	t.Cleanup(func() { deleteASR(t, asrName) })

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	createASR(t, asrName, []string{"e2e-k8s", "test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})

	crName := "pm-" + asrName

	// Wait for kubernetes to provision ClusterRole (proves reconciliation completed)
	waitForClusterRole(t, consumer1, crName, resourcePropagationTimeout)

	mgmtClient := crClientForCluster(t, mgmtClusterName)

	// Verify: kubernetes provider reports ready (ClusterRole provisioned)
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "kubernetes", true)

	// Verify: greenhouse-teams provider reports ready (Team-only mode succeeds)
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "greenhouse-teams", true)

	t.Log("SUCCESS: Both providers independently report ready in conflict mode")
}
