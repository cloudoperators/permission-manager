// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
)

// TestOrphanCleanup_RemovesRBACFromRemovedCluster tests that when an ASR
// previously targeted clusters [A, B] and the CCRNs change to only [A],
// the RBAC on cluster B is deleted and B is removed from status targets.
func TestOrphanCleanup_RemovesRBACFromRemovedCluster(t *testing.T) {
	scheme := newTestScheme()

	// Pre-create RBAC resources on cluster B (the orphan)
	clusterBCR := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pm-test-asr",
			Labels: map[string]string{LabelASRName: "test-asr", LabelManagedBy: ManagedByValue},
		},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"get", "list", "watch"}},
		},
	}
	clusterBCRB := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pm-test-asr",
			Labels: map[string]string{LabelASRName: "test-asr", LabelManagedBy: ManagedByValue},
		},
		RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "pm-test-asr"},
	}

	clusterAClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	clusterBClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(clusterBCR, clusterBCRB).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clusterAClient,
			"cluster-b": clusterBClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")
	clusterInv.Update("cluster-b")

	// ASR now only targets cluster-a, but previously was on both (reflected in status)
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-asr",
			Namespace:  "pm-system",
			Finalizers: []string{FinalizerRBACCleanup},
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "test-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				// Only cluster-a now
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/cluster-a", Capability: "READ"},
			},
		},
		Status: pmv1.ApplicationSpecificRoleStatus{
			Providers: map[string]pmv1.ProviderStatus{
				"kubernetes": {
					Ready:          true,
					LastReconciled: metav1.Now(),
					Targets: []pmv1.TargetStatus{
						{TargetID: "cluster-a", Status: "Success", LastUpdated: metav1.Now()},
						{TargetID: "cluster-b", Status: "Success", LastUpdated: metav1.Now()},
					},
				},
			},
		},
	}

	underlying := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		WithStatusSubresource(&pmv1.ApplicationSpecificRole{}).
		Build()
	mgmtClient := wrapWithSSAInterceptor(underlying)

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
		NamespaceLister:      &StaticNamespaceLister{Namespaces: map[string][]string{"cluster-a": {}, "cluster-b": {}}},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue: %v", result.RequeueAfter)
	}

	// Verify RBAC on cluster B was deleted
	var cr rbacv1.ClusterRole
	err = clusterBClient.Get(ctx, types.NamespacedName{Name: "pm-test-asr"}, &cr)
	if err == nil {
		t.Error("expected ClusterRole on cluster-b to be deleted (orphan cleanup)")
	}

	var crb rbacv1.ClusterRoleBinding
	err = clusterBClient.Get(ctx, types.NamespacedName{Name: "pm-test-asr"}, &crb)
	if err == nil {
		t.Error("expected ClusterRoleBinding on cluster-b to be deleted (orphan cleanup)")
	}

	// Verify status only contains cluster-a
	var updatedASR pmv1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updatedASR)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	k8sStatus, ok := updatedASR.Status.Providers["kubernetes"]
	if !ok {
		t.Fatal("expected 'kubernetes' provider in status")
	}
	if len(k8sStatus.Targets) != 1 {
		t.Fatalf("expected 1 target entry (only cluster-a), got %d: %v",
			len(k8sStatus.Targets), k8sStatus.Targets)
	}
	if k8sStatus.Targets[0].TargetID != "cluster-a" {
		t.Errorf("expected remaining target for cluster-a, got %q",
			k8sStatus.Targets[0].TargetID)
	}
}

// TestOrphanCleanup_UnreachableOrphanStaysInStatus tests that if an orphan
// cluster is unreachable, it stays in status targets with Status="Unreachable"
// for retry on the next reconcile.
func TestOrphanCleanup_UnreachableOrphanStaysInStatus(t *testing.T) {
	scheme := newTestScheme()

	clusterAClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	// cluster-b is NOT in the factory map -> unreachable

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clusterAClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")
	clusterInv.Update("cluster-b")

	// ASR targets only cluster-a but status still has cluster-b
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-asr",
			Namespace:  "pm-system",
			Finalizers: []string{FinalizerRBACCleanup},
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "test-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/cluster-a", Capability: "READ"},
			},
		},
		Status: pmv1.ApplicationSpecificRoleStatus{
			Providers: map[string]pmv1.ProviderStatus{
				"kubernetes": {
					Ready:          true,
					LastReconciled: metav1.Now(),
					Targets: []pmv1.TargetStatus{
						{TargetID: "cluster-a", Status: "Success", LastUpdated: metav1.Now()},
						{TargetID: "cluster-b", Status: "Success", LastUpdated: metav1.Now()},
					},
				},
			},
		},
	}

	underlying2 := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		WithStatusSubresource(&pmv1.ApplicationSpecificRole{}).
		Build()
	mgmtClient := wrapWithSSAInterceptor(underlying2)

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
		NamespaceLister:      &StaticNamespaceLister{Namespaces: map[string][]string{"cluster-a": {}}},
	}

	ctx := context.Background()
	_, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))
	// Should return error for rate-limited requeue (cluster-b unreachable)
	if err == nil {
		t.Fatal("expected error when orphan cluster is unreachable")
	}

	// Verify status contains both clusters: cluster-a=Success, cluster-b=Unreachable
	var updatedASR pmv1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updatedASR)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	k8sStatus, ok := updatedASR.Status.Providers["kubernetes"]
	if !ok {
		t.Fatal("expected 'kubernetes' provider in status")
	}

	statusMap := make(map[string]pmv1.TargetStatus)
	for _, ts := range k8sStatus.Targets {
		statusMap[ts.TargetID] = ts
	}

	if len(statusMap) != 2 {
		t.Fatalf("expected 2 target entries, got %d", len(statusMap))
	}

	aStatus, ok := statusMap["cluster-a"]
	if !ok {
		t.Fatal("expected cluster-a in targets")
	}
	if aStatus.Status != "Success" {
		t.Errorf("cluster-a status = %q, want %q", aStatus.Status, "Success")
	}

	bStatus, ok := statusMap["cluster-b"]
	if !ok {
		t.Fatal("expected cluster-b in targets (unreachable orphan stays)")
	}
	if bStatus.Status != "Unreachable" {
		t.Errorf("cluster-b status = %q, want %q", bStatus.Status, "Unreachable")
	}
}

// TestCleanupRBAC_ErrorAggregation tests that cleanupRBAC returns a multi-error
// aggregating all cluster failures.
func TestCleanupRBAC_ErrorAggregation(t *testing.T) {
	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")
	clusterInv.Update("cluster-b")
	clusterInv.Update("cluster-c")

	// cluster-a and cluster-c are unreachable (not in map), cluster-b is reachable
	scheme := newTestScheme()
	clientB := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-b": clientB,
		},
	}

	reconciler := &Reconciler{
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
		NamespaceLister:      &StaticNamespaceLister{},
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr"},
	}

	ctx := context.Background()
	skipped, err := reconciler.Cleaner.CleanupAll(ctx, asr.Name, []string{"cluster-a", "cluster-b", "cluster-c"})
	// Unreachable clusters are now skipped (best-effort), so no error is returned
	if err != nil {
		t.Fatalf("expected no error when clusters are unreachable (best-effort skip), got: %v", err)
	}
	if len(skipped) != 2 {
		t.Fatalf("expected 2 skipped clusters, got %d", len(skipped))
	}
}

// TestReconcile_ReturnsErrorForRateLimitedRequeue tests that when there are
// cluster errors in the main reconcile path, Reconcile returns an error
// (instead of RequeueAfter: 30s) so controller-runtime handles the backoff.
func TestReconcile_ReturnsErrorForRateLimitedRequeue(t *testing.T) {
	scheme := newTestScheme()

	// cluster-a is unreachable
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-asr",
			Namespace:  "pm-system",
			Finalizers: []string{FinalizerRBACCleanup},
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "test-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/cluster-a", Capability: "READ"},
			},
		},
	}

	underlying3 := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		WithStatusSubresource(&pmv1.ApplicationSpecificRole{}).
		Build()
	mgmtClient := wrapWithSSAInterceptor(underlying3)

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
		NamespaceLister:      &StaticNamespaceLister{Namespaces: map[string][]string{}},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))

	// Should return error (not RequeueAfter)
	if err == nil {
		t.Fatal("expected Reconcile to return error for rate-limited requeue")
	}

	// Result should be zero (no manual requeue)
	if result.RequeueAfter != 0 {
		t.Errorf("expected no RequeueAfter when returning error, got %v", result.RequeueAfter)
	}
}

// TestReconcile_FinalizerPathStillUsesRequeueAfter tests that the finalizer/deletion
// path still uses RequeueAfter (not returning error) when cleanup fails, because
// returning an error would prevent object deletion.
func TestReconcile_FinalizerPathSkipsUnreachableCluster(t *testing.T) {
	scheme := newTestScheme()

	// Only cluster-a reachable; cluster-b unreachable
	clientA := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"cluster-a": clientA,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("cluster-a")
	clusterInv.Update("cluster-b")

	now := metav1.Now()
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-asr",
			Namespace:         "pm-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerRBACCleanup},
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "test-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
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
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
		NamespaceLister:      &StaticNamespaceLister{Namespaces: map[string][]string{"cluster-a": {}}},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))

	// Finalizer path should NOT return error
	if err != nil {
		t.Fatalf("finalizer cleanup should not return error: %v", err)
	}

	// Should requeue — unreachable cluster-b holds the finalizer
	if result.RequeueAfter != DefaultRequeueDelay {
		t.Errorf("expected requeue with %v (finalizer held), got RequeueAfter=%v", DefaultRequeueDelay, result.RequeueAfter)
	}
}
