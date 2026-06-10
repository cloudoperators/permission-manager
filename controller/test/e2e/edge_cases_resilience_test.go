//go:build e2e

package e2e

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
)

// TestEdge_Resilience_ControllerRestart creates an ASR with cluster-scoped READ
// on consumer-1, waits for the ClusterRole to appear, then kills the pm-controller
// pod (the Deployment will recreate it). After the new pod is ready, it force-
// reconciles and verifies that the ClusterRole still exists with correct verbs
// and the ASR status returns to Success.
func TestEdge_Resilience_ControllerRestart(t *testing.T) {
	ctx := context.Background()
	asrName := "edge-resilience-restart"
	consumer1 := consumerClusters[0].Name
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	kubeClient := kubeclientForCluster(t, consumer1)
	clients := map[string]kubernetes.Interface{consumer1: kubeClient}
	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, clients) })
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create ASR with cluster-scoped READ on consumer-1.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "READ"},
	})

	// Wait for ClusterRole to appear on consumer-1.
	waitForClusterRole(t, consumer1, expectedCR, resourcePropagationTimeout)
	t.Logf("ClusterRole %q confirmed on %s before controller restart", expectedCR, consumer1)

	// Kill the pm-controller pod (Deployment will recreate it).
	mgmtCtx := "kind-" + mgmtClusterName
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"delete", "pod", "-l", "app=pm-controller", "--force", "--grace-period=0")
	t.Log("Deleted pm-controller pod — waiting for new pod to become Running")

	// Wait for new pod to be ready.
	err := pollUntil(t, ctx, controllerReadyTimeout, pollInterval, func() (bool, error) {
		out, err := runCmdNoFail("kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
			"get", "pods", "-l", "app=pm-controller", "-o", "jsonpath={.items[0].status.phase}")
		if err != nil {
			return false, nil
		}
		return strings.TrimSpace(out) == "Running", nil
	})
	if err != nil {
		t.Fatalf("Controller pod did not become Running after restart: %v", err)
	}
	t.Log("New pm-controller pod is Running")

	// Force reconcile to ensure the controller picks up the ASR.
	forceReconcile(t, asrName)

	// Verify ClusterRole still exists and has correct verbs (get, list, watch for READ).
	crClient := crClientForCluster(t, consumer1)
	assertClusterRoleExists(t, ctx, crClient, expectedCR)
	assertVerbsInClusterRole(t, ctx, crClient, expectedCR, []string{"get", "list", "watch"})
	t.Logf("ClusterRole %q still correct after controller restart", expectedCR)

	// Verify ASR status returns to Success for consumer-1.
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")

	t.Log("SUCCESS: Controller restart preserved ClusterRole and ASR recovered to Success status")
}

// TestEdge_Resilience_FinalizerBlocksOnUnreachableCluster creates a broken cluster
// (invalid kubeconfig) alongside a real cluster (consumer-1), creates an ASR
// targeting both, then gracefully deletes the ASR. The controller's finalizer
// must HOLD (not remove) because the broken cluster is unreachable, preventing
// the ASR from being deleted. This is the security guarantee: no silent orphaning.
func TestEdge_Resilience_FinalizerBlocksOnUnreachableCluster(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-resilience-blocks"
	consumer1 := consumerClusters[0].Name
	brokenClusterName := "pm-e2e-broken-blocks"
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	kubeClient := kubeclientForCluster(t, consumer1)
	clients := map[string]kubernetes.Interface{consumer1: kubeClient}
	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, clients) })
	t.Cleanup(func() { deleteBrokenCluster(t, brokenClusterName) })
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create a broken cluster with invalid kubeconfig.
	createBrokenCluster(t, brokenClusterName)

	// Create ASR targeting BOTH consumer-1 (real) AND the broken cluster.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "READ"},
		{"resource": clusterCCRN(brokenClusterName), "capability": "READ"},
	})

	// Wait for ClusterRole to appear on consumer-1 (the reachable cluster).
	waitForClusterRole(t, consumer1, expectedCR, resourcePropagationTimeout)
	t.Logf("ClusterRole %q confirmed on %s", expectedCR, consumer1)

	// Gracefully delete the ASR (let controller's finalizer run).
	deleteASRGraceful(t, asrName)
	t.Log("Initiated graceful ASR deletion — finalizer should HOLD due to unreachable cluster")

	// Wait a reasonable time and verify the ASR is NOT deleted (finalizer held)
	time.Sleep(40 * time.Second)
	if !asrExists(t, asrName) {
		t.Fatal("ASR was deleted despite unreachable cluster — finalizer should have held")
	}
	t.Log("ASR still exists after 40s — finalizer correctly held")

	// Verify status shows broken cluster as "Orphaned"
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, brokenClusterName, "Orphaned")
	t.Log("ASR status correctly shows broken cluster as Orphaned")

	// Verify ClusterRole was cleaned from consumer-1 (reachable cluster gets cleaned)
	waitForClusterRoleAbsent(t, consumer1, expectedCR, resourcePropagationTimeout)

	t.Log("SUCCESS: Finalizer held on unreachable cluster; reachable cluster cleaned; Orphaned status reported")
}

// TestEdge_Resilience_OrphanCleanupAfterClusterReturns verifies exit path 1:
// When a broken cluster becomes reachable again, the controller completes cleanup
// and removes the finalizer, allowing ASR deletion to proceed.
func TestEdge_Resilience_OrphanCleanupAfterClusterReturns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-resilience-returns"
	consumer1 := consumerClusters[0].Name
	consumer3 := consumerClusters[2].Name
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	kubeClient := kubeclientForCluster(t, consumer1)
	clients := map[string]kubernetes.Interface{consumer1: kubeClient}
	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, clients) })
	t.Cleanup(func() { fixClusterSecret(t, consumer3) })
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create ASR targeting consumer-1 AND consumer-3.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "READ"},
		{"resource": clusterCCRN(consumer3), "capability": "READ"},
	})

	// Wait for ClusterRoles on both clusters.
	waitForClusterRole(t, consumer1, expectedCR, resourcePropagationTimeout)
	waitForClusterRole(t, consumer3, expectedCR, resourcePropagationTimeout)
	t.Logf("ClusterRoles confirmed on %s and %s", consumer1, consumer3)

	// Break consumer-3's kubeconfig.
	breakClusterSecret(t, consumer3)
	t.Log("Broke consumer-3 kubeconfig — cluster is now unreachable")

	// Gracefully delete the ASR.
	deleteASRGraceful(t, asrName)
	t.Log("Initiated graceful ASR deletion — finalizer should hold for unreachable consumer-3")

	// Verify ASR is still stuck after short wait.
	time.Sleep(40 * time.Second)
	if !asrExists(t, asrName) {
		t.Fatal("ASR was deleted despite consumer-3 being unreachable")
	}
	t.Log("ASR correctly held (consumer-3 unreachable)")

	// Verify consumer-1 ClusterRole was cleaned.
	waitForClusterRoleAbsent(t, consumer1, expectedCR, resourcePropagationTimeout)
	t.Log("consumer-1 ClusterRole cleaned while finalizer held")

	// Restore consumer-3's kubeconfig.
	fixClusterSecret(t, consumer3)
	t.Log("Fixed consumer-3 kubeconfig — cluster is reachable again")

	// Force reconcile to speed up detection.
	forceReconcile(t, asrName)

	// Verify ASR eventually completes deletion (cleanup succeeds on consumer-3).
	err := pollUntil(t, ctx, 2*resourcePropagationTimeout, pollInterval, func() (bool, error) {
		return !asrExists(t, asrName), nil
	})
	if err != nil {
		t.Fatalf("ASR was not deleted after consumer-3 was restored: %v", err)
	}
	t.Log("ASR deleted after consumer-3 became reachable again")

	// Verify ClusterRole cleaned from consumer-3.
	waitForClusterRoleAbsent(t, consumer3, expectedCR, resourcePropagationTimeout)

	t.Log("SUCCESS: Cluster returned, cleanup completed, ASR deleted")
}

// TestEdge_Resilience_GreenhouseClusterRemovedUnblocksDeletion verifies exit path 2:
// When a broken cluster's Greenhouse Cluster CR is deleted (cluster decommissioned),
// the controller recognizes the cluster as "gone" and removes the finalizer.
func TestEdge_Resilience_GreenhouseClusterRemovedUnblocksDeletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-resilience-removed"
	consumer1 := consumerClusters[0].Name
	brokenClusterName := "pm-e2e-broken-removed"
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	kubeClient := kubeclientForCluster(t, consumer1)
	clients := map[string]kubernetes.Interface{consumer1: kubeClient}
	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, clients) })
	t.Cleanup(func() { deleteBrokenCluster(t, brokenClusterName) })
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create broken cluster.
	createBrokenCluster(t, brokenClusterName)

	// Create ASR targeting consumer-1 AND broken cluster.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "READ"},
		{"resource": clusterCCRN(brokenClusterName), "capability": "READ"},
	})

	// Wait for ClusterRole on consumer-1.
	waitForClusterRole(t, consumer1, expectedCR, resourcePropagationTimeout)

	// Gracefully delete the ASR — finalizer will hold.
	deleteASRGraceful(t, asrName)
	t.Log("Initiated graceful ASR deletion — finalizer should hold")

	// Wait and verify ASR is stuck.
	time.Sleep(40 * time.Second)
	if !asrExists(t, asrName) {
		t.Fatal("ASR was deleted despite broken cluster being unreachable")
	}

	// Delete the broken Cluster CR from Greenhouse — this removes it from inventory.
	deleteBrokenCluster(t, brokenClusterName)
	t.Log("Deleted broken Cluster CR — cluster gone from Greenhouse inventory")

	// Force reconcile.
	forceReconcile(t, asrName)

	// Verify ASR is now deleted (cluster gone = not orphaned = finalizer released).
	err := pollUntil(t, ctx, 2*resourcePropagationTimeout, pollInterval, func() (bool, error) {
		return !asrExists(t, asrName), nil
	})
	if err != nil {
		t.Fatalf("ASR was not deleted after Greenhouse Cluster CR was removed: %v", err)
	}
	t.Log("ASR deleted after Greenhouse cluster removed from inventory")

	// Verify ClusterRole cleaned from consumer-1.
	waitForClusterRoleAbsent(t, consumer1, expectedCR, resourcePropagationTimeout)

	t.Log("SUCCESS: Greenhouse cluster removal unblocked ASR deletion")
}

// TestEdge_Resilience_AbandonCleanupAnnotation verifies exit path 3:
// When an operator sets the abandon-cleanup annotation on a stuck ASR, the
// controller removes the finalizer despite unreachable clusters.
func TestEdge_Resilience_AbandonCleanupAnnotation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-resilience-abandon"
	consumer1 := consumerClusters[0].Name
	brokenClusterName := "pm-e2e-broken-abandon"
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	kubeClient := kubeclientForCluster(t, consumer1)
	clients := map[string]kubernetes.Interface{consumer1: kubeClient}
	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, clients) })
	t.Cleanup(func() { deleteBrokenCluster(t, brokenClusterName) })
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create broken cluster.
	createBrokenCluster(t, brokenClusterName)

	// Create ASR targeting consumer-1 AND broken cluster.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "READ"},
		{"resource": clusterCCRN(brokenClusterName), "capability": "READ"},
	})

	// Wait for ClusterRole on consumer-1.
	waitForClusterRole(t, consumer1, expectedCR, resourcePropagationTimeout)

	// Gracefully delete the ASR — finalizer will hold.
	deleteASRGraceful(t, asrName)
	t.Log("Initiated graceful ASR deletion — finalizer should hold")

	// Wait and verify ASR is stuck.
	time.Sleep(40 * time.Second)
	if !asrExists(t, asrName) {
		t.Fatal("ASR was deleted despite broken cluster being unreachable")
	}

	// Set the abandon-cleanup annotation to force finalizer removal.
	mgmtCtx := "kind-" + mgmtClusterName
	abandonAnnotation := "permission-manager.cloudoperators.dev/abandon-cleanup"
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"annotate", "applicationspecificrole", asrName,
		fmt.Sprintf("%s=true", abandonAnnotation))
	t.Log("Set abandon-cleanup annotation — finalizer should be released")

	// The annotation update triggers a reconcile automatically via the watch.
	// Verify ASR is now deleted (annotation bypasses the hold).
	err := pollUntil(t, ctx, 2*resourcePropagationTimeout, pollInterval, func() (bool, error) {
		return !asrExists(t, asrName), nil
	})
	if err != nil {
		t.Fatalf("ASR was not deleted after abandon-cleanup annotation was set: %v", err)
	}
	t.Log("ASR deleted after abandon-cleanup annotation was set")

	// Verify ClusterRole cleaned from consumer-1.
	waitForClusterRoleAbsent(t, consumer1, expectedCR, resourcePropagationTimeout)

	t.Log("SUCCESS: Abandon annotation unblocked ASR deletion; reachable cluster cleaned")
}

// breakClusterSecret patches the kubeconfig Secret for a consumer cluster with
// invalid data, making the cluster unreachable to the controller.
func breakClusterSecret(t *testing.T, clusterName string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	// Patch the Secret's kubeconfig data with base64("invalid")
	patch := `[{"op":"replace","path":"/data/kubeconfig","value":"aW52YWxpZA=="}]`
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"patch", "secret", clusterName, "--type=json", "-p", patch)
	t.Logf("Broke kubeconfig Secret for cluster %q", clusterName)
}

// fixClusterSecret restores the kubeconfig Secret for a consumer cluster with
// the valid internal kubeconfig from the kind cluster.
func fixClusterSecret(t *testing.T, clusterName string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	// Get the valid internal kubeconfig from the kind cluster.
	kubeconfig := getInternalKubeconfig(t, clusterName)
	encoded := base64.StdEncoding.EncodeToString([]byte(kubeconfig))
	patch := fmt.Sprintf(`[{"op":"replace","path":"/data/kubeconfig","value":"%s"}]`, encoded)
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"patch", "secret", clusterName, "--type=json", "-p", patch)
	t.Logf("Restored kubeconfig Secret for cluster %q", clusterName)
}

// TestEdge_Resilience_RapidCreateDelete creates an ASR and immediately deletes
// it (gracefully) before the ClusterRole has time to appear. It verifies that
// the ASR eventually disappears (finalizer completes) and no orphaned ClusterRole
// persists on consumer-1.
func TestEdge_Resilience_RapidCreateDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-resilience-rapid"
	consumer1 := consumerClusters[0].Name
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	kubeClient := kubeclientForCluster(t, consumer1)
	clients := map[string]kubernetes.Interface{consumer1: kubeClient}
	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, clients) })
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create ASR with cluster-scoped READ on consumer-1.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "READ"},
	})

	// Immediately delete it gracefully (before ClusterRole appears).
	deleteASRGraceful(t, asrName)
	t.Log("Created and immediately deleted ASR (graceful) — testing rapid create/delete")

	// Verify ASR eventually disappears (finalizer completes even on rapid deletion).
	err := pollUntil(t, ctx, 2*resourcePropagationTimeout, pollInterval, func() (bool, error) {
		return !asrExists(t, asrName), nil
	})
	if err != nil {
		t.Fatalf("ASR %s was not deleted after rapid create/delete: %v", asrName, err)
	}
	t.Log("ASR deleted successfully after rapid create/delete")

	// Verify no orphaned ClusterRole persists on consumer-1.
	assertClusterRoleNotCreated(t, consumer1, expectedCR, connectorNegativeWait)

	t.Log("SUCCESS: Rapid create/delete completed cleanly with no orphaned ClusterRole")
}
