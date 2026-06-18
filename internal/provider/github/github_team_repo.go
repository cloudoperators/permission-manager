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

// ReconcileTeamRepos creates/updates GithubTeamRepository CRs for the desired state
// and deletes stale ones that are no longer needed. A DesiredTeamRepo with an empty
// Repositories list is skipped and cleaned up as stale.
func ReconcileTeamRepos(
	ctx context.Context,
	c client.Client,
	desired []DesiredTeamRepo,
	teamName string,
	targetNamespace string,
	asrName string,
	asrNamespace string,
	al *audit.Logger,
) error {

	labels := BuildLabels(asrName, asrNamespace)

	desiredNames := make(map[string]struct{}, len(desired))
	for _, dr := range desired {
		if len(dr.Repositories) == 0 {
			continue
		}
		desiredNames[dr.CRName] = struct{}{}

		teamRepo := &repoguardv1.GithubTeamRepository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dr.CRName,
				Namespace: targetNamespace,
			},
		}
		err := upsertWithAudit(ctx, c, al, teamRepo, "GithubTeamRepository", asrName, func() error {
			teamRepo.Labels = labels
			teamRepo.Spec = repoguardv1.GithubTeamRepositorySpec{
				Github:       dr.Instance,
				Organization: dr.Org,
				Team:         teamName,
				Repository:   dr.Repositories,
				Permission:   repoguardv1.GithubTeamPermission(dr.Permission),
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	if err := deleteStaleTeamRepos(ctx, c, targetNamespace, asrName, desiredNames, al); err != nil {
		return fmt.Errorf("failed to delete stale GithubTeamRepositories: %w", err)
	}

	return nil
}

// deleteStaleTeamRepos deletes GithubTeamRepository CRs managed by this ASR that are no longer desired.
func deleteStaleTeamRepos(
	ctx context.Context,
	c client.Client,
	namespace string,
	asrName string,
	desiredNames map[string]struct{},
	al *audit.Logger,
) error {

	var repoList repoguardv1.GithubTeamRepositoryList
	if err := c.List(ctx, &repoList,
		client.InNamespace(namespace),
		client.MatchingLabels{pmapis.LabelSourceASR: asrName},
	); err != nil {
		return fmt.Errorf("failed to list GithubTeamRepositories: %w", err)
	}

	for i := range repoList.Items {
		repo := &repoList.Items[i]
		if _, ok := desiredNames[repo.Name]; ok {
			continue
		}
		if err := deleteWithAudit(ctx, c, al, repo, "GithubTeamRepository", asrName); err != nil {
			return err
		}
	}

	return nil
}
