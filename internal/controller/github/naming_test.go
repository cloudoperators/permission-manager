// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"strings"
	"testing"

	"github.com/cloudoperators/permission-manager/pkg/naming"
)

func TestSanitizeInstance(t *testing.T) {
	tests := []struct {
		name     string
		instance string
		want     string
	}{
		{
			name:     "dots become hyphens",
			instance: "github.example.com",
			want:     "github-example-com",
		},
		{
			name:     "github.com",
			instance: "github.com",
			want:     "github-com",
		},
		{
			name:     "already lowercase",
			instance: "my-instance",
			want:     "my-instance",
		},
		{
			name:     "uppercase gets lowered",
			instance: "GitHub.Example.COM",
			want:     "github-example-com",
		},
		{
			name:     "special chars removed",
			instance: "my_instance!@#.com",
			want:     "myinstance-com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeInstance(tt.instance)
			if got != tt.want {
				t.Errorf("SanitizeInstance(%q) = %q, want %q", tt.instance, got, tt.want)
			}
		})
	}
}

func TestTeamCRName(t *testing.T) {
	tests := []struct {
		name     string
		asrName  string
		instance string
		org      string
		want     string
	}{
		{
			name:     "standard case",
			asrName:  "my-asr",
			instance: "github.example.com",
			org:      "cc",
			want:     "asr-my-asr-github-example-com-cc",
		},
		{
			name:     "github.com instance",
			asrName:  "team-repo-admin",
			instance: "github.com",
			org:      "my-org",
			want:     "asr-team-repo-admin-github-com-my-org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TeamCRName(tt.asrName, tt.instance, tt.org)
			if got != tt.want {
				t.Errorf("TeamCRName(%q, %q, %q) = %q, want %q", tt.asrName, tt.instance, tt.org, got, tt.want)
			}
		})
	}
}

func TestTeamRepoCRName(t *testing.T) {
	tests := []struct {
		name       string
		asrName    string
		instance   string
		org        string
		capability string
		want       string
	}{
		{
			name:       "standard case",
			asrName:    "my-asr",
			instance:   "github.example.com",
			org:        "cc",
			capability: "push",
			want:       "asr-my-asr-github-example-com-cc-push",
		},
		{
			name:       "admin capability",
			asrName:    "cross-product-admin",
			instance:   "github.example.com",
			org:        "my-org",
			capability: "admin",
			want:       "asr-cross-product-admin-github-example-com-my-org-admin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TeamRepoCRName(tt.asrName, tt.instance, tt.org, tt.capability)
			if got != tt.want {
				t.Errorf("TeamRepoCRName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncateWithHash(t *testing.T) {
	t.Run("short name unchanged", func(t *testing.T) {
		name := "asr-my-short-name"
		got := truncateWithHash(name)
		if got != name {
			t.Errorf("truncateWithHash(%q) = %q, expected unchanged", name, got)
		}
	})

	t.Run("long name truncated with hash", func(t *testing.T) {
		// Create a name that exceeds 253 chars
		name := "asr-" + strings.Repeat("a", 260)
		got := truncateWithHash(name)

		if len(got) > naming.MaxK8sNameLength {
			t.Errorf("truncateWithHash produced name of length %d, exceeds max %d", len(got), naming.MaxK8sNameLength)
		}
		if !strings.HasPrefix(got, "asr-") {
			t.Error("truncated name should still have prefix")
		}
		// Should end with a hex hash
		if got[len(got)-1] < '0' && got[len(got)-1] > 'f' {
			t.Error("truncated name should end with hex hash")
		}
	})

	t.Run("exactly 253 chars unchanged", func(t *testing.T) {
		name := strings.Repeat("a", naming.MaxK8sNameLength)
		got := truncateWithHash(name)
		if got != name {
			t.Errorf("name at exactly max length should be unchanged")
		}
	})

	t.Run("different long names produce different results", func(t *testing.T) {
		name1 := "asr-" + strings.Repeat("a", 260) + "-org1"
		name2 := "asr-" + strings.Repeat("a", 260) + "-org2"
		got1 := truncateWithHash(name1)
		got2 := truncateWithHash(name2)
		if got1 == got2 {
			t.Error("different inputs should produce different truncated names")
		}
	})
}

func TestSanitizeSegment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "lowercase passthrough",
			input: "myorg",
			want:  "myorg",
		},
		{
			name:  "underscores become hyphens",
			input: "MY_ORG",
			want:  "my-org",
		},
		{
			name:  "special chars removed",
			input: "org!name",
			want:  "orgname",
		},
		{
			name:  "leading/trailing hyphens trimmed",
			input: "-my-org-",
			want:  "my-org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeSegment(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeSegment(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
