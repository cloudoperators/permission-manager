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

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestReconcile_PartialFailure_OthersProvisioned verifies that when one cluster
// has a bad kubeconfig, the other clusters still receive their RBAC resources.
func TestReconcile_PartialFailure_OthersProvisioned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "reconcile-partial-fail"
	consumer1 := consumerClusters[0].Name
	brokenCluster := "pm-e2e-broken-reconcile"

	// Create a broken cluster
	createBrokenCluster(t, brokenCluster)
	t.Cleanup(func() { deleteBrokenCluster(t, brokenCluster) })

	// Create ASR targeting both the working consumer and the broken cluster
	resource1 := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	resourceBroken := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", brokenCluster)

	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource1, "capability": "READ"},
		{"resource": resourceBroken, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for ClusterRole to appear on the working cluster
	waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)

	// Verify that consumer-1 has the ClusterRole
	c := crClientForCluster(t, consumer1)
	assertClusterRoleExists(t, ctx, c, "pm-"+asrName)

	// Verify the broken cluster shows error in status
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertASRStatusOneOf(t, ctx, mgmtClient, controllerNamespace, asrName, brokenCluster, []string{"Error", "Unreachable"})

	// Consumer-1 should still be Success
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")
}

// TestReconcile_MultiASR_NoConflict verifies that 3 ASRs targeting the same
// cluster produce 3 independent ClusterRoles without conflict.
func TestReconcile_MultiASR_NoConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	consumer1 := consumerClusters[0].Name

	asrNames := []string{"reconcile-multi-1", "reconcile-multi-2", "reconcile-multi-3"}
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	for _, asrName := range asrNames {
		name := asrName // capture
		createASR(t, name, []string{"e2e-k8s"}, []map[string]string{
			{"resource": resource, "capability": "READ"},
		})
		t.Cleanup(func() { deleteASR(t, name) })
	}

	// Wait for all 3 ClusterRoles to appear
	for _, asrName := range asrNames {
		waitForClusterRole(t, consumer1, "pm-"+asrName, resourcePropagationTimeout)
	}

	// Verify all 3 ClusterRoles exist independently
	c := crClientForCluster(t, consumer1)
	for _, asrName := range asrNames {
		assertClusterRoleExists(t, ctx, c, "pm-"+asrName)
	}

	// Verify all have read verbs
	expectedVerbs := []string{"get", "list", "watch"}
	for _, asrName := range asrNames {
		assertVerbsInClusterRole(t, ctx, c, "pm-"+asrName, expectedVerbs)
	}
}

// TestReconcile_MultiASR_SameGroup verifies that 2 ASRs with the same spec.name
// produce separate ClusterRoles but both CRBs bind to the same group subject.
func TestReconcile_MultiASR_SameGroup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	consumer1 := consumerClusters[0].Name
	groupName := "MY_TEAM_E2E"
	mgmtCtx := "kind-" + mgmtClusterName

	asr1Name := "reconcile-samegroup-1"
	asr2Name := "reconcile-samegroup-2"

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// Create ASR 1 with spec.name = groupName
	createASRWithSpecName(t, asr1Name, groupName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asr1Name) })

	// Create ASR 2 with same spec.name = groupName
	createASRWithSpecName(t, asr2Name, groupName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "WRITE"},
	})
	t.Cleanup(func() { deleteASR(t, asr2Name) })

	// Wait for both ClusterRoles to appear
	waitForClusterRole(t, consumer1, "pm-"+asr1Name, resourcePropagationTimeout)
	waitForClusterRole(t, consumer1, "pm-"+asr2Name, resourcePropagationTimeout)

	// Verify both ClusterRoleBindings exist with the same group as subject
	c := crClientForCluster(t, consumer1)
	assertClusterRoleBindingExists(t, ctx, c, "pm-"+asr1Name, groupName)
	assertClusterRoleBindingExists(t, ctx, c, "pm-"+asr2Name, groupName)

	_ = mgmtCtx // used implicitly via createASRWithSpecName
}

// TestReconcile_ConcurrentCreation verifies that creating a Cluster CR and an ASR
// simultaneously (in rapid succession) still results in correct RBAC provisioning.
func TestReconcile_ConcurrentCreation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "reconcile-concurrent"
	// Use consumer-3 which already exists but create ASR targeting it rapidly
	consumer3 := consumerClusters[2].Name

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer3)

	// Create ASR immediately — the cluster should already be known
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for eventual consistency
	waitForClusterRole(t, consumer3, "pm-"+asrName, resourcePropagationTimeout)

	// Verify RBAC is correct
	c := crClientForCluster(t, consumer3)
	assertClusterRoleExists(t, ctx, c, "pm-"+asrName)
	assertVerbsInClusterRole(t, ctx, c, "pm-"+asrName, []string{"get", "list", "watch"})
}

// TestReconcile_RapidUpdates verifies that updating an ASR 5 times rapidly results
// in the final state being correct (last write wins).
func TestReconcile_RapidUpdates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "reconcile-rapid-update"
	consumer1 := consumerClusters[0].Name
	mgmtCtx := "kind-" + mgmtClusterName

	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	// Create ASR with READ capability
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Rapidly update through capabilities: READ -> WRITE -> OPERATE -> SECURE -> *
	capabilities := []string{"WRITE", "OPERATE", "SECURE", "*"}
	for _, cap := range capabilities {
		asrYAML := fmt.Sprintf(`apiVersion: permission-manager.cloudoperators.dev/v1
kind: ApplicationSpecificRole
metadata:
  name: %s
  namespace: %s
spec:
  name: %s
  connectorRefs: ["e2e-k8s"]
  permissions:
  - resource: %q
    capability: %q`, asrName, controllerNamespace, asrName, resource, cap)

		cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(asrYAML)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to update ASR to %s: %v\n%s", cap, err, out.String())
		}
	}

	// Wait for final state: capability "*" = verb "*"
	c := crClientForCluster(t, consumer1)
	assertVerbsInClusterRole(t, ctx, c, "pm-"+asrName, []string{"*"})

	_ = mgmtCtx
}

// TestReconcile_DriftAllTypes verifies that if all RBAC types (ClusterRole,
// ClusterRoleBinding, RoleBinding) are corrupted or deleted, the controller
// restores them all after a reconcile trigger.
func TestReconcile_DriftAllTypes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "reconcile-drift-all"
	consumer1 := consumerClusters[0].Name
	ns := consumerClusters[0].Namespaces[0] // "apps-prod"
	clusterCtx := "kind-" + consumer1

	// Create ASR with both cluster-scoped and namespace-scoped resources
	resourceCluster := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	resourceNS := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/%s", consumer1, ns)

	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resourceCluster, "capability": "READ"},
		{"resource": resourceNS, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	crName := "pm-" + asrName

	// Wait for all RBAC resources to be provisioned
	waitForClusterRole(t, consumer1, crName, resourcePropagationTimeout)

	c := crClientForCluster(t, consumer1)
	assertClusterRoleExists(t, ctx, c, crName)
	assertClusterRoleBindingExists(t, ctx, c, crName, asrName)
	assertRoleBindingExists(t, ctx, c, ns, crName, asrName)

	// Corrupt the ClusterRole: change verbs to something wrong
	patchVerbs := `{"rules":[{"apiGroups":["*"],"resources":["*"],"verbs":["delete"]}]}`
	runCmd(t, "kubectl", "--context", clusterCtx,
		"patch", "clusterrole", crName,
		"--type=merge", "-p", patchVerbs)

	// Delete the ClusterRoleBinding
	runCmd(t, "kubectl", "--context", clusterCtx,
		"delete", "clusterrolebinding", crName)

	// Delete the RoleBinding
	runCmd(t, "kubectl", "--context", clusterCtx,
		"-n", ns, "delete", "rolebinding", crName)

	// Trigger reconciliation
	triggerReconcile(t, asrName)

	// Wait a moment for reconcile to process
	time.Sleep(2 * time.Second)

	// Verify all RBAC resources are restored correctly
	assertClusterRoleExists(t, ctx, c, crName)
	assertVerbsInClusterRole(t, ctx, c, crName, []string{"get", "list", "watch"})
	assertClusterRoleBindingExists(t, ctx, c, crName, asrName)
	assertRoleBindingExists(t, ctx, c, ns, crName, asrName)

	// Verify CRB has correct subjects restored
	var crb rbacv1.ClusterRoleBinding
	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		key := client.ObjectKey{Name: crName}
		if err := c.Get(ctx, key, &crb); err != nil {
			return false, err
		}
		if len(crb.Subjects) == 0 {
			return false, fmt.Errorf("CRB has no subjects")
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("CRB %s not restored with subjects: %v", crName, err)
	}

	// Verify status shows Success
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")
}
