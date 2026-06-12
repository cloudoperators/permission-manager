// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

func TestConnectorBindingIndexer_Update(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	// Add a kubernetes ConnectorBinding
	cb := &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
		Spec: v1alpha1.ConnectorBindingSpec{
			Name:     "sci-k8s",
			Provider: "kubernetes",
		},
	}
	idx.Update(cb)

	if !idx.IsKubernetesConnector("sci-k8s") {
		t.Error("expected sci-k8s to be a kubernetes connector")
	}

	// Add a non-kubernetes ConnectorBinding
	cbOther := &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-azure"},
		Spec: v1alpha1.ConnectorBindingSpec{
			Name:     "sci-azure",
			Provider: "azure",
		},
	}
	idx.Update(cbOther)

	if idx.IsKubernetesConnector("sci-azure") {
		t.Error("expected sci-azure NOT to be a kubernetes connector")
	}
}

func TestConnectorBindingIndexer_Remove(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	cb := &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
		Spec: v1alpha1.ConnectorBindingSpec{
			Name:     "sci-k8s",
			Provider: "kubernetes",
		},
	}
	idx.Update(cb)
	idx.Remove("sci-k8s")

	if idx.IsKubernetesConnector("sci-k8s") {
		t.Error("expected sci-k8s to be removed from index")
	}
}

func TestConnectorBindingIndexer_Sync(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	bindings := []v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s-1"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s-1", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s-2"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s-2", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-azure"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-azure", Provider: "azure"},
		},
	}

	idx.Sync(bindings)

	if !idx.IsKubernetesConnector("sci-k8s-1") {
		t.Error("expected sci-k8s-1 to be indexed")
	}
	if !idx.IsKubernetesConnector("sci-k8s-2") {
		t.Error("expected sci-k8s-2 to be indexed")
	}
	if idx.IsKubernetesConnector("sci-azure") {
		t.Error("expected sci-azure NOT to be indexed")
	}

	names := idx.KubernetesBindingNames()
	if len(names) != 2 {
		t.Errorf("expected 2 kubernetes bindings, got %d", len(names))
	}
}

func TestConnectorBindingIndexer_HasAnyKubernetesRef(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	bindings := []v1alpha1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-azure"},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-azure", Provider: "azure"},
		},
	}
	idx.Sync(bindings)

	tests := []struct {
		name string
		refs []string
		want bool
	}{
		{
			name: "has k8s ref",
			refs: []string{"sci-k8s"},
			want: true,
		},
		{
			name: "has k8s ref among others",
			refs: []string{"sci-azure", "sci-k8s"},
			want: true,
		},
		{
			name: "no k8s ref",
			refs: []string{"sci-azure"},
			want: false,
		},
		{
			name: "unknown ref",
			refs: []string{"unknown"},
			want: false,
		},
		{
			name: "empty refs",
			refs: []string{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idx.HasAnyKubernetesRef(tt.refs)
			if got != tt.want {
				t.Errorf("HasAnyKubernetesRef(%v) = %v, want %v", tt.refs, got, tt.want)
			}
		})
	}
}

func TestConnectorBindingIndexer_ProviderChange(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	// Initially kubernetes
	cb := &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-multi"},
		Spec:       v1alpha1.ConnectorBindingSpec{Name: "sci-multi", Provider: "kubernetes"},
	}
	idx.Update(cb)

	if !idx.IsKubernetesConnector("sci-multi") {
		t.Fatal("expected sci-multi to be kubernetes")
	}

	// Change provider to azure
	cb.Spec.Provider = "azure"
	idx.Update(cb)

	if idx.IsKubernetesConnector("sci-multi") {
		t.Error("expected sci-multi to no longer be kubernetes after provider change")
	}
}

func TestConnectorBindingIndexer_ExplicitStartupSync(t *testing.T) {
	// Tests that ConnectorBindingStartupSyncer lists all ConnectorBindings
	// from the API and syncs the index, making startup behavior explicit
	// rather than relying on watch Add events.

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

	// Build fake client pre-populated with ConnectorBindings
	objs := make([]runtime.Object, len(existingBindings))
	for i := range existingBindings {
		objs[i] = &existingBindings[i]
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		Build()

	idx := NewConnectorBindingIndexer(logr.Discard())

	// Before sync, index is empty
	if idx.IsKubernetesConnector("cb-k8s-1") {
		t.Fatal("index should be empty before startup sync")
	}

	// Run the startup syncer
	syncer := NewConnectorBindingStartupSyncer(idx, fakeClient, logr.Discard())
	if err := syncer.Start(context.Background()); err != nil {
		t.Fatalf("startup syncer failed: %v", err)
	}

	// After sync, all kubernetes bindings should be indexed
	if !idx.IsKubernetesConnector("cb-k8s-1") {
		t.Error("expected cb-k8s-1 to be indexed after startup sync")
	}
	if !idx.IsKubernetesConnector("cb-k8s-2") {
		t.Error("expected cb-k8s-2 to be indexed after startup sync")
	}
	if idx.IsKubernetesConnector("cb-azure") {
		t.Error("expected cb-azure NOT to be indexed after startup sync")
	}

	names := idx.KubernetesBindingNames()
	if len(names) != 2 {
		t.Errorf("expected 2 kubernetes bindings after sync, got %d", len(names))
	}
}

func TestConnectorBindingIndexer_DeleteEventRemovesFromIndex(t *testing.T) {
	// Tests that when a ConnectorBinding is deleted, the handler removes it
	// from the index rather than re-adding it via Update.

	idx := NewConnectorBindingIndexer(logr.Discard())

	// Pre-populate the index with a kubernetes binding
	cb := &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "cb-k8s-1"},
		Spec:       v1alpha1.ConnectorBindingSpec{Name: "cb-k8s-1", Provider: "kubernetes"},
	}
	idx.Update(cb)

	if !idx.IsKubernetesConnector("cb-k8s-1") {
		t.Fatal("expected cb-k8s-1 to be in index before delete")
	}

	// Simulate a delete event: the object has a deletion timestamp set
	deletedCB := &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "cb-k8s-1",
			DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
		},
		Spec: v1alpha1.ConnectorBindingSpec{Name: "cb-k8s-1", Provider: "kubernetes"},
	}

	// HandleConnectorBindingEvent should detect deletion and remove from index
	idx.HandleConnectorBindingEvent(deletedCB)

	if idx.IsKubernetesConnector("cb-k8s-1") {
		t.Error("expected cb-k8s-1 to be removed from index after delete event")
	}
}

func TestConnectorBindingIndexer_HandleEventUpdatesIndexForNonDelete(t *testing.T) {
	// Tests that HandleConnectorBindingEvent updates the index for non-delete events.
	idx := NewConnectorBindingIndexer(logr.Discard())

	cb := &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "cb-k8s-new"},
		Spec:       v1alpha1.ConnectorBindingSpec{Name: "cb-k8s-new", Provider: "kubernetes"},
	}

	// No DeletionTimestamp: should update (add to index)
	idx.HandleConnectorBindingEvent(cb)

	if !idx.IsKubernetesConnector("cb-k8s-new") {
		t.Error("expected cb-k8s-new to be indexed after HandleConnectorBindingEvent for non-delete")
	}
}
