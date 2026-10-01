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

func TestReconcileTeams_SetsOperationalLabelsAndLDAP(t *testing.T) {
	ctx := context.Background()
	c := newClient()
	desired := []DesiredTeam{{
		Instance:     "github.example.com",
		Org:          "cc",
		CRName:       "ghe--cc--my_team",
		LDAPProvider: "ldap-prod",
		TTLs: map[string]string{
			"repo-guard.cloudoperators.dev/completedTTL": "24h",
			"repo-guard.cloudoperators.dev/failedTTL":    "72h",
		},
	}}

	if err := ReconcileTeams(ctx, c, desired, "MY_TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeams: %v", err)
	}

	var team repoguardv1.GithubTeam
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--my_team", Namespace: "pm-system"}, &team); err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Labels["repo-guard.cloudoperators.dev/addUser"] != "true" ||
		team.Labels["repo-guard.cloudoperators.dev/removeUser"] != "true" {
		t.Errorf("membership gates not enabled: %v", team.Labels)
	}
	if team.Labels["repo-guard.cloudoperators.dev/completedTTL"] != "24h" ||
		team.Labels["repo-guard.cloudoperators.dev/failedTTL"] != "72h" {
		t.Errorf("TTL labels not propagated from org: %v", team.Labels)
	}
	if team.Spec.ExternalMemberProvider == nil || team.Spec.ExternalMemberProvider.LDAP == nil {
		t.Fatalf("externalMemberProvider.ldap not set: %+v", team.Spec)
	}
	if got := team.Spec.ExternalMemberProvider.LDAP; got.ExternalMemberProvider != "ldap-prod" || got.Group != "MY_TEAM" {
		t.Errorf("ldap provider/group = %q/%q, want ldap-prod/MY_TEAM", got.ExternalMemberProvider, got.Group)
	}
}

// TestReconcileTeams_PrunesDroppedTTLLabel: a TTL the org no longer sets is removed from the
// team CR (plain maps.Copy would leave it stale), while foreign labels survive.
func TestReconcileTeams_PrunesDroppedTTLLabel(t *testing.T) {
	ctx := context.Background()
	existing := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ghe--cc--my_team",
			Namespace: "pm-system",
			Labels: map[string]string{
				"repo-guard.cloudoperators.dev/completedTTL": "24h",
				"repo-guard.cloudoperators.dev/failedTTL":    "72h",
				"repo-guard.cloudoperators.dev/dryRun":       "true",
			},
		},
	}
	c := newClient(existing)

	// Org now sets only completedTTL (updated); failedTTL was dropped.
	desired := []DesiredTeam{{
		Instance: "github.example.com",
		Org:      "cc",
		CRName:   "ghe--cc--my_team",
		TTLs:     map[string]string{"repo-guard.cloudoperators.dev/completedTTL": "48h"},
	}}
	if err := ReconcileTeams(ctx, c, desired, "MY_TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeams: %v", err)
	}

	var team repoguardv1.GithubTeam
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--my_team", Namespace: "pm-system"}, &team); err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Labels["repo-guard.cloudoperators.dev/completedTTL"] != "48h" {
		t.Errorf("completedTTL = %q, want 48h", team.Labels["repo-guard.cloudoperators.dev/completedTTL"])
	}
	if v, ok := team.Labels["repo-guard.cloudoperators.dev/failedTTL"]; ok {
		t.Errorf("dropped failedTTL should be pruned, got %q", v)
	}
	if team.Labels["repo-guard.cloudoperators.dev/dryRun"] != "true" {
		t.Errorf("foreign dryRun label should be preserved: %v", team.Labels)
	}
}

// TestReconcileTeams_MergePreservesForeignLabels: when PM converges onto a CR another manager
// (e.g. Helm) created, PM claims it (managed-by=github) but keeps unrelated labels and spec fields.
func TestReconcileTeams_MergePreservesForeignLabels(t *testing.T) {
	ctx := context.Background()
	existing := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ghe--cc--my_team",
			Namespace: "pm-system",
			Labels: map[string]string{
				pmapis.LabelManagedByKey:               "Helm",
				"repo-guard.cloudoperators.dev/dryRun": "true",
			},
		},
		Spec: repoguardv1.GithubTeamSpec{GreenhouseTeam: "keep-me"},
	}
	c := newClient(existing)

	desired := []DesiredTeam{{Instance: "github.example.com", Org: "cc", CRName: "ghe--cc--my_team", LDAPProvider: "ldap-prod"}}
	if err := ReconcileTeams(ctx, c, desired, "MY_TEAM", "pm-system", "test-asr", "default", true, audit.Disabled()); err != nil {
		t.Fatalf("ReconcileTeams: %v", err)
	}

	var team repoguardv1.GithubTeam
	if err := c.Get(ctx, types.NamespacedName{Name: "ghe--cc--my_team", Namespace: "pm-system"}, &team); err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Labels[pmapis.LabelManagedByKey] != pmapis.ManagedByGithub {
		t.Errorf("PM should claim managed-by=github on converge, got %q", team.Labels[pmapis.LabelManagedByKey])
	}
	if team.Labels["repo-guard.cloudoperators.dev/dryRun"] != "true" {
		t.Errorf("unrelated dryRun label should be preserved: %v", team.Labels)
	}
	if team.Labels["repo-guard.cloudoperators.dev/addUser"] != "true" || team.Labels[pmapis.LabelSourceASR] != "test-asr" {
		t.Errorf("PM should still merge its own labels: %v", team.Labels)
	}
	if team.Spec.GreenhouseTeam != "keep-me" {
		t.Errorf("unrelated spec field should be preserved, got GreenhouseTeam=%q", team.Spec.GreenhouseTeam)
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
