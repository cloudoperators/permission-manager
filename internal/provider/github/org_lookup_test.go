// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"errors"
	"testing"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// listCountingClient wraps a client.Client and counts List calls, so tests can assert the
// resolver serves repeated lookups from its cache instead of re-listing the apiserver.
type listCountingClient struct {
	client.Client
	listCalls int
}

func (c *listCountingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	c.listCalls++
	return c.Client.List(ctx, list, opts...)
}

func TestOrgResolver_Resolve_ReturnsLabelsAndSpec(t *testing.T) {
	org := newGithubOrgWithMeta("default", "gh--my-org", "github.example.com", "ghe", "corp-ldap", "admin-ondemand", "my-org")
	c := newClient(org)
	r := newOrgResolver(c, "default", logr.Discard())

	got, err := r.resolve(context.Background(), "github.example.com", "my-org")
	if err != nil {
		t.Fatalf("resolve: unexpected error: %v", err)
	}
	want := orgLookup{
		InstanceKey:     "ghe",
		Org:             "my-org",
		LDAPProvider:    "corp-ldap",
		AdminPermission: "admin-ondemand",
	}
	if *got != want {
		t.Fatalf("resolve = %+v, want %+v", *got, want)
	}
}

func TestOrgResolver_Resolve_Defaults(t *testing.T) {
	// No LDAP label, no admin-permission label: LDAP empty, admin defaults to "admin".
	org := newGithubOrgWithMeta("default", "gh--my-org", "github.example.com", "ghe", "", "", "my-org")
	c := newClient(org)
	r := newOrgResolver(c, "default", logr.Discard())

	got, err := r.resolve(context.Background(), "github.example.com", "my-org")
	if err != nil {
		t.Fatalf("resolve: unexpected error: %v", err)
	}
	if got.LDAPProvider != "" {
		t.Errorf("LDAPProvider = %q, want empty", got.LDAPProvider)
	}
	if got.AdminPermission != repoguardv1.GithubTeamPermissionAdmin {
		t.Errorf("AdminPermission = %q, want %q", got.AdminPermission, repoguardv1.GithubTeamPermissionAdmin)
	}
}

func TestOrgResolver_Resolve_UnknownAdminPermissionDefaultsToAdmin(t *testing.T) {
	org := newGithubOrgWithMeta("default", "gh--my-org", "github.example.com", "ghe", "", "bogus", "my-org")
	c := newClient(org)
	r := newOrgResolver(c, "default", logr.Discard())

	got, err := r.resolve(context.Background(), "github.example.com", "my-org")
	if err != nil {
		t.Fatalf("resolve: unexpected error: %v", err)
	}
	if got.AdminPermission != repoguardv1.GithubTeamPermissionAdmin {
		t.Errorf("AdminPermission = %q, want %q", got.AdminPermission, repoguardv1.GithubTeamPermissionAdmin)
	}
}

func TestOrgResolver_Resolve_NoLabelMatch_NotFound(t *testing.T) {
	// Org exists but on a different instance.
	org := newGithubOrgWithMeta("default", "gh--my-org", "github.other.com", "ghe", "", "admin", "my-org")
	c := newClient(org)
	r := newOrgResolver(c, "default", logr.Discard())

	_, err := r.resolve(context.Background(), "github.example.com", "my-org")
	if _, ok := errors.AsType[*githubOrgNotFoundError](err); !ok {
		t.Fatalf("resolve error = %v, want *githubOrgNotFoundError", err)
	}
}

func TestOrgResolver_Resolve_LabelMatchButWrongOrg_NotFound(t *testing.T) {
	// Instance label matches, but no org on it has spec.organization == "my-org".
	org := newGithubOrgWithMeta("default", "gh--other", "github.example.com", "ghe", "", "admin", "other-org")
	c := newClient(org)
	r := newOrgResolver(c, "default", logr.Discard())

	_, err := r.resolve(context.Background(), "github.example.com", "my-org")
	if _, ok := errors.AsType[*githubOrgNotFoundError](err); !ok {
		t.Fatalf("resolve error = %v, want *githubOrgNotFoundError", err)
	}
}

func TestOrgResolver_Resolve_PicksOrgMatchingSpecAmongInstance(t *testing.T) {
	// Two orgs on the same instance; resolve must return the one matching spec.organization.
	a := newGithubOrgWithMeta("default", "gh--org-a", "github.example.com", "key-a", "", "admin", "org-a")
	b := newGithubOrgWithMeta("default", "gh--org-b", "github.example.com", "key-b", "", "admin", "org-b")
	c := newClient(a, b)
	r := newOrgResolver(c, "default", logr.Discard())

	got, err := r.resolve(context.Background(), "github.example.com", "org-b")
	if err != nil {
		t.Fatalf("resolve: unexpected error: %v", err)
	}
	if got.InstanceKey != "key-b" || got.Org != "org-b" {
		t.Fatalf("resolve = %+v, want org-b/key-b", *got)
	}
}

func TestOrgResolver_Resolve_CachesResult(t *testing.T) {
	org := newGithubOrgWithMeta("default", "gh--my-org", "github.example.com", "ghe", "", "admin", "my-org")
	counting := &listCountingClient{Client: newClient(org)}
	r := newOrgResolver(counting, "default", logr.Discard())

	if _, err := r.resolve(context.Background(), "github.example.com", "my-org"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if _, err := r.resolve(context.Background(), "github.example.com", "my-org"); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if counting.listCalls != 1 {
		t.Fatalf("List called %d times, want 1 (second lookup should hit the cache)", counting.listCalls)
	}
}

func TestOrgResolver_Resolve_CachesNotFound(t *testing.T) {
	counting := &listCountingClient{Client: newClient()}
	r := newOrgResolver(counting, "default", logr.Discard())

	_, err1 := r.resolve(context.Background(), "github.example.com", "my-org")
	_, err2 := r.resolve(context.Background(), "github.example.com", "my-org")
	var nf1, nf2 *githubOrgNotFoundError
	if !errors.As(err1, &nf1) || !errors.As(err2, &nf2) {
		t.Fatalf("both resolves should return not-found: %v, %v", err1, err2)
	}
	if counting.listCalls != 1 {
		t.Fatalf("List called %d times, want 1 (cached not-found should not re-list)", counting.listCalls)
	}
}

func TestOrgTargetID(t *testing.T) {
	// Matches the SanitizeInstance(instance)+"/"+org id computeProviderStatus records.
	org := newGithubOrgWithMeta("default", "gh--my-org", "github.example.com", "ghe", "", "", "my-org")
	if got := OrgTargetID(org); got != "github-example-com/my-org" {
		t.Errorf("OrgTargetID = %q, want github-example-com/my-org", got)
	}

	// No instance label -> empty, so watch matching treats it as "no target".
	noLabel := newGithubOrgWithMeta("default", "gh--my-org", "", "", "", "", "my-org")
	if got := OrgTargetID(noLabel); got != "" {
		t.Errorf("OrgTargetID without instance label = %q, want empty", got)
	}

	// No spec.organization -> empty.
	noOrg := newGithubOrgWithMeta("default", "gh--x", "github.example.com", "ghe", "", "", "")
	if got := OrgTargetID(noOrg); got != "" {
		t.Errorf("OrgTargetID without spec.organization = %q, want empty", got)
	}
}
