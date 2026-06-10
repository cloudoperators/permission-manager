package cbindex

import (
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

func TestIndex_Update_MatchingProvider(t *testing.T) {
	idx := New[string]("kubernetes", func(cb *pmv1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:     "sci-k8s",
			Provider: "kubernetes",
		},
	}
	idx.Update(cb)

	if !idx.Has("sci-k8s") {
		t.Error("expected sci-k8s to be indexed")
	}
}

func TestIndex_Update_NonMatchingProvider(t *testing.T) {
	idx := New[string]("kubernetes", func(cb *pmv1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-azure"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:     "sci-azure",
			Provider: "azure",
		},
	}
	idx.Update(cb)

	if idx.Has("sci-azure") {
		t.Error("expected sci-azure NOT to be indexed")
	}
}

func TestIndex_Update_ProviderChange(t *testing.T) {
	idx := New[string]("kubernetes", func(cb *pmv1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-multi"},
		Spec:       pmv1.ConnectorBindingSpec{Name: "sci-multi", Provider: "kubernetes"},
	}
	idx.Update(cb)

	if !idx.Has("sci-multi") {
		t.Fatal("expected sci-multi to be kubernetes")
	}

	// Change provider
	cb.Spec.Provider = "azure"
	idx.Update(cb)

	if idx.Has("sci-multi") {
		t.Error("expected sci-multi to be removed after provider change")
	}
}

func TestIndex_Remove(t *testing.T) {
	idx := New[string]("kubernetes", func(cb *pmv1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
		Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
	}
	idx.Update(cb)
	idx.Remove("sci-k8s")

	if idx.Has("sci-k8s") {
		t.Error("expected sci-k8s to be removed from index")
	}
}

func TestIndex_Sync(t *testing.T) {
	idx := New[string]("kubernetes", func(cb *pmv1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	// Pre-populate with something that should be replaced
	idx.Update(&pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "old-cb"},
		Spec:       pmv1.ConnectorBindingSpec{Name: "old-cb", Provider: "kubernetes"},
	})

	bindings := []pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s-1"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s-1", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s-2"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s-2", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-azure"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-azure", Provider: "azure"},
		},
	}

	idx.Sync(bindings)

	if idx.Has("old-cb") {
		t.Error("expected old-cb to be gone after sync")
	}
	if !idx.Has("sci-k8s-1") {
		t.Error("expected sci-k8s-1 to be indexed")
	}
	if !idx.Has("sci-k8s-2") {
		t.Error("expected sci-k8s-2 to be indexed")
	}
	if idx.Has("sci-azure") {
		t.Error("expected sci-azure NOT to be indexed")
	}
}

func TestIndex_BindingNames(t *testing.T) {
	idx := New[string]("kubernetes", func(cb *pmv1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	bindings := []pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s-1"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s-1", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s-2"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s-2", Provider: "kubernetes"},
		},
	}
	idx.Sync(bindings)

	names := idx.BindingNames()
	if len(names) != 2 {
		t.Errorf("expected 2 bindings, got %d", len(names))
	}

	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["sci-k8s-1"] || !nameSet["sci-k8s-2"] {
		t.Errorf("expected both bindings in names, got %v", names)
	}
}

func TestIndex_HasAnyRef(t *testing.T) {
	idx := New[string]("kubernetes", func(cb *pmv1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	bindings := []pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-azure"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-azure", Provider: "azure"},
		},
	}
	idx.Sync(bindings)

	tests := []struct {
		name string
		refs []string
		want bool
	}{
		{
			name: "has matching ref",
			refs: []string{"sci-k8s"},
			want: true,
		},
		{
			name: "has matching ref among others",
			refs: []string{"sci-azure", "sci-k8s"},
			want: true,
		},
		{
			name: "no matching ref",
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
			got := idx.HasAnyRef(tt.refs)
			if got != tt.want {
				t.Errorf("HasAnyRef(%v) = %v, want %v", tt.refs, got, tt.want)
			}
		})
	}
}

func TestIndex_Get(t *testing.T) {
	idx := New[string]("kubernetes", func(cb *pmv1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
		Spec:       pmv1.ConnectorBindingSpec{Name: "my-k8s-connector", Provider: "kubernetes"},
	}
	idx.Update(cb)

	val, ok := idx.Get("sci-k8s")
	if !ok {
		t.Fatal("expected Get to find sci-k8s")
	}
	if val != "my-k8s-connector" {
		t.Errorf("expected value 'my-k8s-connector', got %q", val)
	}

	_, ok = idx.Get("nonexistent")
	if ok {
		t.Error("expected Get to return false for nonexistent key")
	}
}

func TestIndex_FindRef(t *testing.T) {
	idx := New[pmv1.ConnectorBindingSpec]("github", func(cb *pmv1.ConnectorBinding) pmv1.ConnectorBindingSpec {
		return cb.Spec
	}, logr.Discard())

	bindings := []pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "gh-cb"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "gh-connector", Provider: "github"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "other-cb"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "other", Provider: "azure"},
		},
	}
	idx.Sync(bindings)

	name, val, found := idx.FindRef([]string{"other-cb", "gh-cb"})
	if !found {
		t.Fatal("expected FindRef to find a match")
	}
	if name != "gh-cb" {
		t.Errorf("expected name 'gh-cb', got %q", name)
	}
	if val.Name != "gh-connector" {
		t.Errorf("expected spec name 'gh-connector', got %q", val.Name)
	}

	_, _, found = idx.FindRef([]string{"other-cb", "unknown"})
	if found {
		t.Error("expected FindRef to return false when no match")
	}
}

func TestIndex_HandleEvent_Delete(t *testing.T) {
	idx := New[string]("kubernetes", func(cb *pmv1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
		Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
	}
	idx.Update(cb)

	// Simulate delete with DeletionTimestamp
	deletedCB := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "sci-k8s",
			DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
		},
		Spec: pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
	}
	idx.HandleEvent(deletedCB)

	if idx.Has("sci-k8s") {
		t.Error("expected sci-k8s to be removed after delete event")
	}
}

func TestIndex_HandleEvent_CreateUpdate(t *testing.T) {
	idx := New[string]("kubernetes", func(cb *pmv1.ConnectorBinding) string {
		return cb.Spec.Name
	}, logr.Discard())

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
		Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
	}
	idx.HandleEvent(cb)

	if !idx.Has("sci-k8s") {
		t.Error("expected sci-k8s to be indexed after HandleEvent for create/update")
	}
}
