package greenhouseteams

import (
	"strings"
	"testing"

	"github.com/cloudoperators/permission-manager/controller/pkg/naming"
)

func TestTeamCRName(t *testing.T) {
	tests := []struct {
		name     string
		teamName string
		want     string
	}{
		{
			name:     "simple name",
			teamName: "my-team",
			want:     "pm-my-team",
		},
		{
			name:     "ASR spec name",
			teamName: "ACME_PLATFORM_EU-NL-1_SG-CAPACITY-OPS_VIEWER",
			want:     "pm-ACME_PLATFORM_EU-NL-1_SG-CAPACITY-OPS_VIEWER",
		},
		{
			name:     "empty name",
			teamName: "",
			want:     "pm-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TeamCRName(tt.teamName)
			if got != tt.want {
				t.Errorf("TeamCRName(%q) = %q, want %q", tt.teamName, got, tt.want)
			}
		})
	}
}

func TestTeamCRName_Truncation(t *testing.T) {
	// Create a teamName that would exceed 253 chars with prefix
	longName := strings.Repeat("a", 260)
	got := TeamCRName(longName)

	if len(got) > naming.MaxK8sNameLength {
		t.Errorf("TeamCRName with long input exceeds max length: got %d, max %d", len(got), naming.MaxK8sNameLength)
	}

	if !strings.HasPrefix(got, TeamNamePrefix) {
		t.Errorf("TeamCRName should start with prefix %q, got %q", TeamNamePrefix, got)
	}
}

func TestTRBName(t *testing.T) {
	tests := []struct {
		name        string
		asrName     string
		teamRole    string
		clusterName string
		want        string
	}{
		{
			name:        "simple",
			asrName:     "my-asr",
			teamRole:    "cluster-admin",
			clusterName: "eu-de-1",
			want:        "pm-my-asr-cluster-admin-eu-de-1",
		},
		{
			name:        "complex names",
			asrName:     "ACME_PLATFORM_SG-COMPUTE",
			teamRole:    "sre-viewer",
			clusterName: "s-eu-nl-1",
			want:        "pm-ACME_PLATFORM_SG-COMPUTE-sre-viewer-s-eu-nl-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TRBName(tt.asrName, tt.teamRole, tt.clusterName)
			if got != tt.want {
				t.Errorf("TRBName(%q, %q, %q) = %q, want %q", tt.asrName, tt.teamRole, tt.clusterName, got, tt.want)
			}
		})
	}
}

func TestTRBName_Truncation(t *testing.T) {
	longASRName := strings.Repeat("x", 200)
	longCluster := strings.Repeat("y", 100)

	got := TRBName(longASRName, "admin", longCluster)

	if len(got) > naming.MaxK8sNameLength {
		t.Errorf("TRBName with long input exceeds max length: got %d, max %d", len(got), naming.MaxK8sNameLength)
	}

	// With trailing-hyphen trimming, the result may be slightly shorter than max.
	// The key invariant is that it does not exceed max.

	// Two different long names should produce different results
	got2 := TRBName(longASRName, "viewer", longCluster)
	if got == got2 {
		t.Error("two different TRB names should produce different truncated results")
	}
}

func TestTruncateWithHash_Greenhouse(t *testing.T) {
	// Short name: no truncation
	short := "short-name"
	if got := naming.TruncateWithHash(short, naming.MaxK8sNameLength); got != short {
		t.Errorf("TruncateWithHash(%q) = %q, want %q (no truncation needed)", short, got, short)
	}

	// Exactly max length: no truncation
	exact := strings.Repeat("z", naming.MaxK8sNameLength)
	if got := naming.TruncateWithHash(exact, naming.MaxK8sNameLength); got != exact {
		t.Errorf("TruncateWithHash at exactly max length should not truncate")
	}

	// Over max length: truncation with hash
	over := strings.Repeat("z", naming.MaxK8sNameLength+1)
	got := naming.TruncateWithHash(over, naming.MaxK8sNameLength)
	if len(got) > naming.MaxK8sNameLength {
		t.Errorf("TruncateWithHash over max should not exceed max: got %d", len(got))
	}

	// Hash suffix should be 9 chars: dash + 8 hex chars
	suffix := got[len(got)-naming.HashSuffixLength:]
	if suffix[0] != '-' {
		t.Errorf("hash suffix should start with '-', got %q", suffix)
	}
}
