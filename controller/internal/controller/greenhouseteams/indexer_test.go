// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package greenhouseteams

import (
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

func TestConnectorBindingIndexer_Update(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	// Add a greenhouse-teams ConnectorBinding
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-greenhouse"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:     "sci-greenhouse",
			Provider: "greenhouse-teams",
		},
	}
	idx.Update(cb)

	if !idx.IsProviderConnector("sci-greenhouse") {
		t.Error("expected sci-greenhouse to be a greenhouse-teams connector")
	}

	// Add a non-greenhouse-teams ConnectorBinding
	cbOther := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:     "sci-k8s",
			Provider: "kubernetes",
		},
	}
	idx.Update(cbOther)

	if idx.IsProviderConnector("sci-k8s") {
		t.Error("expected sci-k8s NOT to be a greenhouse-teams connector")
	}
}

func TestConnectorBindingIndexer_Remove(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-greenhouse"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:     "sci-greenhouse",
			Provider: "greenhouse-teams",
		},
	}
	idx.Update(cb)
	idx.Remove("sci-greenhouse")

	if idx.IsProviderConnector("sci-greenhouse") {
		t.Error("expected sci-greenhouse to be removed from index")
	}
}

func TestConnectorBindingIndexer_Sync(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	bindings := []pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-gh-1"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-gh-1", Provider: "greenhouse-teams"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-gh-2"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-gh-2", Provider: "greenhouse-teams"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	}

	idx.Sync(bindings)

	if !idx.IsProviderConnector("sci-gh-1") {
		t.Error("expected sci-gh-1 to be indexed")
	}
	if !idx.IsProviderConnector("sci-gh-2") {
		t.Error("expected sci-gh-2 to be indexed")
	}
	if idx.IsProviderConnector("sci-k8s") {
		t.Error("expected sci-k8s NOT to be indexed")
	}

	names := idx.BindingNames()
	if len(names) != 2 {
		t.Errorf("expected 2 greenhouse-teams bindings, got %d", len(names))
	}
}

func TestConnectorBindingIndexer_HasAnyProviderRef(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	bindings := []pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-greenhouse"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-greenhouse", Provider: "greenhouse-teams"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	}
	idx.Sync(bindings)

	tests := []struct {
		name string
		refs []string
		want bool
	}{
		{
			name: "has greenhouse-teams ref",
			refs: []string{"sci-greenhouse"},
			want: true,
		},
		{
			name: "has greenhouse-teams ref among others",
			refs: []string{"sci-k8s", "sci-greenhouse"},
			want: true,
		},
		{
			name: "no greenhouse-teams ref",
			refs: []string{"sci-k8s"},
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
			got := idx.HasAnyProviderRef(tt.refs)
			if got != tt.want {
				t.Errorf("HasAnyProviderRef(%v) = %v, want %v", tt.refs, got, tt.want)
			}
		})
	}
}

func TestConnectorBindingIndexer_ProviderChange(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	// Initially greenhouse-teams
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-multi"},
		Spec:       pmv1.ConnectorBindingSpec{Name: "sci-multi", Provider: "greenhouse-teams"},
	}
	idx.Update(cb)

	if !idx.IsProviderConnector("sci-multi") {
		t.Fatal("expected sci-multi to be greenhouse-teams")
	}

	// Change provider to kubernetes
	cb.Spec.Provider = "kubernetes"
	idx.Update(cb)

	if idx.IsProviderConnector("sci-multi") {
		t.Error("expected sci-multi to no longer be greenhouse-teams after provider change")
	}
}

func TestConnectorBindingIndexer_HandleConnectorBindingEvent(t *testing.T) {
	idx := NewConnectorBindingIndexer(logr.Discard())

	// greenhouse-teams CB
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-greenhouse"},
		Spec:       pmv1.ConnectorBindingSpec{Name: "sci-greenhouse", Provider: "greenhouse-teams"},
	}
	result := idx.HandleConnectorBindingEvent(cb)
	if !result {
		t.Error("expected HandleConnectorBindingEvent to return true for greenhouse-teams CB")
	}

	// non-greenhouse-teams CB
	cbOther := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
		Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
	}
	result = idx.HandleConnectorBindingEvent(cbOther)
	if result {
		t.Error("expected HandleConnectorBindingEvent to return false for non-greenhouse-teams CB")
	}
}
