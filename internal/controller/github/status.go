// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"fmt"
	"sort"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// computeProviderStatus builds the github provider status from the GithubTeam CRs owned
// by this ASR. Per-team status mapping is in mapTeamStatus; Ready is true only when all
// targets are Ready, or degraded when unmappedCaps is non-empty.
func computeProviderStatus(
	ctx context.Context,
	c client.Client,
	asr *v1alpha1.ApplicationSpecificRole,
	desired *DesiredState,
	unmappedCaps []string,
) pmmeta.ProviderStatus {
	if len(unmappedCaps) > 0 {
		return pmmeta.DegradedProviderStatus(fmt.Sprintf("unmapped capabilities: %v", unmappedCaps))
	}

	// Collect all unique (instance, org) pairs from the desired state.
	// These represent all targets the provider should report on.
	type instanceOrg struct {
		instance string
		org      string
	}
	seen := make(map[instanceOrg]struct{})
	var targets []instanceOrg

	for _, dt := range desired.Teams {
		key := instanceOrg{instance: dt.Instance, org: dt.Org}
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			targets = append(targets, key)
		}
	}
	for _, dtr := range desired.TeamRepos {
		key := instanceOrg{instance: dtr.Instance, org: dtr.Org}
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			targets = append(targets, key)
		}
	}

	// Sort targets for deterministic output
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].instance != targets[j].instance {
			return targets[i].instance < targets[j].instance
		}
		return targets[i].org < targets[j].org
	})

	if len(targets) == 0 {
		return pmmeta.ProviderStatusFromTargets(nil)
	}

	// Fetch all GithubTeam CRs owned by this ASR
	var teamList repoguardv1.GithubTeamList
	listOpts := []client.ListOption{
		client.MatchingLabels{
			pmapis.LabelManagedByKey: pmapis.ManagedByGithub,
			pmapis.LabelSourceASR:    asr.Name,
		},
	}
	if err := c.List(ctx, &teamList, listOpts...); err != nil {
		return pmmeta.DegradedProviderStatus(fmt.Sprintf("failed to list GithubTeams: %v", err))
	}

	// Build a map of GithubTeam CRs by their CR name for lookup
	teamStatuses := make(map[string]repoguardv1.GithubTeamState, len(teamList.Items))
	for i := range teamList.Items {
		team := &teamList.Items[i]
		teamStatuses[team.Name] = team.Status.TeamStatus
	}

	// Compute per-target status
	var targetStatuses []pmmeta.TargetStatus
	for _, t := range targets {
		targetID := SanitizeInstance(t.instance) + "/" + t.org

		// Find the GithubTeam CR for this instance/org
		crName := TeamCRName(asr.Name, t.instance, t.org)
		teamStatus, found := teamStatuses[crName]

		var status pmmeta.TargetStatusValue
		var message string
		if !found {
			// Team CR doesn't exist yet or org is missing
			status = pmmeta.TargetStatusPending
			message = "GithubTeam CR not found"
		} else {
			status, message = mapTeamStatus(teamStatus)
		}

		targetStatuses = pmmeta.SetTargetStatus(targetStatuses, pmmeta.TargetStatus{
			TargetID: targetID,
			Status:   status,
			Message:  message,
		})
	}

	return pmmeta.ProviderStatusFromTargets(targetStatuses)
}

// mapTeamStatus maps a GithubTeam.Status.TeamStatus to a TargetStatusValue.
func mapTeamStatus(teamStatus repoguardv1.GithubTeamState) (status pmmeta.TargetStatusValue, message string) {
	switch teamStatus {
	case repoguardv1.GithubTeamStateComplete:
		return pmmeta.TargetStatusReady, ""
	case repoguardv1.GithubTeamStateFailed:
		return pmmeta.TargetStatusError, "GithubTeam reconciliation failed"
	case repoguardv1.GithubTeamStatePendingOperations:
		return pmmeta.TargetStatusPending, "GithubTeam reconciliation pending"
	case repoguardv1.GithubTeamStateRateLimited:
		return pmmeta.TargetStatusPending, "GithubTeam rate-limited"
	case "":
		return pmmeta.TargetStatusPending, "GithubTeam status not yet reported"
	default:
		return pmmeta.TargetStatusPending, fmt.Sprintf("unknown team status: %q", teamStatus)
	}
}
