// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/pkg/audit"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = repoguardv1.AddToScheme(s)
	return s
}

func TestReconcileTeams_CreatesNewTeams(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	desired := []DesiredTeam{
		{Instance: "github.example.com", Org: "cc", CRName: "asr-test-github-example-com-cc"},
		{Instance: "github.com", Org: "sapcc", CRName: "asr-test-github-com-sapcc"},
	}

	err := ReconcileTeams(ctx, c, desired, "MY_TEAM", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeams failed: %v", err)
	}

	// Verify teams were created
	for _, dt := range desired {
		var team repoguardv1.GithubTeam
		key := types.NamespacedName{Name: dt.CRName, Namespace: "pm-system"}
		if err := c.Get(ctx, key, &team); err != nil {
			t.Errorf("failed to get team %q: %v", dt.CRName, err)
			continue
		}
		if team.Spec.Team != "MY_TEAM" {
			t.Errorf("team %q: expected teamName MY_TEAM, got %q", dt.CRName, team.Spec.Team)
		}
		if team.Labels[pmapis.LabelSourceASR] != "test-asr" {
			t.Errorf("team %q: expected label source-asr=test-asr, got %q", dt.CRName, team.Labels[pmapis.LabelSourceASR])
		}
		if team.Labels[pmapis.LabelManagedByKey] != pmapis.ManagedByGithub {
			t.Errorf("team %q: expected managed-by=github, got %q", dt.CRName, team.Labels[pmapis.LabelManagedByKey])
		}
		if team.Labels[pmapis.LabelSourceNamespace] != "default" {
			t.Errorf("team %q: expected source-namespace=default, got %q", dt.CRName, team.Labels[pmapis.LabelSourceNamespace])
		}
	}
}

func TestReconcileTeams_UpdatesExistingTeam(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create a team with old teamName
	existing := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-github-example-com-cc",
			Namespace: "pm-system",
			Labels: map[string]string{
				pmapis.LabelSourceASR: "test-asr",
			},
		},
		Spec: repoguardv1.GithubTeamSpec{Team: "OLD_NAME"},
	}
	existing.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	desired := []DesiredTeam{
		{Instance: "github.example.com", Org: "cc", CRName: "asr-test-github-example-com-cc"},
	}

	err := ReconcileTeams(ctx, c, desired, "NEW_NAME", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeams failed: %v", err)
	}

	// Verify the team was updated
	var team repoguardv1.GithubTeam
	key := types.NamespacedName{Name: "asr-test-github-example-com-cc", Namespace: "pm-system"}
	if err := c.Get(ctx, key, &team); err != nil {
		t.Fatalf("failed to get team: %v", err)
	}
	if team.Spec.Team != "NEW_NAME" {
		t.Errorf("expected teamName NEW_NAME after update, got %q", team.Spec.Team)
	}
}

func TestReconcileTeams_DeletesStaleTeams(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create a team that is no longer desired
	stale := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-test-github-example-com-old-org",
			Namespace: "pm-system",
			Labels: map[string]string{
				pmapis.LabelSourceASR: "test-asr",
			},
		},
		Spec: repoguardv1.GithubTeamSpec{Team: "MY_TEAM"},
	}
	stale.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build()

	// Desired state no longer includes this team
	desired := []DesiredTeam{
		{Instance: "github.example.com", Org: "cc", CRName: "asr-test-github-example-com-cc"},
	}

	err := ReconcileTeams(ctx, c, desired, "MY_TEAM", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeams failed: %v", err)
	}

	// Verify stale team was deleted
	var team repoguardv1.GithubTeam
	key := types.NamespacedName{Name: "asr-test-github-example-com-old-org", Namespace: "pm-system"}
	err = c.Get(ctx, key, &team)
	if err == nil {
		t.Error("stale team should have been deleted")
	}
}

func TestReconcileTeams_DoesNotDeleteTeamsFromOtherASRs(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create a team owned by a different ASR
	otherASRTeam := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-other-github-example-com-cc",
			Namespace: "pm-system",
			Labels: map[string]string{
				pmapis.LabelSourceASR: "other-asr", // different ASR
			},
		},
		Spec: repoguardv1.GithubTeamSpec{Team: "OTHER_TEAM"},
	}
	otherASRTeam.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(otherASRTeam).Build()

	// Our desired state is empty (no teams needed)
	desired := []DesiredTeam{}

	err := ReconcileTeams(ctx, c, desired, "MY_TEAM", "pm-system", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeams failed: %v", err)
	}

	// Verify other ASR's team was NOT deleted
	var team repoguardv1.GithubTeam
	key := types.NamespacedName{Name: "asr-other-github-example-com-cc", Namespace: "pm-system"}
	if err := c.Get(ctx, key, &team); err != nil {
		t.Errorf("team from other ASR should not be deleted: %v", err)
	}
}
