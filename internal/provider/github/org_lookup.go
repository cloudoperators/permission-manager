// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"fmt"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// repo-guard stamps these labels on each GithubOrganization CR (repo-guard#246).
// PM reads them to name GithubTeam/GithubTeamRepository CRs and set their provider
// config; they are repo-guard's domain, so they live here rather than in api/well_known.go.
const (
	// labelGithubInstance holds the GitHub hostname (matches the CCRN instance); the lookup selector.
	labelGithubInstance = "repo-guard.cloudoperators.dev/github-instance"
	// labelGithubInstanceKey holds the short instance key used as the CR-name prefix.
	labelGithubInstanceKey = "repo-guard.cloudoperators.dev/github-instance-key"
	// labelDefaultLDAPProvider holds the default LDAP provider name for the org's teams.
	labelDefaultLDAPProvider = "repo-guard.cloudoperators.dev/default-ldap-provider"
)

// orgLookup holds the per-org values PM reads from a GithubOrganization CR: the short
// instance key (CR-name prefix), the org name, the default LDAP provider, and the admin
// permission role.
type orgLookup struct {
	InstanceKey     string
	Org             string
	LDAPProvider    string
	AdminPermission string
}

// orgCacheKey identifies a resolved org by the (hostname, org) pair it was looked up with.
type orgCacheKey struct {
	hostname string
	org      string
}

// orgResolveResult memoizes a resolve outcome, including a not-found error, so a repeated
// lookup within one reconcile does not re-list the apiserver.
type orgResolveResult struct {
	lookup *orgLookup
	err    error
}

// orgResolver resolves (CCRN hostname, org name) -> GithubOrganization CR by label selector
// and caches the result for the duration of one reconcile. Construct one per Reconcile and
// pass it to the callers that need per-org values; do not share it across reconciles.
type orgResolver struct {
	c         client.Client
	namespace string
	log       logr.Logger
	cache     map[orgCacheKey]orgResolveResult
}

// newOrgResolver returns a resolver scoped to a single namespace with an empty cache.
func newOrgResolver(c client.Client, namespace string, log logr.Logger) *orgResolver { //nolint:unparam // namespace varies once consumers wire in (#74/#75)
	return &orgResolver{
		c:         c,
		namespace: namespace,
		log:       log,
		cache:     make(map[orgCacheKey]orgResolveResult),
	}
}

// resolve returns the org values for a (hostname, org) pair. It selects GithubOrganization CRs
// in the resolver's namespace by the github-instance label, then matches on spec.organization.
// A missing CR is returned as *githubOrgNotFoundError so the caller can surface InvalidConfig
// (consistent with the org-patching path). Results (including not-found) are cached.
func (r *orgResolver) resolve(ctx context.Context, hostname, org string) (*orgLookup, error) { //nolint:unparam // hostname varies once consumers wire in (#74/#75)
	key := orgCacheKey{hostname: hostname, org: org}
	if cached, ok := r.cache[key]; ok {
		return cached.lookup, cached.err
	}

	res := r.doResolve(ctx, hostname, org)
	r.cache[key] = res
	return res.lookup, res.err
}

func (r *orgResolver) doResolve(ctx context.Context, hostname, org string) orgResolveResult {
	var orgList repoguardv1.GithubOrganizationList
	if err := r.c.List(ctx, &orgList,
		client.InNamespace(r.namespace),
		client.MatchingLabels{labelGithubInstance: hostname},
	); err != nil {
		return orgResolveResult{err: fmt.Errorf("failed to list GithubOrganizations for instance %q: %w", hostname, err)}
	}

	var match *repoguardv1.GithubOrganization
	for i := range orgList.Items {
		if orgList.Items[i].Spec.Organization != org {
			continue
		}
		if match != nil {
			r.log.Info("multiple GithubOrganizations match instance+org, using the first",
				"instance", hostname, "org", org, "using", match.Name, "ignored", orgList.Items[i].Name)
			break
		}
		match = &orgList.Items[i]
	}

	if match == nil {
		// The CR's real name is unknown; use the stable <host>--<org> id consumers expect.
		return orgResolveResult{err: &githubOrgNotFoundError{namespace: r.namespace, name: fmt.Sprintf("%s--%s", hostname, org)}}
	}

	return orgResolveResult{lookup: &orgLookup{
		InstanceKey:     match.Labels[labelGithubInstanceKey],
		Org:             match.Spec.Organization,
		LDAPProvider:    match.Labels[labelDefaultLDAPProvider],
		AdminPermission: adminPermissionOrDefault(match.Labels[adminPermissionLabel]),
	}}
}

// adminPermissionOrDefault normalizes the admin-permission label value, defaulting to "admin"
// (empty or unrecognized -> admin). Mirrors the rule in Phase.orgAdminPermission, without logging.
func adminPermissionOrDefault(v string) string {
	switch v {
	case repoguardv1.GithubTeamPermissionAdmin, repoguardv1.GithubTeamPermissionAdminOndemand:
		return v
	default:
		return repoguardv1.GithubTeamPermissionAdmin
	}
}

// OrgTargetID returns the provider target id for a GithubOrganization CR: the same
// SanitizeInstance(instance)+"/"+org id computeProviderStatus records per team. The instance
// comes from the github-instance label repo-guard stamps on the CR. Returns "" when the CR
// lacks that label or spec.organization, so callers can treat it as "no match".
func OrgTargetID(org *repoguardv1.GithubOrganization) string {
	host := org.Labels[labelGithubInstance]
	if host == "" || org.Spec.Organization == "" {
		return ""
	}
	return SanitizeInstance(host) + "/" + org.Spec.Organization
}
