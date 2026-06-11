// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

// TestOnboarding_NewClusterGetsRBAC verifies that when an ASR targeting a
// specific cluster is created, the controller provisions RBAC resources
// (ClusterRole + ClusterRoleBinding) on that cluster.
func TestOnboarding_NewClusterGetsRBAC(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Controller logs:\n", controllerLogs(t))
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), resourcePropagationTimeout)
	defer cancel()

	const asrName = "onboard-new-cluster"
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create ASR targeting consumer-1 with cluster-level READ permissions.
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      asrName,
			Namespace: controllerNamespace,
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "onboard-new-cluster-group",
			ConnectorRefs: []string{"e2e-k8s"},
			Permissions: []pmv1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-1",
					Capability: "READ",
				},
			},
		},
	}

	createASRObj(t, asr)

	// Wait for ClusterRole to appear on consumer-1.
	waitForClusterRoleByName(t, ctx, "consumer-1", "pm-"+asrName)

	// Verify ClusterRoleBinding also exists.
	assertClusterRoleBindingByName(t, ctx, "consumer-1", "pm-"+asrName)

	// Verify the ClusterRole has the expected READ verbs.
	cl := clientForCluster(t, "consumer-1")
	var cr rbacv1.ClusterRole
	if err := cl.Get(ctx, types.NamespacedName{Name: "pm-" + asrName}, &cr); err != nil {
		t.Fatalf("failed to get ClusterRole on consumer-1: %v", err)
	}

	if len(cr.Rules) == 0 {
		t.Fatal("expected at least one rule in ClusterRole")
	}

	expectedVerbs := map[string]bool{"get": true, "list": true, "watch": true}
	for _, verb := range cr.Rules[0].Verbs {
		if !expectedVerbs[verb] {
			t.Errorf("unexpected verb %q in ClusterRole rules", verb)
		}
	}
	for verb := range expectedVerbs {
		found := false
		for _, v := range cr.Rules[0].Verbs {
			if v == verb {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing expected verb %q in ClusterRole rules", verb)
		}
	}
}

// TestOnboarding_ExistingASRAppliedOnJoin verifies that when a new Cluster CR
// appears (simulating a cluster joining the fleet), existing ASRs targeting that
// cluster get their RBAC provisioned on the newly joined cluster.
// NOTE: Not parallel — mutates consumer-4 cluster state shared with other tests.
func TestOnboarding_ExistingASRAppliedOnJoin(t *testing.T) {
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Controller logs:\n", controllerLogs(t))
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), resourcePropagationTimeout)
	defer cancel()

	const asrName = "onboard-existing-asr"
	t.Cleanup(func() {
		deleteASR(t, asrName)
		// Ensure consumer-4 is restored for subsequent tests.
		recreateClusterCR(t, "consumer-4")
	})

	// Step 1: Delete consumer-4 Cluster CR to simulate it not yet being in the fleet.
	deleteClusterCR(t, "consumer-4")

	// Step 2: Create ASR targeting consumer-4 while the cluster is absent.
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      asrName,
			Namespace: controllerNamespace,
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "onboard-existing-asr-group",
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

	// Give the controller time to process the ASR — it should resolve no clusters.
	time.Sleep(5 * time.Second)

	// Step 3: Re-create consumer-4 Cluster CR to simulate it joining the fleet.
	recreateClusterCR(t, "consumer-4")

	// Step 4: Wait for RBAC to be provisioned on consumer-4 by the controller
	// reacting to the Cluster CR creation event.
	waitForClusterRoleByName(t, ctx, "consumer-4", "pm-"+asrName)
	assertClusterRoleBindingByName(t, ctx, "consumer-4", "pm-"+asrName)
}

// TestOnboarding_MultipleASRsSingleCluster verifies that two ASRs targeting
// the same cluster produce independent ClusterRoles that coexist on the cluster.
func TestOnboarding_MultipleASRsSingleCluster(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Controller logs:\n", controllerLogs(t))
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), resourcePropagationTimeout)
	defer cancel()

	const asrName1 = "onboard-multi-asr-1"
	const asrName2 = "onboard-multi-asr-2"
	t.Cleanup(func() {
		deleteASR(t, asrName1)
		deleteASR(t, asrName2)
	})

	// Create first ASR with READ targeting consumer-2.
	asr1 := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      asrName1,
			Namespace: controllerNamespace,
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "multi-asr-group-1",
			ConnectorRefs: []string{"e2e-k8s"},
			Permissions: []pmv1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-2",
					Capability: "READ",
				},
			},
		},
	}

	// Create second ASR with WRITE targeting consumer-2.
	asr2 := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      asrName2,
			Namespace: controllerNamespace,
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "multi-asr-group-2",
			ConnectorRefs: []string{"e2e-k8s"},
			Permissions: []pmv1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-2",
					Capability: "WRITE",
				},
			},
		},
	}

	createASRObj(t, asr1)
	createASRObj(t, asr2)

	// Wait for both ClusterRoles to appear on consumer-2.
	waitForClusterRoleByName(t, ctx, "consumer-2", "pm-"+asrName1)
	waitForClusterRoleByName(t, ctx, "consumer-2", "pm-"+asrName2)

	// Verify both ClusterRoleBindings exist independently.
	assertClusterRoleBindingByName(t, ctx, "consumer-2", "pm-"+asrName1)
	assertClusterRoleBindingByName(t, ctx, "consumer-2", "pm-"+asrName2)

	// Verify the two ClusterRoles have different verb sets.
	cl := clientForCluster(t, "consumer-2")

	var cr1 rbacv1.ClusterRole
	if err := cl.Get(ctx, types.NamespacedName{Name: "pm-" + asrName1}, &cr1); err != nil {
		t.Fatalf("failed to get ClusterRole for ASR 1: %v", err)
	}
	var cr2 rbacv1.ClusterRole
	if err := cl.Get(ctx, types.NamespacedName{Name: "pm-" + asrName2}, &cr2); err != nil {
		t.Fatalf("failed to get ClusterRole for ASR 2: %v", err)
	}

	if len(cr1.Rules) == 0 || len(cr2.Rules) == 0 {
		t.Fatal("expected rules in both ClusterRoles")
	}

	cr1Verbs := make(map[string]bool)
	for _, v := range cr1.Rules[0].Verbs {
		cr1Verbs[v] = true
	}
	cr2Verbs := make(map[string]bool)
	for _, v := range cr2.Rules[0].Verbs {
		cr2Verbs[v] = true
	}

	// READ verbs: get, list, watch
	if !cr1Verbs["get"] || !cr1Verbs["list"] || !cr1Verbs["watch"] {
		t.Errorf("ASR 1 ClusterRole missing expected READ verbs, got: %v", cr1.Rules[0].Verbs)
	}
	// WRITE verbs: create, update, patch, delete
	if !cr2Verbs["create"] || !cr2Verbs["update"] || !cr2Verbs["patch"] || !cr2Verbs["delete"] {
		t.Errorf("ASR 2 ClusterRole missing expected WRITE verbs, got: %v", cr2.Rules[0].Verbs)
	}
}

// TestOnboarding_ClusterWithNamespacePerms verifies that an ASR with namespace-
// scoped permissions creates a RoleBinding in the target namespace on the cluster,
// while still creating the ClusterRole at cluster scope.
func TestOnboarding_ClusterWithNamespacePerms(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Controller logs:\n", controllerLogs(t))
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), resourcePropagationTimeout)
	defer cancel()

	const asrName = "onboard-ns-perms"
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Create ASR with namespace-scoped READ permission for apps-prod on consumer-1.
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      asrName,
			Namespace: controllerNamespace,
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "onboard-ns-perms-group",
			ConnectorRefs: []string{"e2e-k8s"},
			Permissions: []pmv1.Permission{
				{
					Resource:   "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/pm-e2e-consumer-1/apps-prod",
					Capability: "READ",
				},
			},
		},
	}

	createASRObj(t, asr)

	// Wait for the namespace-scoped Role to appear in apps-prod.
	waitForRoleByName(t, ctx, "consumer-1", "apps-prod", "pm-"+asrName)

	// Verify the RoleBinding exists in the apps-prod namespace (uses base name).
	assertRoleBindingByName(t, ctx, "consumer-1", "apps-prod", "pm-"+asrName)

	// Verify the RoleBinding references the correct Role and subject.
	cl := clientForCluster(t, "consumer-1")
	var rb rbacv1.RoleBinding
	if err := cl.Get(ctx, types.NamespacedName{
		Namespace: "apps-prod",
		Name:      "pm-" + asrName,
	}, &rb); err != nil {
		t.Fatalf("failed to get RoleBinding in apps-prod: %v", err)
	}

	if rb.RoleRef.Kind != "Role" {
		t.Errorf("RoleBinding RoleRef.Kind = %q, want %q", rb.RoleRef.Kind, "Role")
	}
	if rb.RoleRef.Name != "pm-"+asrName {
		t.Errorf("RoleBinding RoleRef.Name = %q, want %q", rb.RoleRef.Name, "pm-"+asrName)
	}
	if len(rb.Subjects) == 0 {
		t.Fatal("expected at least one subject in RoleBinding")
	}
	if rb.Subjects[0].Kind != "Group" {
		t.Errorf("Subject.Kind = %q, want %q", rb.Subjects[0].Kind, "Group")
	}
	if rb.Subjects[0].Name != "onboard-ns-perms-group" {
		t.Errorf("Subject.Name = %q, want %q", rb.Subjects[0].Name, "onboard-ns-perms-group")
	}
}
