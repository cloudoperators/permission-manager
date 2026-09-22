// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"fmt"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/internal/audit"
)

const (
	// labelAddUser and labelRemoveUser gate repo-guard membership management; without them
	// set to labelMutationEnabled repo-guard leaves the team's members untouched.
	labelAddUser    = "repo-guard.cloudoperators.dev/addUser"
	labelRemoveUser = "repo-guard.cloudoperators.dev/removeUser"
	// labelMutationEnabled turns a membership gate on.
	labelMutationEnabled = "true"
)

// ReconcileTeams creates/updates GithubTeam CRs for the desired state and
// deletes stale ones that are no longer needed. Stale deletion is skipped when
// deleteStale is false (e.g. a missing org left the desired set incomplete),
// to avoid deleting CRs the current view cannot account for.
func ReconcileTeams(
	ctx context.Context,
	c client.Client,
	desired []DesiredTeam,
	teamName string,
	targetNamespace string,
	asrName string,
	asrNamespace string,
	deleteStale bool,
	al *audit.Logger,
) error {

	desiredNames := make(map[string]struct{}, len(desired))
	for _, dt := range desired {
		desiredNames[dt.CRName] = struct{}{}
	}

	for _, dt := range desired {
		team := &repoguardv1.GithubTeam{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dt.CRName,
				Namespace: targetNamespace,
			},
		}
		err := upsertWithAudit(ctx, c, al, team, "GithubTeam", asrName, func() error {
			// Merge, don't replace: preserve labels PM doesn't own (e.g. dryRun) on a converged CR.
			if team.Labels == nil {
				team.Labels = map[string]string{}
			}
			mergeOwnershipLabels(team.Labels, asrName, asrNamespace)
			team.Labels[labelAddUser] = labelMutationEnabled
			team.Labels[labelRemoveUser] = labelMutationEnabled
			// Sync TTLs to the org: set present ones, drop any the org dropped.
			for _, k := range ttlLabelKeys {
				if v, ok := dt.TTLs[k]; ok {
					team.Labels[k] = v
				} else {
					delete(team.Labels, k)
				}
			}

			// Set fields individually so a pre-existing CR's other spec fields survive.
			team.Spec.Github = dt.Instance
			team.Spec.Organization = dt.Org
			team.Spec.Team = teamName
			if dt.LDAPProvider != "" {
				team.Spec.ExternalMemberProvider = &repoguardv1.ExternalMemberProviderConfig{
					LDAP: &repoguardv1.GenericProvider{
						ExternalMemberProvider: dt.LDAPProvider,
						Group:                  teamName,
					},
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	if deleteStale {
		if err := deleteStaleTeams(ctx, c, targetNamespace, asrName, desiredNames, al); err != nil {
			return fmt.Errorf("failed to delete stale GithubTeams: %w", err)
		}
	}

	return nil
}

// deleteStaleTeams deletes GithubTeam CRs managed by this ASR that are no longer desired.
func deleteStaleTeams(
	ctx context.Context,
	c client.Client,
	namespace string,
	asrName string,
	desiredNames map[string]struct{},
	al *audit.Logger,
) error {

	var teamList repoguardv1.GithubTeamList
	if err := c.List(ctx, &teamList,
		client.InNamespace(namespace),
		client.MatchingLabels{pmapis.LabelSourceASR: asrName},
	); err != nil {
		return fmt.Errorf("failed to list GithubTeams: %w", err)
	}

	for i := range teamList.Items {
		team := &teamList.Items[i]
		if _, ok := desiredNames[team.Name]; ok {
			continue
		}
		if err := deleteWithAudit(ctx, c, al, team, "GithubTeam", asrName); err != nil {
			return err
		}
	}

	return nil
}
