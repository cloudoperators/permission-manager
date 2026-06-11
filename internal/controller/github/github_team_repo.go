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

// ReconcileTeamRepos creates/updates GithubTeamRepository CRs for the desired state
// and deletes stale ones that are no longer needed.
// If a DesiredTeamRepo has an empty Repositories list, the CR is deleted.
func ReconcileTeamRepos(
	ctx context.Context,
	c client.Client,
	desired []DesiredTeamRepo,
	targetNamespace string,
	asrName string,
	asrNamespace string,
	al *audit.Logger,
) error {
	labels := BuildLabels(asrName, asrNamespace)

	// Build set of desired CR names for stale detection
	desiredNames := make(map[string]struct{}, len(desired))

	for _, dr := range desired {
		// If the repo list is empty, we don't create a CR (it will be cleaned up as stale)
		if len(dr.Repositories) == 0 {
			continue
		}
		desiredNames[dr.CRName] = struct{}{}

		teamRepo := &GithubTeamRepository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dr.CRName,
				Namespace: targetNamespace,
			},
		}
		teamRepo.SetGroupVersionKind(GroupVersion.WithKind("GithubTeamRepository"))

		opResult, err := controllerutil.CreateOrUpdate(ctx, c, teamRepo, func() error {
			teamRepo.Labels = labels
			teamRepo.Spec = GithubTeamRepositorySpec{
				Repositories: dr.Repositories,
				Permission:   dr.Permission,
			}
			return nil
		})
		if err != nil {
			al.Log(ctx, audit.ActionCreate, audit.OutcomeFailure, asrName, audit.ResourceRef{
				Kind:      "GithubTeamRepository",
				Name:      dr.CRName,
				Namespace: targetNamespace,
				Cluster:   "",
			}, err)
			return fmt.Errorf("failed to reconcile GithubTeamRepository %q: %w", dr.CRName, err)
		}
		if opResult != controllerutil.OperationResultNone {
			action := audit.ActionUpdate
			if opResult == controllerutil.OperationResultCreated {
				action = audit.ActionCreate
			}
			al.Log(ctx, action, audit.OutcomeSuccess, asrName, audit.ResourceRef{
				Kind:      "GithubTeamRepository",
				Name:      dr.CRName,
				Namespace: targetNamespace,
				Cluster:   "",
			}, nil)
		}
	}

	// Delete stale team repos (owned by this ASR but no longer in desired state)
	if err := deleteStaleTeamRepos(ctx, c, targetNamespace, asrName, desiredNames, al); err != nil {
		return fmt.Errorf("failed to delete stale GithubTeamRepositories: %w", err)
	}

	return nil
}

// deleteStaleTeamRepos finds and deletes GithubTeamRepository CRs that are managed
// by this ASR but are no longer in the desired state.
func deleteStaleTeamRepos(
	ctx context.Context,
	c client.Client,
	namespace string,
	asrName string,
	desiredNames map[string]struct{},
	al *audit.Logger,
) error {
	var repoList GithubTeamRepositoryList
	if err := c.List(ctx, &repoList,
		client.InNamespace(namespace),
		client.MatchingLabels{LabelSourceASR: asrName},
	); err != nil {
		return fmt.Errorf("failed to list GithubTeamRepositories: %w", err)
	}

	for i := range repoList.Items {
		repo := &repoList.Items[i]
		if _, desired := desiredNames[repo.Name]; !desired {
			if err := c.Delete(ctx, repo); err != nil {
				if client.IgnoreNotFound(err) != nil {
					al.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, audit.ResourceRef{
						Kind:      "GithubTeamRepository",
						Name:      repo.Name,
						Namespace: namespace,
						Cluster:   "",
					}, err)
					return fmt.Errorf("failed to delete stale GithubTeamRepository %q: %w", repo.Name, err)
				}
			} else {
				al.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, audit.ResourceRef{
					Kind:      "GithubTeamRepository",
					Name:      repo.Name,
					Namespace: namespace,
					Cluster:   "",
				}, nil)
			}
		}
	}

	return nil
}
