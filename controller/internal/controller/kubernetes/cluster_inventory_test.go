package kubernetes

import (
	"sort"
	"testing"

	"github.com/go-logr/logr"
)

func TestClusterInventory_UpdateAndHas(t *testing.T) {
	inv := NewClusterInventory(logr.Discard())

	inv.Update("cluster-1")
	inv.Update("cluster-2")

	if !inv.Has("cluster-1") {
		t.Error("expected cluster-1 to be in inventory")
	}
	if !inv.Has("cluster-2") {
		t.Error("expected cluster-2 to be in inventory")
	}
	if inv.Has("cluster-3") {
		t.Error("expected cluster-3 NOT to be in inventory")
	}
}

func TestClusterInventory_Remove(t *testing.T) {
	inv := NewClusterInventory(logr.Discard())

	inv.Update("cluster-1")
	inv.Remove("cluster-1")

	if inv.Has("cluster-1") {
		t.Error("expected cluster-1 to be removed")
	}
}

func TestClusterInventory_Names(t *testing.T) {
	inv := NewClusterInventory(logr.Discard())

	inv.Update("alpha")
	inv.Update("beta")
	inv.Update("gamma")

	names := inv.Names()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}

	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	for _, expected := range []string{"alpha", "beta", "gamma"} {
		if !nameSet[expected] {
			t.Errorf("expected %q in names", expected)
		}
	}
}

func TestClusterInventory_MatchingNames(t *testing.T) {
	inv := NewClusterInventory(logr.Discard())
	inv.Update("nw-a-prod")
	inv.Update("nw-a-staging")
	inv.Update("nw-b-prod")
	inv.Update("eu-west-1")

	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "exact match found",
			pattern: "nw-a-prod",
			want:    []string{"nw-a-prod"},
		},
		{
			name:    "exact match not found",
			pattern: "nonexistent",
			want:    nil,
		},
		{
			name:    "prefix glob matches two",
			pattern: "nw-a-*",
			want:    []string{"nw-a-prod", "nw-a-staging"},
		},
		{
			name:    "prefix glob matches one",
			pattern: "nw-b-*",
			want:    []string{"nw-b-prod"},
		},
		{
			name:    "prefix glob matches none",
			pattern: "nw-c-*",
			want:    nil,
		},
		{
			name:    "bare wildcard matches all",
			pattern: "*",
			want:    []string{"eu-west-1", "nw-a-prod", "nw-a-staging", "nw-b-prod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inv.MatchingNames(tt.pattern)
			sort.Strings(got)
			sort.Strings(tt.want)

			if tt.want == nil && got != nil {
				t.Errorf("MatchingNames(%q) = %v, want nil", tt.pattern, got)
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("MatchingNames(%q) returned %d results, want %d: got %v", tt.pattern, len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("MatchingNames(%q)[%d] = %q, want %q", tt.pattern, i, got[i], tt.want[i])
				}
			}
		})
	}
}
