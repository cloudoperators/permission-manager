// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
)

func TestReconcileDelete_DeletesOwnedResourcesKeepingOtherASRs(t *testing.T) {
	asr := newASR("asr-del", "team-del", nil)

	// Owned resources span two namespaces; other-ASR resources must survive.
	ownedTeam := newOwnedTeam("asr-del-team", "ns-a", "asr-del")
	ownedRepo := newOwnedTeamRepo("asr-del-repo", "ns-b", "asr-del", []string{"r"}, "push")
	otherTeam := newOwnedTeam("asr-other-team", "ns-a", "asr-other")
	otherRepo := newOwnedTeamRepo("asr-other-repo", "ns-b", "asr-other", []string{"r"}, "push")

	p, c := newPhase(ownedTeam, ownedRepo, otherTeam, otherRepo)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete: %v", err)
	}

	var team repoguardv1.GithubTeam
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns-a", Name: "asr-del-team"}, &team); err == nil {
		t.Error("owned team should be deleted")
	}
	var repo repoguardv1.GithubTeamRepository
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns-b", Name: "asr-del-repo"}, &repo); err == nil {
		t.Error("owned team repository should be deleted")
	}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns-a", Name: "asr-other-team"}, &team); err != nil {
		t.Errorf("other ASR's team should be preserved: %v", err)
	}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns-b", Name: "asr-other-repo"}, &repo); err != nil {
		t.Errorf("other ASR's team repository should be preserved: %v", err)
	}
}

func TestReconcileDelete_RemovesOrgContributionsKeepingOtherASRs(t *testing.T) {
	asr := newASR("asr-del", "team-del", nil)

	// org-a is contributed to by the deleting ASR and another ASR.
	orgA := newOrgWithContrib("ns-a", "org-a", "team-del", &orgContribution{
		OwnerTeams:  []string{"team-del"},
		PublicTeams: []repoguardv1.GithubTeamWithPermission{{Team: "team-del", Permission: "push"}},
	})
	orgA.Spec.DefaultPublicRepositoryTeams = append(orgA.Spec.DefaultPublicRepositoryTeams,
		repoguardv1.GithubTeamWithPermission{Team: "team-keep", Permission: "pull"})
	orgA.Annotations[AnnotationASRManagedConfig] = "team-del,team-keep"
	orgA.Annotations[annotationASRDefaultTeamsPrefix+"team-keep"] = `{"publicTeams":[{"name":"team-keep","permission":"pull"}]}`

	// org-b lives in another namespace; cleanup is cluster-wide.
	orgB := newOrgWithContrib("ns-b", "org-b", "team-del", &orgContribution{OwnerTeams: []string{"team-del"}})

	p, c := newPhase(orgA, orgB)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete: %v", err)
	}

	var gotA repoguardv1.GithubOrganization
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns-a", Name: "org-a"}, &gotA); err != nil {
		t.Fatalf("get org-a: %v", err)
	}
	if len(gotA.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("team-del owner contribution not removed: %v", gotA.Spec.OrganizationOwnerTeams)
	}
	if len(gotA.Spec.DefaultPublicRepositoryTeams) != 1 || gotA.Spec.DefaultPublicRepositoryTeams[0].Team != "team-keep" {
		t.Errorf("expected only team-keep public to remain, got %v", gotA.Spec.DefaultPublicRepositoryTeams)
	}
	if got := gotA.Annotations[AnnotationASRManagedConfig]; got != "team-keep" {
		t.Errorf("managed config = %q, want team-keep", got)
	}

	var gotB repoguardv1.GithubOrganization
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns-b", Name: "org-b"}, &gotB); err != nil {
		t.Fatalf("get org-b: %v", err)
	}
	if len(gotB.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("org-b owner contribution not removed: %v", gotB.Spec.OrganizationOwnerTeams)
	}
}

func TestReconcileDelete_RemovesProviderStatus(t *testing.T) {
	asr := newASR("asr", "team", nil)
	asr.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderGithub: {Ready: true},
	}

	p, _ := newPhase()
	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete: %v", err)
	}
	if _, ok := asr.Status.Providers[pmmeta.ProviderGithub]; ok {
		t.Error("github provider status should be removed after successful cleanup")
	}
}

func TestReconcileDelete_IsIdempotent(t *testing.T) {
	asr := newASR("asr", "team", nil)
	p, _ := newPhase()
	for i := range 2 {
		if err := p.ReconcileDelete(context.Background(), asr); err != nil {
			t.Fatalf("ReconcileDelete run %d: %v", i+1, err)
		}
	}
}
