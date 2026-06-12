// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package greenhouseteams

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenhousemetav1alpha1 "github.com/cloudoperators/greenhouse/api/meta/v1alpha1"
	greenhousev1alpha2 "github.com/cloudoperators/greenhouse/api/v1alpha2"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/pkg/provider"
)

const (
	// StatusSuccess indicates the TRB was reconciled successfully.
	StatusSuccess = "Success"
	// StatusError indicates the TRB reconciliation failed.
	StatusError = "Error"
	// StatusPending indicates the TRB is awaiting reconciliation.
	StatusPending = "Pending"
)

// TRBStatus represents the computed status of a single TeamRoleBinding.
type TRBStatus struct {
	// Name is the TRB resource name.
	Name string
	// Status is one of Success, Error, Pending.
	Status string
	// Message is an optional human-readable status message.
	Message string
}

// ComputeTRBStatus derives a status string from a TeamRoleBinding's Ready condition.
// Ready=True → Success, Ready=False → Error, no condition → Pending.
func ComputeTRBStatus(trb *greenhousev1alpha2.TeamRoleBinding) TRBStatus {
	cond := trb.Status.GetConditionByType(greenhousemetav1alpha1.ReadyCondition)
	if cond == nil {
		return TRBStatus{
			Name:    trb.Name,
			Status:  StatusPending,
			Message: "Awaiting reconciliation",
		}
	}

	switch cond.Status {
	case metav1.ConditionTrue:
		return TRBStatus{
			Name:    trb.Name,
			Status:  StatusSuccess,
			Message: "TeamRoleBinding is ready",
		}
	default:
		// ConditionFalse or any other value → Error
		return TRBStatus{
			Name:    trb.Name,
			Status:  StatusError,
			Message: "TeamRoleBinding is not ready",
		}
	}
}

// ComputeProviderStatus computes the overall ProviderStatus for the greenhouse-teams
// provider by aggregating TRB statuses for a given ASR.
func ComputeProviderStatus(ctx context.Context, c client.Client, asr *v1alpha1.ApplicationSpecificRole) v1alpha1.ProviderStatus {
	// List all managed TRBs across all namespaces for this ASR
	trbStatuses := listTRBStatuses(ctx, c, asr.Name, asr.Namespace)

	targets := make([]v1alpha1.TargetStatus, 0, len(trbStatuses))
	allSucceeded := true
	for _, s := range trbStatuses {
		targets = append(targets, v1alpha1.TargetStatus{
			TargetID:    s.Name,
			Status:      s.Status,
			Message:     s.Message,
			LastUpdated: metav1.Now(),
		})
		if s.Status != StatusSuccess {
			allSucceeded = false
		}
	}

	var message string
	if len(targets) == 0 {
		message = "No TeamRoleBindings managed"
		allSucceeded = true
	} else if allSucceeded {
		message = fmt.Sprintf("All %d TeamRoleBindings ready", len(targets))
	} else {
		errCount := 0
		for _, s := range trbStatuses {
			if s.Status != StatusSuccess {
				errCount++
			}
		}
		message = fmt.Sprintf("%d of %d TeamRoleBindings have errors", errCount, len(targets))
	}

	return v1alpha1.ProviderStatus{
		Ready:          allSucceeded,
		Message:        message,
		Targets:        targets,
		LastReconciled: metav1.Now(),
	}
}

// ComputeTeamOnlyStatus returns a ProviderStatus for team-only mode where TRBs
// are not created (conflict suppression). This reports ready=true with a message
// indicating that the provider is operating in team-only mode.
func ComputeTeamOnlyStatus() v1alpha1.ProviderStatus {
	return v1alpha1.ProviderStatus{
		Ready:          true,
		Message:        "Team-only mode: TeamRoleBinding creation suppressed to avoid conflicts",
		LastReconciled: metav1.Now(),
	}
}

// ComputeUnmappedCapabilityStatus returns a ProviderStatus indicating that one
// or more capabilities could not be mapped to a TeamRole. This reports ready=false
// with an error message listing the unmapped capabilities.
func ComputeUnmappedCapabilityStatus(unmapped []string) v1alpha1.ProviderStatus {
	return provider.UnmappedCapabilityStatus(unmapped)
}

// PatchASRProviderStatus writes the greenhouse-teams provider status to the ASR
// using Server-Side Apply.
func PatchASRProviderStatus(ctx context.Context, c client.Client, asr *v1alpha1.ApplicationSpecificRole, status v1alpha1.ProviderStatus) error {
	return provider.PatchProviderStatus(ctx, c, asr, provider.ProviderGreenhouseTeams, status)
}

// listTRBStatuses lists all managed TRBs for an ASR and computes their statuses.
func listTRBStatuses(ctx context.Context, c client.Client, asrName, asrNamespace string) []TRBStatus {
	var trbList greenhousev1alpha2.TeamRoleBindingList
	if err := c.List(ctx, &trbList,
		client.MatchingLabels{
			LabelManagedBy:       LabelManagedByValue,
			LabelSourceASR:       asrName,
			LabelSourceNamespace: asrNamespace,
		},
	); err != nil {
		// If we can't list, return empty — status will show "No TRBs managed"
		return nil
	}

	statuses := make([]TRBStatus, 0, len(trbList.Items))
	for i := range trbList.Items {
		statuses = append(statuses, ComputeTRBStatus(&trbList.Items[i]))
	}
	return statuses
}
