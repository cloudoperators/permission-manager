// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

type teamPerm struct {
	team       string
	permission repoguardv1.GithubTeamPermission
}

func teamPerms(teams []repoguardv1.GithubTeamWithPermission) []teamPerm {
	var out []teamPerm
	for _, t := range teams {
		out = append(out, teamPerm{t.Team, t.Permission})
	}
	return out
}

func TestReconcileOrganizations(t *testing.T) {
	mapping := map[string]string{"ADMIN": "admin", "WRITE": "push", "READ": "pull"}

	tests := []struct {
		name        string
		perms       []v1alpha1.Permission
		wantOwners  []string
		wantPublic  []teamPerm
		wantPrivate []teamPerm
	}{
		{
			name:       "organization admin adds an owner team",
			perms:      []v1alpha1.Permission{perm(orgCCRN("my-org"), "ADMIN")},
			wantOwners: []string{"team-x"},
		},
		{
			name:  "organization non-admin adds nothing",
			perms: []v1alpha1.Permission{perm(orgCCRN("my-org"), "WRITE")},
		},
		{
			name:       "repository_type public adds a default public team",
			perms:      []v1alpha1.Permission{perm(repoTypeCCRN("public"), "WRITE")},
			wantPublic: []teamPerm{{"team-x", "push"}},
		},
		{
			name:        "repository_type private adds a default private team",
			perms:       []v1alpha1.Permission{perm(repoTypeCCRN("private"), "READ")},
			wantPrivate: []teamPerm{{"team-x", "pull"}},
		},
		{
			name:        "repository_type wildcard adds both public and private",
			perms:       []v1alpha1.Permission{perm(repoTypeCCRN("*"), "WRITE")},
			wantPublic:  []teamPerm{{"team-x", "push"}},
			wantPrivate: []teamPerm{{"team-x", "push"}},
		},
		{
			name:  "non-github CCRN is ignored",
			perms: []v1alpha1.Permission{perm("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/c", "ADMIN")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, c := newPhase(newGithubOrg("test-ns", "my-org"))
			asr := newASR("asr", "team-x", tt.perms)

			if err := p.reconcileOrganizations(context.Background(), asr, connectorDef(mapping), "test-ns"); err != nil {
				t.Fatalf("reconcileOrganizations: %v", err)
			}

			var org repoguardv1.GithubOrganization
			if err := c.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &org); err != nil {
				t.Fatalf("get org: %v", err)
			}
			if !slices.Equal(org.Spec.OrganizationOwnerTeams, tt.wantOwners) {
				t.Errorf("owner teams = %v, want %v", org.Spec.OrganizationOwnerTeams, tt.wantOwners)
			}
			if got := teamPerms(org.Spec.DefaultPublicRepositoryTeams); !slices.Equal(got, tt.wantPublic) {
				t.Errorf("public teams = %v, want %v", got, tt.wantPublic)
			}
			if got := teamPerms(org.Spec.DefaultPrivateRepositoryTeams); !slices.Equal(got, tt.wantPrivate) {
				t.Errorf("private teams = %v, want %v", got, tt.wantPrivate)
			}
		})
	}
}

func TestReconcileOrganizations_RemovesStaleContributionsOnReReconcile(t *testing.T) {
	mapping := map[string]string{"ADMIN": "admin", "WRITE": "push", "READ": "pull"}
	p, c := newPhase(newGithubOrg("test-ns", "my-org"))

	// First reconcile: org admin plus a public writer.
	asr := newASR("asr", "team-x", []v1alpha1.Permission{
		perm(orgCCRN("my-org"), "ADMIN"),
		perm(repoTypeCCRN("public"), "WRITE"),
	})
	if err := p.reconcileOrganizations(context.Background(), asr, connectorDef(mapping), "test-ns"); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	// Second reconcile: permissions narrowed to private read only.
	asr.Spec.Permissions = []v1alpha1.Permission{perm(repoTypeCCRN("private"), "READ")}
	if err := p.reconcileOrganizations(context.Background(), asr, connectorDef(mapping), "test-ns"); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	var org repoguardv1.GithubOrganization
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &org); err != nil {
		t.Fatalf("get org: %v", err)
	}
	if len(org.Spec.OrganizationOwnerTeams) != 0 || len(org.Spec.DefaultPublicRepositoryTeams) != 0 {
		t.Errorf("stale contributions not removed: owners=%v public=%v",
			org.Spec.OrganizationOwnerTeams, org.Spec.DefaultPublicRepositoryTeams)
	}
	if got := teamPerms(org.Spec.DefaultPrivateRepositoryTeams); !slices.Equal(got, []teamPerm{{"team-x", "pull"}}) {
		t.Errorf("expected the new private contribution, got %v", got)
	}
}

func TestReconcileOrganizations_MergesMultipleASRs(t *testing.T) {
	mapping := map[string]string{"ADMIN": "admin", "WRITE": "push"}
	p, c := newPhase(newGithubOrg("test-ns", "my-org"))

	asr1 := newASR("asr-1", "team-one", []v1alpha1.Permission{perm(orgCCRN("my-org"), "ADMIN")})
	asr2 := newASR("asr-2", "team-two", []v1alpha1.Permission{perm(repoTypeCCRN("public"), "WRITE")})

	for _, asr := range []*v1alpha1.ApplicationSpecificRole{asr1, asr2} {
		if err := p.reconcileOrganizations(context.Background(), asr, connectorDef(mapping), "test-ns"); err != nil {
			t.Fatalf("reconcile %s: %v", asr.Name, err)
		}
	}

	var org repoguardv1.GithubOrganization
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &org); err != nil {
		t.Fatalf("get org: %v", err)
	}
	if !slices.Equal(org.Spec.OrganizationOwnerTeams, []string{"team-one"}) {
		t.Errorf("owner teams = %v, want [team-one]", org.Spec.OrganizationOwnerTeams)
	}
	if got := teamPerms(org.Spec.DefaultPublicRepositoryTeams); !slices.Equal(got, []teamPerm{{"team-two", "push"}}) {
		t.Errorf("public teams = %v, want [team-two push]", got)
	}
	if got := org.Annotations[AnnotationASRManagedConfig]; got != "team-one,team-two" {
		t.Errorf("managed config = %q, want team-one,team-two", got)
	}
}

func TestReconcileOrganizations_MissingOrgReturnsError(t *testing.T) {
	p, _ := newPhase() // org not seeded
	asr := newASR("asr", "team-x", []v1alpha1.Permission{perm(orgCCRN("missing"), "ADMIN")})

	if err := p.reconcileOrganizations(context.Background(), asr, connectorDef(map[string]string{"ADMIN": "admin"}), "test-ns"); err == nil {
		t.Fatal("expected an error when the GithubOrganization does not exist")
	}
}

func TestRemoveASRContributions(t *testing.T) {
	org := newOrgWithContrib("test-ns", "my-org", "team-a", &orgContribution{
		OwnerTeams:   []string{"team-a"},
		PublicTeams:  []repoguardv1.GithubTeamWithPermission{{Team: "team-a", Permission: "push"}},
		PrivateTeams: []repoguardv1.GithubTeamWithPermission{{Team: "team-a", Permission: "pull"}},
	})
	// A second ASR's public contribution and tracking that must survive.
	org.Spec.DefaultPublicRepositoryTeams = append(org.Spec.DefaultPublicRepositoryTeams,
		repoguardv1.GithubTeamWithPermission{Team: "team-b", Permission: "admin"})
	org.Annotations[AnnotationASRManagedConfig] = "team-a,team-b"
	org.Annotations[annotationASRDefaultTeamsPrefix+"team-b"] = `{"publicTeams":[{"name":"team-b","permission":"admin"}]}`

	removeASRContributions(org, "team-a")
	removeASRFromAnnotations(org, "team-a")

	if len(org.Spec.OrganizationOwnerTeams) != 0 || len(org.Spec.DefaultPrivateRepositoryTeams) != 0 {
		t.Errorf("team-a owner/private contributions not removed")
	}
	if got := teamPerms(org.Spec.DefaultPublicRepositoryTeams); !slices.Equal(got, []teamPerm{{"team-b", "admin"}}) {
		t.Errorf("expected only team-b public to remain, got %v", got)
	}
	if got := org.Annotations[AnnotationASRManagedConfig]; got != "team-b" {
		t.Errorf("managed config = %q, want team-b", got)
	}
	if _, ok := org.Annotations[annotationASRDefaultTeamsPrefix+"team-a"]; ok {
		t.Error("team-a per-ASR annotation should be removed")
	}
}

func TestAppendUniqueAndRemove(t *testing.T) {
	if got := appendUnique([]string{"a"}, "a"); len(got) != 1 {
		t.Errorf("appendUnique should dedupe equal strings, got %v", got)
	}
	if got := appendUnique([]string{"a"}, "b"); len(got) != 2 {
		t.Errorf("appendUnique should add new strings, got %v", got)
	}
	if got := remove([]string{"a", "b", "c"}, "b"); !slices.Equal(got, []string{"a", "c"}) {
		t.Errorf("remove should drop the matching element, got %v", got)
	}

	// Equality includes the permission, so the same team with a different permission is kept.
	teams := []repoguardv1.GithubTeamWithPermission{{Team: "t", Permission: "push"}}
	if got := appendUnique(teams, repoguardv1.GithubTeamWithPermission{Team: "t", Permission: "push"}); len(got) != 1 {
		t.Errorf("appendUnique should dedupe identical team entries, got %v", got)
	}
	if got := appendUnique(teams, repoguardv1.GithubTeamWithPermission{Team: "t", Permission: "pull"}); len(got) != 2 {
		t.Errorf("appendUnique should keep the same team with a different permission, got %v", got)
	}
}

func TestPhaseReconcile_EndToEnd(t *testing.T) {
	mapping := map[string]string{"ADMIN": "admin", "WRITE": "push"}
	p, c := newPhase(newGithubOrg("test-ns", "my-org"))

	asr := newASR("asr-e2e", "team-full", []v1alpha1.Permission{
		perm(orgCCRN("my-org"), "ADMIN"),
		perm(repoCCRN("github.example.com", "my-org", "repo-a"), "WRITE"),
	})

	if err := p.Reconcile(context.Background(), asr, newConnectorBinding(mapping)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// The org owner team was patched from the admin permission.
	var org repoguardv1.GithubOrganization
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &org); err != nil {
		t.Fatalf("get org: %v", err)
	}
	if !slices.Equal(org.Spec.OrganizationOwnerTeams, []string{"team-full"}) {
		t.Errorf("owner teams = %v, want [team-full]", org.Spec.OrganizationOwnerTeams)
	}

	// A team and a team-repository CR were created in the target namespace.
	var teams repoguardv1.GithubTeamList
	if err := c.List(context.Background(), &teams, client.InNamespace("test-ns")); err != nil {
		t.Fatalf("list teams: %v", err)
	}
	if len(teams.Items) != 1 {
		t.Errorf("expected 1 team, got %d", len(teams.Items))
	}
	var repos repoguardv1.GithubTeamRepositoryList
	if err := c.List(context.Background(), &repos, client.InNamespace("test-ns")); err != nil {
		t.Fatalf("list team repositories: %v", err)
	}
	if len(repos.Items) != 1 {
		t.Errorf("expected 1 team repository, got %d", len(repos.Items))
	}

	// Provider status was written on the ASR with the expected target.
	ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderGithub)
	if ps == nil {
		t.Fatal("expected github provider status on the ASR")
	}
	if pmmeta.GetTargetStatus(ps.Targets, "github-example-com/my-org") == nil {
		t.Errorf("expected target github-example-com/my-org, got %+v", ps.Targets)
	}
}
