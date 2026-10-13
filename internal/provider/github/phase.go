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
	Client client.Client
	// APIReader reads uncached so cleanup sees managedFields, which the cache
	// may strip; falls back to Client when unset (tests).
	APIReader client.Reader
	AuditLog  *audit.Logger
	Log       logr.Logger
}

// New returns a Phase with the given dependencies.
func New(c client.Client, al *audit.Logger, log logr.Logger) *Phase {
	return &Phase{
		Client:   c,
		AuditLog: al,
		Log:      log,
	}
}

// GithubConfigProblems collects per-binding ASR misconfigurations; the controller aggregates them
// across bindings for one SetGithubConfigValidity verdict.
type GithubConfigProblems struct {
	MissingOrgs  []string // "namespace/name" of GithubOrganization CRs not found
	InvalidCCRNs []string // unsupported CCRN inputs, e.g. wildcard repository
	UnmappedCaps []string // repository_type caps absent from capabilityMapping
}

// Merge appends another binding's problems in place.
func (p *GithubConfigProblems) Merge(o GithubConfigProblems) {
	p.MissingOrgs = append(p.MissingOrgs, o.MissingOrgs...)
	p.InvalidCCRNs = append(p.InvalidCCRNs, o.InvalidCCRNs...)
	p.UnmappedCaps = append(p.UnmappedCaps, o.UnmappedCaps...)
}

// Reconcile runs the github create/update logic for one ConnectorBinding, returning its desired
// org keys and config problems for the caller to aggregate across bindings.
func (p *Phase) Reconcile(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole, cb *v1alpha1.ConnectorBinding) (desiredOrgs []client.ObjectKey, problems GithubConfigProblems, err error) {
	log := p.Log.WithValues("asr", asr.Name, "connectorBinding", cb.Name)

	advConfig, err := ParseAdvancedConfig(asr.Spec.AdvancedConfig)
	if err != nil {
		log.Error(err, "failed to parse advancedConfig")
		return nil, GithubConfigProblems{}, fmt.Errorf("failed to parse advancedConfig: %w", err)
	}

	cbDef, err := ParseConnectorDefinition(cb.Spec.Definition)
	if err != nil {
		log.Error(err, "failed to parse ConnectorBinding definition")
		return nil, GithubConfigProblems{}, fmt.Errorf("failed to parse ConnectorBinding definition: %w", err)
	}

	targetNamespace := ResolveTargetNamespace(advConfig, cbDef, asr)

	desired, unmappedCaps, invalidCCRNs := BuildDesiredState(asr, cbDef.CapabilityMapping)
	for _, capName := range unmappedCaps {
		log.Info("unmapped capability in ConnectorBinding capabilityMapping",
			"capability", capName,
			"provider", string(pmmeta.ProviderGithub),
		)
	}

	// Resolve CR names against the org CRs (repo-guard naming convention). Teams whose
	// GithubOrganization is missing are dropped and reported as missing orgs.
	resolver := newOrgResolver(p.Client, targetNamespace, log)
	missingFromNaming, err := resolveDesiredNames(ctx, resolver, asr.Spec.Name, desired)
	if err != nil {
		return nil, GithubConfigProblems{}, fmt.Errorf("failed to resolve GithubTeam names: %w", err)
	}

	log.Info("reconciling GitHub resources",
		"targetNamespace", targetNamespace,
		"teams", len(desired.Teams),
		"teamRepos", len(desired.TeamRepos),
	)

	var errs []error

	// Skip stale deletion while an org is missing: the desired set is incomplete, so
	// deleting "unmatched" CRs could remove teams the current view cannot account for.
	deleteStale := len(missingFromNaming) == 0

	if err := ReconcileTeams(ctx, p.Client, desired.Teams, asr.Spec.Name, targetNamespace, asr.Name, asr.Namespace, deleteStale, p.AuditLog); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile GithubTeams: %w", err))
	}

	if err := ReconcileTeamRepos(ctx, p.Client, desired.TeamRepos, asr.Spec.Name, targetNamespace, asr.Name, asr.Namespace, deleteStale, p.AuditLog); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile GithubTeamRepositories: %w", err))
	}

	desiredOrgs, orgProblems, err := p.reconcileOrganizations(ctx, asr, cbDef, targetNamespace)
	if err != nil {
		errs = append(errs, err)
	}

	// This binding's problems; the caller aggregates across bindings. Missing orgs come from
	// both the naming and org-patch paths.
	problems = GithubConfigProblems{
		MissingOrgs:  mergeMissingOrgs(missingFromNaming, orgProblems.MissingOrgs),
		InvalidCCRNs: invalidCCRNs,
		UnmappedCaps: orgProblems.UnmappedCaps,
	}

	ps := computeProviderStatus(ctx, p.Client, asr, desired, unmappedCaps)
	asr.Status.Providers = pmmeta.SetProviderStatus(asr.Status.Providers, pmmeta.ProviderGithub, ps)

	return desiredOrgs, problems, errors.Join(errs...)
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
	// Read uncached: orgManagedByASR needs managedFields, which the cache may strip.
	reader := client.Reader(p.Client)
	if p.APIReader != nil {
		reader = p.APIReader
	}
	var orgList repoguardv1.GithubOrganizationList
	if err := reader.List(ctx, &orgList); err != nil {
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
