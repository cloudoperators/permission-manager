// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

func siConnectorBinding(name, provider string) *v1alpha1.ConnectorBinding {
	return &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       v1alpha1.ConnectorBindingSpec{Name: name, Provider: provider},
	}
}

func siASR(name string, connectorRefs ...string) *v1alpha1.ApplicationSpecificRole {
	return &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: name, ConnectorRefs: connectorRefs},
	}
}

func serviceIdentity(name string, connectorRefs, roles []string) *v1alpha1.ServiceIdentity {
	return &v1alpha1.ServiceIdentity{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       v1alpha1.ServiceIdentitySpec{Name: name, ConnectorRefs: connectorRefs, Roles: roles},
	}
}

func siReadyReason(t *testing.T, si *v1alpha1.ServiceIdentity) (status metav1.ConditionStatus, reason string) {
	t.Helper()
	c := pmmeta.GetConditionByType(si.Status.Conditions, pmmeta.ReadyCondition)
	if c == nil {
		return "", ""
	}
	return c.Status, c.Reason
}

func TestServiceIdentity_Validity(t *testing.T) {
	tests := []struct {
		name          string
		objs          []client.Object
		si            *v1alpha1.ServiceIdentity
		wantStatus    metav1.ConditionStatus // "" => no Ready condition written (ignored)
		wantReason    pmmeta.ConditionReason
		wantMsgSubstr []string
	}{
		{
			name:       "resolved: shared kubernetes connector",
			objs:       []client.Object{siConnectorBinding("sci-k8s", "kubernetes"), siASR("my-asr", "sci-k8s")},
			si:         serviceIdentity("ci1", []string{"sci-k8s"}, []string{"my-asr"}),
			wantStatus: metav1.ConditionTrue,
			wantReason: pmmeta.RolesResolvedReason,
		},
		{
			name:          "resolved: no roles referenced",
			objs:          []client.Object{siConnectorBinding("sci-k8s", "kubernetes")},
			si:            serviceIdentity("ci1", []string{"sci-k8s"}, nil),
			wantStatus:    metav1.ConditionTrue,
			wantReason:    pmmeta.RolesResolvedReason,
			wantMsgSubstr: []string{"no roles referenced"},
		},
		{
			name:          "invalid username: system: prefix",
			objs:          []client.Object{siConnectorBinding("sci-k8s", "kubernetes")},
			si:            serviceIdentity("system:node", []string{"sci-k8s"}, []string{"my-asr"}),
			wantStatus:    metav1.ConditionFalse,
			wantReason:    pmmeta.InvalidUsernameReason,
			wantMsgSubstr: []string{"system:"},
		},
		{
			name:       "dangling: role names no ASR",
			objs:       []client.Object{siConnectorBinding("sci-k8s", "kubernetes")},
			si:         serviceIdentity("ci1", []string{"sci-k8s"}, []string{"ghost"}),
			wantStatus: metav1.ConditionFalse,
			wantReason: pmmeta.DanglingRoleRefReason,
		},
		{
			name:       "dangling: ASR exists but is not a kubernetes ASR",
			objs:       []client.Object{siConnectorBinding("sci-k8s", "kubernetes"), siConnectorBinding("gh", "github"), siASR("gh-asr", "gh")},
			si:         serviceIdentity("ci1", []string{"sci-k8s"}, []string{"gh-asr"}),
			wantStatus: metav1.ConditionFalse,
			wantReason: pmmeta.DanglingRoleRefReason,
		},
		{
			name:       "mismatch: both kubernetes but no shared connector",
			objs:       []client.Object{siConnectorBinding("sci-k8s", "kubernetes"), siConnectorBinding("other-k8s", "kubernetes"), siASR("my-asr", "other-k8s")},
			si:         serviceIdentity("ci1", []string{"sci-k8s"}, []string{"my-asr"}),
			wantStatus: metav1.ConditionFalse,
			wantReason: pmmeta.ConnectorMismatchReason,
		},
		{
			name:          "both dangling and mismatch: dangling reason, both groups listed",
			objs:          []client.Object{siConnectorBinding("sci-k8s", "kubernetes"), siConnectorBinding("other-k8s", "kubernetes"), siASR("mism", "other-k8s")},
			si:            serviceIdentity("ci1", []string{"sci-k8s"}, []string{"ghost", "mism"}),
			wantStatus:    metav1.ConditionFalse,
			wantReason:    pmmeta.DanglingRoleRefReason,
			wantMsgSubstr: []string{"roles not found as kubernetes ASRs: ghost", "do not share a kubernetes connector with this ServiceIdentity: mism"},
		},
		{
			name:       "ignored: ServiceIdentity has no kubernetes connector",
			objs:       []client.Object{siConnectorBinding("gh", "github"), siASR("my-asr", "gh")},
			si:         serviceIdentity("ci1", []string{"gh"}, []string{"my-asr"}),
			wantStatus: "", // no Ready condition written
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(ctrlTestScheme()).WithObjects(tt.objs...).Build()
			r := &ServiceIdentityReconciler{Client: c}

			if err := r.reconcileStatus(context.Background(), tt.si); err != nil {
				t.Fatalf("reconcileStatus: %v", err)
			}

			ready := pmmeta.GetConditionByType(tt.si.Status.Conditions, pmmeta.ReadyCondition)
			gotStatus, gotReason := siReadyReason(t, tt.si)
			if gotStatus != tt.wantStatus {
				t.Errorf("Ready status = %q, want %q (conditions: %+v)", gotStatus, tt.wantStatus, tt.si.Status.Conditions)
			}
			if tt.wantStatus != "" && gotReason != string(tt.wantReason) {
				t.Errorf("Ready reason = %q, want %q", gotReason, tt.wantReason)
			}
			for _, sub := range tt.wantMsgSubstr {
				if ready == nil || !strings.Contains(ready.Message, sub) {
					t.Errorf("Ready message %q should contain %q", readyMsg(ready), sub)
				}
			}
		})
	}
}

func readyMsg(c *metav1.Condition) string {
	if c == nil {
		return ""
	}
	return c.Message
}

// TestServiceIdentity_ResolvedIgnoresASRHealth: a referenced ASR that is itself InvalidConfig
// still counts as resolved — validity is reference well-formedness, not referenced-ASR health.
func TestServiceIdentity_ResolvedIgnoresASRHealth(t *testing.T) {
	asr := siASR("my-asr", "sci-k8s")
	asr.SetCondition(pmmeta.TrueCondition(pmmeta.InvalidConfigCondition, pmmeta.InvalidCCRNReason, "bad ccrn"))
	c := fake.NewClientBuilder().WithScheme(ctrlTestScheme()).
		WithObjects(siConnectorBinding("sci-k8s", "kubernetes"), asr).Build()
	r := &ServiceIdentityReconciler{Client: c}

	si := serviceIdentity("ci1", []string{"sci-k8s"}, []string{"my-asr"})
	if err := r.reconcileStatus(context.Background(), si); err != nil {
		t.Fatalf("reconcileStatus: %v", err)
	}
	if status, reason := siReadyReason(t, si); status != metav1.ConditionTrue || reason != string(pmmeta.RolesResolvedReason) {
		t.Errorf("want Ready=True/RolesResolved despite ASR InvalidConfig, got %s/%s", status, reason)
	}
}

// TestServiceIdentity_ClearsStaleReadyAndObservedGeneration: a ServiceIdentity that no longer names
// a kubernetes connector is ignored and any prior Ready is cleared; a resolved one stamps the
// observed generation.
func TestServiceIdentity_ClearsStaleReadyAndObservedGeneration(t *testing.T) {
	// Stale Ready cleared once no kubernetes connector remains.
	c := fake.NewClientBuilder().WithScheme(ctrlTestScheme()).
		WithObjects(siConnectorBinding("gh", "github")).Build()
	r := &ServiceIdentityReconciler{Client: c}
	si := serviceIdentity("ci1", []string{"gh"}, nil)
	si.SetCondition(pmmeta.TrueCondition(pmmeta.ReadyCondition, pmmeta.RolesResolvedReason, "stale"))
	if err := r.reconcileStatus(context.Background(), si); err != nil {
		t.Fatalf("reconcileStatus: %v", err)
	}
	if pmmeta.GetConditionByType(si.Status.Conditions, pmmeta.ReadyCondition) != nil {
		t.Errorf("stale Ready should be cleared for a non-kubernetes ServiceIdentity: %+v", si.Status.Conditions)
	}

	// Observed generation stamped on a resolved identity.
	c2 := fake.NewClientBuilder().WithScheme(ctrlTestScheme()).
		WithObjects(siConnectorBinding("sci-k8s", "kubernetes"), siASR("my-asr", "sci-k8s")).Build()
	r2 := &ServiceIdentityReconciler{Client: c2}
	si2 := serviceIdentity("ci2", []string{"sci-k8s"}, []string{"my-asr"})
	si2.Generation = 7
	if err := r2.reconcileStatus(context.Background(), si2); err != nil {
		t.Fatalf("reconcileStatus: %v", err)
	}
	ready := pmmeta.GetConditionByType(si2.Status.Conditions, pmmeta.ReadyCondition)
	if ready == nil || ready.ObservedGeneration != 7 {
		t.Errorf("observedGeneration = %+v, want 7", ready)
	}
}

func TestServiceIdentity_EnqueueForASR(t *testing.T) {
	refs := serviceIdentity("ref", []string{"sci-k8s"}, []string{"my-asr"})
	other := serviceIdentity("other", []string{"sci-k8s"}, []string{"different-asr"})

	c := fake.NewClientBuilder().WithScheme(ctrlTestScheme()).
		WithObjects(refs, other).
		WithIndex(&v1alpha1.ServiceIdentity{}, pmapis.ServiceIdentityRolesField, func(obj client.Object) []string {
			return obj.(*v1alpha1.ServiceIdentity).Spec.Roles
		}).Build()
	r := &ServiceIdentityReconciler{Client: c}

	reqs := r.enqueueServiceIdentitiesForASR(context.Background(), siASR("my-asr", "sci-k8s"))
	if len(reqs) != 1 || reqs[0].Name != "ref" {
		t.Errorf("expected exactly the referencing ServiceIdentity 'ref', got %+v", reqs)
	}
}

// TestServiceIdentity_Reconcile exercises the full Reconcile path (Get -> validate -> Status().Patch)
// and confirms the Ready condition is persisted to the API.
func TestServiceIdentity_Reconcile(t *testing.T) {
	si := serviceIdentity("ci1", []string{"sci-k8s"}, []string{"my-asr"})
	c := fake.NewClientBuilder().WithScheme(ctrlTestScheme()).
		WithObjects(si, siConnectorBinding("sci-k8s", "kubernetes"), siASR("my-asr", "sci-k8s")).
		WithStatusSubresource(&v1alpha1.ServiceIdentity{}).
		WithIndex(&v1alpha1.ServiceIdentity{}, pmapis.ServiceIdentityRolesField, func(obj client.Object) []string {
			return obj.(*v1alpha1.ServiceIdentity).Spec.Roles
		}).Build()
	r := &ServiceIdentityReconciler{Client: c}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "ci1", Namespace: "default"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got v1alpha1.ServiceIdentity
	if err := c.Get(context.Background(), types.NamespacedName{Name: "ci1", Namespace: "default"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Finalizers) != 0 {
		t.Errorf("ServiceIdentity must carry no finalizer, got %v", got.Finalizers)
	}
	ready := pmmeta.GetConditionByType(got.Status.Conditions, pmmeta.ReadyCondition)
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.Reason != string(pmmeta.RolesResolvedReason) {
		t.Errorf("persisted Ready = %+v, want True/RolesResolved", ready)
	}
}
