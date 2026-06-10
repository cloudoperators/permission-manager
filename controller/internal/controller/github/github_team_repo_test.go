package github

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
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

	err := ReconcileTeamRepos(ctx, c, desired, "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the GithubTeamRepository was created
	var repo GithubTeamRepository
	key := types.NamespacedName{Name: "asr-test-github-example-com-cc-push", Namespace: "pm-system"}
	if err := c.Get(ctx, key, &repo); err != nil {
		t.Fatalf("failed to get team repo: %v", err)
	}

	if repo.Spec.Permission != "push" {
		t.Errorf("expected permission push, got %q", repo.Spec.Permission)
	}
	if len(repo.Spec.Repositories) != 2 {
		t.Fatalf("expected 2 repositories, got %d", len(repo.Spec.Repositories))
	}
	if repo.Spec.Repositories[0] != "repo-a" || repo.Spec.Repositories[1] != "repo-b" {
		t.Errorf("expected repos [repo-a, repo-b], got %v", repo.Spec.Repositories)
	}
	if repo.Labels[LabelSourceASR] != "test-asr" {
		t.Errorf("expected source-asr label, got %q", repo.Labels[LabelSourceASR])
	}
	if repo.Labels[LabelManagedByKey] != LabelManagedByGithub {
		t.Errorf("expected managed-by=github, got %q", repo.Labels[LabelManagedByKey])
	}
}

func TestReconcileTeamRepos_UpdatesExistingRepo(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create with one repo
	existing := &GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-github-example-com-cc-push",
			Namespace: "pm-system",
			Labels: map[string]string{
				LabelSourceASR: "test-asr",
			},
		},
		Spec: GithubTeamRepositorySpec{
			Repositories: []string{"repo-a"},
			Permission:   "push",
		},
	}
	existing.SetGroupVersionKind(GroupVersion.WithKind("GithubTeamRepository"))

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

	err := ReconcileTeamRepos(ctx, c, desired, "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the repo list was updated
	var repo GithubTeamRepository
	key := types.NamespacedName{Name: "asr-test-github-example-com-cc-push", Namespace: "pm-system"}
	if err := c.Get(ctx, key, &repo); err != nil {
		t.Fatalf("failed to get team repo: %v", err)
	}
	if len(repo.Spec.Repositories) != 2 {
		t.Errorf("expected 2 repos after update, got %d", len(repo.Spec.Repositories))
	}
}

func TestReconcileTeamRepos_RemovesRepoFromList(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create with two repos
	existing := &GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-github-example-com-cc-push",
			Namespace: "pm-system",
			Labels: map[string]string{
				LabelSourceASR: "test-asr",
			},
		},
		Spec: GithubTeamRepositorySpec{
			Repositories: []string{"repo-a", "repo-b"},
			Permission:   "push",
		},
	}
	existing.SetGroupVersionKind(GroupVersion.WithKind("GithubTeamRepository"))

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

	err := ReconcileTeamRepos(ctx, c, desired, "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the repo list was updated (repo-b removed)
	var repo GithubTeamRepository
	key := types.NamespacedName{Name: "asr-test-github-example-com-cc-push", Namespace: "pm-system"}
	if err := c.Get(ctx, key, &repo); err != nil {
		t.Fatalf("failed to get team repo: %v", err)
	}
	if len(repo.Spec.Repositories) != 1 {
		t.Fatalf("expected 1 repo after removal, got %d", len(repo.Spec.Repositories))
	}
	if repo.Spec.Repositories[0] != "repo-a" {
		t.Errorf("expected remaining repo to be repo-a, got %q", repo.Spec.Repositories[0])
	}
}

func TestReconcileTeamRepos_DeletesCRWhenEmpty(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create
	existing := &GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-github-example-com-cc-push",
			Namespace: "pm-system",
			Labels: map[string]string{
				LabelSourceASR: "test-asr",
			},
		},
		Spec: GithubTeamRepositorySpec{
			Repositories: []string{"repo-a"},
			Permission:   "push",
		},
	}
	existing.SetGroupVersionKind(GroupVersion.WithKind("GithubTeamRepository"))

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

	err := ReconcileTeamRepos(ctx, c, desired, "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the CR was deleted (not in desired set with repos)
	var repo GithubTeamRepository
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
	stale := &GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-github-example-com-cc-admin",
			Namespace: "pm-system",
			Labels: map[string]string{
				LabelSourceASR: "test-asr",
			},
		},
		Spec: GithubTeamRepositorySpec{
			Repositories: []string{"old-repo"},
			Permission:   "admin",
		},
	}
	stale.SetGroupVersionKind(GroupVersion.WithKind("GithubTeamRepository"))

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

	err := ReconcileTeamRepos(ctx, c, desired, "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the stale CR was deleted
	var repo GithubTeamRepository
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
	otherASR := &GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-other-github-example-com-cc-push",
			Namespace: "pm-system",
			Labels: map[string]string{
				LabelSourceASR: "other-asr",
			},
		},
		Spec: GithubTeamRepositorySpec{
			Repositories: []string{"their-repo"},
			Permission:   "push",
		},
	}
	otherASR.SetGroupVersionKind(GroupVersion.WithKind("GithubTeamRepository"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(otherASR).Build()

	// Empty desired state for our ASR
	desired := []DesiredTeamRepo{}

	err := ReconcileTeamRepos(ctx, c, desired, "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify the other ASR's repo was NOT deleted
	var repo GithubTeamRepository
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

	err := ReconcileTeamRepos(ctx, c, desired, "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeamRepos failed: %v", err)
	}

	// Verify both CRs created
	for _, d := range desired {
		var repo GithubTeamRepository
		key := types.NamespacedName{Name: d.CRName, Namespace: "pm-system"}
		if err := c.Get(ctx, key, &repo); err != nil {
			t.Errorf("failed to get team repo %q: %v", d.CRName, err)
			continue
		}
		if repo.Spec.Permission != d.Permission {
			t.Errorf("repo %q: expected permission %q, got %q", d.CRName, d.Permission, repo.Spec.Permission)
		}
	}
}

func TestDeleteStaleTeamRepos_NilDesiredNamesDeletesAll(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Create two repos owned by the same ASR
	repo1 := &GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-repo1",
			Namespace: "pm-system",
			Labels:    map[string]string{LabelSourceASR: "test-asr"},
		},
		Spec: GithubTeamRepositorySpec{Repositories: []string{"r1"}, Permission: "push"},
	}
	repo1.SetGroupVersionKind(GroupVersion.WithKind("GithubTeamRepository"))

	repo2 := &GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-repo2",
			Namespace: "pm-system",
			Labels:    map[string]string{LabelSourceASR: "test-asr"},
		},
		Spec: GithubTeamRepositorySpec{Repositories: []string{"r2"}, Permission: "admin"},
	}
	repo2.SetGroupVersionKind(GroupVersion.WithKind("GithubTeamRepository"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo1, repo2).Build()

	// nil desiredNames means "delete all"
	err := deleteStaleTeamRepos(ctx, c, "pm-system", "test-asr", nil, audit.Disabled())
	if err != nil {
		t.Fatalf("deleteStaleTeamRepos failed: %v", err)
	}

	// Verify both deleted
	var list GithubTeamRepositoryList
	if err := c.List(ctx, &list, client.InNamespace("pm-system")); err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("expected 0 items after delete-all, got %d", len(list.Items))
	}
}
