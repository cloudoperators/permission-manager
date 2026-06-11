// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package aggregator_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/internal/controller/aggregator"
)

func setupScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = pmv1.AddToScheme(s)
	return s
}

func TestReconcile_AllProvidersReady(t *testing.T) {
	scheme := setupScheme()
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "test-asr",
			ConnectorRefs: []string{"conn-1"},
		},
		Status: pmv1.ApplicationSpecificRoleStatus{
			Providers: map[string]pmv1.ProviderStatus{
				"kubernetes": {Ready: true, Message: "synced"},
				"greenhouse": {Ready: true, Message: "synced"},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		WithStatusSubresource(asr).
		Build()

	r := &aggregator.Reconciler{
		Client: cl,
		Log:    zap.New(zap.UseDevMode(true)),
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-asr", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Requeue {
		t.Fatal("unexpected requeue")
	}

	// Fetch updated ASR
	var updated pmv1.ApplicationSpecificRole
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "test-asr", Namespace: "default"}, &updated); err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	assertCondition(t, updated.Status.Conditions, pmv1.ConditionReady, metav1.ConditionTrue, "AllProvidersReady")
	assertCondition(t, updated.Status.Conditions, pmv1.ConditionDegraded, metav1.ConditionFalse, "AllProvidersReady")
}

func TestReconcile_PartialFailure(t *testing.T) {
	scheme := setupScheme()
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "test-asr",
			ConnectorRefs: []string{"conn-1"},
		},
		Status: pmv1.ApplicationSpecificRoleStatus{
			Providers: map[string]pmv1.ProviderStatus{
				"kubernetes": {Ready: true, Message: "synced"},
				"greenhouse": {Ready: false, Message: "error"},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		WithStatusSubresource(asr).
		Build()

	r := &aggregator.Reconciler{
		Client: cl,
		Log:    zap.New(zap.UseDevMode(true)),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-asr", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated pmv1.ApplicationSpecificRole
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "test-asr", Namespace: "default"}, &updated); err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	assertCondition(t, updated.Status.Conditions, pmv1.ConditionReady, metav1.ConditionFalse, "PartialFailure")
	assertCondition(t, updated.Status.Conditions, pmv1.ConditionDegraded, metav1.ConditionTrue, "PartialFailure")
}

func TestReconcile_NoProviders(t *testing.T) {
	scheme := setupScheme()
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "test-asr",
			ConnectorRefs: []string{"conn-1"},
		},
		Status: pmv1.ApplicationSpecificRoleStatus{},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		WithStatusSubresource(asr).
		Build()

	r := &aggregator.Reconciler{
		Client: cl,
		Log:    zap.New(zap.UseDevMode(true)),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-asr", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated pmv1.ApplicationSpecificRole
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "test-asr", Namespace: "default"}, &updated); err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	assertCondition(t, updated.Status.Conditions, pmv1.ConditionReady, metav1.ConditionUnknown, "NoProviders")
}

func TestReconcile_SingleProviderReady(t *testing.T) {
	scheme := setupScheme()
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "test-asr",
			ConnectorRefs: []string{"conn-1"},
		},
		Status: pmv1.ApplicationSpecificRoleStatus{
			Providers: map[string]pmv1.ProviderStatus{
				"kubernetes": {Ready: true, Message: "synced"},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		WithStatusSubresource(asr).
		Build()

	r := &aggregator.Reconciler{
		Client: cl,
		Log:    zap.New(zap.UseDevMode(true)),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-asr", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated pmv1.ApplicationSpecificRole
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "test-asr", Namespace: "default"}, &updated); err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	assertCondition(t, updated.Status.Conditions, pmv1.ConditionReady, metav1.ConditionTrue, "AllProvidersReady")
	assertCondition(t, updated.Status.Conditions, pmv1.ConditionDegraded, metav1.ConditionFalse, "AllProvidersReady")
}

func TestReconcile_SingleProviderNotReady(t *testing.T) {
	scheme := setupScheme()
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "test-asr",
			ConnectorRefs: []string{"conn-1"},
		},
		Status: pmv1.ApplicationSpecificRoleStatus{
			Providers: map[string]pmv1.ProviderStatus{
				"kubernetes": {Ready: false, Message: "error connecting"},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		WithStatusSubresource(asr).
		Build()

	r := &aggregator.Reconciler{
		Client: cl,
		Log:    zap.New(zap.UseDevMode(true)),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-asr", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated pmv1.ApplicationSpecificRole
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "test-asr", Namespace: "default"}, &updated); err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	assertCondition(t, updated.Status.Conditions, pmv1.ConditionReady, metav1.ConditionFalse, "AllProvidersFailed")
	assertCondition(t, updated.Status.Conditions, pmv1.ConditionDegraded, metav1.ConditionFalse, "TotalFailure")
}

func TestReconcile_ASRNotFound(t *testing.T) {
	scheme := setupScheme()
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := &aggregator.Reconciler{
		Client: cl,
		Log:    zap.New(zap.UseDevMode(true)),
	}

	// Should not error on not-found
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "missing", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("expected no error for missing ASR, got: %v", err)
	}
	if result.Requeue {
		t.Fatal("unexpected requeue for missing ASR")
	}
}

// assertCondition asserts a condition exists with the expected status and reason.
func assertCondition(t *testing.T, conditions []metav1.Condition, condType string, status metav1.ConditionStatus, reason string) {
	t.Helper()
	for _, c := range conditions {
		if c.Type == condType {
			if c.Status != status {
				t.Errorf("condition %s: expected status %s, got %s", condType, status, c.Status)
			}
			if c.Reason != reason {
				t.Errorf("condition %s: expected reason %s, got %s", condType, reason, c.Reason)
			}
			return
		}
	}
	t.Errorf("condition %s not found in conditions: %+v", condType, conditions)
}
