// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"testing"

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
