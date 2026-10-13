// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/internal/audit"
)

func TestReconcileTeamRepos_CreatesOneCRPerPermission(t *testing.T) {
	ctx := context.Background()
	c := newClient()
	desired := []DesiredTeamRepo{
		{Instance: "github.example.com", Org: "cc", Permission: "push", Repositories: []string{"repo-a", "repo-b"}, CRName: "ghe--cc--test-cc--push"},
		{Instance: "github.example.com", Org: "cc", Permission: "pull", Repositories: []string{"repo-c"}, CRName: "ghe--cc--test-cc--pull"},
	}

	if err := ReconcileTeamRepos(ctx, c, desired, "TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeamRepos: %v", err)
	}

	var push repoguardv1.GithubTeamRepository
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc--push", Namespace: "pm-system"}, &push); err != nil {
		t.Fatalf("get push repo: %v", err)
	}
	if push.Spec.Permission != "push" || len(push.Spec.Repository) != 2 {
		t.Errorf("unexpected push spec: %+v", push.Spec)
	}
	if push.Labels[pmapis.LabelSourceASR] != "test-asr" || push.Labels[pmapis.LabelManagedByKey] != pmapis.ManagedByGithub {
		t.Errorf("unexpected labels: %v", push.Labels)
	}

	var pull repoguardv1.GithubTeamRepository
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc--pull", Namespace: "pm-system"}, &pull); err != nil {
		t.Errorf("the pull CR should also be created: %v", err)
	}
}

func TestReconcileTeamRepos_UpdatesRepositoryList(t *testing.T) {
	ctx := context.Background()
	existing := newOwnedTeamRepo("ghe--cc--test-cc--push", "pm-system", "test-asr", []string{"repo-a"}, "push")
	c := newClient(existing)

	desired := []DesiredTeamRepo{
		{Instance: "github.example.com", Org: "cc", Permission: "push", Repositories: []string{"repo-a", "repo-b"}, CRName: "ghe--cc--test-cc--push"},
	}
	if err := ReconcileTeamRepos(ctx, c, desired, "TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeamRepos: %v", err)
	}

	var repo repoguardv1.GithubTeamRepository
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc--push", Namespace: "pm-system"}, &repo); err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if len(repo.Spec.Repository) != 2 {
		t.Errorf("expected 2 repos after update, got %v", repo.Spec.Repository)
	}
}

func TestReconcileTeamRepos_EmptyRepositoriesDeletesCR(t *testing.T) {
	ctx := context.Background()
	existing := newOwnedTeamRepo("ghe--cc--test-cc--push", "pm-system", "test-asr", []string{"repo-a"}, "push")
	c := newClient(existing)

	// An empty Repositories list means the CR is no longer desired.
	desired := []DesiredTeamRepo{
		{Instance: "github.example.com", Org: "cc", Permission: "push", Repositories: nil, CRName: "ghe--cc--test-cc--push"},
	}
	if err := ReconcileTeamRepos(ctx, c, desired, "TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeamRepos: %v", err)
	}

	var repo repoguardv1.GithubTeamRepository
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc--push", Namespace: "pm-system"}, &repo); err == nil {
		t.Error("a CR with empty repositories should have been deleted")
	}
}

func TestReconcileTeamRepos_DeletesStaleButKeepsOtherASRs(t *testing.T) {
	ctx := context.Background()
	stale := newOwnedTeamRepo("ghe--cc--test-cc--admin", "pm-system", "test-asr", []string{"old"}, "admin")
	otherASR := newOwnedTeamRepo("ghe--cc--other-cc--push", "pm-system", "other-asr", []string{"theirs"}, "push")
	c := newClient(stale, otherASR)

	desired := []DesiredTeamRepo{
		{Instance: "github.example.com", Org: "cc", Permission: "push", Repositories: []string{"new"}, CRName: "ghe--cc--test-cc--push"},
	}
	if err := ReconcileTeamRepos(ctx, c, desired, "TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeamRepos: %v", err)
	}

	var repo repoguardv1.GithubTeamRepository
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc--admin", Namespace: "pm-system"}, &repo); err == nil {
		t.Error("stale repo should have been deleted")
	}
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--other-cc--push", Namespace: "pm-system"}, &repo); err != nil {
		t.Errorf("other ASR's repo should be preserved: %v", err)
	}
}

// TestReconcileTeamRepos_SetsAllSpecFields: all PM-owned spec fields + source labels are written.
// TestReconcileTeamRepos_ConvergesOntoForeignCR: converge onto a same-name CR — no duplicate,
// managed-by claimed, foreign labels kept.
func TestReconcileTeamRepos_ConvergesOntoForeignCR(t *testing.T) {
	ctx := context.Background()
	existing := &repoguardv1.GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ghe--cc--test-cc--push",
			Namespace: "pm-system",
			Labels: map[string]string{
				pmapis.LabelManagedByKey:               "Helm",
				"repo-guard.cloudoperators.dev/dryRun": "true",
			},
		},
		Spec: repoguardv1.GithubTeamRepositorySpec{Repository: []string{"repo-a"}, Permission: "push"},
	}
	c := newClient(existing)

	desired := []DesiredTeamRepo{
		{Instance: "github.example.com", Org: "cc", Permission: "push", Repositories: []string{"repo-a", "repo-b"}, CRName: "ghe--cc--test-cc--push"},
	}
	if err := ReconcileTeamRepos(ctx, c, desired, "TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeamRepos: %v", err)
	}

	// Exactly one CR — PM converged, did not create a duplicate.
	var list repoguardv1.GithubTeamRepositoryList
	if err := c.List(ctx, &list); err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected exactly one GithubTeamRepository, got %d", len(list.Items))
	}

	repo := list.Items[0]
	if repo.Labels[pmapis.LabelManagedByKey] != pmapis.ManagedByGithub {
		t.Errorf("PM should claim managed-by=github on converge, got %q", repo.Labels[pmapis.LabelManagedByKey])
	}
	if repo.Labels["repo-guard.cloudoperators.dev/dryRun"] != "true" {
		t.Errorf("unrelated dryRun label should be preserved: %v", repo.Labels)
	}
	if repo.Labels[pmapis.LabelSourceASR] != "test-asr" {
		t.Errorf("PM should stamp its source-ASR label: %v", repo.Labels)
	}
	if len(repo.Spec.Repository) != 2 {
		t.Errorf("spec should be updated to PM's desired repositories, got %v", repo.Spec.Repository)
	}
}

func TestReconcileTeamRepos_SetsAllSpecFields(t *testing.T) {
	ctx := context.Background()
	c := newClient()
	desired := []DesiredTeamRepo{
		{Instance: "github.example.com", Org: "cc", Permission: "push", Repositories: []string{"repo-a"}, CRName: "ghe--cc--test-cc--push"},
	}
	if err := ReconcileTeamRepos(ctx, c, desired, "TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeamRepos: %v", err)
	}

	var repo repoguardv1.GithubTeamRepository
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc--push", Namespace: "pm-system"}, &repo); err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if repo.Spec.Github != "github.example.com" || repo.Spec.Organization != "cc" || repo.Spec.Team != "TEAM" {
		t.Errorf("unexpected identity spec: github=%q org=%q team=%q", repo.Spec.Github, repo.Spec.Organization, repo.Spec.Team)
	}
	if repo.Spec.Permission != "push" || len(repo.Spec.Repository) != 1 || repo.Spec.Repository[0] != "repo-a" {
		t.Errorf("unexpected permission/repository: %+v", repo.Spec)
	}
	if repo.Labels[pmapis.LabelSourceASR] != "test-asr" || repo.Labels[pmapis.LabelManagedByKey] != pmapis.ManagedByGithub {
		t.Errorf("unexpected labels: %v", repo.Labels)
	}
}

// TestReconcileTeamRepos_PermissionChangeReplacesCR: changing the permission replaces the CR (name segment).
func TestReconcileTeamRepos_PermissionChangeReplacesCR(t *testing.T) {
	ctx := context.Background()
	existing := newOwnedTeamRepo("ghe--cc--test-cc--admin", "pm-system", "test-asr", []string{"repo-a"}, "admin")
	c := newClient(existing)

	desired := []DesiredTeamRepo{
		{Instance: "github.example.com", Org: "cc", Permission: "admin-ondemand", Repositories: []string{"repo-a"}, CRName: "ghe--cc--test-cc--admin-ondemand"},
	}
	if err := ReconcileTeamRepos(ctx, c, desired, "TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeamRepos: %v", err)
	}

	var repo repoguardv1.GithubTeamRepository
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc--admin", Namespace: "pm-system"}, &repo); err == nil {
		t.Error("old-permission CR should have been deleted")
	}
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc--admin-ondemand", Namespace: "pm-system"}, &repo); err != nil {
		t.Fatalf("new-permission CR should have been created: %v", err)
	}
	if repo.Spec.Permission != "admin-ondemand" {
		t.Errorf("permission = %q, want admin-ondemand", repo.Spec.Permission)
	}
}

// TestReconcileTeamRepos_SkipsStaleDeletionWhenDisabled: deleteStale=false keeps an undesired owned CR.
func TestReconcileTeamRepos_SkipsStaleDeletionWhenDisabled(t *testing.T) {
	ctx := context.Background()
	stale := newOwnedTeamRepo("ghe--cc--test-cc--admin", "pm-system", "test-asr", []string{"old"}, "admin")
	c := newClient(stale)

	desired := []DesiredTeamRepo{
		{Instance: "github.example.com", Org: "cc", Permission: "push", Repositories: []string{"new"}, CRName: "ghe--cc--test-cc--push"},
	}
	if err := ReconcileTeamRepos(ctx, c, desired, "TEAM", "pm-system", "test-asr", "default", false, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeamRepos: %v", err)
	}

	var repo repoguardv1.GithubTeamRepository
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc--admin", Namespace: "pm-system"}, &repo); err != nil {
		t.Errorf("stale CR should be kept when deleteStale is false: %v", err)
	}
}
