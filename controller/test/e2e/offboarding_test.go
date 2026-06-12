// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

// TestOffboarding_ClusterCRDeleted verifies that when a Cluster CR is deleted
// (simulating a cluster leaving the fleet), the controller removes it from the
// inventory. New ASRs targeting that cluster will NOT get RBAC provisioned.
// NOTE: Not parallel — mutates consumer-4 cluster state shared with other tests.
func TestOffboarding_ClusterCRDeleted(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Controller logs:\n", controllerLogs(t))
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), resourcePropagationTimeout)
	defer cancel()

	const asrName = "offboard-cluster-deleted"
	t.Cleanup(func() {
		deleteASR(t, asrName)
		// Re-create consumer-4 for subsequent tests.
		recreateClusterCR(t, "consumer-4")
	})

	// Step 1: Create ASR targeting consumer-4 and wait for RBAC provisioning.
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      asrName,
			Namespace: controllerNamespace,
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "offboard-cluster-deleted-group",
			ConnectorRefs: []string{"e2e-k8s"},
			Permissions: []pmv1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-4",
					Capability: "READ",
				},
			},
		},
	}

	createASRObj(t, asr)
	waitForClusterRoleByName(t, ctx, "consumer-4", "pm-"+asrName)
	assertClusterRoleBindingByName(t, ctx, "consumer-4", "pm-"+asrName)

	// Step 2: Delete consumer-4 Cluster CR to simulate cluster removal.
	deleteClusterCR(t, "consumer-4")

	// Step 3: Verify that a NEW ASR targeting consumer-4 does NOT get RBAC
	// (because consumer-4 is no longer in the inventory).
	const verifyASR = "offboard-verify-no-provision"
	t.Cleanup(func() { deleteASR(t, verifyASR) })
	createASR(t, verifyASR, []string{"e2e-k8s"}, []map[string]string{
		{"resource": "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-4", "capability": "READ"},
	})

	// Wait a few seconds — the ClusterRole should NOT appear since consumer-4 is gone.
	time.Sleep(10 * time.Second)
	if clusterRoleExists(t, fullClusterName("consumer-4"), "pm-"+verifyASR) {
		t.Fatal("ClusterRole was unexpectedly created on consumer-4 after its Cluster CR was deleted")
	}
	t.Log("SUCCESS: No RBAC provisioned on consumer-4 after its Cluster CR was deleted")
}

// TestOffboarding_ClusterInvalid verifies that when a Cluster CR's
// KubeConfigValid condition is set to False, the controller removes it from
// the inventory. New ASRs targeting that cluster will NOT get RBAC provisioned.
// NOTE: Not parallel — mutates consumer-4 cluster state shared with other tests.
func TestOffboarding_ClusterInvalid(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Controller logs:\n", controllerLogs(t))
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), resourcePropagationTimeout)
	defer cancel()

	const asrName = "offboard-cluster-invalid"
	t.Cleanup(func() {
		deleteASR(t, asrName)
		// Restore consumer-4 to a valid state for subsequent tests.
		recreateClusterCR(t, "consumer-4")
	})

	// Step 1: Create ASR targeting consumer-4 and wait for RBAC provisioning.
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      asrName,
			Namespace: controllerNamespace,
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "offboard-cluster-invalid-group",
			ConnectorRefs: []string{"e2e-k8s"},
			Permissions: []pmv1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-4",
					Capability: "READ",
				},
			},
		},
	}

	createASRObj(t, asr)
	waitForClusterRoleByName(t, ctx, "consumer-4", "pm-"+asrName)
	assertClusterRoleBindingByName(t, ctx, "consumer-4", "pm-"+asrName)

	// Step 2: Invalidate consumer-4 by setting KubeConfigValid=False.
	invalidateClusterKubeConfig(t, "consumer-4")

	// Step 3: Verify that a NEW ASR targeting consumer-4 does NOT get RBAC
	// (because consumer-4 is now invalid and removed from inventory).
	const verifyASR = "offboard-verify-invalid"
	t.Cleanup(func() { deleteASR(t, verifyASR) })
	createASR(t, verifyASR, []string{"e2e-k8s"}, []map[string]string{
		{"resource": "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-4", "capability": "READ"},
	})

	// Wait a few seconds — the ClusterRole should NOT appear since consumer-4 is invalid.
	time.Sleep(10 * time.Second)
	if clusterRoleExists(t, fullClusterName("consumer-4"), "pm-"+verifyASR) {
		t.Fatal("ClusterRole was unexpectedly created on consumer-4 after it was invalidated")
	}
	t.Log("SUCCESS: No RBAC provisioned on consumer-4 after KubeConfigValid=False")
}

// TestOffboarding_OrphanDetection verifies that when an ASR is updated to
// narrow its target clusters (from consumer-1 + consumer-2 to only consumer-1),
// the RBAC previously provisioned on the removed cluster (consumer-2) is cleaned
// up by the orphan detection logic.
func TestOffboarding_OrphanDetection(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Controller logs:\n", controllerLogs(t))
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), resourcePropagationTimeout)
	defer cancel()

	const asrName = "offboard-orphan"
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Step 1: Create ASR targeting both consumer-1 and consumer-2.
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      asrName,
			Namespace: controllerNamespace,
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "offboard-orphan-group",
			ConnectorRefs: []string{"e2e-k8s"},
			Permissions: []pmv1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-1",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-2",
					Capability: "READ",
				},
			},
		},
	}

	createASRObj(t, asr)

	// Wait for RBAC to appear on both clusters.
	waitForClusterRoleByName(t, ctx, "consumer-1", "pm-"+asrName)
	waitForClusterRoleByName(t, ctx, "consumer-2", "pm-"+asrName)
	assertClusterRoleBindingByName(t, ctx, "consumer-1", "pm-"+asrName)
	assertClusterRoleBindingByName(t, ctx, "consumer-2", "pm-"+asrName)

	// Step 2: Update ASR to only target consumer-1 (remove consumer-2 permission).
	// Use delete+recreate approach as recommended.
	mgmtClient := clientForCluster(t, mgmtClusterName)
	var current pmv1.ApplicationSpecificRole
	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      asrName,
	}, &current); err != nil {
		t.Fatalf("failed to get current ASR: %v", err)
	}

	// Update spec to only target consumer-1.
	current.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-1",
			Capability: "READ",
		},
	}
	if err := mgmtClient.Update(ctx, &current); err != nil {
		t.Fatalf("failed to update ASR to narrow targets: %v", err)
	}

	// Step 3: Wait for RBAC to be removed from consumer-2 (orphan cleanup).
	waitForClusterRoleAbsentByName(t, ctx, "consumer-2", "pm-"+asrName)

	// Step 4: Verify RBAC still exists on consumer-1 (not removed).
	assertClusterRoleByName(t, ctx, "consumer-1", "pm-"+asrName)
	assertClusterRoleBindingByName(t, ctx, "consumer-1", "pm-"+asrName)
}

// TestOffboarding_ASRDelete_FinalizerCleansAll verifies that when an ASR is
// deleted, the RBAC cleanup finalizer removes all provisioned RBAC resources
// from ALL targeted clusters before allowing the deletion to complete.
func TestOffboarding_ASRDelete_FinalizerCleansAll(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Controller logs:\n", controllerLogs(t))
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), resourcePropagationTimeout)
	defer cancel()

	const asrName = "offboard-finalizer-clean"

	// Step 1: Create ASR targeting consumer-1 and consumer-2 with cluster and namespace perms.
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      asrName,
			Namespace: controllerNamespace,
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "offboard-finalizer-clean-group",
			ConnectorRefs: []string{"e2e-k8s"},
			Permissions: []pmv1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-1",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-2",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-1/apps-prod",
					Capability: "WRITE",
				},
			},
		},
	}

	createASRObj(t, asr)

	// Wait for RBAC on both clusters.
	waitForClusterRoleByName(t, ctx, "consumer-1", "pm-"+asrName)
	waitForClusterRoleByName(t, ctx, "consumer-2", "pm-"+asrName)
	assertClusterRoleBindingByName(t, ctx, "consumer-1", "pm-"+asrName)
	assertClusterRoleBindingByName(t, ctx, "consumer-2", "pm-"+asrName)
	assertRoleBindingByName(t, ctx, "consumer-1", "apps-prod", "pm-"+asrName)

	// Step 2: Verify the finalizer is present on the ASR before deletion.
	mgmtClient := clientForCluster(t, mgmtClusterName)
	var current pmv1.ApplicationSpecificRole
	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      asrName,
	}, &current); err != nil {
		t.Fatalf("failed to get ASR: %v", err)
	}

	hasFinalizer := false
	for _, f := range current.Finalizers {
		if f == "permission-manager.cloudoperators.dev/kubernetes" {
			hasFinalizer = true
			break
		}
	}
	if !hasFinalizer {
		t.Fatal("expected RBAC cleanup finalizer to be present on ASR before deletion")
	}

	// Step 3: Delete the ASR gracefully (letting the controller's finalizer handle cleanup).
	deleteASRGraceful(t, asrName)

	// Step 4: Wait for RBAC to be removed from all clusters.
	waitForClusterRoleAbsentByName(t, ctx, "consumer-1", "pm-"+asrName)
	waitForClusterRoleAbsentByName(t, ctx, "consumer-2", "pm-"+asrName)
	waitForRoleAbsentByName(t, ctx, "consumer-1", "apps-prod", "pm-"+asrName)

	// Verify RoleBinding in apps-prod is also removed.
	assertRoleBindingAbsentByName(t, ctx, "consumer-1", "apps-prod", "pm-"+asrName)

	// Step 5: Verify the ASR itself is gone (finalizer allowed deletion to proceed).
	pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		err := mgmtClient.Get(ctx, types.NamespacedName{
			Namespace: controllerNamespace,
			Name:      asrName,
		}, &asr)
		return err != nil, nil // true when not found
	})

	// Final check: confirm no orphaned RBAC resources remain.
	assertClusterRoleAbsentByName(t, ctx, "consumer-1", "pm-"+asrName)
	assertClusterRoleAbsentByName(t, ctx, "consumer-2", "pm-"+asrName)
	assertRoleAbsentByName(t, ctx, "consumer-1", "apps-prod", "pm-"+asrName)
}
