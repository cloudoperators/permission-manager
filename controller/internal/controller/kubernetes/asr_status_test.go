// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
)

func TestReconcile_StatusWrittenWithMixedResults(t *testing.T) {
	scheme := newTestScheme()

	// prod-eu is reachable, staging-us is NOT (will return ClusterUnreachableError)
	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu": prodClient,
			// staging-us is NOT in the map -> unreachable
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
	clusterInv.Update("prod-eu")
	clusterInv.Update("staging-us")

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
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/staging-us", Capability: "READ"},
			},
		},
	}

	// Use WithStatusSubresource to enable status patches on the fake client
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
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))

	// Should return error for rate-limited requeue (staging-us unreachable)
	if err == nil {
		t.Error("expected error due to cluster unreachable")
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no RequeueAfter when returning error, got %v", result.RequeueAfter)
	}

	// Fetch the ASR and verify its status was written
	var updatedASR pmv1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updatedASR)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	// Verify Providers["kubernetes"] has targets
	if updatedASR.Status.Providers == nil {
		t.Fatal("expected Providers map to be non-nil")
	}
	k8sStatus, ok := updatedASR.Status.Providers["kubernetes"]
	if !ok {
		t.Fatal("expected 'kubernetes' entry in Providers")
	}
	if len(k8sStatus.Targets) != 2 {
		t.Fatalf("expected 2 target entries, got %d", len(k8sStatus.Targets))
	}

	// Build a map for easier assertion
	statusMap := make(map[string]pmv1.TargetStatus)
	for _, ts := range k8sStatus.Targets {
		statusMap[ts.TargetID] = ts
	}

	// prod-eu should be Success
	prodStatus, ok := statusMap["prod-eu"]
	if !ok {
		t.Fatal("expected prod-eu in targets")
	}
	if prodStatus.Status != "Success" {
		t.Errorf("prod-eu status = %q, want %q", prodStatus.Status, "Success")
	}

	// staging-us should be Unreachable
	stagingStatus, ok := statusMap["staging-us"]
	if !ok {
		t.Fatal("expected staging-us in targets")
	}
	if stagingStatus.Status != "Unreachable" {
		t.Errorf("staging-us status = %q, want %q", stagingStatus.Status, "Unreachable")
	}

	// Provider should not be Ready (partial failure)
	if k8sStatus.Ready {
		t.Error("expected Ready=false for partial failure")
	}
}

func TestReconcile_StatusReadyTrueWhenAllClustersSucceed(t *testing.T) {
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
	cbIndex.Sync([]pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")
	clusterInv.Update("staging-us")

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
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/staging-us", Capability: "READ"},
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
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue: %v", result.RequeueAfter)
	}

	// Fetch the ASR and verify its status
	var updatedASR pmv1.ApplicationSpecificRole
	err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updatedASR)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	// Verify Providers["kubernetes"] has targets
	if updatedASR.Status.Providers == nil {
		t.Fatal("expected Providers map to be non-nil")
	}
	k8sStatus, ok := updatedASR.Status.Providers["kubernetes"]
	if !ok {
		t.Fatal("expected 'kubernetes' entry in Providers")
	}

	// All clusters succeeded
	if len(k8sStatus.Targets) != 2 {
		t.Fatalf("expected 2 target entries, got %d", len(k8sStatus.Targets))
	}

	for _, ts := range k8sStatus.Targets {
		if ts.Status != "Success" {
			t.Errorf("target %q status = %q, want %q", ts.TargetID, ts.Status, "Success")
		}
	}

	// Provider should be Ready
	if !k8sStatus.Ready {
		t.Error("expected Ready=true when all clusters succeed")
	}
}
