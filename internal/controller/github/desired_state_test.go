// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// stripURNPrefix removes the "urn:ccrn:" prefix if present.
// This is a test helper used only to verify prefix handling behavior.
func stripURNPrefix(resource string) string {
	return strings.TrimPrefix(resource, urnCCRNPrefix)
}

func TestBuildDesiredState_RepositoryCCRNs(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cross-product-admin",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SCI_GITHUB_TEAM_CROSS_PRODUCT_ADMIN",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/cross-product",
					Capability: "ADMIN",
				},
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/another-repo",
					Capability: "ADMIN",
				},
			},
		},
	}

	capabilityMapping := map[string]string{
		"ADMIN": "admin",
		"WRITE": "push",
		"READ":  "pull",
	}

	state, _ := BuildDesiredState(asr, capabilityMapping)

	// Should produce one team for (github.example.com, my-org)
	if len(state.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(state.Teams))
	}
	if state.Teams[0].Instance != "github.example.com" {
		t.Errorf("expected instance github.example.com, got %q", state.Teams[0].Instance)
	}
	if state.Teams[0].Org != "my-org" {
		t.Errorf("expected org my-org, got %q", state.Teams[0].Org)
	}

	// Should produce one team repo with both repos aggregated under "admin"
	if len(state.TeamRepos) != 1 {
		t.Fatalf("expected 1 team repo, got %d", len(state.TeamRepos))
	}
	tr := state.TeamRepos[0]
	if tr.Permission != "admin" {
		t.Errorf("expected permission admin, got %q", tr.Permission)
	}
	if len(tr.Repositories) != 2 {
		t.Fatalf("expected 2 repositories, got %d", len(tr.Repositories))
	}
	// Should be sorted
	if tr.Repositories[0] != "another-repo" || tr.Repositories[1] != "cross-product" {
		t.Errorf("expected sorted repos [another-repo, cross-product], got %v", tr.Repositories)
	}
}

func TestBuildDesiredState_OrganizationCCRNCreatesTeamOnly(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "org-admin",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SCI_GITHUB_ORG_ADMIN",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/github.example.com/cc",
					Capability: "ADMIN",
				},
			},
		},
	}

	capabilityMapping := map[string]string{"ADMIN": "admin"}

	state, _ := BuildDesiredState(asr, capabilityMapping)

	// Organization CCRN should create a team
	if len(state.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(state.Teams))
	}
	if state.Teams[0].Org != "cc" {
		t.Errorf("expected org cc, got %q", state.Teams[0].Org)
	}

	// Organization CCRN should NOT create a GithubTeamRepository
	if len(state.TeamRepos) != 0 {
		t.Fatalf("expected 0 team repos for organization CCRN, got %d", len(state.TeamRepos))
	}
}

func TestBuildDesiredState_WildcardRepositoryTypeSkipped(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "all-repo-user",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SCI_GITHUB_ALL_REPO_USER",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/internal",
					Capability: "WRITE",
				},
				{
					Resource:   "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/public",
					Capability: "WRITE",
				},
			},
		},
	}

	capabilityMapping := map[string]string{"WRITE": "push"}

	state, _ := BuildDesiredState(asr, capabilityMapping)

	// Wildcard (repository_type) CCRNs should be skipped entirely
	if len(state.Teams) != 0 {
		t.Errorf("expected 0 teams for wildcard CCRNs, got %d", len(state.Teams))
	}
	if len(state.TeamRepos) != 0 {
		t.Errorf("expected 0 team repos for wildcard CCRNs, got %d", len(state.TeamRepos))
	}
}

func TestBuildDesiredState_MultipleCapabilities(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mixed-asr",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "MIXED_ASR",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo-a",
					Capability: "WRITE",
				},
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo-b",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo-c",
					Capability: "WRITE",
				},
			},
		},
	}

	capabilityMapping := map[string]string{
		"WRITE": "push",
		"READ":  "pull",
	}

	state, _ := BuildDesiredState(asr, capabilityMapping)

	// One team for (github.example.com, cc)
	if len(state.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(state.Teams))
	}

	// Two GithubTeamRepository CRs: one for push, one for pull
	if len(state.TeamRepos) != 2 {
		t.Fatalf("expected 2 team repos, got %d", len(state.TeamRepos))
	}

	// Find pull and push repos
	var pullRepo, pushRepo *DesiredTeamRepo
	for i := range state.TeamRepos {
		switch state.TeamRepos[i].Permission {
		case "pull":
			pullRepo = &state.TeamRepos[i]
		case "push":
			pushRepo = &state.TeamRepos[i]
		}
	}

	if pullRepo == nil {
		t.Fatal("expected a team repo with pull permission")
		return // staticcheck: unreachable but convinces SA5011 that pullRepo != nil below
	}
	if pushRepo == nil {
		t.Fatal("expected a team repo with push permission")
		return // staticcheck: unreachable but convinces SA5011 that pushRepo != nil below
	}

	if len(pullRepo.Repositories) != 1 || pullRepo.Repositories[0] != "repo-b" {
		t.Errorf("pull repo should contain [repo-b], got %v", pullRepo.Repositories)
	}
	if len(pushRepo.Repositories) != 2 {
		t.Fatalf("push repo should contain 2 repos, got %d", len(pushRepo.Repositories))
	}
	if pushRepo.Repositories[0] != "repo-a" || pushRepo.Repositories[1] != "repo-c" {
		t.Errorf("push repos should be [repo-a, repo-c], got %v", pushRepo.Repositories)
	}
}

func TestBuildDesiredState_MultipleInstancesAndOrgs(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-instance",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "MULTI_INSTANCE",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo1",
					Capability: "WRITE",
				},
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.com/sapcc/repo2",
					Capability: "WRITE",
				},
			},
		},
	}

	capabilityMapping := map[string]string{"WRITE": "push"}

	state, _ := BuildDesiredState(asr, capabilityMapping)

	// Two teams: (github.example.com, cc) and (github.com, sapcc)
	if len(state.Teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(state.Teams))
	}

	// Two GithubTeamRepository CRs: one per instance+org+capability
	if len(state.TeamRepos) != 2 {
		t.Fatalf("expected 2 team repos, got %d", len(state.TeamRepos))
	}
}

func TestBuildDesiredState_NoCapabilityMapping(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-mapping",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "NO_MAPPING",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo1",
					Capability: "WRITE",
				},
			},
		},
	}

	// nil capability mapping
	state, _ := BuildDesiredState(asr, nil)

	// Team should still be created (it doesn't depend on capability mapping)
	if len(state.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(state.Teams))
	}

	// But no team repos (capability can't be mapped)
	if len(state.TeamRepos) != 0 {
		t.Errorf("expected 0 team repos with nil mapping, got %d", len(state.TeamRepos))
	}
}

func TestBuildDesiredState_UnmappedCapability(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unmapped",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "UNMAPPED",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo1",
					Capability: "UNKNOWN_CAP",
				},
			},
		},
	}

	capabilityMapping := map[string]string{"WRITE": "push"}

	state, unmapped := BuildDesiredState(asr, capabilityMapping)

	// Team should still be created
	if len(state.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(state.Teams))
	}

	// But no team repos (capability "UNKNOWN_CAP" is not in the mapping)
	if len(state.TeamRepos) != 0 {
		t.Errorf("expected 0 team repos for unmapped capability, got %d", len(state.TeamRepos))
	}

	// Should report unmapped capabilities
	if len(unmapped) != 1 {
		t.Fatalf("expected 1 unmapped capability, got %d", len(unmapped))
	}
	if unmapped[0] != "UNKNOWN_CAP" {
		t.Errorf("expected unmapped capability UNKNOWN_CAP, got %q", unmapped[0])
	}
}

func TestBuildDesiredState_UnmappedCapabilityMultiple(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-unmapped",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "MULTI_UNMAPPED",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo1",
					Capability: "UNKNOWN_A",
				},
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo2",
					Capability: "WRITE",
				},
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo3",
					Capability: "UNKNOWN_B",
				},
			},
		},
	}

	capabilityMapping := map[string]string{"WRITE": "push"}

	state, unmapped := BuildDesiredState(asr, capabilityMapping)

	// Team should still be created (valid permission processed)
	if len(state.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(state.Teams))
	}

	// Valid capability creates a team repo
	if len(state.TeamRepos) != 1 {
		t.Fatalf("expected 1 team repo for valid WRITE capability, got %d", len(state.TeamRepos))
	}

	// Should report both unmapped capabilities
	if len(unmapped) != 2 {
		t.Fatalf("expected 2 unmapped capabilities, got %d: %v", len(unmapped), unmapped)
	}
}

func TestBuildDesiredState_UnmappedCapabilityDeduplicated(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dedup-unmapped",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "DEDUP_UNMAPPED",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo1",
					Capability: "UNKNOWN_CAP",
				},
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo2",
					Capability: "UNKNOWN_CAP",
				},
			},
		},
	}

	capabilityMapping := map[string]string{"WRITE": "push"}

	_, unmapped := BuildDesiredState(asr, capabilityMapping)

	// Same capability unmapped twice should be deduplicated
	if len(unmapped) != 1 {
		t.Fatalf("expected 1 deduplicated unmapped capability, got %d: %v", len(unmapped), unmapped)
	}
	if unmapped[0] != "UNKNOWN_CAP" {
		t.Errorf("expected UNKNOWN_CAP, got %q", unmapped[0])
	}
}

func TestBuildDesiredState_AllMappedReturnsNilUnmapped(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "all-mapped",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "ALL_MAPPED",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo1",
					Capability: "WRITE",
				},
			},
		},
	}

	capabilityMapping := map[string]string{"WRITE": "push"}

	_, unmapped := BuildDesiredState(asr, capabilityMapping)

	if len(unmapped) != 0 {
		t.Errorf("expected 0 unmapped capabilities, got %d: %v", len(unmapped), unmapped)
	}
}

func TestBuildDesiredState_NonGithubCCRNSkipped(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mixed-providers",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "MIXED",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/my-cluster",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo1",
					Capability: "WRITE",
				},
			},
		},
	}

	capabilityMapping := map[string]string{"WRITE": "push", "READ": "pull"}

	state, _ := BuildDesiredState(asr, capabilityMapping)

	// Only the github CCRN should contribute
	if len(state.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(state.Teams))
	}
	if len(state.TeamRepos) != 1 {
		t.Fatalf("expected 1 team repo, got %d", len(state.TeamRepos))
	}
}

func TestBuildDesiredState_DuplicateReposDeduped(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dupes",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "DUPES",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo1",
					Capability: "WRITE",
				},
				{
					Resource:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo1",
					Capability: "WRITE",
				},
			},
		},
	}

	capabilityMapping := map[string]string{"WRITE": "push"}

	state, _ := BuildDesiredState(asr, capabilityMapping)

	if len(state.TeamRepos) != 1 {
		t.Fatalf("expected 1 team repo, got %d", len(state.TeamRepos))
	}
	if len(state.TeamRepos[0].Repositories) != 1 {
		t.Errorf("expected deduplicated to 1 repo, got %d", len(state.TeamRepos[0].Repositories))
	}
}

func TestBuildLabels(t *testing.T) {
	labels := BuildLabels("my-asr", "pm-system")

	if labels[pmapis.LabelManagedByKey] != pmapis.ManagedByGithub {
		t.Errorf("expected managed-by=%q, got %q", pmapis.ManagedByGithub, labels[pmapis.LabelManagedByKey])
	}
	if labels[pmapis.LabelSourceASR] != "my-asr" {
		t.Errorf("expected source-asr=my-asr, got %q", labels[pmapis.LabelSourceASR])
	}
	if labels[pmapis.LabelSourceNamespace] != "pm-system" {
		t.Errorf("expected source-namespace=pm-system, got %q", labels[pmapis.LabelSourceNamespace])
	}
}

func TestStripURNPrefix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "with prefix",
			input: "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo",
			want:  "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo",
		},
		{
			name:  "without prefix",
			input: "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo",
			want:  "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripURNPrefix(tt.input)
			if got != tt.want {
				t.Errorf("stripURNPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMapCapability(t *testing.T) {
	mapping := map[string]string{
		"ADMIN": "admin",
		"WRITE": "push",
		"READ":  "pull",
	}

	tests := []struct {
		capability string
		want       string
	}{
		{"ADMIN", "admin"},
		{"WRITE", "push"},
		{"READ", "pull"},
		{"UNKNOWN", ""},
	}

	for _, tt := range tests {
		t.Run(tt.capability, func(t *testing.T) {
			got := mapCapability(tt.capability, mapping)
			if got != tt.want {
				t.Errorf("mapCapability(%q) = %q, want %q", tt.capability, got, tt.want)
			}
		})
	}

	t.Run("nil mapping returns empty", func(t *testing.T) {
		got := mapCapability("WRITE", nil)
		if got != "" {
			t.Errorf("mapCapability with nil mapping = %q, want empty", got)
		}
	})
}
