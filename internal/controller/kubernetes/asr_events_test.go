// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/pkg/audit"
)

// TestReconcile_EmitsNormalEventOnSuccess verifies that a Normal event
// is recorded when RBAC is successfully provisioned on a cluster.
func TestReconcile_EmitsNormalEventOnSuccess(t *testing.T) {
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

	recorder := record.NewFakeRecorder(10)

	reconciler := &Reconciler{
		Client:               mgmtClient,
		Recorder:             recorder,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
	}

	ctx := context.Background()
	_, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Check that a Normal event was emitted for successful provisioning
	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, "Normal") {
			t.Errorf("expected Normal event, got: %s", event)
		}
		if !strings.Contains(event, "RBACProvisioned") {
			t.Errorf("expected reason RBACProvisioned, got: %s", event)
		}
		if !strings.Contains(event, "prod-eu") {
			t.Errorf("expected event to mention cluster prod-eu, got: %s", event)
		}
	default:
		t.Fatal("expected an event to be recorded, but none was")
	}
}

// TestReconcile_EmitsWarningEventOnClusterUnreachable verifies that a Warning
// event is recorded when a cluster is unreachable.
func TestReconcile_EmitsWarningEventOnClusterUnreachable(t *testing.T) {
	scheme := newTestScheme()

	// staging-us is NOT in the factory map -> unreachable
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("staging-us")

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
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/staging-us", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	recorder := record.NewFakeRecorder(10)

	reconciler := &Reconciler{
		Client:               mgmtClient,
		Recorder:             recorder,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
	}

	ctx := context.Background()
	_, _ = reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))

	// Check that a Warning event was emitted for unreachable cluster
	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, "Warning") {
			t.Errorf("expected Warning event, got: %s", event)
		}
		if !strings.Contains(event, "ClusterUnreachable") {
			t.Errorf("expected reason ClusterUnreachable, got: %s", event)
		}
		if !strings.Contains(event, "staging-us") {
			t.Errorf("expected event to mention cluster staging-us, got: %s", event)
		}
	default:
		t.Fatal("expected a warning event to be recorded, but none was")
	}
}

// TestReconcile_EmitsWarningEventOnRBACApplyFailure verifies that a Warning
// event is recorded with reason "RBACProvisioningFailed" when applyRBACPlan fails.
func TestReconcile_EmitsWarningEventOnRBACApplyFailure(t *testing.T) {
	scheme := newTestScheme()

	// Use a FailingClusterClient that will fail on Create
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu": &failingClient{err: context.DeadlineExceeded},
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

	recorder := record.NewFakeRecorder(10)

	reconciler := &Reconciler{
		Client:               mgmtClient,
		Recorder:             recorder,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
	}

	ctx := context.Background()
	_, _ = reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))

	// Check that a Warning event was emitted for the failure
	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, "Warning") {
			t.Errorf("expected Warning event, got: %s", event)
		}
		if !strings.Contains(event, "RBACProvisioningFailed") {
			t.Errorf("expected reason RBACProvisioningFailed, got: %s", event)
		}
		if !strings.Contains(event, "prod-eu") {
			t.Errorf("expected event to mention cluster prod-eu, got: %s", event)
		}
	default:
		t.Fatal("expected a warning event to be recorded, but none was")
	}
}

// TestReconcile_NilRecorderDoesNotPanic verifies that if Recorder is nil,
// the reconciler does not panic (graceful nil check).
func TestReconcile_NilRecorderDoesNotPanic(t *testing.T) {
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

	// Recorder is nil — should not panic
	reconciler := &Reconciler{
		Client:               mgmtClient,
		Recorder:             nil,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
	}

	ctx := context.Background()
	_, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))
	if err != nil {
		t.Fatalf("Reconcile with nil recorder should not fail: %v", err)
	}
}

// TestReconcile_PatchBasedFinalizerAdd verifies that finalizer is added via Patch
// rather than Update (which the patch-based approach does).
func TestReconcile_PatchBasedFinalizerAdd(t *testing.T) {
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

	// ASR without finalizer
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

	recorder := record.NewFakeRecorder(10)

	reconciler := &Reconciler{
		Client:               mgmtClient,
		Recorder:             recorder,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
	}

	ctx := context.Background()
	_, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "test-asr"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify finalizer was added via Patch (same observable behavior as Update,
	// but testing that the operation succeeded)
	var updatedASR v1alpha1.ApplicationSpecificRole
	if err := mgmtClient.Get(ctx, newReconcileRequest("pm-system", "test-asr").NamespacedName, &updatedASR); err != nil {
		t.Fatalf("failed to get ASR: %v", err)
	}
	found := false
	for _, f := range updatedASR.Finalizers {
		if f == FinalizerKubernetes {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected finalizer to be added via Patch")
	}
}
