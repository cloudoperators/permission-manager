// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"slices"
	"testing"

	"github.com/go-logr/logr"
)

// TestResolveDesiredNames names resolvable entries, drops ones whose org is missing, and reports
// each missing org once (shared resolver cache).
func TestResolveDesiredNames(t *testing.T) {
	// Only "resolved-org" has a GithubOrganization CR; "absent" does not.
	c := newClient(newGithubOrgWithMeta("test-ns", "org-cr", "github.example.com", "ghe", "", "admin", "resolved-org"))
	r := newOrgResolver(c, "test-ns", logr.Discard())

	ds := &DesiredState{
		Teams: []DesiredTeam{
			{Instance: "github.example.com", Org: "resolved-org"},
			{Instance: "github.example.com", Org: "absent"},
		},
		TeamRepos: []DesiredTeamRepo{
			{Instance: "github.example.com", Org: "resolved-org", Permission: "push"},
			{Instance: "github.example.com", Org: "absent", Permission: "pull"},
		},
	}

	missing, err := resolveDesiredNames(context.Background(), r, "my-team", ds)
	if err != nil {
		t.Fatalf("resolveDesiredNames: %v", err)
	}

	// Resolved entries keep their place and get the instance-key naming; missing ones drop.
	if len(ds.Teams) != 1 || ds.Teams[0].CRName != "ghe--resolved-org--my-team" {
		t.Errorf("teams = %+v, want one named ghe--resolved-org--my-team", ds.Teams)
	}
	if len(ds.TeamRepos) != 1 || ds.TeamRepos[0].CRName != "ghe--resolved-org--my-team--push" {
		t.Errorf("team repos = %+v, want one named ghe--resolved-org--my-team--push", ds.TeamRepos)
	}

	// The absent org is reported once, even though a team and a repo both referenced it.
	want := []string{"test-ns/github.example.com--absent"}
	if !slices.Equal(missing, want) {
		t.Errorf("missingOrgs = %v, want %v", missing, want)
	}
}

// TestResolveDesiredNames_PropagatesLDAPAndTTLs threads the org CR's default-ldap-provider
// and job-retention TTL labels onto the resolved DesiredTeam.
func TestResolveDesiredNames_PropagatesLDAPAndTTLs(t *testing.T) {
	org := newGithubOrgWithMeta("test-ns", "org-cr", "github.example.com", "ghe", "ldap-prod", "admin", "resolved-org")
	org.Labels["repo-guard.cloudoperators.dev/completedTTL"] = "24h"
	org.Labels["repo-guard.cloudoperators.dev/failedTTL"] = "72h"
	c := newClient(org)
	r := newOrgResolver(c, "test-ns", logr.Discard())

	ds := &DesiredState{Teams: []DesiredTeam{{Instance: "github.example.com", Org: "resolved-org"}}}
	if _, err := resolveDesiredNames(context.Background(), r, "my-team", ds); err != nil {
		t.Fatalf("resolveDesiredNames: %v", err)
	}
	if len(ds.Teams) != 1 {
		t.Fatalf("teams = %+v, want one", ds.Teams)
	}
	got := ds.Teams[0]
	if got.LDAPProvider != "ldap-prod" {
		t.Errorf("LDAPProvider = %q, want ldap-prod", got.LDAPProvider)
	}
	if got.TTLs["repo-guard.cloudoperators.dev/completedTTL"] != "24h" ||
		got.TTLs["repo-guard.cloudoperators.dev/failedTTL"] != "72h" {
		t.Errorf("TTLs = %v, want completedTTL=24h failedTTL=72h", got.TTLs)
	}
}

// TestResolveDesiredNames_AdminOndemandOverride: an admin-ondemand org promotes "admin"; plain stays.
func TestResolveDesiredNames_AdminOndemandOverride(t *testing.T) {
	ondemand := newGithubOrgWithMeta("test-ns", "ondemand-cr", "github.example.com", "ghe", "", "admin-ondemand", "ondemand-org")
	plain := newGithubOrgWithMeta("test-ns", "plain-cr", "github.example.com", "ghe", "", "admin", "plain-org")
	c := newClient(ondemand, plain)
	r := newOrgResolver(c, "test-ns", logr.Discard())

	ds := &DesiredState{TeamRepos: []DesiredTeamRepo{
		{Instance: "github.example.com", Org: "ondemand-org", Permission: "admin"},
		{Instance: "github.example.com", Org: "plain-org", Permission: "admin"},
	}}
	if _, err := resolveDesiredNames(context.Background(), r, "my-team", ds); err != nil {
		t.Fatalf("resolveDesiredNames: %v", err)
	}

	byOrg := map[string]DesiredTeamRepo{}
	for _, tr := range ds.TeamRepos {
		byOrg[tr.Org] = tr
	}
	if got := byOrg["ondemand-org"]; got.Permission != "admin-ondemand" || got.CRName != "ghe--ondemand-org--my-team--admin-ondemand" {
		t.Errorf("ondemand org: permission=%q crName=%q, want admin-ondemand / ghe--ondemand-org--my-team--admin-ondemand", got.Permission, got.CRName)
	}
	if got := byOrg["plain-org"]; got.Permission != "admin" || got.CRName != "ghe--plain-org--my-team--admin" {
		t.Errorf("plain org: permission=%q crName=%q, want admin / ghe--plain-org--my-team--admin", got.Permission, got.CRName)
	}
}

func TestMergeMissingOrgs(t *testing.T) {
	if got := mergeMissingOrgs(nil, nil); got != nil {
		t.Errorf("empty union should be nil, got %v", got)
	}
	got := mergeMissingOrgs([]string{"ns/b", "ns/a"}, []string{"ns/a", "ns/c"})
	want := []string{"ns/a", "ns/b", "ns/c"}
	if !slices.Equal(got, want) {
		t.Errorf("merge = %v, want %v (sorted, de-duplicated)", got, want)
	}
}
