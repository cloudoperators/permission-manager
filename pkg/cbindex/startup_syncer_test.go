// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cbindex

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

func TestStartupSyncer_Start(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	existingBindings := []v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cb-k8s-1"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "cb-k8s-1", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cb-k8s-2"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "cb-k8s-2", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cb-azure"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "cb-azure", Provider: "azure"},
		},
	}

	objs := make([]runtime.Object, len(existingBindings))
	for i := range existingBindings {
		objs[i] = &existingBindings[i]
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		Build()

	idx := New[string]("kubernetes", func(cb *v1alpha1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	// Before sync, index is empty
	if idx.Has("cb-k8s-1") {
		t.Fatal("index should be empty before startup sync")
	}

	syncer := NewStartupSyncer[string](idx, fakeClient, logr.Discard())
	if err := syncer.Start(context.Background()); err != nil {
		t.Fatalf("startup syncer failed: %v", err)
	}

	// After sync, kubernetes bindings should be indexed
	if !idx.Has("cb-k8s-1") {
		t.Error("expected cb-k8s-1 to be indexed after startup sync")
	}
	if !idx.Has("cb-k8s-2") {
		t.Error("expected cb-k8s-2 to be indexed after startup sync")
	}
	if idx.Has("cb-azure") {
		t.Error("expected cb-azure NOT to be indexed after startup sync")
	}

	names := idx.BindingNames()
	if len(names) != 2 {
		t.Errorf("expected 2 bindings after sync, got %d", len(names))
	}
}
