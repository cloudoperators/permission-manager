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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// unmanagedNegativeWait is how long we wait after a reconcile to confirm that
// unmanaged resources were NOT touched. Must be long enough for the controller
// to have completed its reconcile cycle.
const unmanagedNegativeWait = 15 * time.Second

// TestUnmanaged_Kubernetes_RBAC verifies that pre-existing ClusterRoles and
// ClusterRoleBindings on consumer clusters (without pm-controller ownership labels)
// are never modified or deleted during reconciliation.
func TestUnmanaged_Kubernetes_RBAC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	consumer := consumerClusters[0]
	crClient := crClientForCluster(t, consumer.Name)

	// --- Setup: create unmanaged RBAC resources on consumer-1 ---

	unmanagedCR := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "unmanaged-existing-role",
			// Deliberately NO app.kubernetes.io/managed-by label
			Labels: map[string]string{
				"team": "platform",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"get", "list"},
			},
		},
	}

	unmanagedCRB := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "unmanaged-existing-binding",
			// Deliberately NO pm-controller labels
			Labels: map[string]string{
				"team": "platform",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "unmanaged-existing-role",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:     "Group",
				Name:     "platform-engineers",
				APIGroup: "rbac.authorization.k8s.io",
			},
		},
	}

	if err := crClient.Create(ctx, unmanagedCR); err != nil {
		t.Fatalf("failed to create unmanaged ClusterRole: %v", err)
	}
	t.Cleanup(func() { _ = crClient.Delete(ctx, unmanagedCR) })

	if err := crClient.Create(ctx, unmanagedCRB); err != nil {
		t.Fatalf("failed to create unmanaged ClusterRoleBinding: %v", err)
	}
	t.Cleanup(func() { _ = crClient.Delete(ctx, unmanagedCRB) })

	// --- Action: create an ASR that targets the same consumer cluster ---

	asrName := "unmanaged-k8s-test"
	resource := clusterCCRN(consumer.Name)
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for the managed ClusterRole to appear (proves controller reconciled)
	managedResourceName := resourcePrefix + asrName
	waitForClusterRole(t, consumer.Name, managedResourceName, resourcePropagationTimeout)
	assertClusterRoleExists(t, ctx, crClient, managedResourceName)

	// --- Assertion: unmanaged resources must still exist with original spec ---

	// Verify unmanaged ClusterRole still exists with correct rules
	var cr rbacv1.ClusterRole
	if err := crClient.Get(ctx, types.NamespacedName{Name: "unmanaged-existing-role"}, &cr); err != nil {
		t.Fatalf("unmanaged ClusterRole was deleted or is inaccessible: %v", err)
	}
	if cr.Labels["team"] != "platform" {
		t.Errorf("unmanaged ClusterRole labels were modified: got %v", cr.Labels)
	}
	if len(cr.Rules) != 1 || cr.Rules[0].Resources[0] != "configmaps" {
		t.Errorf("unmanaged ClusterRole rules were modified: got %v", cr.Rules)
	}

	// Verify unmanaged ClusterRoleBinding still exists with correct spec
	var crb rbacv1.ClusterRoleBinding
	if err := crClient.Get(ctx, types.NamespacedName{Name: "unmanaged-existing-binding"}, &crb); err != nil {
		t.Fatalf("unmanaged ClusterRoleBinding was deleted or is inaccessible: %v", err)
	}
	if crb.RoleRef.Name != "unmanaged-existing-role" {
		t.Errorf("unmanaged ClusterRoleBinding RoleRef was modified: got %q", crb.RoleRef.Name)
	}
	if len(crb.Subjects) != 1 || crb.Subjects[0].Name != "platform-engineers" {
		t.Errorf("unmanaged ClusterRoleBinding subjects were modified: got %v", crb.Subjects)
	}

	// --- Additional: force another reconcile and verify again ---

	forceReconcile(t, asrName)
	time.Sleep(unmanagedNegativeWait)

	if err := crClient.Get(ctx, types.NamespacedName{Name: "unmanaged-existing-role"}, &cr); err != nil {
		t.Fatalf("unmanaged ClusterRole was deleted after force-reconcile: %v", err)
	}
	if err := crClient.Get(ctx, types.NamespacedName{Name: "unmanaged-existing-binding"}, &crb); err != nil {
		t.Fatalf("unmanaged ClusterRoleBinding was deleted after force-reconcile: %v", err)
	}

	// --- Verify that deleting the ASR does not touch unmanaged resources ---

	deleteASRGraceful(t, asrName)
	// Wait for managed resources to be cleaned up (proves finalizer ran)
	waitForClusterRoleAbsent(t, consumer.Name, managedResourceName, resourcePropagationTimeout)

	if err := crClient.Get(ctx, types.NamespacedName{Name: "unmanaged-existing-role"}, &cr); err != nil {
		t.Fatalf("unmanaged ClusterRole was deleted during ASR cleanup: %v", err)
	}
	if err := crClient.Get(ctx, types.NamespacedName{Name: "unmanaged-existing-binding"}, &crb); err != nil {
		t.Fatalf("unmanaged ClusterRoleBinding was deleted during ASR cleanup: %v", err)
	}

	t.Log("✓ Unmanaged RBAC resources survived full reconcile lifecycle")
}

// TestUnmanaged_GitHub_CRs verifies that pre-existing GithubTeam and
// GithubTeamRepository CRs (without pm-controller ownership labels) are never
// modified or deleted during reconciliation.
func TestUnmanaged_GitHub_CRs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	// --- Setup: create unmanaged GithubTeam CR ---

	unmanagedTeam := &unstructured.Unstructured{}
	unmanagedTeam.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "repo-guard.cloudoperators.dev",
		Version: "v1",
		Kind:    "GithubTeam",
	})
	unmanagedTeam.SetName("unmanaged-github-team")
	unmanagedTeam.SetNamespace(controllerNamespace)
	unmanagedTeam.SetLabels(map[string]string{
		"team": "security", // deliberately NOT the pm-controller managed-by label
	})
	_ = unstructured.SetNestedField(unmanagedTeam.Object, "my-org", "spec", "organization")
	_ = unstructured.SetNestedField(unmanagedTeam.Object, "security-team", "spec", "teamName")
	_ = unstructured.SetNestedField(unmanagedTeam.Object, "github.example.com", "spec", "instance")

	if err := mgmtClient.Create(ctx, unmanagedTeam); err != nil {
		t.Fatalf("failed to create unmanaged GithubTeam: %v", err)
	}
	t.Cleanup(func() { _ = mgmtClient.Delete(ctx, unmanagedTeam) })

	// --- Setup: create unmanaged GithubTeamRepository CR ---

	unmanagedRepo := &unstructured.Unstructured{}
	unmanagedRepo.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "repo-guard.cloudoperators.dev",
		Version: "v1",
		Kind:    "GithubTeamRepository",
	})
	unmanagedRepo.SetName("unmanaged-github-teamrepo")
	unmanagedRepo.SetNamespace(controllerNamespace)
	unmanagedRepo.SetLabels(map[string]string{
		"team": "security",
	})
	_ = unstructured.SetNestedField(unmanagedRepo.Object, "my-org", "spec", "organization")
	_ = unstructured.SetNestedField(unmanagedRepo.Object, "security-team", "spec", "teamName")
	_ = unstructured.SetNestedSlice(unmanagedRepo.Object, []interface{}{"my-repo"}, "spec", "repositories")
	_ = unstructured.SetNestedField(unmanagedRepo.Object, "github.example.com", "spec", "instance")
	_ = unstructured.SetNestedField(unmanagedRepo.Object, "push", "spec", "permission")

	if err := mgmtClient.Create(ctx, unmanagedRepo); err != nil {
		t.Fatalf("failed to create unmanaged GithubTeamRepository: %v", err)
	}
	t.Cleanup(func() { _ = mgmtClient.Delete(ctx, unmanagedRepo) })

	// --- Action: create an ASR that uses the github provider ---

	asrName := "unmanaged-gh-test"
	githubResource := "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/test-org/test-repo"
	createASR(t, asrName, []string{"test-github"}, []map[string]string{
		{"resource": githubResource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for the managed GithubTeam to appear (proves controller reconciled)
	// Naming convention: asr-<asrName>-<sanitizedInstance>-<org>
	managedTeamName := fmt.Sprintf("asr-%s-github-example-com-test-org", asrName)
	assertGithubTeamExists(t, ctx, mgmtClient, controllerNamespace, managedTeamName)

	// --- Assertion: unmanaged resources must still exist ---

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "repo-guard.cloudoperators.dev",
		Version: "v1",
		Kind:    "GithubTeam",
	})
	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      "unmanaged-github-team",
	}, obj); err != nil {
		t.Fatalf("unmanaged GithubTeam was deleted or inaccessible after reconcile: %v", err)
	}
	if obj.GetLabels()["team"] != "security" {
		t.Errorf("unmanaged GithubTeam labels were modified: got %v", obj.GetLabels())
	}

	repoObj := &unstructured.Unstructured{}
	repoObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "repo-guard.cloudoperators.dev",
		Version: "v1",
		Kind:    "GithubTeamRepository",
	})
	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      "unmanaged-github-teamrepo",
	}, repoObj); err != nil {
		t.Fatalf("unmanaged GithubTeamRepository was deleted or inaccessible after reconcile: %v", err)
	}

	// --- Verify again after ASR deletion ---

	deleteASRGraceful(t, asrName)
	// Wait for managed resources to be cleaned up (proves finalizer ran)
	assertGithubTeamAbsent(t, ctx, mgmtClient, controllerNamespace, managedTeamName)

	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      "unmanaged-github-team",
	}, obj); err != nil {
		t.Fatalf("unmanaged GithubTeam was deleted during ASR cleanup: %v", err)
	}
	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      "unmanaged-github-teamrepo",
	}, repoObj); err != nil {
		t.Fatalf("unmanaged GithubTeamRepository was deleted during ASR cleanup: %v", err)
	}

	t.Log("✓ Unmanaged GitHub CRs survived full reconcile lifecycle")
}

// TestUnmanaged_GreenhouseTeams_CRs verifies that pre-existing Team and
// TeamRoleBinding CRs (without pm-controller ownership labels) are never
// modified or deleted during reconciliation.
func TestUnmanaged_GreenhouseTeams_CRs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	// --- Setup: create unmanaged Team CR ---

	unmanagedTeam := &unstructured.Unstructured{}
	unmanagedTeam.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "greenhouse.sap",
		Version: "v1alpha1",
		Kind:    "Team",
	})
	unmanagedTeam.SetName("unmanaged-manual-team")
	unmanagedTeam.SetNamespace(controllerNamespace)
	unmanagedTeam.SetLabels(map[string]string{
		"team": "ops", // deliberately NOT the pm-controller managed-by labels
	})
	_ = unstructured.SetNestedField(unmanagedTeam.Object, "OPS-ENGINEERS", "spec", "mappedIdPGroup")

	if err := mgmtClient.Create(ctx, unmanagedTeam); err != nil {
		t.Fatalf("failed to create unmanaged Team: %v", err)
	}
	t.Cleanup(func() { _ = mgmtClient.Delete(ctx, unmanagedTeam) })

	// --- Setup: create unmanaged TeamRoleBinding CR ---

	unmanagedTRB := &unstructured.Unstructured{}
	unmanagedTRB.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "greenhouse.sap",
		Version: "v1alpha2",
		Kind:    "TeamRoleBinding",
	})
	unmanagedTRB.SetName("unmanaged-manual-trb")
	unmanagedTRB.SetNamespace(controllerNamespace)
	unmanagedTRB.SetLabels(map[string]string{
		"team": "ops",
	})
	_ = unstructured.SetNestedField(unmanagedTRB.Object, "cluster-admin", "spec", "teamRoleRef")
	_ = unstructured.SetNestedSlice(unmanagedTRB.Object, []interface{}{
		map[string]interface{}{"name": "unmanaged-manual-team", "namespace": controllerNamespace},
	}, "spec", "teamRefs")
	_ = unstructured.SetNestedMap(unmanagedTRB.Object, map[string]interface{}{
		"name": consumerClusters[0].Name,
	}, "spec", "clusterSelector")

	if err := mgmtClient.Create(ctx, unmanagedTRB); err != nil {
		t.Fatalf("failed to create unmanaged TeamRoleBinding: %v", err)
	}
	t.Cleanup(func() { _ = mgmtClient.Delete(ctx, unmanagedTRB) })

	// --- Action: create an ASR that uses the greenhouse-teams provider ---

	asrName := "unmanaged-ght-test"
	consumer1 := consumerClusters[0].Name
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)
	createASR(t, asrName, []string{"test-greenhouse-teams"}, []map[string]string{
		{"resource": resource, "capability": "read"},
	})
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Wait for the managed Team to appear (proves controller reconciled)
	managedTeamName := "pm-" + asrName
	assertTeamExists(t, ctx, mgmtClient, controllerNamespace, managedTeamName)

	// --- Assertion: unmanaged resources must still exist ---

	teamObj := &unstructured.Unstructured{}
	teamObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "greenhouse.sap",
		Version: "v1alpha1",
		Kind:    "Team",
	})
	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      "unmanaged-manual-team",
	}, teamObj); err != nil {
		t.Fatalf("unmanaged Team was deleted or inaccessible after reconcile: %v", err)
	}
	if teamObj.GetLabels()["team"] != "ops" {
		t.Errorf("unmanaged Team labels were modified: got %v", teamObj.GetLabels())
	}
	idpGroup, _, _ := unstructured.NestedString(teamObj.Object, "spec", "mappedIdPGroup")
	if idpGroup != "OPS-ENGINEERS" {
		t.Errorf("unmanaged Team spec was modified: mappedIdPGroup = %q, want %q", idpGroup, "OPS-ENGINEERS")
	}

	trbObj := &unstructured.Unstructured{}
	trbObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "greenhouse.sap",
		Version: "v1alpha2",
		Kind:    "TeamRoleBinding",
	})
	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      "unmanaged-manual-trb",
	}, trbObj); err != nil {
		t.Fatalf("unmanaged TeamRoleBinding was deleted or inaccessible after reconcile: %v", err)
	}
	trbTeamRoleRef, _, _ := unstructured.NestedString(trbObj.Object, "spec", "teamRoleRef")
	if trbTeamRoleRef != "cluster-admin" {
		t.Errorf("unmanaged TeamRoleBinding spec was modified: teamRoleRef = %q, want %q", trbTeamRoleRef, "cluster-admin")
	}

	// --- Additional: force another reconcile and verify again ---

	forceReconcile(t, asrName)
	time.Sleep(unmanagedNegativeWait)

	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      "unmanaged-manual-team",
	}, teamObj); err != nil {
		t.Fatalf("unmanaged Team was deleted after force-reconcile: %v", err)
	}
	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      "unmanaged-manual-trb",
	}, trbObj); err != nil {
		t.Fatalf("unmanaged TeamRoleBinding was deleted after force-reconcile: %v", err)
	}

	// --- Verify again after ASR deletion ---

	deleteASRGraceful(t, asrName)
	// Wait for managed Team to be cleaned up (proves finalizer ran)
	assertTeamAbsent(t, ctx, mgmtClient, controllerNamespace, managedTeamName)

	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      "unmanaged-manual-team",
	}, teamObj); err != nil {
		t.Fatalf("unmanaged Team was deleted during ASR cleanup: %v", err)
	}
	if err := mgmtClient.Get(ctx, types.NamespacedName{
		Namespace: controllerNamespace,
		Name:      "unmanaged-manual-trb",
	}, trbObj); err != nil {
		t.Fatalf("unmanaged TeamRoleBinding was deleted during ASR cleanup: %v", err)
	}

	t.Log("✓ Unmanaged Greenhouse Teams CRs survived full reconcile lifecycle")
}
