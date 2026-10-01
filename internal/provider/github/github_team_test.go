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

func TestReconcileTeams_CreatesTeamWithSpecAndLabels(t *testing.T) {
	ctx := context.Background()
	c := newClient()
	desired := []DesiredTeam{{Instance: "github.example.com", Org: "cc", CRName: "ghe--cc--test-cc"}}

	if err := ReconcileTeams(ctx, c, desired, "MY_TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeams: %v", err)
	}

	var team repoguardv1.GithubTeam
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc", Namespace: "pm-system"}, &team); err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Spec.Team != "MY_TEAM" || team.Spec.Github != "github.example.com" || team.Spec.Organization != "cc" {
		t.Errorf("unexpected spec: %+v", team.Spec)
	}
	if team.Labels[pmapis.LabelSourceASR] != "test-asr" || team.Labels[pmapis.LabelManagedByKey] != pmapis.ManagedByGithub {
		t.Errorf("unexpected labels: %v", team.Labels)
	}
}

func TestReconcileTeams_UpdatesExistingTeam(t *testing.T) {
	ctx := context.Background()
	existing := newOwnedTeam("ghe--cc--test-cc", "pm-system", "test-asr")
	existing.Spec.Team = "OLD"
	c := newClient(existing)

	desired := []DesiredTeam{{Instance: "github.example.com", Org: "cc", CRName: "ghe--cc--test-cc"}}
	if err := ReconcileTeams(ctx, c, desired, "NEW", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeams: %v", err)
	}

	var team repoguardv1.GithubTeam
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-cc", Namespace: "pm-system"}, &team); err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Spec.Team != "NEW" {
		t.Errorf("expected Team=NEW after update, got %q", team.Spec.Team)
	}
}

func TestReconcileTeams_DeletesStaleButKeepsOtherASRs(t *testing.T) {
	ctx := context.Background()
	stale := newOwnedTeam("ghe--cc--test-stale", "pm-system", "test-asr")
	otherASR := newOwnedTeam("ghe--cc--other-cc", "pm-system", "other-asr")
	c := newClient(stale, otherASR)

	// Desired state no longer includes the stale team.
	desired := []DesiredTeam{{Instance: "github.example.com", Org: "cc", CRName: "ghe--cc--test-cc"}}
	if err := ReconcileTeams(ctx, c, desired, "MY_TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeams: %v", err)
	}

	var team repoguardv1.GithubTeam
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--test-stale", Namespace: "pm-system"}, &team); err == nil {
		t.Error("stale team should have been deleted")
	}
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--other-cc", Namespace: "pm-system"}, &team); err != nil {
		t.Errorf("other ASR's team should be preserved: %v", err)
	}
}
