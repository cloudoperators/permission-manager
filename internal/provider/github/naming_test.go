// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"strings"
	"testing"

	"github.com/cloudoperators/permission-manager/internal/naming"
)

func TestSanitizeInstance(t *testing.T) {
	tests := []struct{ in, want string }{
		{"github.example.com", "github-example-com"},
		{"GitHub.Example.COM", "github-example-com"},
		{"my_instance!@#.com", "myinstance-com"},
	}
	for _, tt := range tests {
		if got := SanitizeInstance(tt.in); got != tt.want {
			t.Errorf("SanitizeInstance(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCRNames(t *testing.T) {
	// repo-guard convention: <instance-key>--<org>--<team-slug>.
	if got, want := TeamCRName("ghe", "my-org", "my-team"), "ghe--my-org--my-team"; got != want {
		t.Errorf("TeamCRName = %q, want %q", got, want)
	}
	// Underscores/spaces in the team slug become hyphens; permission is appended.
	if got, want := TeamRepoCRName("ghe", "my-org", "my_team", "admin"), "ghe--my-org--my-team--admin"; got != want {
		t.Errorf("TeamRepoCRName = %q, want %q", got, want)
	}
}

func TestTruncateWithHash(t *testing.T) {
	short := "asr-my-short-name"
	if got := truncateWithHash(short); got != short {
		t.Errorf("short name should be unchanged, got %q", got)
	}

	long := "asr-" + strings.Repeat("a", 300)
	got := truncateWithHash(long)
	if len(got) > naming.MaxK8sNameLength {
		t.Errorf("length %d exceeds max %d", len(got), naming.MaxK8sNameLength)
	}
	if !strings.HasPrefix(got, "asr-") {
		t.Error("prefix should be preserved after truncation")
	}
	if truncateWithHash(long+"-x") == got {
		t.Error("different inputs should produce different truncated names")
	}
}
