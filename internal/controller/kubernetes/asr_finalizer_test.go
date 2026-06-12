// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/pkg/audit"
)

func TestFinalizerConstants(t *testing.T) {
	// Verify the legacy constant is still available (for migration detection)
	if FinalizerRBACCleanup != "permission-manager.cloudoperators.dev/rbac-cleanup" {
		t.Errorf("FinalizerRBACCleanup = %q, want %q", FinalizerRBACCleanup, "permission-manager.cloudoperators.dev/rbac-cleanup")
	}

	// Verify the new per-provider finalizer
	if FinalizerKubernetes != "permission-manager.cloudoperators.dev/kubernetes" {
		t.Errorf("FinalizerKubernetes = %q, want %q", FinalizerKubernetes, "permission-manager.cloudoperators.dev/kubernetes")
	}
}

func TestReconcile_AddsNewFinalizerOnFreshCreate(t *testing.T) {
	scheme := newTestScheme()

	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu": prodClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")

	// ASR with NO finalizers (fresh create)
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "test-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister:      &StaticNamespaceLister{Namespaces: map[string][]string{"prod-eu": {}}},
	}

	ctx := context.Background()
	_, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify the NEW finalizer was added
	var updatedASR v1alpha1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updatedASR)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	if !controllerutil.ContainsFinalizer(&updatedASR, FinalizerKubernetes) {
		t.Error("expected new per-provider finalizer to be added to ASR")
	}
	if controllerutil.ContainsFinalizer(&updatedASR, FinalizerRBACCleanup) {
		t.Error("expected old legacy finalizer to NOT be present on fresh create")
	}
}

func TestReconcile_MigratesOldFinalizerToNew(t *testing.T) {
	scheme := newTestScheme()

	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu": prodClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")

	// ASR with OLD finalizer only (pre-migration)
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-asr",
			Namespace:  "pm-system",
			Finalizers: []string{FinalizerRBACCleanup},
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "test-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister:      &StaticNamespaceLister{Namespaces: map[string][]string{"prod-eu": {}}},
	}

	ctx := context.Background()
	_, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify migration happened: old removed, new added
	var updatedASR v1alpha1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updatedASR)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	if !controllerutil.ContainsFinalizer(&updatedASR, FinalizerKubernetes) {
		t.Error("expected new per-provider finalizer after migration")
	}
	if controllerutil.ContainsFinalizer(&updatedASR, FinalizerRBACCleanup) {
		t.Error("expected old legacy finalizer to be removed after migration")
	}
}

func TestReconcile_AlreadyMigratedNoChange(t *testing.T) {
	scheme := newTestScheme()

	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu": prodClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")

	// ASR already has NEW finalizer (already migrated)
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-asr",
			Namespace:  "pm-system",
			Finalizers: []string{FinalizerKubernetes},
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "test-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
	}

	ctx := context.Background()
	_, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify the finalizer is still the new one only
	var updatedASR v1alpha1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updatedASR)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	if !controllerutil.ContainsFinalizer(&updatedASR, FinalizerKubernetes) {
		t.Error("expected new per-provider finalizer to remain")
	}
	if controllerutil.ContainsFinalizer(&updatedASR, FinalizerRBACCleanup) {
		t.Error("old finalizer should not be added back")
	}

	// Verify only one finalizer exists
	finalizerCount := 0
	for _, f := range updatedASR.Finalizers {
		if f == FinalizerKubernetes {
			finalizerCount++
		}
	}
	if finalizerCount != 1 {
		t.Errorf("expected exactly 1 new finalizer, got %d", finalizerCount)
	}
}

func TestReconcile_DeletionWithNewFinalizer(t *testing.T) {
	scheme := newTestScheme()

	// Pre-create RBAC resources on two clusters that should be cleaned up
	prodCR := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pm-team-access",
			Labels: map[string]string{
				LabelManagedBy: ManagedByValue,
				LabelASRName:   "team-access",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"get", "list", "watch"}},
		},
	}
	prodCRB := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pm-team-access",
			Labels: map[string]string{
				LabelManagedBy: ManagedByValue,
				LabelASRName:   "team-access",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "pm-team-access",
		},
	}

	stagingCR := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pm-team-access",
			Labels: map[string]string{
				LabelManagedBy: ManagedByValue,
				LabelASRName:   "team-access",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"create", "update"}},
		},
	}
	stagingRB := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-team-access",
			Namespace: "default",
			Labels: map[string]string{
				LabelManagedBy: ManagedByValue,
				LabelASRName:   "team-access",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "pm-team-access",
		},
	}

	prodClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(prodCR, prodCRB).Build()
	stagingClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stagingCR, stagingRB).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu":    prodClient,
			"staging-us": stagingClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")
	clusterInv.Update("staging-us")

	// ASR with deletionTimestamp set and NEW finalizer present
	now := metav1.Now()
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "team-access",
			Namespace:         "pm-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerKubernetes},
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "my-team",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister: &StaticNamespaceLister{
			Namespaces: map[string][]string{
				"prod-eu":    {"default"},
				"staging-us": {"default"},
			},
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "team-access"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue: %v", result.RequeueAfter)
	}

	// Verify ClusterRole deleted on prod
	var cr rbacv1.ClusterRole
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-team-access"}, &cr)
	if err == nil {
		t.Error("expected ClusterRole to be deleted on prod-eu")
	}

	// Verify ClusterRoleBinding deleted on prod
	var crb rbacv1.ClusterRoleBinding
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-team-access"}, &crb)
	if err == nil {
		t.Error("expected ClusterRoleBinding to be deleted on prod-eu")
	}

	// Verify ClusterRole deleted on staging
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-team-access"}, &cr)
	if err == nil {
		t.Error("expected ClusterRole to be deleted on staging-us")
	}

	// Verify RoleBinding deleted on staging
	var rb rbacv1.RoleBinding
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-team-access", Namespace: "default"}, &rb)
	if err == nil {
		t.Error("expected RoleBinding to be deleted on staging-us")
	}

	// Verify finalizer was removed
	var updatedASR v1alpha1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "team-access"}, &updatedASR)
	if err == nil {
		if controllerutil.ContainsFinalizer(&updatedASR, FinalizerKubernetes) {
			t.Error("expected new finalizer to be removed after cleanup")
		}
	}
	// If err != nil (not found), the fake client completed deletion, which is correct.
}

func TestReconcile_DeletionWithOldFinalizer_MigratesAndCleans(t *testing.T) {
	scheme := newTestScheme()

	// An ASR that is being deleted but still has the OLD finalizer.
	// The reconciler should migrate it and then clean up.
	prodCR := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pm-old-asr",
			Labels: map[string]string{
				LabelManagedBy: ManagedByValue,
				LabelASRName:   "old-asr",
			},
		},
	}
	prodClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(prodCR).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu": prodClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")

	now := metav1.Now()
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "old-asr",
			Namespace:         "pm-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerRBACCleanup},
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "old-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "old-asr"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue: %v", result.RequeueAfter)
	}

	// The migration should have swapped finalizers, then the deletion path should
	// have cleaned up RBAC and removed the new finalizer.
	var cr rbacv1.ClusterRole
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-old-asr"}, &cr)
	if err == nil {
		t.Error("expected ClusterRole on prod-eu to be deleted")
	}

	var updatedASR v1alpha1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "old-asr"}, &updatedASR)
	if err == nil {
		if controllerutil.ContainsFinalizer(&updatedASR, FinalizerKubernetes) {
			t.Error("expected new finalizer to be removed after cleanup")
		}
		if controllerutil.ContainsFinalizer(&updatedASR, FinalizerRBACCleanup) {
			t.Error("expected old finalizer to be removed after migration+cleanup")
		}
	}
	// Not found is also acceptable (fake client completed deletion).
}

func TestReconcile_DeletionRequeuesOnUnreachableCluster(t *testing.T) {
	scheme := newTestScheme()

	// prod-eu is reachable but staging-us is not
	prodCR := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pm-team-access",
			Labels: map[string]string{
				LabelManagedBy: ManagedByValue,
				LabelASRName:   "team-access",
			},
		},
	}
	prodClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(prodCR).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu": prodClient,
			// staging-us is NOT in the map -> unreachable
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")
	clusterInv.Update("staging-us") // known but unreachable

	now := metav1.Now()
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "team-access",
			Namespace:         "pm-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerKubernetes},
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "my-team",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister:      &StaticNamespaceLister{Namespaces: map[string][]string{"prod-eu": {}, "staging-us": {}}},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "team-access"))
	if err != nil {
		t.Fatalf("Reconcile should not return error for unreachable clusters during cleanup: %v", err)
	}

	// Should requeue with default delay — finalizer is held due to unreachable cluster
	if result.RequeueAfter != DefaultRequeueDelay {
		t.Errorf("expected requeue with %v (finalizer held), got RequeueAfter=%v", DefaultRequeueDelay, result.RequeueAfter)
	}

	// Finalizer should still be present (held due to unreachable staging-us)
	var updatedASR v1alpha1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "team-access"}, &updatedASR)
	if err != nil {
		t.Fatalf("expected ASR to still exist (finalizer held): %v", err)
	}
	if !controllerutil.ContainsFinalizer(&updatedASR, FinalizerKubernetes) {
		t.Error("expected finalizer to still be present (held due to unreachable cluster)")
	}

	// But prod-eu RBAC should still have been cleaned up
	var cr rbacv1.ClusterRole
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-team-access"}, &cr)
	if err == nil {
		t.Error("expected ClusterRole on reachable prod-eu to be deleted even if staging-us is unreachable")
	}
}

func TestReconcile_DeletionWithNoFinalizer(t *testing.T) {
	scheme := newTestScheme()

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")

	// ASR being deleted but with a different finalizer (not ours).
	// Our reconciler should just return without doing cleanup.
	now := metav1.Now()
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "no-finalizer-asr",
			Namespace:         "pm-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{"some-other-finalizer"}, // not ours
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "some-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: &StaticClusterClientFactory{Clients: map[string]ClusterClient{}},
		AuditLog:             audit.Disabled(),
		NamespaceLister:      &StaticNamespaceLister{},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "no-finalizer-asr"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue for ASR without our finalizer: %v", result.RequeueAfter)
	}
}

func TestReconcile_FullCreateDeleteLifecycle(t *testing.T) {
	scheme := newTestScheme()

	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	stagingClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu":    prodClient,
			"staging-us": stagingClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")
	clusterInv.Update("staging-us")

	// Step 1: Create ASR (no finalizer yet)
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lifecycle-asr",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "lifecycle-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/staging-us/default", Capability: "WRITE"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister: &StaticNamespaceLister{
			Namespaces: map[string][]string{
				"prod-eu":    {"default"},
				"staging-us": {"default"},
			},
		},
	}

	ctx := context.Background()

	// Step 2: First reconcile - should add NEW finalizer
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "lifecycle-asr"))
	if err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue after first reconcile: %v", result.RequeueAfter)
	}

	// Verify NEW finalizer was added
	var updatedASR v1alpha1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "lifecycle-asr"}, &updatedASR)
	if err != nil {
		t.Fatalf("failed to get ASR after first reconcile: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&updatedASR, FinalizerKubernetes) {
		t.Fatal("expected new finalizer to be added after first reconcile")
	}

	// Step 3: Second reconcile (finalizer already present) - should provision RBAC
	result, err = reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "lifecycle-asr"))
	if err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue after second reconcile: %v", result.RequeueAfter)
	}

	// Verify RBAC was created on prod-eu
	var prodCR rbacv1.ClusterRole
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-lifecycle-asr"}, &prodCR)
	if err != nil {
		t.Fatalf("expected ClusterRole on prod-eu: %v", err)
	}
	if prodCR.Labels[LabelASRName] != "lifecycle-asr" {
		t.Errorf("ClusterRole ASR label = %q, want %q", prodCR.Labels[LabelASRName], "lifecycle-asr")
	}

	var prodCRB rbacv1.ClusterRoleBinding
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-lifecycle-asr"}, &prodCRB)
	if err != nil {
		t.Fatalf("expected ClusterRoleBinding on prod-eu: %v", err)
	}

	// Verify RBAC was created on staging-us (namespace-only → Role per namespace)
	var stagingRole rbacv1.Role
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-lifecycle-asr", Namespace: "default"}, &stagingRole)
	if err != nil {
		t.Fatalf("expected namespace-scoped Role on staging-us: %v", err)
	}

	var stagingRB rbacv1.RoleBinding
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-lifecycle-asr", Namespace: "default"}, &stagingRB)
	if err != nil {
		t.Fatalf("expected RoleBinding in default on staging-us: %v", err)
	}

	// Step 4: Simulate deletion by setting DeletionTimestamp
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "lifecycle-asr"}, &updatedASR)
	if err != nil {
		t.Fatalf("failed to get ASR for deletion: %v", err)
	}

	now := metav1.NewTime(time.Now())
	updatedASR.DeletionTimestamp = &now
	mgmtClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(&updatedASR).Build()
	reconciler.Client = mgmtClient

	// Step 5: Reconcile with deletion - should clean up RBAC and remove finalizer
	result, err = reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "lifecycle-asr"))
	if err != nil {
		t.Fatalf("deletion Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue after deletion reconcile: %v", result.RequeueAfter)
	}

	// Verify all RBAC cleaned up on prod-eu
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-lifecycle-asr"}, &prodCR)
	if err == nil {
		t.Error("expected ClusterRole to be deleted on prod-eu after ASR deletion")
	}
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-lifecycle-asr"}, &prodCRB)
	if err == nil {
		t.Error("expected ClusterRoleBinding to be deleted on prod-eu after ASR deletion")
	}

	// Verify all RBAC cleaned up on staging-us
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-lifecycle-asr", Namespace: "default"}, &stagingRole)
	if err == nil {
		t.Error("expected namespace-scoped Role to be deleted on staging-us after ASR deletion")
	}
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-lifecycle-asr", Namespace: "default"}, &stagingRB)
	if err == nil {
		t.Error("expected RoleBinding to be deleted on staging-us after ASR deletion")
	}

	// Verify finalizer was removed
	var finalASR v1alpha1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "lifecycle-asr"}, &finalASR)
	if err == nil {
		if controllerutil.ContainsFinalizer(&finalASR, FinalizerKubernetes) {
			t.Error("expected new finalizer to be removed after successful cleanup")
		}
	}
	// Not found is acceptable — means the fake client completed deletion.
}

func TestCleanupRBAC_CleansAllResourceTypes(t *testing.T) {
	scheme := newTestScheme()

	// Create various RBAC resources with the ASR label on a single cluster
	cr1 := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pm-my-asr",
			Labels: map[string]string{LabelASRName: "my-asr", LabelManagedBy: ManagedByValue},
		},
	}
	crb1 := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pm-my-asr",
			Labels: map[string]string{LabelASRName: "my-asr", LabelManagedBy: ManagedByValue},
		},
		RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "pm-my-asr"},
	}
	rb1 := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-my-asr",
			Namespace: "ns1",
			Labels:    map[string]string{LabelASRName: "my-asr", LabelManagedBy: ManagedByValue},
		},
		RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "pm-my-asr"},
	}
	rb2 := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-my-asr",
			Namespace: "ns2",
			Labels:    map[string]string{LabelASRName: "my-asr", LabelManagedBy: ManagedByValue},
		},
		RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "pm-my-asr"},
	}
	// This resource should NOT be deleted (different ASR label)
	otherCR := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pm-other-asr",
			Labels: map[string]string{LabelASRName: "other-asr", LabelManagedBy: ManagedByValue},
		},
	}

	clusterClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(cr1, crb1, rb1, rb2, otherCR).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clusterClient,
		},
	}

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")

	reconciler := &Reconciler{
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister:      &StaticNamespaceLister{},
	}

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr"},
	}

	ctx := context.Background()
	_, err := reconciler.Cleaner.CleanupAll(ctx, asr.Name, []string{"cluster-a"})
	if err != nil {
		t.Fatalf("cleanupRBAC failed: %v", err)
	}

	// Verify the matching resources are deleted
	var cr rbacv1.ClusterRole
	err = clusterClient.Get(ctx, types.NamespacedName{Name: "pm-my-asr"}, &cr)
	if err == nil {
		t.Error("expected ClusterRole pm-my-asr to be deleted")
	}

	var crb rbacv1.ClusterRoleBinding
	err = clusterClient.Get(ctx, types.NamespacedName{Name: "pm-my-asr"}, &crb)
	if err == nil {
		t.Error("expected ClusterRoleBinding pm-my-asr to be deleted")
	}

	var rb rbacv1.RoleBinding
	err = clusterClient.Get(ctx, types.NamespacedName{Name: "pm-my-asr", Namespace: "ns1"}, &rb)
	if err == nil {
		t.Error("expected RoleBinding in ns1 to be deleted")
	}
	err = clusterClient.Get(ctx, types.NamespacedName{Name: "pm-my-asr", Namespace: "ns2"}, &rb)
	if err == nil {
		t.Error("expected RoleBinding in ns2 to be deleted")
	}

	// Verify unrelated resource is NOT deleted
	err = clusterClient.Get(ctx, types.NamespacedName{Name: "pm-other-asr"}, &cr)
	if err != nil {
		t.Errorf("expected ClusterRole pm-other-asr to NOT be deleted: %v", err)
	}
}

func TestCleanupRBAC_CleansAllClusters(t *testing.T) {
	scheme := newTestScheme()

	crA := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pm-my-asr",
			Labels: map[string]string{LabelASRName: "my-asr"},
		},
	}
	crB := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pm-my-asr",
			Labels: map[string]string{LabelASRName: "my-asr"},
		},
	}

	clientA := fake.NewClientBuilder().WithScheme(scheme).WithObjects(crA).Build()
	clientB := fake.NewClientBuilder().WithScheme(scheme).WithObjects(crB).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clientA,
			"cluster-b": clientB,
		},
	}

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")
	clusterInv.Update("cluster-b")

	reconciler := &Reconciler{
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister:      &StaticNamespaceLister{},
	}

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr"},
	}

	ctx := context.Background()
	_, err := reconciler.Cleaner.CleanupAll(ctx, asr.Name, []string{"cluster-a", "cluster-b"})
	if err != nil {
		t.Fatalf("cleanupRBAC failed: %v", err)
	}

	// Both clusters should have the CR deleted
	var cr rbacv1.ClusterRole
	err = clientA.Get(ctx, types.NamespacedName{Name: "pm-my-asr"}, &cr)
	if err == nil {
		t.Error("expected ClusterRole on cluster-a to be deleted")
	}
	err = clientB.Get(ctx, types.NamespacedName{Name: "pm-my-asr"}, &cr)
	if err == nil {
		t.Error("expected ClusterRole on cluster-b to be deleted")
	}
}

func TestCleanupRBAC_SkipsUnreachableCluster(t *testing.T) {
	scheme := newTestScheme()

	clientA := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clientA,
			// cluster-b is unreachable (not in the map)
		},
	}

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")
	clusterInv.Update("cluster-b") // known but unreachable

	reconciler := &Reconciler{
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister:      &StaticNamespaceLister{},
	}

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr"},
	}

	ctx := context.Background()
	skipped, err := reconciler.Cleaner.CleanupAll(ctx, asr.Name, []string{"cluster-a", "cluster-b"})
	if err != nil {
		t.Fatalf("expected no error when cluster is unreachable (best-effort skip), got: %v", err)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped cluster, got %d", len(skipped))
	}
	if skipped[0].ClusterName != "cluster-b" {
		t.Errorf("expected skipped cluster to be cluster-b, got %s", skipped[0].ClusterName)
	}
	if skipped[0].Reason == "" {
		t.Error("expected non-empty reason for skipped cluster")
	}
}

func TestCleanupRBAC_NoClustersInInventory(t *testing.T) {
	clusterInv := NewClusterInventory(logr.Discard())
	// Empty inventory

	reconciler := &Reconciler{
		ClusterInventory:     clusterInv,
		ClusterClientFactory: &StaticClusterClientFactory{Clients: map[string]ClusterClient{}},
		AuditLog:             audit.Disabled(),
		NamespaceLister:      &StaticNamespaceLister{},
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: &StaticClusterClientFactory{Clients: map[string]ClusterClient{}}, AuditLog: audit.Disabled()},
	}

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr"},
	}

	ctx := context.Background()
	skipped, err := reconciler.Cleaner.CleanupAll(ctx, asr.Name, []string{})
	if err != nil {
		t.Fatalf("cleanupRBAC should succeed with empty inventory: %v", err)
	}
	if skipped != nil {
		t.Fatalf("expected nil skipped, got %v", skipped)
	}
}

func TestCleanupRBAC_NoResourcesOnCluster(t *testing.T) {
	scheme := newTestScheme()

	// Cluster has no resources with the ASR label
	clusterClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clusterClient,
		},
	}

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")

	reconciler := &Reconciler{
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister:      &StaticNamespaceLister{},
	}

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr"},
	}

	ctx := context.Background()
	skipped, err := reconciler.Cleaner.CleanupAll(ctx, asr.Name, []string{"cluster-a"})
	if err != nil {
		t.Fatalf("cleanupRBAC should succeed when no resources exist: %v", err)
	}
	if skipped != nil {
		t.Fatalf("expected nil skipped, got %v", skipped)
	}
}

// TestRBACIntegration_DeleteCleansUpRBACOnAllClusters is the integration test
// that validates the full create→delete lifecycle end-to-end.
func TestRBACIntegration_DeleteCleansUpRBACOnAllClusters(t *testing.T) {
	scheme := newTestScheme()

	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	stagingClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu":    prodClient,
			"staging-us": stagingClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")
	clusterInv.Update("staging-us")

	// Create ASR
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "integration-asr",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "integration-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/staging-us/default", Capability: "WRITE"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/staging-us/monitoring", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()
	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister: &StaticNamespaceLister{
			Namespaces: map[string][]string{
				"prod-eu":    {"default", "monitoring"},
				"staging-us": {"default", "monitoring"},
			},
		},
	}

	ctx := context.Background()

	// -- Phase 1: Add finalizer --
	_, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "integration-asr"))
	if err != nil {
		t.Fatalf("phase 1 reconcile failed: %v", err)
	}

	// -- Phase 2: Provision RBAC (finalizer already there from previous reconcile) --
	_, err = reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "integration-asr"))
	if err != nil {
		t.Fatalf("phase 2 reconcile failed: %v", err)
	}

	// Verify RBAC exists
	var prodCR rbacv1.ClusterRole
	if err := prodClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr"}, &prodCR); err != nil {
		t.Fatalf("expected ClusterRole on prod-eu: %v", err)
	}
	var prodCRB rbacv1.ClusterRoleBinding
	if err := prodClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr"}, &prodCRB); err != nil {
		t.Fatalf("expected ClusterRoleBinding on prod-eu: %v", err)
	}
	var stagingRole1 rbacv1.Role
	if err := stagingClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr", Namespace: "default"}, &stagingRole1); err != nil {
		t.Fatalf("expected namespace-scoped Role in default on staging-us: %v", err)
	}
	var stagingRole2 rbacv1.Role
	if err := stagingClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr", Namespace: "monitoring"}, &stagingRole2); err != nil {
		t.Fatalf("expected namespace-scoped Role in monitoring on staging-us: %v", err)
	}
	var stagingRB1 rbacv1.RoleBinding
	if err := stagingClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr", Namespace: "default"}, &stagingRB1); err != nil {
		t.Fatalf("expected RoleBinding in default on staging-us: %v", err)
	}
	var stagingRB2 rbacv1.RoleBinding
	if err := stagingClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr", Namespace: "monitoring"}, &stagingRB2); err != nil {
		t.Fatalf("expected RoleBinding in monitoring on staging-us: %v", err)
	}

	// -- Phase 3: Simulate deletion --
	var currentASR v1alpha1.ApplicationSpecificRole
	if err := mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "integration-asr"}, &currentASR); err != nil {
		t.Fatalf("failed to get current ASR: %v", err)
	}
	now := metav1.NewTime(time.Now())
	currentASR.DeletionTimestamp = &now
	// Rebuild the fake client with the deletion-stamped ASR
	mgmtClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(&currentASR).Build()
	reconciler.Client = mgmtClient

	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "integration-asr"))
	if err != nil {
		t.Fatalf("deletion reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue after deletion: %v", result.RequeueAfter)
	}

	// -- Phase 4: Verify all RBAC removed --
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr"}, &prodCR)
	if err == nil {
		t.Error("expected ClusterRole on prod-eu to be deleted")
	}
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr"}, &prodCRB)
	if err == nil {
		t.Error("expected ClusterRoleBinding on prod-eu to be deleted")
	}
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr", Namespace: "default"}, &stagingRole1)
	if err == nil {
		t.Error("expected namespace-scoped Role in default on staging-us to be deleted")
	}
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr", Namespace: "monitoring"}, &stagingRole2)
	if err == nil {
		t.Error("expected namespace-scoped Role in monitoring on staging-us to be deleted")
	}

	var rb1 rbacv1.RoleBinding
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr", Namespace: "default"}, &rb1)
	if err == nil {
		t.Error("expected RoleBinding in default on staging-us to be deleted")
	}
	var rb2 rbacv1.RoleBinding
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-integration-asr", Namespace: "monitoring"}, &rb2)
	if err == nil {
		t.Error("expected RoleBinding in monitoring on staging-us to be deleted")
	}

	// Verify finalizer removed
	var finalASR v1alpha1.ApplicationSpecificRole
	if err := mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "integration-asr"}, &finalASR); err == nil {
		if controllerutil.ContainsFinalizer(&finalASR, FinalizerKubernetes) {
			t.Error("expected finalizer to be removed after successful cleanup")
		}
	}
	// Not found is acceptable — means the fake client completed deletion.
}

func TestCleanupAll_ReturnsSkippedClusters(t *testing.T) {
	scheme := newTestScheme()

	clientA := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clientA,
			// cluster-b is unreachable (not in the map)
		},
	}

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")
	clusterInv.Update("cluster-b")

	cleaner := &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()}

	ctx := context.Background()
	skipped, err := cleaner.CleanupAll(ctx, "test-asr", []string{"cluster-a", "cluster-b"})
	if err != nil {
		t.Fatalf("expected no error for reachable clusters, got: %v", err)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped cluster, got %d", len(skipped))
	}
	if skipped[0].ClusterName != "cluster-b" {
		t.Errorf("expected skipped cluster to be cluster-b, got %s", skipped[0].ClusterName)
	}
	if skipped[0].Reason == "" {
		t.Error("expected non-empty reason for skipped cluster")
	}
}

func TestExtractTargetClusterNames(t *testing.T) {
	inventory := NewClusterInventory(logr.Discard())
	inventory.Update("inv-cluster-1")
	inventory.Update("inv-cluster-2")

	tests := []struct {
		name     string
		asr      *v1alpha1.ApplicationSpecificRole
		expected []string
	}{
		{
			name: "no providers in status falls back to inventory",
			asr: &v1alpha1.ApplicationSpecificRole{
				Status: v1alpha1.ApplicationSpecificRoleStatus{
					Providers: nil,
				},
			},
			expected: inventory.Names(),
		},
		{
			name: "kubernetes key not present falls back to inventory",
			asr: &v1alpha1.ApplicationSpecificRole{
				Status: v1alpha1.ApplicationSpecificRoleStatus{
					Providers: map[string]v1alpha1.ProviderStatus{
						"github": {Ready: true},
					},
				},
			},
			expected: inventory.Names(),
		},
		{
			name: "kubernetes key present with empty targets returns empty list",
			asr: &v1alpha1.ApplicationSpecificRole{
				Status: v1alpha1.ApplicationSpecificRoleStatus{
					Providers: map[string]v1alpha1.ProviderStatus{
						"kubernetes": {
							Ready:   true,
							Targets: []v1alpha1.TargetStatus{},
						},
					},
				},
			},
			expected: []string{},
		},
		{
			name: "kubernetes key present with targets returns those TargetIDs",
			asr: &v1alpha1.ApplicationSpecificRole{
				Status: v1alpha1.ApplicationSpecificRoleStatus{
					Providers: map[string]v1alpha1.ProviderStatus{
						"kubernetes": {
							Ready: true,
							Targets: []v1alpha1.TargetStatus{
								{TargetID: "cluster-x"},
								{TargetID: "cluster-y"},
							},
						},
					},
				},
			},
			expected: []string{"cluster-x", "cluster-y"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTargetClusterNames(tt.asr, inventory)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d clusters, got %d: %v", len(tt.expected), len(result), result)
			}
			// Check all expected names are present (order may differ for inventory fallback)
			expectedSet := make(map[string]bool)
			for _, n := range tt.expected {
				expectedSet[n] = true
			}
			for _, n := range result {
				if !expectedSet[n] {
					t.Errorf("unexpected cluster name %q in result", n)
				}
			}
		})
	}
}

func TestReconcile_DeletionWithAbandonAnnotation(t *testing.T) {
	scheme := newTestScheme()

	clientA := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clientA,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")
	clusterInv.Update("cluster-b") // known but unreachable

	now := metav1.Now()
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "abandon-test",
			Namespace:         "pm-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerKubernetes},
			Annotations: map[string]string{
				AbandonCleanupAnnotation: "true",
			},
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "my-team",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/cluster-a", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister:      &StaticNamespaceLister{Namespaces: map[string][]string{"cluster-a": {}}},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "abandon-test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue (abandon annotation), got RequeueAfter=%v", result.RequeueAfter)
	}

	var updatedASR v1alpha1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "abandon-test"}, &updatedASR)
	if err == nil {
		if controllerutil.ContainsFinalizer(&updatedASR, FinalizerKubernetes) {
			t.Error("expected finalizer to be removed when abandon annotation is set")
		}
	}
}

func TestReconcile_DeletionUnblocksWhenClusterGoneFromInventory(t *testing.T) {
	scheme := newTestScheme()

	clientA := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clientA,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")
	// cluster-b deliberately NOT in inventory

	now := metav1.Now()
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "gone-test",
			Namespace:         "pm-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerKubernetes},
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "my-team",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions:   []v1alpha1.Permission{},
		},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[string]v1alpha1.ProviderStatus{
				"kubernetes": {
					Targets: []v1alpha1.TargetStatus{
						{TargetID: "cluster-a", Status: "Success"},
						{TargetID: "cluster-b", Status: "Success"},
					},
				},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister:      &StaticNamespaceLister{Namespaces: map[string][]string{"cluster-a": {}}},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "gone-test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue (cluster gone from inventory), got RequeueAfter=%v", result.RequeueAfter)
	}

	var updatedASR v1alpha1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "gone-test"}, &updatedASR)
	if err == nil {
		if controllerutil.ContainsFinalizer(&updatedASR, FinalizerKubernetes) {
			t.Error("expected finalizer to be removed when cluster is gone from inventory")
		}
	}
}

func TestReconcile_RequeueEscalation(t *testing.T) {
	scheme := newTestScheme()

	clientA := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clientA,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")
	clusterInv.Update("cluster-b") // known but unreachable

	now := metav1.Now()
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "escalation-test",
			Namespace:         "pm-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerKubernetes},
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "my-team",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions:   []v1alpha1.Permission{},
		},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[string]v1alpha1.ProviderStatus{
				"kubernetes": {
					Targets: []v1alpha1.TargetStatus{
						{TargetID: "cluster-a", Status: "Success"},
						{TargetID: "cluster-b", Status: "Orphaned", LastUpdated: metav1.Time{Time: time.Now().Add(-2 * time.Minute)}},
					},
				},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		AuditLog:             audit.Disabled(),
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		NamespaceLister:      &StaticNamespaceLister{Namespaces: map[string][]string{"cluster-a": {}}},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "escalation-test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RequeueAfter != OrphanRequeueDelay {
		t.Errorf("expected escalated requeue %v, got RequeueAfter=%v", OrphanRequeueDelay, result.RequeueAfter)
	}
}
