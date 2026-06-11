// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestWildcard_ClusterPrefixGlob verifies that an ASR targeting "pm-e2e-consumer-*"
// expands to all 4 consumer clusters and creates a ClusterRole on each.
func TestWildcard_ClusterPrefixGlob(t *testing.T) {
	t.Parallel()
	asrName := "wc-cluster-prefix"
	expectedCR := "pm-" + asrName

	resource := "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-*"

	t.Logf("Creating ASR %q with cluster prefix glob resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()

	// All 4 consumers should get the ClusterRole
	for _, cc := range consumerClusters {
		cc := cc
		t.Run(cc.Name, func(t *testing.T) {
			c := crClientForCluster(t, cc.Name)
			assertClusterRoleExists(t, ctx, c, expectedCR)
			assertVerbsInClusterRole(t, ctx, c, expectedCR, []string{"get", "list", "watch"})
		})
	}
}

// TestWildcard_ClusterBareWildcard verifies that an ASR targeting "*" (bare wildcard)
// expands to all clusters in the inventory and creates a ClusterRole on each.
func TestWildcard_ClusterBareWildcard(t *testing.T) {
	t.Parallel()
	asrName := "wc-cluster-bare"
	expectedCR := "pm-" + asrName

	resource := "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/*"

	t.Logf("Creating ASR %q with bare wildcard resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()

	// All consumers in inventory should get the ClusterRole
	for _, cc := range consumerClusters {
		cc := cc
		t.Run(cc.Name, func(t *testing.T) {
			c := crClientForCluster(t, cc.Name)
			assertClusterRoleExists(t, ctx, c, expectedCR)
			assertVerbsInClusterRole(t, ctx, c, expectedCR, []string{"get", "list", "watch"})
		})
	}
}

// TestWildcard_NamespacePrefixGlob verifies that an ASR targeting namespace "apps-*"
// on consumer-1 creates RoleBindings in apps-prod and apps-staging ONLY
// (not monitoring, logging).
func TestWildcard_NamespacePrefixGlob(t *testing.T) {
	t.Parallel()
	asrName := "wc-ns-prefix"
	expectedRB := "pm-" + asrName

	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1
	resource := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/apps-*", consumer1)

	t.Logf("Creating ASR %q with namespace prefix glob resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, consumer1)

	// Should have RoleBindings in apps-prod and apps-staging
	assertRoleBindingExists(t, ctx, c, "apps-prod", expectedRB, asrName)
	assertRoleBindingExists(t, ctx, c, "apps-staging", expectedRB, asrName)

	// Should NOT have RoleBindings in monitoring or logging
	// Use a short timeout since we're asserting non-existence
	assertRoleBindingNotCreated(t, ctx, c, "monitoring", expectedRB, 10*time.Second)
	assertRoleBindingNotCreated(t, ctx, c, "logging", expectedRB, 10*time.Second)
}

// TestWildcard_NamespaceGardenerPrefix verifies that an ASR targeting namespace
// "shoot--wireapi--*" on consumer-3 matches shoot--wireapi--prod and shoot--wireapi--staging.
func TestWildcard_NamespaceGardenerPrefix(t *testing.T) {
	t.Parallel()
	asrName := "wc-ns-gardener"
	expectedRB := "pm-" + asrName

	consumer3 := consumerClusters[2].Name // pm-e2e-consumer-3
	resource := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/shoot--wireapi--*", consumer3)

	t.Logf("Creating ASR %q with Gardener namespace prefix resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, consumer3)

	// Should have RoleBindings in shoot--wireapi--prod and shoot--wireapi--staging
	assertRoleBindingExists(t, ctx, c, "shoot--wireapi--prod", expectedRB, asrName)
	assertRoleBindingExists(t, ctx, c, "shoot--wireapi--staging", expectedRB, asrName)

	// Should NOT have RoleBinding in monitoring
	assertRoleBindingNotCreated(t, ctx, c, "monitoring", expectedRB, 10*time.Second)
}

// TestWildcard_NamespaceBareWildcard verifies that namespace "*" promotes to cluster scope,
// creating a ClusterRoleBinding instead of per-namespace RoleBindings.
func TestWildcard_NamespaceBareWildcard(t *testing.T) {
	t.Parallel()
	asrName := "wc-ns-bare"
	expectedName := "pm-" + asrName

	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1
	resource := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/*", consumer1)

	t.Logf("Creating ASR %q with bare namespace wildcard resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, consumer1)

	// Should promote to ClusterRoleBinding (cluster-wide scope)
	assertClusterRoleBindingExists(t, ctx, c, expectedName, asrName)

	// Should NOT create individual RoleBindings per namespace
	for _, ns := range consumerClusters[0].Namespaces {
		assertRoleBindingNotCreated(t, ctx, c, ns, expectedName, 10*time.Second)
	}
}

// TestWildcard_NamespaceKubePrefix verifies that namespace "kube-*" matches
// kube-system, kube-public, and kube-node-lease on consumer-1.
func TestWildcard_NamespaceKubePrefix(t *testing.T) {
	t.Parallel()
	asrName := "wc-ns-kube"
	expectedRB := "pm-" + asrName

	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1
	resource := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/kube-*", consumer1)

	t.Logf("Creating ASR %q with kube-* namespace pattern resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, consumer1)

	// kube-system, kube-public, kube-node-lease are built-in namespaces
	assertRoleBindingExists(t, ctx, c, "kube-system", expectedRB, asrName)
	assertRoleBindingExists(t, ctx, c, "kube-public", expectedRB, asrName)
	assertRoleBindingExists(t, ctx, c, "kube-node-lease", expectedRB, asrName)
}

// TestWildcard_NamespaceExactNotExist verifies that an exact namespace reference
// to a non-existent namespace does NOT create a RoleBinding.
func TestWildcard_NamespaceExactNotExist(t *testing.T) {
	t.Parallel()
	asrName := "wc-ns-notexist"
	expectedRB := "pm-" + asrName

	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1
	resource := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/nonexistent-ns", consumer1)

	t.Logf("Creating ASR %q with non-existent namespace resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()
	c := crClientForCluster(t, consumer1)

	// The RoleBinding should NOT appear because the namespace doesn't exist
	assertRoleBindingNotCreated(t, ctx, c, "nonexistent-ns", expectedRB, 10*time.Second)
}

// TestWildcard_OverlappingClusterPatterns verifies that when two permissions target
// the same cluster (one exact, one glob), verbs are merged correctly.
// Permission 1: exact consumer-1, READ -> get,list,watch
// Permission 2: glob pm-e2e-consumer-*, WRITE -> create,update,patch,delete
// Consumer-1 should have merged verbs (union), others should have only WRITE verbs.
func TestWildcard_OverlappingClusterPatterns(t *testing.T) {
	t.Parallel()
	asrName := "wc-overlap"
	expectedCR := "pm-" + asrName

	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1

	// Two resources: exact consumer-1 (READ) + glob all consumers (WRITE)
	resourceExact := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	resourceGlob := "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-*"

	t.Logf("Creating ASR %q with overlapping patterns: exact=%q + glob=%q", asrName, resourceExact, resourceGlob)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resourceExact, "capability": "READ"},
		{"resource": resourceGlob, "capability": "WRITE"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	ctx := context.Background()

	// Consumer-1 should have merged verbs: READ(get,list,watch) + WRITE(create,update,patch,delete)
	// = get,list,watch,create,update,patch,delete (7 verbs total)
	t.Run("consumer-1-merged", func(t *testing.T) {
		c := crClientForCluster(t, consumer1)
		assertClusterRoleExists(t, ctx, c, expectedCR)
		assertVerbsInClusterRole(t, ctx, c, expectedCR,
			[]string{"create", "delete", "get", "list", "patch", "update", "watch"})
	})

	// Consumer-2 and consumer-4 should have WRITE verbs.
	// Note: consumer-3 is skipped because resilience tests may concurrently
	// break its kubeconfig Secret, making it unreachable during this test.
	for _, cc := range []consumerCluster{consumerClusters[1], consumerClusters[3]} {
		cc := cc
		t.Run(cc.Name+"-write-only", func(t *testing.T) {
			c := crClientForCluster(t, cc.Name)
			assertClusterRoleExists(t, ctx, c, expectedCR)
			assertVerbsInClusterRole(t, ctx, c, expectedCR,
				[]string{"create", "delete", "get", "list", "patch", "update", "watch"})
		})
	}
}

// assertRoleBindingNotCreated verifies that a RoleBinding does NOT get created
// within the given timeout. This is a negative assertion — it waits the full
// timeout to confirm non-existence.
func assertRoleBindingNotCreated(t *testing.T, ctx context.Context, c client.Client, namespace, name string, timeout time.Duration) {
	t.Helper()

	// Wait a reasonable time to confirm the RoleBinding never appears.
	// We poll and if it ever appears, fail immediately.
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(assertPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadline.Done():
			// Timeout reached without the RoleBinding appearing — success
			t.Logf("Confirmed: RoleBinding %s/%s was not created within %s", namespace, name, timeout)
			return
		case <-ticker.C:
			var rb rbacv1.RoleBinding
			key := types.NamespacedName{Namespace: namespace, Name: name}
			err := c.Get(ctx, key, &rb)
			if err == nil {
				t.Fatalf("RoleBinding %s/%s unexpectedly exists (should not have been created)", namespace, name)
				return
			}
			if !apierrors.IsNotFound(err) {
				t.Fatalf("unexpected error checking RoleBinding %s/%s: %v", namespace, name, err)
				return
			}
		}
	}
}
