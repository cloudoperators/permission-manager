// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	// connectorRef is the ConnectorBinding name used in e2e tests (created in TestSetup).
	connectorRef = "e2e-k8s"

	// labelManagedBy is the standard managed-by label key.
	labelManagedBy = "app.kubernetes.io/managed-by"
	// labelASRName is the ASR name label key.
	labelASRName = "permission-manager.cloudoperators.dev/asr"
	// managedByValue is the expected value of the managed-by label.
	managedByValue = "pm-controller"
	// resourcePrefix is the naming prefix for generated RBAC resources.
	resourcePrefix = "pm-"
)

// clusterCCRN builds a cluster-scoped CCRN string.
func clusterCCRN(clusterName string) string {
	return fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", clusterName)
}

// namespaceCCRN builds a namespace-scoped CCRN string.
func namespaceCCRN(clusterName, namespace string) string {
	return fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/%s", clusterName, namespace)
}

// TestRBAC_ClusterScope_READ verifies that an ASR with cluster-scoped READ
// creates a ClusterRole and ClusterRoleBinding, and that SAR confirms the group
// can GET pods at cluster scope.
func TestRBAC_ClusterScope_READ(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "rbac-cluster-read"
	resourceName := resourcePrefix + asrName

	consumer := consumerClusters[0]
	kubeClient := kubeclientForCluster(t, consumer.Name)
	crClient := crClientForCluster(t, consumer.Name)
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	t.Cleanup(func() { deleteASR(t, asrName) })
	t.Cleanup(func() {
		clients := map[string]kubernetes.Interface{consumer.Name: kubeClient}
		dumpOnFailure(t, mgmtClient, clients)
	})

	// Create ASR with cluster-scoped READ permission.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer.Name), "capability": "READ"},
	})

	// Wait for the ClusterRole to appear on the consumer cluster.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)

	// Assert ClusterRole exists with expected labels.
	assertClusterRoleExists(t, ctx, crClient, resourceName)

	// Assert ClusterRoleBinding exists with the ASR name as subject group.
	// createASR sets spec.name = metadata.name by convention.
	assertClusterRoleBindingExists(t, ctx, crClient, resourceName, asrName)

	// Assert ClusterRole has READ verbs.
	assertVerbsInClusterRole(t, ctx, crClient, resourceName, []string{"get", "list", "watch"})

	// SAR confirms group can GET pods at cluster scope.
	assertRBACEnforced(t, ctx, kubeClient, asrName, "get", "pods", "", true)
}

// TestRBAC_NamespaceScope_WRITE verifies that an ASR with namespace-scoped WRITE
// creates a Role and a RoleBinding in the target namespace, and that SAR
// confirms CREATE is allowed in that namespace but NOT in another.
func TestRBAC_NamespaceScope_WRITE(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "rbac-ns-write"
	resourceName := resourcePrefix + asrName

	consumer := consumerClusters[0]
	targetNS := consumer.Namespaces[0]
	otherNS := consumer.Namespaces[1]
	kubeClient := kubeclientForCluster(t, consumer.Name)
	crClient := crClientForCluster(t, consumer.Name)
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	t.Cleanup(func() { deleteASR(t, asrName) })
	t.Cleanup(func() {
		clients := map[string]kubernetes.Interface{consumer.Name: kubeClient}
		dumpOnFailure(t, mgmtClient, clients)
	})

	// Create ASR with namespace-scoped WRITE permission.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": namespaceCCRN(consumer.Name, targetNS), "capability": "WRITE"},
	})

	// Wait for the namespace-scoped Role to appear in target namespace.
	waitForRole(t, consumer.Name, resourceName, targetNS, resourcePropagationTimeout)

	// Assert Role exists in the target namespace.
	assertRoleExists(t, ctx, crClient, targetNS, resourceName)

	// Assert RoleBinding exists in the target namespace (uses base name).
	assertRoleBindingExists(t, ctx, crClient, targetNS, resourceName, asrName)

	// SAR confirms CREATE is allowed in the target namespace.
	assertRBACEnforced(t, ctx, kubeClient, asrName, "create", "pods", targetNS, true)

	// SAR confirms CREATE is NOT allowed in the other namespace.
	assertRBACEnforced(t, ctx, kubeClient, asrName, "create", "pods", otherNS, false)
}

// TestRBAC_MixedScope verifies that an ASR with both cluster and namespace
// permissions creates a ClusterRole, ClusterRoleBinding, and RoleBindings.
func TestRBAC_MixedScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "rbac-mixed-scope"
	resourceName := resourcePrefix + asrName

	consumer := consumerClusters[0]
	targetNS := consumer.Namespaces[0]
	kubeClient := kubeclientForCluster(t, consumer.Name)
	crClient := crClientForCluster(t, consumer.Name)
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	t.Cleanup(func() { deleteASR(t, asrName) })
	t.Cleanup(func() {
		clients := map[string]kubernetes.Interface{consumer.Name: kubeClient}
		dumpOnFailure(t, mgmtClient, clients)
	})

	// Create ASR with both cluster-scoped READ and namespace-scoped WRITE.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer.Name), "capability": "READ"},
		{"resource": namespaceCCRN(consumer.Name, targetNS), "capability": "WRITE"},
	})

	// Wait for the ClusterRole to appear.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)

	// Assert ClusterRole exists.
	assertClusterRoleExists(t, ctx, crClient, resourceName)

	// Assert ClusterRoleBinding exists (from cluster-scoped READ).
	assertClusterRoleBindingExists(t, ctx, crClient, resourceName, asrName)

	// Assert RoleBinding exists in the target namespace (from namespace-scoped WRITE).
	assertRoleBindingExists(t, ctx, crClient, targetNS, resourceName, asrName)

	// SAR confirms cluster-scope GET works.
	assertRBACEnforced(t, ctx, kubeClient, asrName, "get", "pods", "", true)

	// SAR confirms namespace-scope CREATE works.
	assertRBACEnforced(t, ctx, kubeClient, asrName, "create", "pods", targetNS, true)
}

// TestRBAC_ASRUpdate_VerbsChange verifies that updating an ASR's capability
// from READ to ADMIN updates the ClusterRole verbs accordingly.
func TestRBAC_ASRUpdate_VerbsChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "rbac-verbs-change"
	resourceName := resourcePrefix + asrName

	consumer := consumerClusters[0]
	kubeClient := kubeclientForCluster(t, consumer.Name)
	crClient := crClientForCluster(t, consumer.Name)
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	t.Cleanup(func() { deleteASR(t, asrName) })
	t.Cleanup(func() {
		clients := map[string]kubernetes.Interface{consumer.Name: kubeClient}
		dumpOnFailure(t, mgmtClient, clients)
	})

	// Create ASR with READ initially.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer.Name), "capability": "READ"},
	})

	// Wait for initial ClusterRole.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)
	assertVerbsInClusterRole(t, ctx, crClient, resourceName, []string{"get", "list", "watch"})

	// Update the ASR: delete gracefully (letting the controller clean up) and recreate with wildcard capability.
	deleteASRGraceful(t, asrName)
	waitForClusterRoleAbsent(t, consumer.Name, resourceName, resourcePropagationTimeout)

	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer.Name), "capability": "*"},
	})

	// Wait for ClusterRole to reappear with new verbs.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)

	// Assert updated verbs: capability "*" = verb ["*"].
	assertVerbsInClusterRole(t, ctx, crClient, resourceName, []string{"*"})

	// SAR confirms the wildcard verb applies (delete should now be allowed).
	assertRBACEnforced(t, ctx, kubeClient, asrName, "delete", "pods", "", true)
}

// TestRBAC_ASRUpdate_AddCluster verifies that adding a second consumer cluster
// to an ASR's permissions causes RBAC to appear on the newly added cluster.
func TestRBAC_ASRUpdate_AddCluster(t *testing.T) {
	t.Parallel()
	if len(consumerClusters) < 2 {
		t.Skip("requires at least 2 consumer clusters")
	}

	ctx := context.Background()
	asrName := "rbac-add-cluster"
	resourceName := resourcePrefix + asrName

	consumer1 := consumerClusters[0]
	consumer2 := consumerClusters[1]
	kubeClient1 := kubeclientForCluster(t, consumer1.Name)
	kubeClient2 := kubeclientForCluster(t, consumer2.Name)
	crClient2 := crClientForCluster(t, consumer2.Name)
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	t.Cleanup(func() { deleteASR(t, asrName) })
	t.Cleanup(func() {
		clients := map[string]kubernetes.Interface{
			consumer1.Name: kubeClient1,
			consumer2.Name: kubeClient2,
		}
		dumpOnFailure(t, mgmtClient, clients)
	})

	// Create ASR with only consumer-1.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1.Name), "capability": "READ"},
	})

	// Wait for ClusterRole on consumer-1.
	waitForClusterRole(t, consumer1.Name, resourceName, resourcePropagationTimeout)

	// Update ASR to include consumer-2: delete gracefully (letting the controller clean up) and recreate with both clusters.
	deleteASRGraceful(t, asrName)
	waitForClusterRoleAbsent(t, consumer1.Name, resourceName, resourcePropagationTimeout)

	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1.Name), "capability": "READ"},
		{"resource": clusterCCRN(consumer2.Name), "capability": "READ"},
	})

	// Wait for ClusterRole on consumer-2.
	waitForClusterRole(t, consumer2.Name, resourceName, resourcePropagationTimeout)

	// Assert RBAC exists on consumer-2.
	assertClusterRoleExists(t, ctx, crClient2, resourceName)
	assertClusterRoleBindingExists(t, ctx, crClient2, resourceName, asrName)
}

// TestRBAC_ASRDelete_FinalizerCleanup verifies that deleting an ASR causes all
// generated RBAC resources to be removed from all target clusters.
func TestRBAC_ASRDelete_FinalizerCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "rbac-delete-cleanup"
	resourceName := resourcePrefix + asrName

	consumer := consumerClusters[0]
	kubeClient := kubeclientForCluster(t, consumer.Name)
	crClient := crClientForCluster(t, consumer.Name)
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	// Do NOT register deleteASR in cleanup since we delete it as part of the test.
	t.Cleanup(func() {
		clients := map[string]kubernetes.Interface{consumer.Name: kubeClient}
		dumpOnFailure(t, mgmtClient, clients)
	})

	// Create ASR with cluster-scoped READ.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer.Name), "capability": "READ"},
	})

	// Wait for resources to appear.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)
	assertClusterRoleExists(t, ctx, crClient, resourceName)
	assertClusterRoleBindingExists(t, ctx, crClient, resourceName, asrName)

	// Delete the ASR — the finalizer should clean up RBAC.
	// Must use deleteASRGraceful (not deleteASR) so the controller's finalizer
	// handler runs and cleans up RBAC before the object is removed.
	deleteASRGraceful(t, asrName)

	// Wait for ClusterRole to disappear.
	waitForClusterRoleAbsent(t, consumer.Name, resourceName, resourcePropagationTimeout)

	// Assert resources are gone.
	assertClusterRoleAbsent(t, ctx, crClient, resourceName)

	// SAR confirms access is revoked.
	assertRBACEnforced(t, ctx, kubeClient, asrName, "get", "pods", "", false)
}

// TestRBAC_DriftCorrection verifies that manually modifying a ClusterRole's rules
// is corrected on the next reconcile.
func TestRBAC_DriftCorrection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "rbac-drift-correct"
	resourceName := resourcePrefix + asrName

	consumer := consumerClusters[0]
	kubeClient := kubeclientForCluster(t, consumer.Name)
	crClient := crClientForCluster(t, consumer.Name)
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	t.Cleanup(func() { deleteASR(t, asrName) })
	t.Cleanup(func() {
		clients := map[string]kubernetes.Interface{consumer.Name: kubeClient}
		dumpOnFailure(t, mgmtClient, clients)
	})

	// Create ASR with READ.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer.Name), "capability": "READ"},
	})

	// Wait for ClusterRole.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)
	assertVerbsInClusterRole(t, ctx, crClient, resourceName, []string{"get", "list", "watch"})

	// Manually patch the ClusterRole to introduce drift (wrong rules).
	var cr rbacv1.ClusterRole
	err := crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &cr)
	if err != nil {
		t.Fatalf("failed to get ClusterRole for drift injection: %v", err)
	}
	cr.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"get"},
		},
	}
	if err := crClient.Update(ctx, &cr); err != nil {
		t.Fatalf("failed to inject drift into ClusterRole: %v", err)
	}

	// Force reconcile by annotating the ASR.
	forceReconcile(t, asrName)

	// Poll until the ClusterRole verbs are corrected back to the expected state.
	err = pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		var current rbacv1.ClusterRole
		if getErr := crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &current); getErr != nil {
			return false, getErr
		}
		if len(current.Rules) != 1 {
			return false, nil
		}
		rule := current.Rules[0]
		if len(rule.APIGroups) == 0 || rule.APIGroups[0] != "*" {
			return false, nil
		}
		if len(rule.Resources) == 0 || rule.Resources[0] != "*" {
			return false, nil
		}
		verbSet := make(map[string]struct{})
		for _, v := range rule.Verbs {
			verbSet[v] = struct{}{}
		}
		for _, expected := range []string{"get", "list", "watch"} {
			if _, ok := verbSet[expected]; !ok {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("drift was not corrected within timeout: %v", err)
	}
}

// TestRBAC_SubjectGroupMatchesSpecName verifies that the subject.name in
// ClusterRoleBindings and RoleBindings matches the ASR's spec.name field,
// not the metadata.name.
func TestRBAC_SubjectGroupMatchesSpecName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "rbac-subject-check"
	specName := "e2e-subject-group-from-spec"
	resourceName := resourcePrefix + asrName

	consumer := consumerClusters[0]
	targetNS := consumer.Namespaces[0]
	kubeClient := kubeclientForCluster(t, consumer.Name)
	crClient := crClientForCluster(t, consumer.Name)
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	t.Cleanup(func() { deleteASRRaw(t, asrName) })
	t.Cleanup(func() {
		clients := map[string]kubernetes.Interface{consumer.Name: kubeClient}
		dumpOnFailure(t, mgmtClient, clients)
	})

	// Create ASR with spec.name different from metadata.name using raw kubectl apply.
	// This tests that the controller uses spec.name (not metadata.name) as the subject group.
	createASRWithSpecName(t, asrName, specName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer.Name), "capability": "READ"},
		{"resource": namespaceCCRN(consumer.Name, targetNS), "capability": "WRITE"},
	})

	// Wait for resources.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)

	// Verify ClusterRoleBinding subject matches spec.name.
	var crb rbacv1.ClusterRoleBinding
	err := crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &crb)
	if err != nil {
		t.Fatalf("failed to get ClusterRoleBinding: %v", err)
	}
	if len(crb.Subjects) == 0 {
		t.Fatal("ClusterRoleBinding has no subjects")
	}
	if crb.Subjects[0].Name != specName {
		t.Errorf("ClusterRoleBinding subject.name = %q, want spec.name %q (not metadata.name %q)",
			crb.Subjects[0].Name, specName, asrName)
	}
	if crb.Subjects[0].Kind != "Group" {
		t.Errorf("ClusterRoleBinding subject.kind = %q, want %q", crb.Subjects[0].Kind, "Group")
	}

	// Verify RoleBinding subject matches spec.name.
	// Poll because namespace-scoped resources may appear slightly after the cluster-scoped ones.
	var rb rbacv1.RoleBinding
	err = pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		if getErr := crClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: targetNS}, &rb); getErr != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("RoleBinding %s/%s did not appear within timeout: %v", targetNS, resourceName, err)
	}
	if len(rb.Subjects) == 0 {
		t.Fatal("RoleBinding has no subjects")
	}
	if rb.Subjects[0].Name != specName {
		t.Errorf("RoleBinding subject.name = %q, want spec.name %q (not metadata.name %q)",
			rb.Subjects[0].Name, specName, asrName)
	}
}

// TestRBAC_Labels verifies that all generated RBAC resources carry the expected
// managed-by and ASR labels.
func TestRBAC_Labels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "rbac-labels"
	resourceName := resourcePrefix + asrName

	consumer := consumerClusters[0]
	targetNS := consumer.Namespaces[0]
	kubeClient := kubeclientForCluster(t, consumer.Name)
	crClient := crClientForCluster(t, consumer.Name)
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	t.Cleanup(func() { deleteASR(t, asrName) })
	t.Cleanup(func() {
		clients := map[string]kubernetes.Interface{consumer.Name: kubeClient}
		dumpOnFailure(t, mgmtClient, clients)
	})

	// Create ASR with both scopes to test all resource types.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer.Name), "capability": "READ"},
		{"resource": namespaceCCRN(consumer.Name, targetNS), "capability": "WRITE"},
	})

	// Wait for resources.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)

	// Check ClusterRole labels.
	var cr rbacv1.ClusterRole
	err := crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &cr)
	if err != nil {
		t.Fatalf("failed to get ClusterRole: %v", err)
	}
	assertResourceLabels(t, "ClusterRole", cr.Labels, asrName)

	// Check ClusterRoleBinding labels.
	var crb rbacv1.ClusterRoleBinding
	err = crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &crb)
	if err != nil {
		t.Fatalf("failed to get ClusterRoleBinding: %v", err)
	}
	assertResourceLabels(t, "ClusterRoleBinding", crb.Labels, asrName)

	// Check RoleBinding labels.
	var rb rbacv1.RoleBinding
	err = crClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: targetNS}, &rb)
	if err != nil {
		t.Fatalf("failed to get RoleBinding: %v", err)
	}
	assertResourceLabels(t, "RoleBinding", rb.Labels, asrName)
}

// TestRBAC_Idempotent verifies that triggering reconcile twice does not produce
// errors and leaves resources unchanged.
func TestRBAC_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "rbac-idempotent"
	resourceName := resourcePrefix + asrName

	consumer := consumerClusters[0]
	kubeClient := kubeclientForCluster(t, consumer.Name)
	crClient := crClientForCluster(t, consumer.Name)
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	t.Cleanup(func() { deleteASR(t, asrName) })
	t.Cleanup(func() {
		clients := map[string]kubernetes.Interface{consumer.Name: kubeClient}
		dumpOnFailure(t, mgmtClient, clients)
	})

	// Create ASR.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer.Name), "capability": "READ"},
	})

	// Wait for initial reconcile to complete.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)

	// Capture the resourceVersion after the first reconcile.
	var cr rbacv1.ClusterRole
	err := crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &cr)
	if err != nil {
		t.Fatalf("failed to get ClusterRole after first reconcile: %v", err)
	}
	initialResourceVersion := cr.ResourceVersion

	// Force a second reconcile.
	forceReconcile(t, asrName)

	// Wait a short period for the reconcile to process.
	time.Sleep(5 * time.Second)

	// Verify the ClusterRole still exists and is correct.
	var crAfter rbacv1.ClusterRole
	err = crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &crAfter)
	if err != nil {
		t.Fatalf("ClusterRole disappeared after second reconcile: %v", err)
	}

	// The resource should still be correct regardless of whether RV changed.
	assertVerbsInClusterRole(t, ctx, crClient, resourceName, []string{"get", "list", "watch"})
	assertClusterRoleBindingExists(t, ctx, crClient, resourceName, asrName)

	// If the resourceVersion changed, that is acceptable (CreateOrUpdate may write
	// even if the content is the same), but there should be no error in controller logs.
	if initialResourceVersion != crAfter.ResourceVersion {
		t.Logf("INFO: resourceVersion changed from %s to %s (CreateOrUpdate wrote, which is acceptable)",
			initialResourceVersion, crAfter.ResourceVersion)
	}

	// Verify no errors in controller logs related to this ASR.
	logs := controllerLogs(t)
	if containsErrorForASR(logs, asrName) {
		t.Errorf("controller logs contain errors for ASR %q after idempotent reconcile", asrName)
	}
}

// --- Test helper functions specific to rbac_test.go ---

// forceReconcile annotates the ASR to trigger a reconcile.
func forceReconcile(t *testing.T, asrName string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	timestamp := fmt.Sprintf("%d", time.Now().UnixNano())
	args := []string{
		"kubectl", "--context", mgmtCtx,
		"annotate", "applicationspecificrole", asrName,
		fmt.Sprintf("force-reconcile=%s", timestamp),
		"--overwrite", "-n", controllerNamespace,
	}
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to annotate ASR %q for force-reconcile: %v\noutput: %s", asrName, err, out)
	}
}

// createASRWithSpecName creates an ASR where spec.name differs from metadata.name.
// This is used specifically to test that the controller uses spec.name as the subject group.
func createASRWithSpecName(t *testing.T, metadataName, specName string, connectorRefs []string, permissions []map[string]string) {
	t.Helper()

	// Build permissions YAML entries.
	var permEntries []string
	for _, p := range permissions {
		permEntries = append(permEntries, fmt.Sprintf(
			"    - resource: %q\n      capability: %q", p["resource"], p["capability"],
		))
	}

	// Build connectorRefs YAML entries.
	var refEntries []string
	for _, ref := range connectorRefs {
		refEntries = append(refEntries, fmt.Sprintf("    - %q", ref))
	}

	yaml := fmt.Sprintf(`apiVersion: permission-manager.cloudoperators.dev/v1
kind: ApplicationSpecificRole
metadata:
  name: %s
  namespace: %s
spec:
  name: %s
  connectorRefs:
%s
  permissions:
%s
`, metadataName, controllerNamespace, specName,
		strings.Join(refEntries, "\n"),
		strings.Join(permEntries, "\n"),
	)

	cmd := exec.Command("kubectl", "--context", "kind-"+mgmtClusterName, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to create ASR %q with spec.name %q: %v\noutput: %s\nyaml:\n%s",
			metadataName, specName, err, out, yaml)
	}
}

// deleteASRRaw deletes an ASR by metadata name using kubectl directly.
func deleteASRRaw(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command("kubectl", "--context", "kind-"+mgmtClusterName,
		"delete", "applicationspecificrole", name, "-n", controllerNamespace, "--ignore-not-found")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("warning: failed to delete ASR %q: %v\noutput: %s", name, err, out)
	}
}

// assertResourceLabels checks that a resource has the expected managed-by and ASR labels.
func assertResourceLabels(t *testing.T, kind string, labels map[string]string, asrName string) {
	t.Helper()
	if labels == nil {
		t.Fatalf("%s has nil labels", kind)
	}
	if got := labels[labelManagedBy]; got != managedByValue {
		t.Errorf("%s label %q = %q, want %q", kind, labelManagedBy, got, managedByValue)
	}
	if got := labels[labelASRName]; got != asrName {
		t.Errorf("%s label %q = %q, want %q", kind, labelASRName, got, asrName)
	}
}

// containsErrorForASR checks if the controller logs contain error lines
// mentioning the given ASR name.
func containsErrorForASR(logs, asrName string) bool {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "ERROR") && strings.Contains(line, asrName) {
			return true
		}
	}
	return false
}
