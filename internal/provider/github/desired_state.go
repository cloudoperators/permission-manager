// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"cmp"
	"slices"
	"strings"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
)

const (
	// urnCCRNPrefix is the prefix that permission resources use before the github CCRN type.
	urnCCRNPrefix = "urn:ccrn:"
)

// DesiredTeam represents the desired state of a GithubTeam CR.
type DesiredTeam struct {
	// Instance is the GitHub Enterprise hostname.
	Instance string
	// Org is the GitHub organization name.
	Org string
	// CRName is the computed Kubernetes CR name.
	CRName string
}

// DesiredTeamRepo represents the desired state of a GithubTeamRepository CR.
type DesiredTeamRepo struct {
	// Instance is the GitHub Enterprise hostname.
	Instance string
	// Org is the GitHub organization name.
	Org string
	// Permission is the mapped GitHub permission level (e.g., "push", "pull", "admin").
	Permission string
	// Repositories is the sorted list of repository names.
	Repositories []string
	// CRName is the computed Kubernetes CR name.
	CRName string
}

// DesiredState holds the complete desired state for a single ASR reconciliation.
type DesiredState struct {
	Teams     []DesiredTeam
	TeamRepos []DesiredTeamRepo
}

// teamKey is a deduplication key for teams.
type teamKey struct {
	instance string
	org      string
}

// repoKey is a grouping key for team repositories.
type repoKey struct {
	instance   string
	org        string
	permission string
}

// githubPerm is a parsed, github-scoped permission from an ASR.
type githubPerm struct {
	gh         *ccrn.GithubResource
	capability string
}

// parseGithubPerms parses the ASR's permissions and keeps only the github ones.
// The urn:ccrn: prefix is prepended when missing so bare CCRNs still parse.
func parseGithubPerms(asr *v1alpha1.ApplicationSpecificRole) []githubPerm {
	var perms []githubPerm
	for _, perm := range asr.Spec.Permissions {
		resource := perm.Resource
		if !strings.HasPrefix(resource, urnCCRNPrefix) {
			resource = urnCCRNPrefix + resource
		}
		parsed, err := ccrn.Parse(resource)
		if err != nil || !parsed.IsGithub() {
			continue
		}
		perms = append(perms, githubPerm{gh: parsed.Github, capability: perm.Capability})
	}
	return perms
}

// BuildDesiredState parses the ASR permissions and groups them into desired
// GithubTeam and GithubTeamRepository CRs.
//
// Rules:
//   - organization CCRNs create a GithubTeam but NO GithubTeamRepository
//   - repository CCRNs create both a GithubTeam AND aggregate into GithubTeamRepository
//   - repository_type (wildcard) CCRNs are SKIPPED (handled by org-patching, separate issue)
//   - Capability is mapped through capabilityMapping from the ConnectorBinding definition
//
// Returns the desired state and a deduplicated list of unmapped capability names.
func BuildDesiredState(asr *v1alpha1.ApplicationSpecificRole, capabilityMapping map[string]string) (desired *DesiredState, unmappedCaps []string) {
	teams := make(map[teamKey]struct{})
	repos := make(map[repoKey][]string)
	unmappedSeen := make(map[string]struct{})

	for _, p := range parseGithubPerms(asr) {
		gh := p.gh

		// repository_type and wildcard repository CCRNs are handled by org patching.
		if gh.ResourceType == ccrn.GithubResourceTypeRepositoryType {
			continue
		}
		if gh.ResourceType == ccrn.GithubResourceTypeRepository && gh.Repo == "*" {
			continue
		}

		// Every non-wildcard github CCRN contributes a team.
		teams[teamKey{instance: gh.Instance, org: gh.Org}] = struct{}{}

		// Organization CCRNs create only a team, no team repository.
		if gh.ResourceType == ccrn.GithubResourceTypeOrganization {
			continue
		}

		mappedPermission := mapCapability(p.capability, capabilityMapping)
		if mappedPermission == "" {
			if _, seen := unmappedSeen[p.capability]; !seen {
				unmappedSeen[p.capability] = struct{}{}
				unmappedCaps = append(unmappedCaps, p.capability)
			}
			continue
		}

		rk := repoKey{instance: gh.Instance, org: gh.Org, permission: mappedPermission}
		repos[rk] = append(repos[rk], gh.Repo)
	}

	return buildResult(asr.Name, teams, repos), unmappedCaps
}

// buildResult transforms the collected maps into the final DesiredState.
func buildResult(asrName string, teams map[teamKey]struct{}, repos map[repoKey][]string) *DesiredState {
	state := &DesiredState{}

	// Build teams (sorted for deterministic output)
	teamKeys := make([]teamKey, 0, len(teams))
	for k := range teams {
		teamKeys = append(teamKeys, k)
	}
	slices.SortFunc(teamKeys, func(a, b teamKey) int {
		if c := cmp.Compare(a.instance, b.instance); c != 0 {
			return c
		}
		return cmp.Compare(a.org, b.org)
	})

	for _, tk := range teamKeys {
		state.Teams = append(state.Teams, DesiredTeam{
			Instance: tk.instance,
			Org:      tk.org,
			CRName:   TeamCRName(asrName, tk.instance, tk.org),
		})
	}

	// Build team repos (sorted for deterministic output)
	repoKeys := make([]repoKey, 0, len(repos))
	for k := range repos {
		repoKeys = append(repoKeys, k)
	}
	slices.SortFunc(repoKeys, func(a, b repoKey) int {
		if c := cmp.Compare(a.instance, b.instance); c != 0 {
			return c
		}
		if c := cmp.Compare(a.org, b.org); c != 0 {
			return c
		}
		return cmp.Compare(a.permission, b.permission)
	})

	for _, rk := range repoKeys {
		repositories := repos[rk]
		slices.Sort(repositories)
		repositories = slices.Compact(repositories)

		state.TeamRepos = append(state.TeamRepos, DesiredTeamRepo{
			Instance:     rk.instance,
			Org:          rk.org,
			Permission:   rk.permission,
			Repositories: repositories,
			CRName:       TeamRepoCRName(asrName, rk.instance, rk.org, rk.permission),
		})
	}

	return state
}

// mapCapability maps a PM capability (e.g., "WRITE") to a GitHub permission (e.g., "push")
// using the capabilityMapping from the ConnectorBinding definition.
// Returns empty string if no mapping exists.
func mapCapability(capability string, capabilityMapping map[string]string) string {
	if capabilityMapping == nil {
		return ""
	}
	return capabilityMapping[capability]
}

// BuildLabels returns the standard labels for a generated CR.
func BuildLabels(asrName, asrNamespace string) map[string]string {
	return map[string]string{
		pmapis.LabelManagedByKey:    pmapis.ManagedByGithub,
		pmapis.LabelSourceASR:       asrName,
		pmapis.LabelSourceNamespace: asrNamespace,
	}
}
