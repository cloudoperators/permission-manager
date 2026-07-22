// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// TestEdge_Wildcard_NamespaceDynamicCreation verifies that when a new namespace
// matching a wildcard pattern is created on a consumer cluster, a reconcile
// picks it up and creates the corresponding RoleBinding in the new namespace.
func TestEdge_Wildcard_NamespaceDynamicCreation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-wc-dynamic-ns"
	expectedRB := "pm-" + asrName

	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1
	clusterCtx := "kind-" + consumer1

	// Target apps-* on consumer-1 (pre-existing: apps-prod, apps-staging)
	resource := namespaceCCRN(consumer1, "apps-*")

	t.Logf("Creating ASR %q with namespace wildcard resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for RoleBindings in existing namespaces
	c := crClientForCluster(t, consumer1)
	assertRoleBindingExists(t, ctx, c, "apps-prod", expectedRB, asrName)
	assertRoleBindingExists(t, ctx, c, "apps-staging", expectedRB, asrName)

	// Create a new namespace that matches the pattern
	t.Log("Creating namespace apps-canary on consumer-1")
	runCmd(t, "kubectl", "--context", clusterCtx, "create", "namespace", "apps-canary")
	t.Cleanup(func() {
		runCmd(t, "kubectl", "--context", clusterCtx, "delete", "namespace", "apps-canary", "--ignore-not-found")
	})

	// Force reconcile so the controller re-evaluates namespace matches
	forceReconcile(t, asrName)

	// Verify the RoleBinding appears in apps-canary
	assertRoleBindingExists(t, ctx, c, "apps-canary", expectedRB, asrName)
	t.Log("SUCCESS: RoleBinding appeared in dynamically created namespace apps-canary")
}

// TestEdge_Wildcard_NamespaceDeleted verifies that when a namespace matching a
// wildcard pattern is deleted, the controller does not report an error status
// for that cluster. The ASR should still show Success.
func TestEdge_Wildcard_NamespaceDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-wc-ns-deleted"
	expectedRB := "pm-" + asrName

	consumer2 := consumerClusters[1].Name // pm-e2e-consumer-2 (has: apps-prod, apps-dev, monitoring)
	clusterCtx := "kind-" + consumer2

	// Target apps-* on consumer-2 (matches: apps-prod, apps-dev)
	resource := namespaceCCRN(consumer2, "apps-*")

	t.Logf("Creating ASR %q with namespace wildcard resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Verify RoleBindings exist in both matching namespaces
	c := crClientForCluster(t, consumer2)
	assertRoleBindingExists(t, ctx, c, "apps-prod", expectedRB, asrName)
	assertRoleBindingExists(t, ctx, c, "apps-dev", expectedRB, asrName)

	// Delete the apps-dev namespace
	t.Log("Deleting namespace apps-dev on consumer-2")
	runCmd(t, "kubectl", "--context", clusterCtx, "delete", "namespace", "apps-dev")
	t.Cleanup(func() {
		// Recreate apps-dev at the end to restore cluster state
		t.Log("Recreating namespace apps-dev on consumer-2")
		runCmd(t, "kubectl", "--context", clusterCtx, "create", "namespace", "apps-dev")
	})

	// Force reconcile so the controller re-evaluates
	forceReconcile(t, asrName)

	// Give the controller time to reconcile and update status
	time.Sleep(5 * time.Second)

	// Verify ASR still shows Success for consumer-2 (not Error)
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer2, "Success")

	// Verify RoleBinding still exists in apps-prod
	assertRoleBindingExists(t, ctx, c, "apps-prod", expectedRB, asrName)

	t.Log("SUCCESS: ASR status remains Success after matching namespace deletion")
}

// TestEdge_Wildcard_GlobMatchesZero verifies that when a wildcard pattern matches
// zero namespaces, no RBAC resources are created (no ClusterRole, no RoleBindings),
// and the ASR still reports Success for that cluster.
func TestEdge_Wildcard_GlobMatchesZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-wc-zero-match"
	expectedRB := "pm-" + asrName
	expectedCR := "pm-" + asrName

	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1

	// Target a pattern that matches nothing
	resource := namespaceCCRN(consumer1, "nonexistent-prefix-*")

	t.Logf("Creating ASR %q with non-matching wildcard resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	c := crClientForCluster(t, consumer1)

	// Verify ASR reports Success for consumer-1 (zero-match is not an error)
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	assertASRStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer1, "Success")

	// Verify no ClusterRole is created (zero matching namespaces means no verbs, so no ClusterRole)
	assertClusterRoleNotCreated(t, consumer1, expectedCR, connectorNegativeWait)

	// Verify no RoleBindings are created in any namespace on consumer-1
	for _, ns := range consumerClusters[0].Namespaces {
		assertRoleBindingNotCreated(t, ctx, c, ns, expectedRB, 10*time.Second)
	}

	t.Log("SUCCESS: Zero-match wildcard produces no RBAC resources and reports Success")
}

// TestEdge_Wildcard_OverlappingNamespacePatterns verifies that when two permissions
// target overlapping namespace patterns on the same cluster, verbs are merged in
// the ClusterRole and all matching namespaces get RoleBindings referencing it.
//
// Permission 1: namespace apps-* with READ (get, list, watch)
// Permission 2: namespace apps-prod with WRITE (create, update, patch, delete)
//
// Expected:
// - Role in apps-prod with merged verbs (7 verbs: READ + WRITE)
// - Role in apps-staging with READ verbs only (3 verbs)
// - RoleBinding in apps-prod referencing its Role
// - RoleBinding in apps-staging referencing its Role
// - Each namespace gets exactly its own verb set (no verb union leakage)
func TestEdge_Wildcard_OverlappingNamespacePatterns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-wc-overlap-ns"
	expectedRoleName := "pm-" + asrName // Role uses base name
	expectedRB := "pm-" + asrName       // RoleBinding uses base name

	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1

	// Two namespace-scoped permissions on the same cluster with overlapping patterns
	resourceGlob := namespaceCCRN(consumer1, "apps-*")
	resourceExact := namespaceCCRN(consumer1, "apps-prod")

	t.Logf("Creating ASR %q with overlapping namespace patterns: glob=%q (READ) + exact=%q (WRITE)",
		asrName, resourceGlob, resourceExact)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resourceGlob, "capability": "READ"},
		{"resource": resourceExact, "capability": "WRITE"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	c := crClientForCluster(t, consumer1)

	// Wait for namespace-scoped Role to appear in apps-prod with merged verbs
	waitForRole(t, consumer1, expectedRoleName, "apps-prod", resourcePropagationTimeout)

	// Verify the Role in apps-prod has all 7 merged verbs (READ + WRITE)
	allVerbs := []string{"create", "delete", "get", "list", "patch", "update", "watch"}
	assertVerbsInRole(t, ctx, c, "apps-prod", expectedRoleName, allVerbs)

	// Verify the Role in apps-staging has only 3 READ verbs
	readVerbs := []string{"get", "list", "watch"}
	assertVerbsInRole(t, ctx, c, "apps-staging", expectedRoleName, readVerbs)

	// Verify RoleBinding exists in apps-prod (matches both patterns)
	assertRoleBindingExists(t, ctx, c, "apps-prod", expectedRB, asrName)

	// Verify RoleBinding exists in apps-staging (matches apps-* only)
	assertRoleBindingExists(t, ctx, c, "apps-staging", expectedRB, asrName)

	// Verify no RoleBinding in monitoring (does not match either pattern)
	assertRoleBindingNotCreated(t, ctx, c, "monitoring", expectedRB, 10*time.Second)

	// Verify no RoleBinding in logging (does not match either pattern)
	assertRoleBindingNotCreated(t, ctx, c, "logging", expectedRB, 10*time.Second)

	t.Logf("SUCCESS: Overlapping namespace patterns merge verbs per-namespace correctly; "+
		"apps-prod gets %d verbs (READ+WRITE), apps-staging gets %d verbs (READ only)", len(allVerbs), len(readVerbs))
}
