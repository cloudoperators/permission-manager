// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	greenhouseapis "github.com/cloudoperators/greenhouse/api/meta/v1alpha1"
	greenhouse "github.com/cloudoperators/greenhouse/api/v1alpha1"
)

// testClusterClientFactory is a minimal stub for the GreenhouseClusterClientFactory
// that records InvalidateCache calls.
type testClusterClientFactory struct {
	invalidatedClusters []string
}

func (f *testClusterClientFactory) InvalidateCache(clusterName string) {
	f.invalidatedClusters = append(f.invalidatedClusters, clusterName)
}

func TestClusterReconciler_ClusterWithValidKubeConfig(t *testing.T) {
	// Setup
	scheme := runtime.NewScheme()
	_ = greenhouse.AddToScheme(scheme)

	cluster := &greenhouse.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: "greenhouse",
		},
		Status: greenhouse.ClusterStatus{
			StatusConditions: greenhouseapis.StatusConditions{
				Conditions: []greenhouseapis.Condition{
					{
						Type:   greenhouse.KubeConfigValid,
						Status: metav1.ConditionTrue,
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		Build()

	inv := NewClusterInventory(logr.Discard())
	factory := &testClusterClientFactory{}
	var changedCluster string
	onClusterChange := func(name string) { changedCluster = name }

	r := &ClusterReconciler{
		Client:               client,
		ClusterInventory:     inv,
		ClusterClientFactory: factory,
		OnClusterChange:      onClusterChange,
	}

	// Act
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "prod-eu", Namespace: "greenhouse"},
	})

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}
	if !inv.Has("prod-eu") {
		t.Error("expected cluster 'prod-eu' to be in inventory")
	}
	if len(factory.invalidatedClusters) != 1 || factory.invalidatedClusters[0] != "prod-eu" {
		t.Errorf("expected InvalidateCache called with 'prod-eu', got %v", factory.invalidatedClusters)
	}
	if changedCluster != "prod-eu" {
		t.Errorf("expected OnClusterChange called with 'prod-eu', got %q", changedCluster)
	}
}

func TestClusterReconciler_ClusterWithInvalidKubeConfig(t *testing.T) {
	// Setup
	scheme := runtime.NewScheme()
	_ = greenhouse.AddToScheme(scheme)

	cluster := &greenhouse.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: "greenhouse",
		},
		Status: greenhouse.ClusterStatus{
			StatusConditions: greenhouseapis.StatusConditions{
				Conditions: []greenhouseapis.Condition{
					{
						Type:   greenhouse.KubeConfigValid,
						Status: metav1.ConditionFalse,
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		Build()

	inv := NewClusterInventory(logr.Discard())
	// Pre-populate inventory to verify removal
	inv.Update("prod-eu")

	factory := &testClusterClientFactory{}

	r := &ClusterReconciler{
		Client:               client,
		ClusterInventory:     inv,
		ClusterClientFactory: factory,
		OnClusterChange:      nil,
	}

	// Act
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "prod-eu", Namespace: "greenhouse"},
	})

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}
	if inv.Has("prod-eu") {
		t.Error("expected cluster 'prod-eu' to be removed from inventory")
	}
	// Should NOT invalidate cache when cluster is invalid (it's being removed)
	if len(factory.invalidatedClusters) != 0 {
		t.Errorf("expected no InvalidateCache calls for invalid cluster, got %v", factory.invalidatedClusters)
	}
}

func TestClusterReconciler_ClusterWithMissingConditions(t *testing.T) {
	// Setup - cluster with empty status (no conditions at all)
	scheme := runtime.NewScheme()
	_ = greenhouse.AddToScheme(scheme)

	cluster := &greenhouse.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "staging-us",
			Namespace: "greenhouse",
		},
		// No Status set - empty conditions
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		Build()

	inv := NewClusterInventory(logr.Discard())
	// Pre-populate to verify removal
	inv.Update("staging-us")

	factory := &testClusterClientFactory{}

	r := &ClusterReconciler{
		Client:               client,
		ClusterInventory:     inv,
		ClusterClientFactory: factory,
		OnClusterChange:      nil,
	}

	// Act
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "staging-us", Namespace: "greenhouse"},
	})

	// Assert - should not crash, should remove from inventory
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}
	if inv.Has("staging-us") {
		t.Error("expected cluster 'staging-us' to be removed from inventory when conditions are missing")
	}
}

func TestClusterReconciler_ClusterDeleted(t *testing.T) {
	// Setup - cluster not found (already deleted)
	scheme := runtime.NewScheme()
	_ = greenhouse.AddToScheme(scheme)

	// No cluster objects - simulates deletion
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	inv := NewClusterInventory(logr.Discard())
	// Pre-populate to verify removal on delete
	inv.Update("deleted-cluster")

	factory := &testClusterClientFactory{}

	r := &ClusterReconciler{
		Client:               client,
		ClusterInventory:     inv,
		ClusterClientFactory: factory,
		OnClusterChange:      nil,
	}

	// Act
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "deleted-cluster", Namespace: "greenhouse"},
	})

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}
	if inv.Has("deleted-cluster") {
		t.Error("expected cluster 'deleted-cluster' to be removed from inventory on deletion")
	}
	// Should NOT attempt cache invalidation for deleted clusters
	if len(factory.invalidatedClusters) != 0 {
		t.Errorf("expected no InvalidateCache calls for deleted cluster, got %v", factory.invalidatedClusters)
	}
}

func TestClusterReconciler_ClusterUpdatedFromInvalidToValid(t *testing.T) {
	// Setup
	scheme := runtime.NewScheme()
	_ = greenhouse.AddToScheme(scheme)

	cluster := &greenhouse.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "recovered-cluster",
			Namespace: "greenhouse",
		},
		Status: greenhouse.ClusterStatus{
			StatusConditions: greenhouseapis.StatusConditions{
				Conditions: []greenhouseapis.Condition{
					{
						Type:   greenhouse.KubeConfigValid,
						Status: metav1.ConditionTrue,
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		Build()

	inv := NewClusterInventory(logr.Discard())
	// NOT in inventory initially (was previously invalid/removed)
	factory := &testClusterClientFactory{}
	var changedCluster string
	onClusterChange := func(name string) { changedCluster = name }

	r := &ClusterReconciler{
		Client:               client,
		ClusterInventory:     inv,
		ClusterClientFactory: factory,
		OnClusterChange:      onClusterChange,
	}

	// Act
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "recovered-cluster", Namespace: "greenhouse"},
	})

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}
	if !inv.Has("recovered-cluster") {
		t.Error("expected cluster 'recovered-cluster' to be added to inventory")
	}
	if len(factory.invalidatedClusters) != 1 || factory.invalidatedClusters[0] != "recovered-cluster" {
		t.Errorf("expected InvalidateCache called with 'recovered-cluster', got %v", factory.invalidatedClusters)
	}
	if changedCluster != "recovered-cluster" {
		t.Errorf("expected OnClusterChange called with 'recovered-cluster', got %q", changedCluster)
	}
}

func TestClusterReconciler_OnClusterChangeNilDoesNotPanic(t *testing.T) {
	// Setup - valid cluster but nil OnClusterChange callback
	scheme := runtime.NewScheme()
	_ = greenhouse.AddToScheme(scheme)

	cluster := &greenhouse.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "safe-cluster",
			Namespace: "greenhouse",
		},
		Status: greenhouse.ClusterStatus{
			StatusConditions: greenhouseapis.StatusConditions{
				Conditions: []greenhouseapis.Condition{
					{
						Type:   greenhouse.KubeConfigValid,
						Status: metav1.ConditionTrue,
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		Build()

	inv := NewClusterInventory(logr.Discard())
	factory := &testClusterClientFactory{}

	r := &ClusterReconciler{
		Client:               client,
		ClusterInventory:     inv,
		ClusterClientFactory: factory,
		OnClusterChange:      nil, // explicitly nil
	}

	// Act - should not panic
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "safe-cluster", Namespace: "greenhouse"},
	})

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}
	if !inv.Has("safe-cluster") {
		t.Error("expected cluster 'safe-cluster' to be in inventory")
	}
}

func TestClusterReconciler_ClusterWithUnknownConditionStatus(t *testing.T) {
	// A condition with Status = "Unknown" should be treated as invalid
	scheme := runtime.NewScheme()
	_ = greenhouse.AddToScheme(scheme)

	cluster := &greenhouse.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unknown-status",
			Namespace: "greenhouse",
		},
		Status: greenhouse.ClusterStatus{
			StatusConditions: greenhouseapis.StatusConditions{
				Conditions: []greenhouseapis.Condition{
					{
						Type:   greenhouse.KubeConfigValid,
						Status: metav1.ConditionUnknown,
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		Build()

	inv := NewClusterInventory(logr.Discard())
	inv.Update("unknown-status")
	factory := &testClusterClientFactory{}

	r := &ClusterReconciler{
		Client:               client,
		ClusterInventory:     inv,
		ClusterClientFactory: factory,
		OnClusterChange:      nil,
	}

	// Act
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "unknown-status", Namespace: "greenhouse"},
	})

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Requeue {
		t.Error("unexpected requeue")
	}
	if inv.Has("unknown-status") {
		t.Error("expected cluster with 'Unknown' condition status to be removed from inventory")
	}
}
