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
			team.Labels = labels
			team.Spec = repoguardv1.GithubTeamSpec{
				Github:       dt.Instance,
				Organization: dt.Org,
				Team:         teamName,
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	if err := deleteStaleTeams(ctx, c, targetNamespace, asrName, desiredNames, al); err != nil {
		return fmt.Errorf("failed to delete stale GithubTeams: %w", err)
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
