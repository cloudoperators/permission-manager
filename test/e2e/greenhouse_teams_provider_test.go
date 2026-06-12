// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// --- Helpers specific to the greenhouse-teams provider E2E tests ---

// assertTeamRoleBindingNamespaces polls until the named TeamRoleBinding CR has the expected
// spec.namespaces values (order-independent).
func assertTeamRoleBindingNamespaces(t *testing.T, ctx context.Context, c client.Client, namespace, name string, expectedNamespaces []string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "greenhouse.sap",
			Version: "v1alpha2",
			Kind:    "TeamRoleBinding",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}

		nsSlice, found, err := unstructured.NestedStringSlice(obj.Object, "spec", "namespaces")
		if err != nil || !found {
			if len(expectedNamespaces) == 0 {
				return true, nil
			}
			return false, fmt.Errorf("TeamRoleBinding %s/%s missing spec.namespaces", namespace, name)
		}

		if len(nsSlice) != len(expectedNamespaces) {
			return false, fmt.Errorf("TeamRoleBinding %s/%s spec.namespaces length=%d, want %d", namespace, name, len(nsSlice), len(expectedNamespaces))
		}

		nsSet := make(map[string]bool, len(nsSlice))
		for _, ns := range nsSlice {
			nsSet[ns] = true
		}
		for _, expected := range expectedNamespaces {
			if !nsSet[expected] {
				return false, fmt.Errorf("TeamRoleBinding %s/%s missing namespace %q in spec.namespaces (got: %v)", namespace, name, expected, nsSlice)
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("assertTeamRoleBindingNamespaces(%s/%s, %v): %v", namespace, name, expectedNamespaces, err)
	}
}

// assertTeamRoleBindingTeamRef polls until the named TeamRoleBinding CR has the expected
// teamRef (from spec.teamRefs[0].name or spec.teamRef depending on API shape).
func assertTeamRoleBindingTeamRef(t *testing.T, ctx context.Context, c client.Client, namespace, name, expectedTeamRef string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "greenhouse.sap",
			Version: "v1alpha2",
			Kind:    "TeamRoleBinding",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}

		// Try spec.teamRef as a slice (JSON tag is singular "teamRef" for []TeamRef)
		teamRefs, found, _ := unstructured.NestedSlice(obj.Object, "spec", "teamRef")
		if found && len(teamRefs) > 0 {
			if ref, ok := teamRefs[0].(map[string]interface{}); ok {
				if refName, ok := ref["name"].(string); ok {
					if refName == expectedTeamRef {
						return true, nil
					}
					return false, fmt.Errorf("TeamRoleBinding %s/%s teamRef[0].name=%q, want %q", namespace, name, refName, expectedTeamRef)
				}
			}
		}

		// Fallback: try spec.teamRefs as a slice (plural form)
		teamRefs2, found2, _ := unstructured.NestedSlice(obj.Object, "spec", "teamRefs")
		if found2 && len(teamRefs2) > 0 {
			if ref, ok := teamRefs2[0].(map[string]interface{}); ok {
				if refName, ok := ref["name"].(string); ok {
					if refName == expectedTeamRef {
						return true, nil
					}
					return false, fmt.Errorf("TeamRoleBinding %s/%s teamRefs[0].name=%q, want %q", namespace, name, refName, expectedTeamRef)
				}
			}
		}

		return false, fmt.Errorf("TeamRoleBinding %s/%s has no teamRefs or teamRef field", namespace, name)
	})
	if err != nil {
		t.Fatalf("assertTeamRoleBindingTeamRef(%s/%s, %q): %v", namespace, name, expectedTeamRef, err)
	}
}

// --- Basic Operations (1-7) ---

// TestGreenhouseTeams_Basic_TeamAndTRBCreation verifies that creating an ASR with one
// cluster CCRN results in the Team and TeamRoleBinding CRs appearing.
func TestGreenhouseTeams_Basic_TeamAndTRBCreation(t *testing.T) {
	t.Parallel()
	asrName := "ght-basic-1"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	// Team name: pm-<asrName> (since no advancedConfig.teamName override)
	teamName := "pm-" + asrName
	// TRB name: pm-<asrName>-<teamRole>-<clusterName>
	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)

	assertTeamExists(t, ctx, c, controllerNamespace, teamName)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbName)
	assertTeamMappedIDPGroup(t, ctx, c, controllerNamespace, teamName, asrName)
	assertTeamRoleBindingTeamRoleRef(t, ctx, c, controllerNamespace, trbName, "cluster-viewer")
	assertTeamRoleBindingCluster(t, ctx, c, controllerNamespace, trbName, consumer1)
}

// TestGreenhouseTeams_Basic_CustomTeamName verifies that advancedConfig.teamName overrides
// the default team name derivation.
func TestGreenhouseTeams_Basic_CustomTeamName(t *testing.T) {
	t.Parallel()
	asrName := "ght-custom-name"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	advancedConfig := `{"greenhouse-teams":{"teamName":"my-custom-team"}}`

	createASRWithAdvancedConfig(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	}, advancedConfig)
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	// Team uses the custom name: pm-my-custom-team
	teamName := "pm-my-custom-team"
	assertTeamExists(t, ctx, c, controllerNamespace, teamName)
	// mappedIdPGroup is always asr.Spec.Name
	assertTeamMappedIDPGroup(t, ctx, c, controllerNamespace, teamName, asrName)
}

// TestGreenhouseTeams_Basic_DerivedTeamName verifies that without a teamName override,
// the Team name is derived from the ASR spec.name.
func TestGreenhouseTeams_Basic_DerivedTeamName(t *testing.T) {
	t.Parallel()
	asrName := "ght-derived-name"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	// Team name: pm-<asrName>
	teamName := "pm-" + asrName
	assertTeamExists(t, ctx, c, controllerNamespace, teamName)
	assertTeamMappedIDPGroup(t, ctx, c, controllerNamespace, teamName, asrName)
}

// TestGreenhouseTeams_Basic_TwoCapsOneCluster verifies that two capabilities on the same
// cluster produce two distinct TRBs (one per teamRole).
func TestGreenhouseTeams_Basic_TwoCapsOneCluster(t *testing.T) {
	t.Parallel()
	asrName := "ght-two-caps"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
		{"resource": resource, "capability": "write"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	trbReader := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	trbWriter := fmt.Sprintf("pm-%s-cluster-developer-%s", asrName, consumer1)

	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbReader)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbWriter)
	assertTeamRoleBindingTeamRoleRef(t, ctx, c, controllerNamespace, trbReader, "cluster-viewer")
	assertTeamRoleBindingTeamRoleRef(t, ctx, c, controllerNamespace, trbWriter, "cluster-developer")
}

// TestGreenhouseTeams_Basic_TwoDifferentClusters verifies that permissions on two different
// clusters produce two TRBs with different clusterSelector values.
func TestGreenhouseTeams_Basic_TwoDifferentClusters(t *testing.T) {
	t.Parallel()
	asrName := "ght-two-clusters"
	consumer1 := consumerClusters[0].Name
	consumer2 := consumerClusters[1].Name
	resource1 := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	resource2 := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer2)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource1, "capability": "read"},
		{"resource": resource2, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	trb1 := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	trb2 := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer2)

	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trb1)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trb2)
	assertTeamRoleBindingCluster(t, ctx, c, controllerNamespace, trb1, consumer1)
	assertTeamRoleBindingCluster(t, ctx, c, controllerNamespace, trb2, consumer2)
}

// TestGreenhouseTeams_Basic_MultipleTargetNamespaces verifies that CRs are replicated
// to each targetNamespace specified in advancedConfig.
func TestGreenhouseTeams_Basic_MultipleTargetNamespaces(t *testing.T) {
	t.Parallel()
	asrName := "ght-multi-ns"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// Create target namespaces
	ensureNamespace(t, "ght-org-ns-1")
	ensureNamespace(t, "ght-org-ns-2")

	advancedConfig := `{"greenhouse-teams":{"targetNamespaces":["ght-org-ns-1","ght-org-ns-2"]}}`

	createASRWithAdvancedConfig(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	}, advancedConfig)
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	teamName := "pm-" + asrName
	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)

	// Assert CRs exist in both namespaces
	assertTeamExists(t, ctx, c, "ght-org-ns-1", teamName)
	assertTeamExists(t, ctx, c, "ght-org-ns-2", teamName)
	assertTeamRoleBindingExists(t, ctx, c, "ght-org-ns-1", trbName)
	assertTeamRoleBindingExists(t, ctx, c, "ght-org-ns-2", trbName)
}

// TestGreenhouseTeams_Basic_NamespaceScopedCCRN verifies that a namespace-scoped CCRN
// results in a TRB with spec.namespaces populated.
func TestGreenhouseTeams_Basic_NamespaceScopedCCRN(t *testing.T) {
	t.Parallel()
	asrName := "ght-ns-scoped"
	consumer1 := consumerClusters[0].Name
	// Namespace-scoped CCRN: urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/<cluster>/<namespace>
	resource := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/apps-prod", consumer1)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbName)
	assertTeamRoleBindingNamespaces(t, ctx, c, controllerNamespace, trbName, []string{"apps-prod"})
}

// --- Capability Mapping (8-9) ---

// TestGreenhouseTeams_CapMapping_VerifyAllMappings verifies that read->cluster-viewer,
// write->cluster-developer, admin->cluster-admin all produce correct teamRoleRef values.
func TestGreenhouseTeams_CapMapping_VerifyAllMappings(t *testing.T) {
	t.Parallel()
	asrName := "ght-cap-mapping"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
		{"resource": resource, "capability": "write"},
		{"resource": resource, "capability": "admin"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	trbViewer := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	trbDev := fmt.Sprintf("pm-%s-cluster-developer-%s", asrName, consumer1)
	trbAdmin := fmt.Sprintf("pm-%s-cluster-admin-%s", asrName, consumer1)

	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbViewer)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbDev)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbAdmin)

	assertTeamRoleBindingTeamRoleRef(t, ctx, c, controllerNamespace, trbViewer, "cluster-viewer")
	assertTeamRoleBindingTeamRoleRef(t, ctx, c, controllerNamespace, trbDev, "cluster-developer")
	assertTeamRoleBindingTeamRoleRef(t, ctx, c, controllerNamespace, trbAdmin, "cluster-admin")
}

// TestGreenhouseTeams_CapMapping_UnmappedCapability verifies that an ASR with an unmapped
// capability (e.g., "execute") causes the provider status to be not-ready.
func TestGreenhouseTeams_CapMapping_UnmappedCapability(t *testing.T) {
	t.Parallel()
	asrName := "ght-unmapped-cap"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// "execute" is not in the capability mapping
	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "execute"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	// Provider should report not ready due to unmapped capability
	expectProviderReady(t, ctx, c, controllerNamespace, asrName, "greenhouse-teams", false)
}

// --- Updates (10-11) ---

// TestGreenhouseTeams_Update_AddPermission verifies that adding a second permission
// to an existing ASR results in a new TRB appearing.
func TestGreenhouseTeams_Update_AddPermission(t *testing.T) {
	t.Parallel()
	asrName := "ght-update-add"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// Start with one permission
	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	trbReader := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbReader)

	// Now update to add a second permission
	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
		{"resource": resource, "capability": "write"},
	})

	trbWriter := fmt.Sprintf("pm-%s-cluster-developer-%s", asrName, consumer1)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbWriter)
	// Original TRB should still exist
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbReader)
}

// TestGreenhouseTeams_Update_RemovePermission verifies that removing a permission from
// an existing ASR results in the stale TRB being deleted.
func TestGreenhouseTeams_Update_RemovePermission(t *testing.T) {
	t.Parallel()
	asrName := "ght-update-rm"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// Start with two permissions
	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
		{"resource": resource, "capability": "write"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	trbReader := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	trbWriter := fmt.Sprintf("pm-%s-cluster-developer-%s", asrName, consumer1)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbReader)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbWriter)

	// Remove the write permission
	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})

	// Writer TRB should be cleaned up
	assertTeamRoleBindingAbsent(t, ctx, c, controllerNamespace, trbWriter)
	// Reader TRB should remain
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbReader)
}

// --- Deletion (12) ---

// TestGreenhouseTeams_Deletion_CleanupAll verifies that deleting an ASR removes all
// Team and TRB CRs, including across multiple target namespaces.
func TestGreenhouseTeams_Deletion_CleanupAll(t *testing.T) {
	t.Parallel()
	asrName := "ght-delete-all"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
		{"resource": resource, "capability": "admin"},
	})

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	teamName := "pm-" + asrName
	trbReader := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	trbAdmin := fmt.Sprintf("pm-%s-cluster-admin-%s", asrName, consumer1)

	// Wait for CRs to appear
	assertTeamExists(t, ctx, c, controllerNamespace, teamName)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbReader)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbAdmin)

	// Delete the ASR gracefully (let the controller's finalizer handle cleanup)
	deleteASRGraceful(t, asrName)

	// All CRs should be cleaned up
	assertTeamAbsent(t, ctx, c, controllerNamespace, teamName)
	assertTeamRoleBindingAbsent(t, ctx, c, controllerNamespace, trbReader)
	assertTeamRoleBindingAbsent(t, ctx, c, controllerNamespace, trbAdmin)
}

// --- ConnectorBinding Lifecycle (13-15) ---

// TestGreenhouseTeams_CB_NonGreenhouseCBOnly verifies that an ASR referencing only
// a non-greenhouse-teams CB produces no greenhouse CRs.
func TestGreenhouseTeams_CB_NonGreenhouseCBOnly(t *testing.T) {
	t.Parallel()
	asrName := "ght-no-gh-cb"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// Reference only the kubernetes ConnectorBinding
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	teamName := "pm-" + asrName
	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)

	// Wait a reasonable time and verify no greenhouse CRs are created
	time.Sleep(15 * time.Second)
	assertTeamAbsent(t, ctx, c, controllerNamespace, teamName)
	assertTeamRoleBindingAbsent(t, ctx, c, controllerNamespace, trbName)
}

// TestGreenhouseTeams_CB_AddedLater verifies that if an ASR exists before the
// greenhouse-teams CB is added, CRs appear once the CB is created.
func TestGreenhouseTeams_CB_AddedLater(t *testing.T) {
	// NOT parallel: deletes the shared test-greenhouse-teams ConnectorBinding.
	asrName := "ght-cb-added"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// First, delete the greenhouse-teams CB and wait for the provider to notice.
	deleteConnectorBinding(t, "test-greenhouse-teams")
	// Restore CB at end
	t.Cleanup(func() { createGreenhouseTeamsConnectorBinding(t) })
	time.Sleep(5 * time.Second) // Give the provider time to drop the CB from its index

	// Create an ASR that references the (now missing) greenhouse-teams CB
	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	teamName := "pm-" + asrName
	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)

	// Clean up any stale Team from a prior run (deleteASR bypasses controller cleanup).
	mgmtCtx := "kind-" + mgmtClusterName
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"delete", "team", teamName, "--ignore-not-found")

	// No CRs should exist yet
	time.Sleep(10 * time.Second)
	assertTeamAbsent(t, ctx, c, controllerNamespace, teamName)

	// Now add the greenhouse-teams CB back
	createGreenhouseTeamsConnectorBinding(t)

	// CRs should appear
	assertTeamExists(t, ctx, c, controllerNamespace, teamName)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbName)
}

// TestGreenhouseTeams_CB_Removed verifies that removing the greenhouse-teams CB
// triggers cleanup of all greenhouse CRs.
func TestGreenhouseTeams_CB_Removed(t *testing.T) {
	// NOT parallel: deletes the shared test-greenhouse-teams ConnectorBinding.
	asrName := "ght-cb-removed"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	teamName := "pm-" + asrName
	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)

	// Wait for CRs to appear
	assertTeamExists(t, ctx, c, controllerNamespace, teamName)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbName)

	// Remove the greenhouse-teams CB
	deleteConnectorBinding(t, "test-greenhouse-teams")
	// Restore CB at end
	t.Cleanup(func() { createGreenhouseTeamsConnectorBinding(t) })

	// CRs should be cleaned up
	assertTeamAbsent(t, ctx, c, controllerNamespace, teamName)
	assertTeamRoleBindingAbsent(t, ctx, c, controllerNamespace, trbName)
}

// --- Status Sync (16-18) ---

// TestGreenhouseTeams_Status_TRBReadyTrue verifies that when a TRB has status
// Ready=True, the provider reports Success for that target.
func TestGreenhouseTeams_Status_TRBReadyTrue(t *testing.T) {
	t.Parallel()
	asrName := "ght-status-ready"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbName)

	// Mock TRB status Ready=True
	patchTeamRoleBindingStatus(t, controllerNamespace, trbName, true)

	// Provider should report success
	expectProviderTargetStatus(t, ctx, c, controllerNamespace, asrName, "greenhouse-teams", trbName, "Success")
}

// TestGreenhouseTeams_Status_TRBReadyFalse verifies that when a TRB has status
// Ready=False, the provider reports Error for that target.
func TestGreenhouseTeams_Status_TRBReadyFalse(t *testing.T) {
	t.Parallel()
	asrName := "ght-status-err"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbName)

	// Mock TRB status Ready=False
	patchTeamRoleBindingStatus(t, controllerNamespace, trbName, false)

	// Provider should report error
	expectProviderTargetStatus(t, ctx, c, controllerNamespace, asrName, "greenhouse-teams", trbName, "Error")
}

// TestGreenhouseTeams_Status_TRBNonExistentCluster verifies that a TRB targeting a
// non-existent cluster (no status conditions set) results in Pending status.
func TestGreenhouseTeams_Status_TRBNonExistentCluster(t *testing.T) {
	t.Parallel()
	asrName := "ght-status-pending"
	// Use a cluster name that does not exist in the kind environment
	fakeCluster := "pm-e2e-nonexistent-cluster"
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", fakeCluster)

	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, fakeCluster)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbName)

	// TRB targeting a non-existent cluster should have no Ready condition → Pending
	expectProviderTargetStatus(t, ctx, c, controllerNamespace, asrName, "greenhouse-teams", trbName, "Pending")
}

// --- Cross-Namespace (19) ---

// TestGreenhouseTeams_CrossNamespace_DefaultTargetNamespace verifies that CRs are created
// in the greenhouse org namespace specified by the CB's defaultTargetNamespace rather than
// the ASR namespace.
func TestGreenhouseTeams_CrossNamespace_DefaultTargetNamespace(t *testing.T) {
	t.Parallel()
	asrName := "ght-cross-ns"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// The ConnectorBinding has defaultTargetNamespace = "permission-manager"
	// Create the ASR - it lives in controllerNamespace, and CRs should appear in controllerNamespace
	// (the defaultTargetNamespace from the CB definition)
	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, mgmtClusterName)

	teamName := "pm-" + asrName
	trbName := fmt.Sprintf("pm-%s-cluster-viewer-%s", asrName, consumer1)

	// CRs should be in the defaultTargetNamespace (permission-manager), not somewhere else
	assertTeamExists(t, ctx, c, controllerNamespace, teamName)
	assertTeamRoleBindingExists(t, ctx, c, controllerNamespace, trbName)

	// Verify the TRB references the correct team
	assertTeamRoleBindingTeamRef(t, ctx, c, controllerNamespace, trbName, teamName)
}
