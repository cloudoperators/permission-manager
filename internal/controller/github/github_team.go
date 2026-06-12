// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/cloudoperators/permission-manager/pkg/audit"
)

// ReconcileTeams creates/updates GithubTeam CRs for the desired state and
// deletes stale ones that are no longer needed.
func ReconcileTeams(
	ctx context.Context,
	c client.Client,
	desired []DesiredTeam,
	teamName string,
	targetNamespace string,
	asrName string,
	asrNamespace string,
	al *audit.Logger,
) error {
	labels := BuildLabels(asrName, asrNamespace)

	// Build set of desired CR names for stale detection
	desiredNames := make(map[string]struct{}, len(desired))
	for _, dt := range desired {
		desiredNames[dt.CRName] = struct{}{}
	}

	// Create or update desired teams
	for _, dt := range desired {
		team := &GithubTeam{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dt.CRName,
				Namespace: targetNamespace,
			},
		}
		team.SetGroupVersionKind(GroupVersion.WithKind("GithubTeam"))

		opResult, err := controllerutil.CreateOrUpdate(ctx, c, team, func() error {
			team.Labels = labels
			team.Spec = GithubTeamSpec{
				TeamName: teamName,
			}
			return nil
		})
		if err != nil {
			al.Log(ctx, audit.ActionCreate, audit.OutcomeFailure, asrName, audit.ResourceRef{
				Kind:      "GithubTeam",
				Name:      dt.CRName,
				Namespace: targetNamespace,
				Cluster:   "",
			}, err)
			return fmt.Errorf("failed to reconcile GithubTeam %q: %w", dt.CRName, err)
		}
		if opResult != controllerutil.OperationResultNone {
			action := audit.ActionUpdate
			if opResult == controllerutil.OperationResultCreated {
				action = audit.ActionCreate
			}
			al.Log(ctx, action, audit.OutcomeSuccess, asrName, audit.ResourceRef{
				Kind:      "GithubTeam",
				Name:      dt.CRName,
				Namespace: targetNamespace,
				Cluster:   "",
			}, nil)
		}
	}

	// Delete stale teams (owned by this ASR but no longer in desired state)
	if err := deleteStaleTeams(ctx, c, targetNamespace, asrName, desiredNames, al); err != nil {
		return fmt.Errorf("failed to delete stale GithubTeams: %w", err)
	}

	return nil
}

// deleteStaleTeams finds and deletes GithubTeam CRs that are managed by this ASR
// but are no longer in the desired state.
func deleteStaleTeams(
	ctx context.Context,
	c client.Client,
	namespace string,
	asrName string,
	desiredNames map[string]struct{},
	al *audit.Logger,
) error {
	var teamList GithubTeamList
	if err := c.List(ctx, &teamList,
		client.InNamespace(namespace),
		client.MatchingLabels{LabelSourceASR: asrName},
	); err != nil {
		return fmt.Errorf("failed to list GithubTeams: %w", err)
	}

	for i := range teamList.Items {
		team := &teamList.Items[i]
		if _, desired := desiredNames[team.Name]; !desired {
			if err := c.Delete(ctx, team); err != nil {
				if client.IgnoreNotFound(err) != nil {
					al.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, audit.ResourceRef{
						Kind:      "GithubTeam",
						Name:      team.Name,
						Namespace: namespace,
						Cluster:   "",
					}, err)
					return fmt.Errorf("failed to delete stale GithubTeam %q: %w", team.Name, err)
				}
			} else {
				al.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, audit.ResourceRef{
					Kind:      "GithubTeam",
					Name:      team.Name,
					Namespace: namespace,
					Cluster:   "",
				}, nil)
			}
		}
	}

	return nil
}
