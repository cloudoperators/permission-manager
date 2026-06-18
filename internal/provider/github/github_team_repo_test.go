// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/internal/audit"
)

func TestReconcileTeamRepos_CreatesNewRepos(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	desired := []DesiredTeamRepo{
		{
			Instance:     "github.example.com",
			Org:          "cc",
			Permission:   "push",
			Repositories: []string{"repo-a", "repo-b"},
			CRName:       "asr-test-github-example-com-cc-push",
		},
	}

	err := ReconcileTeamRepos(ctx, c, desired, "test-asr", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the GithubTeamRepository was created
	var repo repoguardv1.GithubTeamRepository
	key := types.NamespacedName{Name: "asr-test-github-example-com-cc-push", Namespace: "pm-system"}
	if err := c.Get(ctx, key, &repo); err != nil {
		t.Fatalf("failed to get team repo: %v", err)
	}

	if repo.Spec.Permission != "push" {
		t.Errorf("expected permission push, got %q", repo.Spec.Permission)
	}
	if len(repo.Spec.Repository) != 2 {
		t.Fatalf("expected 2 repositories, got %d", len(repo.Spec.Repository))
	}
	if repo.Spec.Repository[0] != "repo-a" || repo.Spec.Repository[1] != "repo-b" {
		t.Errorf("expected repos [repo-a, repo-b], got %v", repo.Spec.Repository)
	}
	if repo.Labels[pmapis.LabelSourceASR] != "test-asr" {
		t.Errorf("expected source-asr label, got %q", repo.Labels[pmapis.LabelSourceASR])
	}
	if repo.Labels[pmapis.LabelManagedByKey] != pmapis.ManagedByGithub {
		t.Errorf("expected managed-by=github, got %q", repo.Labels[pmapis.LabelManagedByKey])
	}
}

func TestReconcileTeamRepos_UpdatesExistingRepo(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create with one repo
	existing := &repoguardv1.GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-github-example-com-cc-push",
			Namespace: "pm-system",
			Labels: map[string]string{
				pmapis.LabelSourceASR: "test-asr",
			},
		},
		Spec: repoguardv1.GithubTeamRepositorySpec{
			Repository: []string{"repo-a"},
			Permission: repoguardv1.GithubTeamPermission("push"),
		},
	}
	existing.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeamRepository"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	// Desired state now has two repos
	desired := []DesiredTeamRepo{
		{
			Instance:     "github.example.com",
			Org:          "cc",
			Permission:   "push",
			Repositories: []string{"repo-a", "repo-b"},
			CRName:       "asr-test-github-example-com-cc-push",
		},
	}

	err := ReconcileTeamRepos(ctx, c, desired, "test-asr", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the repo list was updated
	var repo repoguardv1.GithubTeamRepository
	key := types.NamespacedName{Name: "asr-test-github-example-com-cc-push", Namespace: "pm-system"}
	if err := c.Get(ctx, key, &repo); err != nil {
		t.Fatalf("failed to get team repo: %v", err)
	}
	if len(repo.Spec.Repository) != 2 {
		t.Errorf("expected 2 repos after update, got %d", len(repo.Spec.Repository))
	}
}

func TestReconcileTeamRepos_RemovesRepoFromList(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create with two repos
	existing := &repoguardv1.GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-github-example-com-cc-push",
			Namespace: "pm-system",
			Labels: map[string]string{
				pmapis.LabelSourceASR: "test-asr",
			},
		},
		Spec: repoguardv1.GithubTeamRepositorySpec{
			Repository: []string{"repo-a", "repo-b"},
			Permission: repoguardv1.GithubTeamPermission("push"),
		},
	}
	existing.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeamRepository"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	// Desired state now only has one repo
	desired := []DesiredTeamRepo{
		{
			Instance:     "github.example.com",
			Org:          "cc",
			Permission:   "push",
			Repositories: []string{"repo-a"},
			CRName:       "asr-test-github-example-com-cc-push",
		},
	}

	err := ReconcileTeamRepos(ctx, c, desired, "test-asr", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the repo list was updated (repo-b removed)
	var repo repoguardv1.GithubTeamRepository
	key := types.NamespacedName{Name: "asr-test-github-example-com-cc-push", Namespace: "pm-system"}
	if err := c.Get(ctx, key, &repo); err != nil {
		t.Fatalf("failed to get team repo: %v", err)
	}
	if len(repo.Spec.Repository) != 1 {
		t.Fatalf("expected 1 repo after removal, got %d", len(repo.Spec.Repository))
	}
	if repo.Spec.Repository[0] != "repo-a" {
		t.Errorf("expected remaining repo to be repo-a, got %q", repo.Spec.Repository[0])
	}
}

func TestReconcileTeamRepos_DeletesCRWhenEmpty(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create
	existing := &repoguardv1.GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-github-example-com-cc-push",
			Namespace: "pm-system",
			Labels: map[string]string{
				pmapis.LabelSourceASR: "test-asr",
			},
		},
		Spec: repoguardv1.GithubTeamRepositorySpec{
			Repository: []string{"repo-a"},
			Permission: repoguardv1.GithubTeamPermission("push"),
		},
	}
	existing.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeamRepository"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	// Desired state has empty repos (should be treated as "delete this CR")
	desired := []DesiredTeamRepo{
		{
			Instance:     "github.example.com",
			Org:          "cc",
			Permission:   "push",
			Repositories: []string{}, // empty
			CRName:       "asr-test-github-example-com-cc-push",
		},
	}

	err := ReconcileTeamRepos(ctx, c, desired, "test-asr", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the CR was deleted (not in desired set with repos)
	var repo repoguardv1.GithubTeamRepository
	key := types.NamespacedName{Name: "asr-test-github-example-com-cc-push", Namespace: "pm-system"}
	err = c.Get(ctx, key, &repo)
	if err == nil {
		t.Error("CR with empty repos should have been deleted")
	}
}

func TestReconcileTeamRepos_DeletesStaleRepos(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create a stale team repo
	stale := &repoguardv1.GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-github-example-com-cc-admin",
			Namespace: "pm-system",
			Labels: map[string]string{
				pmapis.LabelSourceASR: "test-asr",
			},
		},
		Spec: repoguardv1.GithubTeamRepositorySpec{
			Repository: []string{"old-repo"},
			Permission: repoguardv1.GithubTeamPermission("admin"),
		},
	}
	stale.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeamRepository"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build()

	// Desired state has a different CR name (the old one is stale)
	desired := []DesiredTeamRepo{
		{
			Instance:     "github.example.com",
			Org:          "cc",
			Permission:   "push",
			Repositories: []string{"new-repo"},
			CRName:       "asr-test-github-example-com-cc-push",
		},
	}

	err := ReconcileTeamRepos(ctx, c, desired, "test-asr", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the stale CR was deleted
	var repo repoguardv1.GithubTeamRepository
	key := types.NamespacedName{Name: "asr-test-github-example-com-cc-admin", Namespace: "pm-system"}
	err = c.Get(ctx, key, &repo)
	if err == nil {
		t.Error("stale team repo should have been deleted")
	}

	// Verify the new one was created
	key = types.NamespacedName{Name: "asr-test-github-example-com-cc-push", Namespace: "pm-system"}
	if err := c.Get(ctx, key, &repo); err != nil {
		t.Errorf("new team repo should exist: %v", err)
	}
}

func TestReconcileTeamRepos_DoesNotDeleteReposFromOtherASRs(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create a team repo from another ASR
	otherASR := &repoguardv1.GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-other-github-example-com-cc-push",
			Namespace: "pm-system",
			Labels: map[string]string{
				pmapis.LabelSourceASR: "other-asr",
			},
		},
		Spec: repoguardv1.GithubTeamRepositorySpec{
			Repository: []string{"their-repo"},
			Permission: repoguardv1.GithubTeamPermission("push"),
		},
	}
	otherASR.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeamRepository"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(otherASR).Build()

	// Empty desired state for our ASR
	desired := []DesiredTeamRepo{}

	err := ReconcileTeamRepos(ctx, c, desired, "test-asr", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the other ASR's repo was NOT deleted
	var repo repoguardv1.GithubTeamRepository
	key := types.NamespacedName{Name: "asr-other-github-example-com-cc-push", Namespace: "pm-system"}
	if err := c.Get(ctx, key, &repo); err != nil {
		t.Errorf("team repo from other ASR should not be deleted: %v", err)
	}
}

func TestReconcileTeamRepos_MultiplePermissions(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	desired := []DesiredTeamRepo{
		{
			Instance:     "github.example.com",
			Org:          "cc",
			Permission:   "push",
			Repositories: []string{"repo-a"},
			CRName:       "asr-test-github-example-com-cc-push",
		},
		{
			Instance:     "github.example.com",
			Org:          "cc",
			Permission:   "pull",
			Repositories: []string{"repo-b"},
			CRName:       "asr-test-github-example-com-cc-pull",
		},
	}

	err := ReconcileTeamRepos(ctx, c, desired, "test-asr", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify both CRs created
	for _, d := range desired {
		var repo repoguardv1.GithubTeamRepository
		key := types.NamespacedName{Name: d.CRName, Namespace: "pm-system"}
		if err := c.Get(ctx, key, &repo); err != nil {
			t.Errorf("failed to get team repo %q: %v", d.CRName, err)
			continue
		}
		if repo.Spec.Permission != repoguardv1.GithubTeamPermission(d.Permission) {
			t.Errorf("repo %q: expected permission %q, got %q", d.CRName, d.Permission, repo.Spec.Permission)
		}
	}
}

func TestDeleteStaleTeamRepos_NilDesiredNamesDeletesAll(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Create two repos owned by the same ASR
	repo1 := &repoguardv1.GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-repo1",
			Namespace: "pm-system",
			Labels:    map[string]string{pmapis.LabelSourceASR: "test-asr"},
		},
		Spec: repoguardv1.GithubTeamRepositorySpec{Repository: []string{"r1"}, Permission: repoguardv1.GithubTeamPermission("push")},
	}
	repo1.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeamRepository"))

	repo2 := &repoguardv1.GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-repo2",
			Namespace: "pm-system",
			Labels:    map[string]string{pmapis.LabelSourceASR: "test-asr"},
		},
		Spec: repoguardv1.GithubTeamRepositorySpec{Repository: []string{"r2"}, Permission: repoguardv1.GithubTeamPermission("admin")},
	}
	repo2.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeamRepository"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo1, repo2).Build()

	// nil desiredNames means "delete all"
	err := deleteStaleTeamRepos(ctx, c, "pm-system", "test-asr", nil, audit.Disabled())
	if err != nil {
		t.Fatalf("deleteStaleTeamRepos failed: %v", err)
	}

	// Verify both deleted
	var list repoguardv1.GithubTeamRepositoryList
	if err := c.List(ctx, &list, client.InNamespace("pm-system")); err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("expected 0 items after delete-all, got %d", len(list.Items))
	}
}
