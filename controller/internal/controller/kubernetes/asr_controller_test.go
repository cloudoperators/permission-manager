package kubernetes

import (
	"testing"
)

func TestCapabilityVerbs(t *testing.T) {
	tests := []struct {
		capability string
		wantVerbs  []string
	}{
		{"READ", []string{"get", "list", "watch"}},
		{"WRITE", []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
		{"OPERATE", []string{"exec", "port-forward", "proxy"}},
		{"SECURE", []string{"bind", "escalate", "impersonate"}},
		{"*", []string{"*"}},
	}

	for _, tt := range tests {
		t.Run(tt.capability, func(t *testing.T) {
			got, ok := CapabilityVerbs[tt.capability]
			if !ok {
				t.Fatalf("capability %q not found in CapabilityVerbs", tt.capability)
			}
			if len(got) != len(tt.wantVerbs) {
				t.Fatalf("expected %d verbs, got %d", len(tt.wantVerbs), len(got))
			}
			for i, v := range got {
				if v != tt.wantVerbs[i] {
					t.Errorf("verb[%d] = %q, want %q", i, v, tt.wantVerbs[i])
				}
			}
		})
	}
}

func TestCapabilityVerbs_ManageIsUnmapped(t *testing.T) {
	if _, ok := CapabilityVerbs["MANAGE"]; ok {
		t.Error("MANAGE should not be in CapabilityVerbs (deprecated)")
	}
}

func TestCapabilityVerbs_AdminIsUnmapped(t *testing.T) {
	if _, ok := CapabilityVerbs["ADMIN"]; ok {
		t.Error("ADMIN should not be in CapabilityVerbs (deprecated)")
	}
}
