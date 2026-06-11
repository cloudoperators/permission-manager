// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestGroupVersionRegistration(t *testing.T) {
	if GroupVersion.Group != "repo-guard.cloudoperators.dev" {
		t.Errorf("GroupVersion.Group = %q, want %q", GroupVersion.Group, "repo-guard.cloudoperators.dev")
	}
	if GroupVersion.Version != "v1" {
		t.Errorf("GroupVersion.Version = %q, want %q", GroupVersion.Version, "v1")
	}
}

func TestAddToScheme(t *testing.T) {
	scheme := runtime.NewScheme()
	err := AddToScheme(scheme)
	if err != nil {
		t.Fatalf("AddToScheme failed: %v", err)
	}

	// Verify GithubTeam is registered
	gvk := schema.GroupVersionKind{Group: "repo-guard.cloudoperators.dev", Version: "v1", Kind: "GithubTeam"}
	obj, err := scheme.New(gvk)
	if err != nil {
		t.Fatalf("scheme.New(GithubTeam) failed: %v", err)
	}
	if _, ok := obj.(*GithubTeam); !ok {
		t.Fatalf("expected *GithubTeam, got %T", obj)
	}

	// Verify GithubTeamList is registered
	gvk = schema.GroupVersionKind{Group: "repo-guard.cloudoperators.dev", Version: "v1", Kind: "GithubTeamList"}
	obj, err = scheme.New(gvk)
	if err != nil {
		t.Fatalf("scheme.New(GithubTeamList) failed: %v", err)
	}
	if _, ok := obj.(*GithubTeamList); !ok {
		t.Fatalf("expected *GithubTeamList, got %T", obj)
	}

	// Verify GithubTeamRepository is registered
	gvk = schema.GroupVersionKind{Group: "repo-guard.cloudoperators.dev", Version: "v1", Kind: "GithubTeamRepository"}
	obj, err = scheme.New(gvk)
	if err != nil {
		t.Fatalf("scheme.New(GithubTeamRepository) failed: %v", err)
	}
	if _, ok := obj.(*GithubTeamRepository); !ok {
		t.Fatalf("expected *GithubTeamRepository, got %T", obj)
	}

	// Verify GithubTeamRepositoryList is registered
	gvk = schema.GroupVersionKind{Group: "repo-guard.cloudoperators.dev", Version: "v1", Kind: "GithubTeamRepositoryList"}
	obj, err = scheme.New(gvk)
	if err != nil {
		t.Fatalf("scheme.New(GithubTeamRepositoryList) failed: %v", err)
	}
	if _, ok := obj.(*GithubTeamRepositoryList); !ok {
		t.Fatalf("expected *GithubTeamRepositoryList, got %T", obj)
	}

	// Verify GithubOrganization is registered
	gvk = schema.GroupVersionKind{Group: "repo-guard.cloudoperators.dev", Version: "v1", Kind: "GithubOrganization"}
	obj, err = scheme.New(gvk)
	if err != nil {
		t.Fatalf("scheme.New(GithubOrganization) failed: %v", err)
	}
	if _, ok := obj.(*GithubOrganization); !ok {
		t.Fatalf("expected *GithubOrganization, got %T", obj)
	}

	// Verify GithubOrganizationList is registered
	gvk = schema.GroupVersionKind{Group: "repo-guard.cloudoperators.dev", Version: "v1", Kind: "GithubOrganizationList"}
	obj, err = scheme.New(gvk)
	if err != nil {
		t.Fatalf("scheme.New(GithubOrganizationList) failed: %v", err)
	}
	if _, ok := obj.(*GithubOrganizationList); !ok {
		t.Fatalf("expected *GithubOrganizationList, got %T", obj)
	}
}

func TestGithubTeamFields(t *testing.T) {
	team := &GithubTeam{
		Spec: GithubTeamSpec{
			TeamName: "my-team",
		},
		Status: GithubTeamStatus{
			TeamStatus: "complete",
		},
	}

	if team.Spec.TeamName != "my-team" {
		t.Errorf("TeamName = %q, want %q", team.Spec.TeamName, "my-team")
	}
	if team.Status.TeamStatus != "complete" {
		t.Errorf("TeamStatus = %q, want %q", team.Status.TeamStatus, "complete")
	}
}

func TestGithubTeamRepositoryFields(t *testing.T) {
	repo := &GithubTeamRepository{
		Spec: GithubTeamRepositorySpec{
			Repositories: []string{"my-repo"},
			Permission:   "write",
		},
	}

	if len(repo.Spec.Repositories) != 1 || repo.Spec.Repositories[0] != "my-repo" {
		t.Errorf("Repositories = %v, want [my-repo]", repo.Spec.Repositories)
	}
	if repo.Spec.Permission != "write" {
		t.Errorf("Permission = %q, want %q", repo.Spec.Permission, "write")
	}
}

func TestGithubOrganizationFields(t *testing.T) {
	org := &GithubOrganization{
		Spec: GithubOrganizationSpec{
			DefaultPublicRepositoryTeams: []GithubTeamWithPermission{
				{Name: "devs", Permission: "push"},
			},
			DefaultPrivateRepositoryTeams: []GithubTeamWithPermission{
				{Name: "admins", Permission: "admin"},
			},
			OrganizationOwnerTeams: []string{"org-owners"},
		},
	}

	if len(org.Spec.DefaultPublicRepositoryTeams) != 1 {
		t.Fatalf("expected 1 public team, got %d", len(org.Spec.DefaultPublicRepositoryTeams))
	}
	if org.Spec.DefaultPublicRepositoryTeams[0].Name != "devs" {
		t.Errorf("public team name = %q, want %q", org.Spec.DefaultPublicRepositoryTeams[0].Name, "devs")
	}
	if org.Spec.DefaultPublicRepositoryTeams[0].Permission != "push" {
		t.Errorf("public team permission = %q, want %q", org.Spec.DefaultPublicRepositoryTeams[0].Permission, "push")
	}

	if len(org.Spec.DefaultPrivateRepositoryTeams) != 1 {
		t.Fatalf("expected 1 private team, got %d", len(org.Spec.DefaultPrivateRepositoryTeams))
	}
	if org.Spec.DefaultPrivateRepositoryTeams[0].Name != "admins" {
		t.Errorf("private team name = %q, want %q", org.Spec.DefaultPrivateRepositoryTeams[0].Name, "admins")
	}

	if len(org.Spec.OrganizationOwnerTeams) != 1 {
		t.Fatalf("expected 1 owner team, got %d", len(org.Spec.OrganizationOwnerTeams))
	}
	if org.Spec.OrganizationOwnerTeams[0] != "org-owners" {
		t.Errorf("owner team = %q, want %q", org.Spec.OrganizationOwnerTeams[0], "org-owners")
	}
}

func TestDeepCopyObject(t *testing.T) {
	team := &GithubTeam{
		Spec: GithubTeamSpec{TeamName: "original"},
		Status: GithubTeamStatus{TeamStatus: "complete"},
	}

	copied := team.DeepCopyObject()
	copiedTeam, ok := copied.(*GithubTeam)
	if !ok {
		t.Fatalf("DeepCopyObject returned %T, want *GithubTeam", copied)
	}

	// Verify it's a different pointer
	if copiedTeam == team {
		t.Error("DeepCopyObject returned same pointer")
	}

	// Verify values are copied
	if copiedTeam.Spec.TeamName != "original" {
		t.Errorf("copied TeamName = %q, want %q", copiedTeam.Spec.TeamName, "original")
	}

	// Verify modifying copy doesn't affect original
	copiedTeam.Spec.TeamName = "modified"
	if team.Spec.TeamName != "original" {
		t.Error("modifying copy affected original")
	}
}

func TestGithubTeamListDeepCopy(t *testing.T) {
	list := &GithubTeamList{
		Items: []GithubTeam{
			{Spec: GithubTeamSpec{TeamName: "team1"}},
			{Spec: GithubTeamSpec{TeamName: "team2"}},
		},
	}

	copied := list.DeepCopyObject()
	copiedList, ok := copied.(*GithubTeamList)
	if !ok {
		t.Fatalf("DeepCopyObject returned %T, want *GithubTeamList", copied)
	}

	if len(copiedList.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(copiedList.Items))
	}
	if copiedList.Items[0].Spec.TeamName != "team1" {
		t.Errorf("first item TeamName = %q, want %q", copiedList.Items[0].Spec.TeamName, "team1")
	}

	// Modifying copy doesn't affect original
	copiedList.Items[0].Spec.TeamName = "modified"
	if list.Items[0].Spec.TeamName != "team1" {
		t.Error("modifying copy affected original")
	}
}

func TestGithubOrganizationDeepCopy(t *testing.T) {
	org := &GithubOrganization{
		Spec: GithubOrganizationSpec{
			DefaultPublicRepositoryTeams: []GithubTeamWithPermission{
				{Name: "devs", Permission: "push"},
			},
			OrganizationOwnerTeams: []string{"owners"},
		},
	}

	copied := org.DeepCopyObject()
	copiedOrg, ok := copied.(*GithubOrganization)
	if !ok {
		t.Fatalf("DeepCopyObject returned %T, want *GithubOrganization", copied)
	}

	// Verify deep copy of slices
	copiedOrg.Spec.DefaultPublicRepositoryTeams[0].Name = "modified"
	if org.Spec.DefaultPublicRepositoryTeams[0].Name != "devs" {
		t.Error("modifying copy affected original slice")
	}

	copiedOrg.Spec.OrganizationOwnerTeams[0] = "modified"
	if org.Spec.OrganizationOwnerTeams[0] != "owners" {
		t.Error("modifying copy affected original string slice")
	}
}

func TestGithubTeamRepositoryDeepCopy(t *testing.T) {
	repo := &GithubTeamRepository{
		Spec: GithubTeamRepositorySpec{
			Repositories: []string{"my-repo"},
			Permission:   "admin",
		},
	}

	copied := repo.DeepCopyObject()
	copiedRepo, ok := copied.(*GithubTeamRepository)
	if !ok {
		t.Fatalf("DeepCopyObject returned %T, want *GithubTeamRepository", copied)
	}

	if len(copiedRepo.Spec.Repositories) != 1 || copiedRepo.Spec.Repositories[0] != "my-repo" {
		t.Errorf("Repositories = %v, want [my-repo]", copiedRepo.Spec.Repositories)
	}

	copiedRepo.Spec.Repositories[0] = "modified"
	if repo.Spec.Repositories[0] != "my-repo" {
		t.Error("modifying copy affected original")
	}
}

func TestGithubTeamRepositoryListDeepCopy(t *testing.T) {
	list := &GithubTeamRepositoryList{
		Items: []GithubTeamRepository{
			{Spec: GithubTeamRepositorySpec{Repositories: []string{"repo1"}, Permission: "push"}},
			{Spec: GithubTeamRepositorySpec{Repositories: []string{"repo2"}, Permission: "pull"}},
		},
	}

	copied := list.DeepCopyObject()
	copiedList, ok := copied.(*GithubTeamRepositoryList)
	if !ok {
		t.Fatalf("DeepCopyObject returned %T, want *GithubTeamRepositoryList", copied)
	}

	if len(copiedList.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(copiedList.Items))
	}

	copiedList.Items[0].Spec.Repositories[0] = "modified"
	if list.Items[0].Spec.Repositories[0] != "repo1" {
		t.Error("modifying copy affected original")
	}
}

func TestGithubOrganizationListDeepCopy(t *testing.T) {
	list := &GithubOrganizationList{
		Items: []GithubOrganization{
			{Spec: GithubOrganizationSpec{OrganizationOwnerTeams: []string{"team1"}}},
		},
	}

	copied := list.DeepCopyObject()
	copiedList, ok := copied.(*GithubOrganizationList)
	if !ok {
		t.Fatalf("DeepCopyObject returned %T, want *GithubOrganizationList", copied)
	}

	if len(copiedList.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(copiedList.Items))
	}

	copiedList.Items[0].Spec.OrganizationOwnerTeams[0] = "modified"
	if list.Items[0].Spec.OrganizationOwnerTeams[0] != "team1" {
		t.Error("modifying copy affected original")
	}
}

func TestDeepCopyNilReceivers(t *testing.T) {
	// Test nil receivers return nil
	var nilTeam *GithubTeam
	if nilTeam.DeepCopy() != nil {
		t.Error("DeepCopy of nil GithubTeam should return nil")
	}

	var nilTeamList *GithubTeamList
	if nilTeamList.DeepCopy() != nil {
		t.Error("DeepCopy of nil GithubTeamList should return nil")
	}

	var nilRepo *GithubTeamRepository
	if nilRepo.DeepCopy() != nil {
		t.Error("DeepCopy of nil GithubTeamRepository should return nil")
	}

	var nilRepoList *GithubTeamRepositoryList
	if nilRepoList.DeepCopy() != nil {
		t.Error("DeepCopy of nil GithubTeamRepositoryList should return nil")
	}

	var nilOrg *GithubOrganization
	if nilOrg.DeepCopy() != nil {
		t.Error("DeepCopy of nil GithubOrganization should return nil")
	}

	var nilOrgList *GithubOrganizationList
	if nilOrgList.DeepCopy() != nil {
		t.Error("DeepCopy of nil GithubOrganizationList should return nil")
	}
}

func TestDeepCopyWithNilSlices(t *testing.T) {
	// Test deep copy when spec slices are nil
	org := &GithubOrganization{
		Spec: GithubOrganizationSpec{},
	}

	copied := org.DeepCopy()
	if copied.Spec.DefaultPublicRepositoryTeams != nil {
		t.Error("expected nil DefaultPublicRepositoryTeams in copy")
	}
	if copied.Spec.DefaultPrivateRepositoryTeams != nil {
		t.Error("expected nil DefaultPrivateRepositoryTeams in copy")
	}
	if copied.Spec.OrganizationOwnerTeams != nil {
		t.Error("expected nil OrganizationOwnerTeams in copy")
	}
}
