// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"fmt"
	"sort"
	"time"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/pkg/provider"
)

const (
	// Target status result values.
	targetStatusSuccess = "Success"
	targetStatusError   = "Error"
	targetStatusPending = "Pending"
)

// computeProviderStatus computes the provider status for the github provider
// by examining all GithubTeam CRs owned by this ASR and correlating them with
// GithubOrganization resources.
//
// Target IDs are formatted as "<sanitized-instance>/<org>".
// Status mapping:
//   - "complete" -> Success
//   - "failed" -> Error
//   - "pending" -> Pending
//   - "ratelimited" -> Pending
//   - Missing GithubOrganization -> Pending
//
// Overall ready=true only when ALL targets are Success.
func computeProviderStatus(
	ctx context.Context,
	c client.Client,
	asr *v1alpha1.ApplicationSpecificRole,
	desired *DesiredState,
) v1alpha1.ProviderStatus {
	now := metav1.NewTime(time.Now())

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
		return v1alpha1.ProviderStatus{
			Ready:          true,
			Message:        "no targets",
			LastReconciled: now,
		}
	}

	// Fetch all GithubTeam CRs owned by this ASR
	var teamList repoguardv1.GithubTeamList
	listOpts := []client.ListOption{
		client.MatchingLabels{
			LabelManagedByKey: LabelManagedByGithub,
			LabelSourceASR:    asr.Name,
		},
	}
	if err := c.List(ctx, &teamList, listOpts...); err != nil {
		return v1alpha1.ProviderStatus{
			Ready:          false,
			Message:        fmt.Sprintf("failed to list GithubTeams: %v", err),
			LastReconciled: now,
		}
	}

	// Build a map of GithubTeam CRs by their CR name for lookup
	teamStatuses := make(map[string]repoguardv1.GithubTeamState, len(teamList.Items))
	for i := range teamList.Items {
		team := &teamList.Items[i]
		teamStatuses[team.Name] = team.Status.TeamStatus
	}

	// Compute per-target status
	allSuccess := true
	targetStatuses := make([]v1alpha1.TargetStatus, 0, len(targets))

	for _, t := range targets {
		targetID := SanitizeInstance(t.instance) + "/" + t.org

		// Find the GithubTeam CR for this instance/org
		crName := TeamCRName(asr.Name, t.instance, t.org)
		teamStatus, found := teamStatuses[crName]

		var status, message string
		if !found {
			// Team CR doesn't exist yet or org is missing
			status = targetStatusPending
			message = "GithubTeam CR not found"
		} else {
			status, message = mapTeamStatus(teamStatus)
		}

		if status != targetStatusSuccess {
			allSuccess = false
		}

		targetStatuses = append(targetStatuses, v1alpha1.TargetStatus{
			TargetID:    targetID,
			Status:      status,
			Message:     message,
			LastUpdated: now,
		})
	}

	overallMessage := ""
	if !allSuccess {
		overallMessage = "one or more targets not yet successful"
	}

	return v1alpha1.ProviderStatus{
		Ready:          allSuccess,
		Message:        overallMessage,
		Targets:        targetStatuses,
		LastReconciled: now,
	}
}

// mapTeamStatus maps a GithubTeam.Status.TeamStatus to a TargetStatus status string.
func mapTeamStatus(teamStatus repoguardv1.GithubTeamState) (status string, message string) {
	switch teamStatus {
	case repoguardv1.GithubTeamStateComplete:
		return targetStatusSuccess, ""
	case repoguardv1.GithubTeamStateFailed:
		return targetStatusError, "GithubTeam reconciliation failed"
	case repoguardv1.GithubTeamStatePendingOperations:
		return targetStatusPending, "GithubTeam reconciliation pending"
	case repoguardv1.GithubTeamStateRateLimited:
		return targetStatusPending, "GithubTeam rate-limited"
	case "":
		return targetStatusPending, "GithubTeam status not yet reported"
	default:
		return targetStatusPending, fmt.Sprintf("unknown team status: %q", teamStatus)
	}
}

// computeUnmappedCapabilityStatus returns a ProviderStatus indicating that one
// or more capabilities could not be mapped to a GitHub permission. This reports
// ready=false with an error message listing the unmapped capabilities.
func computeUnmappedCapabilityStatus(unmapped []string) v1alpha1.ProviderStatus {
	return provider.UnmappedCapabilityStatus(unmapped)
}

// patchProviderStatus writes the provider status to the ASR using SSA.
func patchProviderStatus(ctx context.Context, c client.Client, asr *v1alpha1.ApplicationSpecificRole, status v1alpha1.ProviderStatus) error {
	return provider.PatchProviderStatus(ctx, c, asr, provider.ProviderGithub, status)
}

// extractASRFromGithubTeam extracts the owning ASR's ObjectKey from a GithubTeam's labels.
// Returns nil if the required labels are missing or the managed-by value doesn't match.
func extractASRFromGithubTeam(team *repoguardv1.GithubTeam) []client.ObjectKey {
	labels := team.GetLabels()
	if labels == nil {
		return nil
	}

	managedBy := labels[LabelManagedByKey]
	if managedBy != LabelManagedByGithub {
		return nil
	}

	asrName := labels[LabelSourceASR]
	asrNamespace := labels[LabelSourceNamespace]
	if asrName == "" || asrNamespace == "" {
		return nil
	}

	return []client.ObjectKey{{Name: asrName, Namespace: asrNamespace}}
}
