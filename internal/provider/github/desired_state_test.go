// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"reflect"
	"testing"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

func TestBuildDesiredState(t *testing.T) {
	mapping := map[string]string{"ADMIN": "admin", "WRITE": "push", "READ": "pull"}

	type wantRepo struct {
		permission string
		repos      []string
	}
	tests := []struct {
		name         string
		perms        []v1alpha1.Permission
		mapping      map[string]string
		wantTeams    []string // formatted as "instance/org"
		wantRepos    []wantRepo
		wantUnmapped []string
	}{
		{
			name: "repository CCRNs aggregate and sort under the mapped permission",
			perms: []v1alpha1.Permission{
				perm(repoCCRN("github.example.com", "my-org", "cross-product"), "ADMIN"),
				perm(repoCCRN("github.example.com", "my-org", "another-repo"), "ADMIN"),
			},
			mapping:   mapping,
			wantTeams: []string{"github.example.com/my-org"},
			wantRepos: []wantRepo{{"admin", []string{"another-repo", "cross-product"}}},
		},
		{
			name:      "organization CCRN creates a team but no team repository",
			perms:     []v1alpha1.Permission{perm(orgCCRN("cc"), "ADMIN")},
			mapping:   mapping,
			wantTeams: []string{"github.example.com/cc"},
		},
		{
			name: "repository_type and wildcard repository are skipped",
			perms: []v1alpha1.Permission{
				perm("urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/internal", "WRITE"),
				perm(repoCCRN("github.example.com", "cc", "*"), "WRITE"),
			},
			mapping: mapping,
		},
		{
			name: "distinct capabilities produce one team repository each",
			perms: []v1alpha1.Permission{
				perm(repoCCRN("github.example.com", "cc", "repo-a"), "WRITE"),
				perm(repoCCRN("github.example.com", "cc", "repo-b"), "READ"),
				perm(repoCCRN("github.example.com", "cc", "repo-c"), "WRITE"),
			},
			mapping:   mapping,
			wantTeams: []string{"github.example.com/cc"},
			wantRepos: []wantRepo{
				{"pull", []string{"repo-b"}},
				{"push", []string{"repo-a", "repo-c"}},
			},
		},
		{
			name: "distinct instance and org produce distinct teams in sorted order",
			perms: []v1alpha1.Permission{
				perm(repoCCRN("github.example.com", "cc", "repo1"), "WRITE"),
				perm(repoCCRN("github.com", "sapcc", "repo2"), "WRITE"),
			},
			mapping:   mapping,
			wantTeams: []string{"github.com/sapcc", "github.example.com/cc"},
			wantRepos: []wantRepo{
				{"push", []string{"repo2"}},
				{"push", []string{"repo1"}},
			},
		},
		{
			name: "unmapped capability is reported once, valid ones still processed",
			perms: []v1alpha1.Permission{
				perm(repoCCRN("github.example.com", "cc", "repo1"), "UNKNOWN"),
				perm(repoCCRN("github.example.com", "cc", "repo2"), "UNKNOWN"),
				perm(repoCCRN("github.example.com", "cc", "repo3"), "WRITE"),
			},
			mapping:      mapping,
			wantTeams:    []string{"github.example.com/cc"},
			wantRepos:    []wantRepo{{"push", []string{"repo3"}}},
			wantUnmapped: []string{"UNKNOWN"},
		},
		{
			name:         "nil mapping keeps the team and reports the capability as unmapped",
			perms:        []v1alpha1.Permission{perm(repoCCRN("github.example.com", "cc", "repo1"), "WRITE")},
			mapping:      nil,
			wantTeams:    []string{"github.example.com/cc"},
			wantUnmapped: []string{"WRITE"},
		},
		{
			name: "non-github CCRN is skipped",
			perms: []v1alpha1.Permission{
				perm("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/my-cluster", "READ"),
				perm(repoCCRN("github.example.com", "cc", "repo1"), "WRITE"),
			},
			mapping:   mapping,
			wantTeams: []string{"github.example.com/cc"},
			wantRepos: []wantRepo{{"push", []string{"repo1"}}},
		},
		{
			name: "duplicate repositories are deduplicated",
			perms: []v1alpha1.Permission{
				perm(repoCCRN("github.example.com", "cc", "repo1"), "WRITE"),
				perm(repoCCRN("github.example.com", "cc", "repo1"), "WRITE"),
			},
			mapping:   mapping,
			wantTeams: []string{"github.example.com/cc"},
			wantRepos: []wantRepo{{"push", []string{"repo1"}}},
		},
		{
			name:      "bare CCRN without the urn:ccrn: prefix is still parsed",
			perms:     []v1alpha1.Permission{perm("repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo1", "WRITE")},
			mapping:   mapping,
			wantTeams: []string{"github.example.com/cc"},
			wantRepos: []wantRepo{{"push", []string{"repo1"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, unmapped := BuildDesiredState(newASR("asr", "SPEC", tt.perms), tt.mapping)

			var gotTeams []string
			for _, team := range state.Teams {
				gotTeams = append(gotTeams, team.Instance+"/"+team.Org)
			}
			if !reflect.DeepEqual(gotTeams, tt.wantTeams) {
				t.Errorf("teams = %v, want %v", gotTeams, tt.wantTeams)
			}

			var gotRepos []wantRepo
			for _, tr := range state.TeamRepos {
				gotRepos = append(gotRepos, wantRepo{tr.Permission, tr.Repositories})
			}
			if !reflect.DeepEqual(gotRepos, tt.wantRepos) {
				t.Errorf("team repos = %v, want %v", gotRepos, tt.wantRepos)
			}

			if !reflect.DeepEqual(unmapped, tt.wantUnmapped) {
				t.Errorf("unmapped = %v, want %v", unmapped, tt.wantUnmapped)
			}
		})
	}
}

func TestBuildLabels(t *testing.T) {
	got := BuildLabels("my-asr", "pm-system")
	want := map[string]string{
		pmapis.LabelManagedByKey:    pmapis.ManagedByGithub,
		pmapis.LabelSourceASR:       "my-asr",
		pmapis.LabelSourceNamespace: "pm-system",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildLabels = %v, want %v", got, want)
	}
}
