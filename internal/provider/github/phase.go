// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"errors"
	"fmt"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
)

// Phase holds dependencies shared across github provider reconcile calls.
type Phase struct {
	Client   client.Client
	AuditLog *audit.Logger
	Log      logr.Logger
}

// New returns a Phase with the given dependencies.
func New(c client.Client, al *audit.Logger, log logr.Logger) *Phase {
	return &Phase{
		Client:   c,
		AuditLog: al,
		Log:      log,
	}
}

// Reconcile runs the github provider create/update logic for one ConnectorBinding.
// Returns this binding's missing org CRs so the caller aggregates them across bindings.
func (p *Phase) Reconcile(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole, cb *v1alpha1.ConnectorBinding) ([]string, error) {
	log := p.Log.WithValues("asr", asr.Name, "connectorBinding", cb.Name)

	advConfig, err := ParseAdvancedConfig(asr.Spec.AdvancedConfig)
	if err != nil {
		log.Error(err, "failed to parse advancedConfig")
		return nil, fmt.Errorf("failed to parse advancedConfig: %w", err)
	}

	cbDef, err := ParseConnectorDefinition(cb.Spec.Definition)
	if err != nil {
		log.Error(err, "failed to parse ConnectorBinding definition")
		return nil, fmt.Errorf("failed to parse ConnectorBinding definition: %w", err)
	}

	targetNamespace := ResolveTargetNamespace(advConfig, cbDef, asr)

	desired, unmappedCaps := BuildDesiredState(asr, cbDef.CapabilityMapping)
	for _, capName := range unmappedCaps {
		log.Info("unmapped capability in ConnectorBinding capabilityMapping",
			"capability", capName,
			"provider", string(pmmeta.ProviderGithub),
		)
	}

	log.Info("reconciling GitHub resources",
		"targetNamespace", targetNamespace,
		"teams", len(desired.Teams),
		"teamRepos", len(desired.TeamRepos),
	)

	var errs []error

	if err := ReconcileTeams(ctx, p.Client, desired.Teams, asr.Spec.Name, targetNamespace, asr.Name, asr.Namespace, p.AuditLog); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile GithubTeams: %w", err))
	}

	if err := ReconcileTeamRepos(ctx, p.Client, desired.TeamRepos, asr.Spec.Name, targetNamespace, asr.Name, asr.Namespace, p.AuditLog); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile GithubTeamRepositories: %w", err))
	}

	missingOrgs, err := p.reconcileOrganizations(ctx, asr, cbDef, targetNamespace)
	if err != nil {
		errs = append(errs, err)
	}

	ps := computeProviderStatus(ctx, p.Client, asr, desired, unmappedCaps)
	asr.Status.Providers = pmmeta.SetProviderStatus(asr.Status.Providers, pmmeta.ProviderGithub, ps)

	return missingOrgs, errors.Join(errs...)
}

// ReconcileDelete runs the github provider cleanup logic for the ASR.
func (p *Phase) ReconcileDelete(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole) error {
	var errs []error

	if err := p.cleanupOwnedResources(ctx, asr); err != nil {
		p.Log.Error(err, "failed to clean up GitHub resources", "asr", asr.Name)
		errs = append(errs, err)
	}

	if err := p.cleanupOrgEntries(ctx, asr); err != nil {
		p.Log.Error(err, "failed to clean up org entries", "asr", asr.Name)
		errs = append(errs, err)
	}

	if len(errs) == 0 {
		asr.Status.Providers = pmmeta.RemoveProviderStatus(asr.Status.Providers, pmmeta.ProviderGithub)
	}

	return errors.Join(errs...)
}

// cleanupOwnedResources deletes all GithubTeam and GithubTeamRepository CRs
// owned by this ASR, across all namespaces (label-based). Called during ASR deletion.
func (p *Phase) cleanupOwnedResources(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole) error {
	var teamList repoguardv1.GithubTeamList
	if err := p.Client.List(ctx, &teamList, client.MatchingLabels{pmapis.LabelSourceASR: asr.Name}); err != nil {
		return fmt.Errorf("failed to list GithubTeams for cleanup: %w", err)
	}
	for i := range teamList.Items {
		team := &teamList.Items[i]
		if err := p.Client.Delete(ctx, team); client.IgnoreNotFound(err) != nil {
			p.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asr.Name, audit.ResourceRef{
				Kind:      "GithubTeam",
				Name:      team.Name,
				Namespace: team.Namespace,
			}, err)
			return fmt.Errorf("failed to delete GithubTeam %q: %w", team.Name, err)
		}
		p.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asr.Name, audit.ResourceRef{
			Kind:      "GithubTeam",
			Name:      team.Name,
			Namespace: team.Namespace,
		}, nil)
	}

	var repoList repoguardv1.GithubTeamRepositoryList
	if err := p.Client.List(ctx, &repoList, client.MatchingLabels{pmapis.LabelSourceASR: asr.Name}); err != nil {
		return fmt.Errorf("failed to list GithubTeamRepositories for cleanup: %w", err)
	}
	for i := range repoList.Items {
		repo := &repoList.Items[i]
		if err := p.Client.Delete(ctx, repo); client.IgnoreNotFound(err) != nil {
			p.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asr.Name, audit.ResourceRef{
				Kind:      "GithubTeamRepository",
				Name:      repo.Name,
				Namespace: repo.Namespace,
			}, err)
			return fmt.Errorf("failed to delete GithubTeamRepository %q: %w", repo.Name, err)
		}
		p.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asr.Name, audit.ResourceRef{
			Kind:      "GithubTeamRepository",
			Name:      repo.Name,
			Namespace: repo.Namespace,
		}, nil)
	}

	return nil
}

// cleanupOrgEntries removes all organization entries contributed by this ASR
// across all GithubOrganization resources in all namespaces.
func (p *Phase) cleanupOrgEntries(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole) error {
	var orgList repoguardv1.GithubOrganizationList
	if err := p.Client.List(ctx, &orgList); err != nil {
		return fmt.Errorf("failed to list GithubOrganizations: %w", err)
	}

	for i := range orgList.Items {
		org := &orgList.Items[i]
		if orgManagedByASR(org, asr.Namespace, asr.Name) {
			if err := p.removeASRFromOrg(ctx, org, asr.Namespace, asr.Name); err != nil {
				return fmt.Errorf("failed to clean up org %s/%s: %w", org.Namespace, org.Name, err)
			}
		}
	}
	return nil
}
