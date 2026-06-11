// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package greenhouseteams implements the Greenhouse Teams provider controller.
// It reconciles Teams and TeamRoleBindings (TRBs) for ASRs that reference a
// greenhouse-teams ConnectorBinding. When the same ASR also references a
// kubernetes ConnectorBinding, the controller operates in "Team-only mode":
// Teams are still reconciled, but TRBs are skipped (the kubernetes provider
// handles RBAC directly).
package greenhouseteams

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
	greenhouse "github.com/cloudoperators/permission-manager/controller/pkg/greenhouse"
	"github.com/cloudoperators/permission-manager/controller/pkg/provider"
)

// CleanupTRBs uses the same label keys/values as the rest of the package
// (LabelManagedBy, LabelManagedByValue, LabelSourceASR) defined in team.go.

// ConflictResult holds the result of a kubernetes conflict check for an ASR.
type ConflictResult struct {
	// HasConflict is true when the ASR references both greenhouse-teams and
	// kubernetes ConnectorBindings, meaning TRBs should not be created.
	HasConflict bool
}

// hasKubernetesProvider checks all connectorRefs on the given ASR and determines
// whether any of them reference a ConnectorBinding with provider=kubernetes.
// It fetches each ConnectorBinding via the cached client. ConnectorBindings that
// are NotFound are skipped (they do not constitute a conflict).
func hasKubernetesProvider(ctx context.Context, c client.Reader, asr *pmv1.ApplicationSpecificRole) (*ConflictResult, error) {
	result := &ConflictResult{}

	for _, ref := range asr.Spec.ConnectorRefs {
		cb := &pmv1.ConnectorBinding{}
		key := client.ObjectKey{
			Name:      ref,
			Namespace: asr.Namespace,
		}

		if err := c.Get(ctx, key, cb); err != nil {
			if client.IgnoreNotFound(err) == nil {
				// ConnectorBinding not found — skip, don't treat as kubernetes.
				continue
			}
			return nil, fmt.Errorf("failed to get ConnectorBinding %q: %w", ref, err)
		}

		if cb.Spec.Provider == provider.ProviderKubernetes {
			result.HasConflict = true
		}
	}

	return result, nil
}

// ReconcileMode represents the operating mode for the greenhouse teams controller.
type ReconcileMode int

const (
	// ModeFullReconcile indicates both Teams and TRBs should be reconciled.
	ModeFullReconcile ReconcileMode = iota

	// ModeTeamOnly indicates only Teams should be reconciled; TRBs are skipped
	// because the kubernetes provider handles RBAC directly.
	ModeTeamOnly
)

// String returns the human-readable mode name.
func (m ReconcileMode) String() string {
	switch m {
	case ModeTeamOnly:
		return "team-only"
	default:
		return "full"
	}
}

// DetermineMode checks for kubernetes conflict and returns the appropriate
// reconcile mode for the greenhouse teams controller. This is the main entry
// point for conflict detection during reconciliation.
func DetermineMode(ctx context.Context, c client.Reader, asr *pmv1.ApplicationSpecificRole, log logr.Logger) (ReconcileMode, *ConflictResult, error) {
	result, err := hasKubernetesProvider(ctx, c, asr)
	if err != nil {
		return ModeFullReconcile, nil, fmt.Errorf("conflict detection failed: %w", err)
	}

	if result.HasConflict {
		log.Info("kubernetes conflict detected, operating in team-only mode",
			"asr", asr.Name,
		)
		return ModeTeamOnly, result, nil
	}

	return ModeFullReconcile, result, nil
}

// CleanupTRBs deletes all TeamRoleBindings owned by the given ASR. This is
// called when a kubernetes conflict is first detected and existing TRBs need
// to be removed. The label selector uses the standard ASR ownership label.
func CleanupTRBs(ctx context.Context, c client.Client, asr *pmv1.ApplicationSpecificRole, log logr.Logger, al *audit.Logger) error {
	// TeamRoleBindings are labeled with the owning ASR name for easy selection.
	// Use the same label keys/values applied during TRB creation (defined in team.go).
	labelSelector := client.MatchingLabels{
		LabelManagedBy: LabelManagedByValue,
		LabelSourceASR: asr.Name,
	}

	// List all TeamRoleBindings (TRBs) matching this ASR.
	// TRBs are namespace-scoped — list across all namespaces.
	trbList := &greenhouse.TeamRoleBindingList{}
	if err := c.List(ctx, trbList, labelSelector); err != nil {
		return fmt.Errorf("failed to list TeamRoleBindings for ASR %q: %w", asr.Name, err)
	}

	var deleteErrors []error
	for i := range trbList.Items {
		trb := &trbList.Items[i]
		log.Info("deleting TeamRoleBinding due to kubernetes conflict",
			"trb", trb.Name,
			"namespace", trb.Namespace,
			"asr", asr.Name,
		)
		if err := c.Delete(ctx, trb); err != nil {
			if client.IgnoreNotFound(err) != nil {
				deleteErrors = append(deleteErrors, fmt.Errorf("failed to delete TRB %q/%q: %w", trb.Namespace, trb.Name, err))
			}
		} else {
			al.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asr.Name, audit.ResourceRef{
				Kind:      "TeamRoleBinding",
				Name:      trb.Name,
				Namespace: trb.Namespace,
				Cluster:   "",
			}, nil)
		}
	}

	if len(deleteErrors) > 0 {
		return fmt.Errorf("errors deleting TRBs: %v", deleteErrors)
	}

	return nil
}

// ShouldReconcileTRBs is a convenience function that returns true if the mode
// allows TRB creation/update. Use in reconciler flow after DetermineMode.
func ShouldReconcileTRBs(mode ReconcileMode) bool {
	return mode == ModeFullReconcile
}
