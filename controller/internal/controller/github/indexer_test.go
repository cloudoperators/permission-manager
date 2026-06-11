// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

func TestGithubIndexer_Update(t *testing.T) {
	idx := NewGithubIndexer(logr.Discard())

	// Add a github ConnectorBinding
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-github"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:     "sci-github",
			Provider: "github",
		},
	}
	idx.Update(cb)

	if !idx.IsGithubConnector("sci-github") {
		t.Error("expected sci-github to be a github connector")
	}

	// Add a non-github ConnectorBinding
	cbOther := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:     "sci-k8s",
			Provider: "kubernetes",
		},
	}
	idx.Update(cbOther)

	if idx.IsGithubConnector("sci-k8s") {
		t.Error("expected sci-k8s NOT to be a github connector")
	}
}

func TestGithubIndexer_Remove(t *testing.T) {
	idx := NewGithubIndexer(logr.Discard())

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-github"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:     "sci-github",
			Provider: "github",
		},
	}
	idx.Update(cb)
	idx.Remove("sci-github")

	if idx.IsGithubConnector("sci-github") {
		t.Error("expected sci-github to be removed from index")
	}
}

func TestGithubIndexer_Sync(t *testing.T) {
	idx := NewGithubIndexer(logr.Discard())

	bindings := []pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-github-1"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-github-1", Provider: "github"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-github-2"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-github-2", Provider: "github"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	}

	idx.Sync(bindings)

	if !idx.IsGithubConnector("sci-github-1") {
		t.Error("expected sci-github-1 to be indexed")
	}
	if !idx.IsGithubConnector("sci-github-2") {
		t.Error("expected sci-github-2 to be indexed")
	}
	if idx.IsGithubConnector("sci-k8s") {
		t.Error("expected sci-k8s NOT to be indexed")
	}

	names := idx.GithubBindingNames()
	if len(names) != 2 {
		t.Errorf("expected 2 github bindings, got %d", len(names))
	}
}

func TestGithubIndexer_HasAnyGithubRef(t *testing.T) {
	idx := NewGithubIndexer(logr.Discard())

	bindings := []pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-github"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-github", Provider: "github"},
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
			name: "has github ref",
			refs: []string{"sci-github"},
			want: true,
		},
		{
			name: "has github ref among others",
			refs: []string{"sci-k8s", "sci-github"},
			want: true,
		},
		{
			name: "no github ref",
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
			got := idx.HasAnyGithubRef(tt.refs)
			if got != tt.want {
				t.Errorf("HasAnyGithubRef(%v) = %v, want %v", tt.refs, got, tt.want)
			}
		})
	}
}

func TestGithubIndexer_ProviderChange(t *testing.T) {
	idx := NewGithubIndexer(logr.Discard())

	// Initially github
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-multi"},
		Spec:       pmv1.ConnectorBindingSpec{Name: "sci-multi", Provider: "github"},
	}
	idx.Update(cb)

	if !idx.IsGithubConnector("sci-multi") {
		t.Fatal("expected sci-multi to be github")
	}

	// Change provider to kubernetes
	cb.Spec.Provider = "kubernetes"
	idx.Update(cb)

	if idx.IsGithubConnector("sci-multi") {
		t.Error("expected sci-multi to no longer be github after provider change")
	}
}

func TestGithubIndexer_GetBinding(t *testing.T) {
	idx := NewGithubIndexer(logr.Discard())

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-github"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:     "sci-github",
			Provider: "github",
		},
	}
	idx.Update(cb)

	got, ok := idx.GetBinding("sci-github")
	if !ok {
		t.Fatal("expected to find sci-github binding")
	}
	if got.Name != "sci-github" {
		t.Errorf("GetBinding().Name = %q, want %q", got.Name, "sci-github")
	}

	_, ok = idx.GetBinding("not-found")
	if ok {
		t.Error("expected not-found to return false")
	}
}
