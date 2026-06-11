// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// TestEdge_DriftCorrection_BindingSubjects verifies that manually changing the
// subject group on a ClusterRoleBinding from the correct spec.name value to an
// attacker-controlled group is corrected by the controller on reconcile.
func TestEdge_DriftCorrection_BindingSubjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-drift-crb-subject"
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

	// Wait for the ClusterRole and ClusterRoleBinding to appear.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)
	assertClusterRoleBindingExists(t, ctx, crClient, resourceName, asrName)

	// Inject drift: change the CRB subject to an attacker group.
	var crb rbacv1.ClusterRoleBinding
	err := crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &crb)
	if err != nil {
		t.Fatalf("failed to get ClusterRoleBinding for drift injection: %v", err)
	}
	crb.Subjects = []rbacv1.Subject{{
		Kind:     "Group",
		Name:     "attacker-group",
		APIGroup: "rbac.authorization.k8s.io",
	}}
	if err := crClient.Update(ctx, &crb); err != nil {
		t.Fatalf("failed to inject drift into ClusterRoleBinding subjects: %v", err)
	}

	// Force reconcile by annotating the ASR.
	forceReconcile(t, asrName)

	// Poll until the subject is corrected back to the ASR's spec.name.
	err = pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		var current rbacv1.ClusterRoleBinding
		if getErr := crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &current); getErr != nil {
			return false, getErr
		}
		for _, subj := range current.Subjects {
			if subj.Kind == "Group" && subj.Name == asrName {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("ClusterRoleBinding subject drift was not corrected within timeout: %v", err)
	}
}

// TestEdge_DriftCorrection_RoleBindingSubjects verifies that manually changing
// the subject group on a namespace-scoped RoleBinding from the correct spec.name
// value to an attacker-controlled group is corrected by the controller on reconcile.
func TestEdge_DriftCorrection_RoleBindingSubjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-drift-rb-subject"
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

	// Create ASR with namespace-scoped WRITE permission.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": namespaceCCRN(consumer.Name, targetNS), "capability": "WRITE"},
	})

	// Wait for the namespace-scoped Role and RoleBinding to appear.
	waitForRole(t, consumer.Name, resourceName, targetNS, resourcePropagationTimeout)
	assertRoleBindingExists(t, ctx, crClient, targetNS, resourceName, asrName)

	// Inject drift: change the RoleBinding subject to an attacker group.
	var rb rbacv1.RoleBinding
	err := crClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: targetNS}, &rb)
	if err != nil {
		t.Fatalf("failed to get RoleBinding for drift injection: %v", err)
	}
	rb.Subjects = []rbacv1.Subject{{
		Kind:     "Group",
		Name:     "attacker-group",
		APIGroup: "rbac.authorization.k8s.io",
	}}
	if err := crClient.Update(ctx, &rb); err != nil {
		t.Fatalf("failed to inject drift into RoleBinding subjects: %v", err)
	}

	// Force reconcile by annotating the ASR.
	forceReconcile(t, asrName)

	// Poll until the subject is corrected back to the ASR's spec.name.
	err = pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		var current rbacv1.RoleBinding
		if getErr := crClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: targetNS}, &current); getErr != nil {
			return false, getErr
		}
		for _, subj := range current.Subjects {
			if subj.Kind == "Group" && subj.Name == asrName {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("RoleBinding subject drift was not corrected within timeout: %v", err)
	}
}

// TestEdge_PartialDeletion_CRBOnly verifies that deleting only the
// ClusterRoleBinding (while leaving the ClusterRole intact) causes the
// controller to re-create the CRB with the correct subject on reconcile.
func TestEdge_PartialDeletion_CRBOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-partial-del-crb"
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

	// Wait for RBAC resources to appear.
	waitForClusterRole(t, consumer.Name, resourceName, resourcePropagationTimeout)
	assertClusterRoleExists(t, ctx, crClient, resourceName)
	assertClusterRoleBindingExists(t, ctx, crClient, resourceName, asrName)

	// Delete only the ClusterRoleBinding (leave ClusterRole intact).
	var crb rbacv1.ClusterRoleBinding
	err := crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &crb)
	if err != nil {
		t.Fatalf("failed to get ClusterRoleBinding for deletion: %v", err)
	}
	if err := crClient.Delete(ctx, &crb); err != nil {
		t.Fatalf("failed to delete ClusterRoleBinding: %v", err)
	}

	// Force reconcile by annotating the ASR.
	forceReconcile(t, asrName)

	// Poll until the ClusterRoleBinding is re-created with the correct subject.
	err = pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		var current rbacv1.ClusterRoleBinding
		if getErr := crClient.Get(ctx, types.NamespacedName{Name: resourceName}, &current); getErr != nil {
			return false, nil // not found yet, keep polling
		}
		for _, subj := range current.Subjects {
			if subj.Kind == "Group" && subj.Name == asrName {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("ClusterRoleBinding was not re-created with correct subject within timeout: %v", err)
	}

	// Verify the ClusterRole was never deleted (still exists).
	assertClusterRoleExists(t, ctx, crClient, resourceName)
}

// TestEdge_PartialDeletion_RoleBindingOnly verifies that deleting only the
// RoleBinding in a target namespace (while leaving the Role intact)
// causes the controller to re-create the RoleBinding on reconcile.
func TestEdge_PartialDeletion_RoleBindingOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-partial-del-rb"
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

	// Create ASR with namespace-scoped WRITE permission.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": namespaceCCRN(consumer.Name, targetNS), "capability": "WRITE"},
	})

	// Wait for RBAC resources to appear.
	waitForRole(t, consumer.Name, resourceName, targetNS, resourcePropagationTimeout)
	assertRoleExists(t, ctx, crClient, targetNS, resourceName)
	assertRoleBindingExists(t, ctx, crClient, targetNS, resourceName, asrName)

	// Delete only the RoleBinding (leave Role intact).
	var rb rbacv1.RoleBinding
	err := crClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: targetNS}, &rb)
	if err != nil {
		t.Fatalf("failed to get RoleBinding for deletion: %v", err)
	}
	if err := crClient.Delete(ctx, &rb); err != nil {
		t.Fatalf("failed to delete RoleBinding: %v", err)
	}

	// Force reconcile by annotating the ASR.
	forceReconcile(t, asrName)

	// Poll until the RoleBinding is re-created with the correct subject.
	err = pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		var current rbacv1.RoleBinding
		if getErr := crClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: targetNS}, &current); getErr != nil {
			return false, nil // not found yet, keep polling
		}
		for _, subj := range current.Subjects {
			if subj.Kind == "Group" && subj.Name == asrName {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("RoleBinding was not re-created with correct subject within timeout: %v", err)
	}

	// Verify the Role was never deleted (still exists).
	assertRoleExists(t, ctx, crClient, targetNS, resourceName)
}
