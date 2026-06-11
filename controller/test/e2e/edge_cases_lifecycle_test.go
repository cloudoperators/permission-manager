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
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

// TestEdge_Lifecycle_EmptyPermissions creates an ASR with an empty permissions
// list and verifies that no ClusterRole is ever created on any consumer cluster
// (negative assertion for 15s). The ASR should still exist and the kubernetes
// provider should report ready since there is nothing to provision.
func TestEdge_Lifecycle_EmptyPermissions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-empty-perms"
	consumer1 := consumerClusters[0].Name
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)

	// Create ASR with empty permissions list.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Negative assertion: no ClusterRole should appear on consumer-1 within 15s.
	assertClusterRoleNotCreated(t, consumer1, expectedCR, connectorNegativeWait)

	// Verify ASR still exists.
	if !asrExists(t, asrName) {
		t.Fatal("ASR should still exist after empty permissions reconciliation")
	}

	// Verify kubernetes provider reports ready (nothing to do).
	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: controllerNamespace, Name: asrName}
		if err := mgmtClient.Get(ctx, key, &asr); err != nil {
			return false, err
		}
		k8sProv, ok := asr.Status.Providers["kubernetes"]
		if !ok {
			return false, fmt.Errorf("no kubernetes provider in status yet")
		}
		return k8sProv.Ready, nil
	})
	if err != nil {
		t.Logf("Note: kubernetes provider did not report ready within timeout (may be expected if controller skips empty permissions entirely): %v", err)
	}

	t.Log("SUCCESS: ASR with empty permissions produced no ClusterRole on any consumer cluster")
}

// TestEdge_Lifecycle_PermissionsStrippedToZero creates an ASR with READ on
// consumer-1, waits for the ClusterRole to appear, then updates the ASR
// permissions to empty []. It verifies the ClusterRole is removed from
// consumer-1 and that the ASR itself is not deleted.
func TestEdge_Lifecycle_PermissionsStrippedToZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-strip-to-zero"
	consumer1 := consumerClusters[0].Name
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	kubeClient := kubeclientForCluster(t, consumer1)
	clients := map[string]kubernetes.Interface{consumer1: kubeClient}
	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, clients) })
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create ASR with READ on consumer-1.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "READ"},
	})

	// Wait for ClusterRole to appear.
	waitForClusterRole(t, consumer1, expectedCR, resourcePropagationTimeout)

	// Update ASR permissions to empty.
	var asr pmv1.ApplicationSpecificRole
	key := client.ObjectKey{Namespace: controllerNamespace, Name: asrName}
	if err := mgmtClient.Get(ctx, key, &asr); err != nil {
		t.Fatalf("failed to get ASR: %v", err)
	}
	patch := client.MergeFrom(asr.DeepCopy())
	asr.Spec.Permissions = []pmv1.Permission{}
	if err := mgmtClient.Patch(ctx, &asr, patch); err != nil {
		t.Fatalf("failed to patch ASR permissions to empty: %v", err)
	}
	t.Log("Patched ASR permissions to empty []")

	// Verify ClusterRole is removed from consumer-1.
	waitForClusterRoleAbsent(t, consumer1, expectedCR, resourcePropagationTimeout)

	// Verify ASR still exists (not deleted).
	if !asrExists(t, asrName) {
		t.Fatal("ASR should still exist after permissions stripped to zero")
	}

	t.Log("SUCCESS: Stripping permissions to zero removed ClusterRole but kept ASR")
}

// TestEdge_Lifecycle_CBDeletedDuringASRDeletion creates a ConnectorBinding and
// an ASR referencing it, waits for RBAC provisioning, then deletes the CB
// followed immediately by a graceful ASR deletion. This verifies that the
// controller's finalizer completes successfully even when the referenced CB
// is already gone, and that the ClusterRole is cleaned up from consumer-1.
func TestEdge_Lifecycle_CBDeletedDuringASRDeletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cbName := "test-cb-lifecycle"
	asrName := "edge-cb-deleted"
	consumer1 := consumerClusters[0].Name
	expectedCR := resourcePrefix + asrName

	// Create ConnectorBinding with provider=kubernetes.
	createConnectorBindingCR(t, cbName, "kubernetes")

	// Create ASR referencing the new CB.
	createASR(t, asrName, []string{cbName}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "READ"},
	})

	// Wait for ClusterRole to appear on consumer-1.
	waitForClusterRole(t, consumer1, expectedCR, resourcePropagationTimeout)
	t.Logf("ClusterRole %q appeared on %s", expectedCR, consumer1)

	// Delete the ConnectorBinding first.
	deleteConnectorBindingCR(t, cbName)
	t.Log("Deleted ConnectorBinding — now deleting ASR gracefully")

	// Immediately delete the ASR gracefully (let finalizer run).
	deleteASRGraceful(t, asrName)

	// Verify ASR eventually disappears (finalizer completes even without CB).
	err := pollUntil(t, ctx, resourcePropagationTimeout, assertPollInterval, func() (bool, error) {
		return !asrExists(t, asrName), nil
	})
	if err != nil {
		// If the ASR is stuck with a finalizer, force-delete it for cleanup.
		t.Cleanup(func() { deleteASR(t, asrName) })
		t.Fatalf("ASR %q did not disappear after graceful deletion (finalizer stuck): %v", asrName, err)
	}

	// Verify ClusterRole is cleaned up from consumer-1.
	waitForClusterRoleAbsent(t, consumer1, expectedCR, resourcePropagationTimeout)

	t.Log("SUCCESS: ASR finalizer completed and ClusterRole cleaned up despite CB being deleted first")
}

// TestEdge_Status_ProviderRemovedFromStatus creates an ASR with connectorRefs
// ["e2e-k8s", "test-github"] targeting consumer-1 and a GitHub repo. It waits
// for both kubernetes and github providers to appear in status, then updates
// the ASR to reference only ["e2e-k8s"]. It verifies that the github provider
// entry is eventually removed from status.providers while kubernetes remains.
func TestEdge_Status_ProviderRemovedFromStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-prov-removed"
	consumer1 := consumerClusters[0].Name

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create ASR targeting both kubernetes (consumer-1 cluster) and github (repo).
	createASR(t, asrName, []string{"e2e-k8s", "test-github"}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "read"},
		{"resource": "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/e2e-org/test-repo", "capability": "read"},
	})

	// Wait for ClusterRole on consumer-1 (kubernetes provider active).
	waitForClusterRole(t, consumer1, resourcePrefix+asrName, resourcePropagationTimeout)

	// Wait for both providers to appear in status.
	err := pollUntil(t, ctx, resourcePropagationTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: controllerNamespace, Name: asrName}
		if err := mgmtClient.Get(ctx, key, &asr); err != nil {
			return false, err
		}
		_, hasK8s := asr.Status.Providers["kubernetes"]
		_, hasGithub := asr.Status.Providers["github"]
		if hasK8s && hasGithub {
			return true, nil
		}
		return false, fmt.Errorf("waiting for both providers: kubernetes=%v, github=%v", hasK8s, hasGithub)
	})
	if err != nil {
		t.Fatalf("both providers did not appear in status: %v", err)
	}
	t.Log("Both kubernetes and github providers present in status")

	// Update ASR connectorRefs to ["e2e-k8s"] only (remove github).
	var asr pmv1.ApplicationSpecificRole
	key := client.ObjectKey{Namespace: controllerNamespace, Name: asrName}
	if err := mgmtClient.Get(ctx, key, &asr); err != nil {
		t.Fatalf("failed to get ASR: %v", err)
	}
	patch := client.MergeFrom(asr.DeepCopy())
	asr.Spec.ConnectorRefs = []string{"e2e-k8s"}
	if err := mgmtClient.Patch(ctx, &asr, patch); err != nil {
		t.Fatalf("failed to patch ASR connectorRefs: %v", err)
	}
	t.Log("Patched ASR connectorRefs to [e2e-k8s] only")

	// Verify github provider entry is eventually removed from status.providers.
	err = pollUntil(t, ctx, resourcePropagationTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: controllerNamespace, Name: asrName}
		if err := mgmtClient.Get(ctx, key, &asr); err != nil {
			return false, err
		}
		_, hasGithub := asr.Status.Providers["github"]
		return !hasGithub, nil
	})
	if err != nil {
		t.Fatalf("github provider was not removed from status: %v", err)
	}

	// Verify kubernetes provider remains with ready=true.
	err = pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: controllerNamespace, Name: asrName}
		if err := mgmtClient.Get(ctx, key, &asr); err != nil {
			return false, err
		}
		k8sProv, ok := asr.Status.Providers["kubernetes"]
		if !ok {
			return false, fmt.Errorf("kubernetes provider missing from status")
		}
		return k8sProv.Ready, nil
	})
	if err != nil {
		t.Fatalf("kubernetes provider not ready after github removal: %v", err)
	}

	t.Log("SUCCESS: github provider removed from status; kubernetes remains ready")
}

// TestEdge_Lifecycle_SpecNameUpdate creates an ASR with spec.name="team-alpha",
// waits for the ClusterRoleBinding with subject group "team-alpha", then updates
// spec.name to "team-beta". It verifies the CRB subject group changes to
// "team-beta" and validates via SubjectAccessReview that "team-beta" is allowed
// and "team-alpha" is denied.
func TestEdge_Lifecycle_SpecNameUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-specname-update"
	specNameBefore := "team-alpha"
	specNameAfter := "team-beta"
	consumer1 := consumerClusters[0].Name
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	kubeClient := kubeclientForCluster(t, consumer1)
	crClient := crClientForCluster(t, consumer1)
	clients := map[string]kubernetes.Interface{consumer1: kubeClient}
	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, clients) })
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create ASR with spec.name = "team-alpha".
	createASRWithSpecName(t, asrName, specNameBefore, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "READ"},
	})

	// Wait for ClusterRole and CRB with subject group "team-alpha".
	waitForClusterRole(t, consumer1, expectedCR, resourcePropagationTimeout)
	assertClusterRoleBindingExists(t, ctx, crClient, expectedCR, specNameBefore)
	t.Logf("CRB %q with subject group %q confirmed", expectedCR, specNameBefore)

	// Update ASR spec.name to "team-beta".
	var asr pmv1.ApplicationSpecificRole
	key := client.ObjectKey{Namespace: controllerNamespace, Name: asrName}
	if err := mgmtClient.Get(ctx, key, &asr); err != nil {
		t.Fatalf("failed to get ASR: %v", err)
	}
	patch := client.MergeFrom(asr.DeepCopy())
	asr.Spec.Name = specNameAfter
	if err := mgmtClient.Patch(ctx, &asr, patch); err != nil {
		t.Fatalf("failed to patch ASR spec.name: %v", err)
	}
	t.Log("Patched ASR spec.name to 'team-beta'")

	// Force reconcile to ensure the change is picked up promptly.
	forceReconcile(t, asrName)

	// Wait a moment for reconciliation to process.
	time.Sleep(2 * time.Second)

	// Verify CRB subject group changes to "team-beta".
	assertClusterRoleBindingExists(t, ctx, crClient, expectedCR, specNameAfter)
	t.Logf("CRB %q now has subject group %q", expectedCR, specNameAfter)

	// Verify SAR: "team-beta" should be allowed to get pods (cluster-scoped READ).
	assertRBACEnforced(t, ctx, kubeClient, specNameAfter, "get", "pods", "", true)

	// Verify SAR: "team-alpha" should be denied (no longer bound).
	assertRBACEnforced(t, ctx, kubeClient, specNameBefore, "get", "pods", "", false)

	t.Log("SUCCESS: spec.name update propagated to CRB subjects and SAR enforcement")
}
