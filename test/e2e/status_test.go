// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// TestStatus_AllSuccess_ReadyTrue verifies that when all targeted clusters are
// reachable and RBAC is provisioned successfully, the ASR Ready condition is True
// and all target status entries show "Success".
func TestStatus_AllSuccess_ReadyTrue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "status-all-success"
	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for ClusterRole to appear (proves reconciliation happened)
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	// Verify using provider-abstraction assertion helpers
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	expectASRReady(t, ctx, mgmtClient, controllerNamespace, asrName, true)
	expectClusterStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")
	expectProviderReady(t, ctx, mgmtClient, controllerNamespace, asrName, "kubernetes", true)
}

// TestStatus_PartialFailure_ReadyFalse verifies that when one cluster has a bad
// kubeconfig, the ASR Ready condition is False.
func TestStatus_PartialFailure_ReadyFalse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "status-partial-fail"
	consumer1 := consumerClusters[0].Name
	brokenCluster := "pm-e2e-broken-partial"

	// Create a broken cluster (invalid kubeconfig)
	createBrokenCluster(t, brokenCluster)
	t.Cleanup(func() { deleteBrokenCluster(t, brokenCluster) })

	// Create ASR targeting both the real consumer-1 and the broken cluster
	resource1 := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	resourceBroken := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", brokenCluster)

	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource1, "capability": "READ"},
		{"resource": resourceBroken, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for the working cluster to get RBAC provisioned
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	// Verify Ready=False (because broken cluster failed)
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertASRReadyCondition(t, ctx, mgmtClient, controllerNamespace, asrName, false)

	// Verify consumer-1 still got Success
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")

	// Verify broken cluster shows Error or Unreachable
	assertASRStatusOneOf(t, ctx, mgmtClient, controllerNamespace, asrName, brokenCluster, []string{"Error", "Unreachable"})
}

// TestStatus_PropagationPerCluster verifies that each cluster targeted by an ASR
// has an independent target status entry.
func TestStatus_PropagationPerCluster(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "status-per-cluster"
	consumer1 := consumerClusters[0].Name
	consumer2 := consumerClusters[1].Name

	resource1 := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	resource2 := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer2)

	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource1, "capability": "READ"},
		{"resource": resource2, "capability": "WRITE"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for RBAC to appear on both clusters
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)
	waitForClusterRole(t, consumer2, "pm-"+asrName, resourcePropagationTimeout)

	// Verify each cluster has its own target status entry
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer2, "Success")
}

// TestStatus_LastUpdatedTimestamp verifies that the LastUpdated timestamp in
// target status entries is recent (within 60 seconds of now).
func TestStatus_LastUpdatedTimestamp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "status-timestamp"
	consumer1 := consumerClusters[0].Name

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	// Fetch ASR and check LastUpdated
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: controllerNamespace, Name: asrName}
		if err := mgmtClient.Get(ctx, key, &asr); err != nil {
			return false, err
		}

		k8sProvider, ok := asr.Status.Providers["kubernetes"]
		if !ok {
			return false, fmt.Errorf("no kubernetes provider in status")
		}

		for _, entry := range k8sProvider.Targets {
			if entry.TargetID == consumer1 {
				age := time.Since(entry.LastUpdated.Time)
				if age > 60*time.Second {
					return false, fmt.Errorf("LastUpdated is too old: %v (age %v)", entry.LastUpdated.Time, age)
				}
				return true, nil
			}
		}
		return false, fmt.Errorf("no target entry for cluster %s", consumer1)
	})
	if err != nil {
		t.Fatalf("TestStatus_LastUpdatedTimestamp: %v", err)
	}
}

// TestStatus_UnreachableCluster verifies that an ASR targeting a cluster with
// invalid kubeconfig shows "Unreachable" or "Error" in target status.
func TestStatus_UnreachableCluster(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "status-unreachable"
	brokenCluster := "pm-e2e-broken-unreach"

	// Create a broken cluster with invalid kubeconfig
	createBrokenCluster(t, brokenCluster)
	t.Cleanup(func() { deleteBrokenCluster(t, brokenCluster) })

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", brokenCluster)

	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for the controller to attempt reconciliation and report status
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertASRStatusOneOf(t, ctx, mgmtClient, controllerNamespace, asrName, brokenCluster, []string{"Error", "Unreachable"})
}

// TestStatus_RecoveryAfterError verifies that after fixing a broken cluster's
// kubeconfig, the ASR status transitions from error to Success and Ready=True.
func TestStatus_RecoveryAfterError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "status-recovery"
	brokenCluster := "pm-e2e-broken-recover"
	mgmtCtx := "kind-" + mgmtClusterName

	// Create a broken cluster with invalid kubeconfig
	createBrokenCluster(t, brokenCluster)
	t.Cleanup(func() { deleteBrokenCluster(t, brokenCluster) })

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", brokenCluster)

	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for error status
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertASRStatusOneOf(t, ctx, mgmtClient, controllerNamespace, asrName, brokenCluster, []string{"Error", "Unreachable"})

	// Fix the kubeconfig: replace the broken secret with a valid one
	// Use consumer-1's kubeconfig as a valid kubeconfig for the broken cluster name
	validKubeconfig := getInternalKubeconfig(t, consumerClusters[0].Name)

	// Update the Secret with valid kubeconfig data
	cmd := exec.Command("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"create", "secret", "generic", brokenCluster,
		"--from-literal=kubeconfig="+validKubeconfig,
		"--dry-run=client", "-o", "yaml")
	var yamlOut bytes.Buffer
	cmd.Stdout = &yamlOut
	cmd.Stderr = &yamlOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to generate fixed secret yaml: %v\n%s", err, yamlOut.String())
	}

	applyCmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(yamlOut.String())
	var applyOut bytes.Buffer
	applyCmd.Stdout = &applyOut
	applyCmd.Stderr = &applyOut
	if err := applyCmd.Run(); err != nil {
		t.Fatalf("failed to apply fixed kubeconfig secret: %v\n%s", err, applyOut.String())
	}

	// Trigger reconcile by annotating the ASR
	triggerReconcile(t, asrName)

	// Verify status transitions to Success
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, brokenCluster, "Success")
	assertASRReadyCondition(t, ctx, mgmtClient, controllerNamespace, asrName, true)
}

// TestStatus_OrphansRemovedFromStatus verifies that when a cluster is removed
// from an ASR's target list, its target status entry is removed.
func TestStatus_OrphansRemovedFromStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "status-orphan-remove"
	consumer1 := consumerClusters[0].Name
	consumer2 := consumerClusters[1].Name

	resource1 := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	resource2 := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer2)

	// Create ASR targeting both consumer-1 and consumer-2
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource1, "capability": "READ"},
		{"resource": resource2, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for RBAC on both clusters
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)
	waitForClusterRole(t, consumer2, "pm-"+asrName, resourcePropagationTimeout)

	// Verify both clusters appear in targets
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer2, "Success")

	// Update ASR to only target consumer-1
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource1, "capability": "READ"},
	})

	// Wait for consumer-2's RBAC to be removed (orphan cleanup)
	waitForClusterRoleAbsent(t, consumer2, "pm-"+asrName, resourcePropagationTimeout)

	// Verify consumer-2 entry disappears from targets
	err := pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: controllerNamespace, Name: asrName}
		if err := mgmtClient.Get(ctx, key, &asr); err != nil {
			return false, err
		}

		k8sProvider, ok := asr.Status.Providers["kubernetes"]
		if !ok {
			return false, fmt.Errorf("no kubernetes provider in status")
		}

		for _, entry := range k8sProvider.Targets {
			if entry.TargetID == consumer2 {
				return false, nil // Still present, keep polling
			}
		}
		return true, nil // Removed!
	})
	if err != nil {
		t.Fatalf("consumer-2 target entry was not removed after orphan cleanup: %v", err)
	}

	// Consumer-1 should still be Success
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")
}

// TestStatus_DriftReprovisioned verifies that if RBAC is manually deleted from
// a cluster, the next reconcile re-creates it and status returns to Success.
func TestStatus_DriftReprovisioned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "status-drift-reprov"
	consumer1 := consumerClusters[0].Name
	clusterCtx := "kind-" + consumer1

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for initial provisioning
	crName := "pm-" + asrName
	waitForClusterRole(t, consumer1, crName, resourcePropagationTimeout)

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")

	// Manually delete the ClusterRole to simulate drift
	runCmd(t, "kubectl", "--context", clusterCtx, "delete", "clusterrole", crName)

	// Verify it was deleted
	if clusterRoleExists(t, consumer1, crName) {
		t.Fatal("ClusterRole should have been deleted")
	}

	// Trigger reconcile
	triggerReconcile(t, asrName)

	// Wait for ClusterRole to reappear
	waitForClusterRole(t, consumer1, crName, resourcePropagationTimeout)

	// Verify status is still/back to Success
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")
}

// --- Helper functions for status tests ---

// createBrokenCluster creates a Cluster CR with an invalid kubeconfig Secret.
func createBrokenCluster(t *testing.T, clusterName string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	// Create Secret with invalid kubeconfig data
	secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
data:
  kubeconfig: aW52YWxpZA==`, clusterName, controllerNamespace) // base64("invalid")

	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(secretYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create broken secret for %s: %v\n%s", clusterName, err, out.String())
	}

	// Create Cluster CR pointing to the broken secret
	clusterYAML := fmt.Sprintf(`apiVersion: greenhouse.sap/v1alpha1
kind: Cluster
metadata:
  name: %s
  namespace: %s
spec:
  accessMode: direct`, clusterName, controllerNamespace)

	cmd = exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(clusterYAML)
	out.Reset()
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create broken Cluster CR %s: %v\n%s", clusterName, err, out.String())
	}

	// Patch status to set KubeConfigValid=True (controller trusts this)
	statusPatch := `{"status":{"statusConditions":{"conditions":[{"type":"KubeConfigValid","status":"True"}]}}}`
	runCmd(t, "kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"patch", "cluster.greenhouse.sap", clusterName,
		"--type=merge", "--subresource=status",
		"-p", statusPatch)

	t.Logf("Created broken Cluster CR %q with invalid kubeconfig", clusterName)
}

// deleteBrokenCluster removes the broken cluster's Secret and Cluster CR.
func deleteBrokenCluster(t *testing.T, clusterName string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"delete", "cluster.greenhouse.sap", clusterName, "--ignore-not-found")
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"delete", "secret", clusterName, "--ignore-not-found")
}

// triggerReconcile annotates the ASR to force the controller to reconcile it.
func triggerReconcile(t *testing.T, asrName string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	annotation := fmt.Sprintf(`{"metadata":{"annotations":{"e2e-trigger":"%d"}}}`, time.Now().UnixNano())
	runCmd(t, "kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"patch", "applicationspecificrole", asrName,
		"--type=merge", "-p", annotation)
}

// assertASRStatusOneOf verifies the target status entry for a cluster matches
// one of the expectedStatuses (e.g., ["Error", "Unreachable"]).
func assertASRStatusOneOf(t *testing.T, ctx context.Context, mgmtClient client.Client, namespace, asrName, clusterName string, expectedStatuses []string) {
	t.Helper()

	err := pollUntil(t, ctx, resourcePropagationTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: namespace, Name: asrName}
		if err := mgmtClient.Get(ctx, key, &asr); err != nil {
			return false, fmt.Errorf("failed to get ASR %s/%s: %w", namespace, asrName, err)
		}

		k8sProvider, ok := asr.Status.Providers["kubernetes"]
		if !ok {
			return false, fmt.Errorf("ASR %s has no kubernetes provider in status", asrName)
		}

		for _, entry := range k8sProvider.Targets {
			if entry.TargetID == clusterName {
				for _, expected := range expectedStatuses {
					if string(entry.Status) == expected {
						return true, nil
					}
				}
				return false, fmt.Errorf("ASR %s cluster %q status is %q, want one of %v",
					asrName, clusterName, entry.Status, expectedStatuses)
			}
		}
		return false, fmt.Errorf("ASR %s has no target entry for cluster %q (entries: %d)",
			asrName, clusterName, len(k8sProvider.Targets))
	})
	if err != nil {
		t.Fatalf("assertASRStatusOneOf(%s/%s, cluster=%q, expected=%v): %v",
			namespace, asrName, clusterName, expectedStatuses, err)
	}
}
