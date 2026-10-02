// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// resolveDesiredNames fills the CRName of every desired GithubTeam / GithubTeamRepository
// using the org CR's instance key (repo-guard naming convention). Entries whose
// GithubOrganization CR is missing are dropped from ds and reported in missingOrgs
// (as "namespace/name"), so the caller can surface InvalidConfig instead of naming a team
// it cannot address. A non-not-found list error aborts the pass and is returned.
func resolveDesiredNames(ctx context.Context, r *orgResolver, team string, ds *DesiredState) (missingOrgs []string, err error) {
	missing := make(map[string]struct{})

	keptTeams := ds.Teams[:0]
	for _, t := range ds.Teams {
		lookup, resErr := r.resolve(ctx, t.Instance, t.Org)
		if resErr != nil {
			if notFound, ok := errors.AsType[*githubOrgNotFoundError](resErr); ok {
				missing[fmt.Sprintf("%s/%s", notFound.namespace, notFound.name)] = struct{}{}
				continue
			}
			return nil, resErr
		}
		t.CRName = TeamCRName(lookup.InstanceKey, lookup.Org, team)
		t.LDAPProvider = lookup.LDAPProvider
		t.TTLs = lookup.TTLs
		keptTeams = append(keptTeams, t)
	}
	ds.Teams = keptTeams

	keptRepos := ds.TeamRepos[:0]
	for _, tr := range ds.TeamRepos {
		lookup, resErr := r.resolve(ctx, tr.Instance, tr.Org)
		if resErr != nil {
			if notFound, ok := errors.AsType[*githubOrgNotFoundError](resErr); ok {
				missing[fmt.Sprintf("%s/%s", notFound.namespace, notFound.name)] = struct{}{}
				continue
			}
			return nil, resErr
		}
		tr.CRName = TeamRepoCRName(lookup.InstanceKey, lookup.Org, team, tr.Permission)
		keptRepos = append(keptRepos, tr)
	}
	ds.TeamRepos = keptRepos

	missingOrgs = make([]string, 0, len(missing))
	for m := range missing {
		missingOrgs = append(missingOrgs, m)
	}
	slices.Sort(missingOrgs)
	return missingOrgs, nil
}

// mergeMissingOrgs returns the sorted, de-duplicated union of missing-org ids from the
// team-naming and org-patching paths, for a single InvalidConfig verdict.
func mergeMissingOrgs(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	merged := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string{}, a...), b...) {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		merged = append(merged, s)
	}
	slices.Sort(merged)
	return merged
}
