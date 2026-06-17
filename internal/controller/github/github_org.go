// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/pkg/audit"
	"github.com/cloudoperators/permission-manager/pkg/ccrn"
)

const (
	// AnnotationASRManagedConfig tracks which ASR names have contributed to this org.
	AnnotationASRManagedConfig = "repo-guard.cloudoperators.dev/asr-managed-config"

	// annotationASRDefaultTeamsPrefix is the prefix for per-ASR annotation tracking contributed entries.
	annotationASRDefaultTeamsPrefix = "repo-guard.cloudoperators.dev/asr-default-teams-"
)

// orgContribution represents what an ASR contributes to a GithubOrganization.
type orgContribution struct {
	PublicTeams  []repoguardv1.GithubTeamWithPermission `json:"publicTeams,omitempty"`
	PrivateTeams []repoguardv1.GithubTeamWithPermission `json:"privateTeams,omitempty"`
	OwnerTeams   []string                               `json:"ownerTeams,omitempty"`
}

// reconcileOrganizations patches GithubOrganization CRs for org-level permissions in the ASR.
func (p *Phase) reconcileOrganizations(
	ctx context.Context,
	asr *v1alpha1.ApplicationSpecificRole,
	cbDef *GithubConnectorDefinition,
	targetNamespace string,
) error {
	log := p.Log.WithValues("asr", asr.Name)

	// Collect org-level contributions from ASR permissions
	orgContribs := make(map[string]*orgContribution) // keyed by org name

	teamName := asr.Spec.Name
	for _, p := range parseGithubPerms(asr) {
		gh := p.gh

		mappedPermission, ok := cbDef.CapabilityMapping[p.capability]
		if !ok {
			log.V(1).Info("capability not in mapping, skipping", "capability", p.capability)
			continue
		}

		switch gh.ResourceType {
		case ccrn.GithubResourceTypeOrganization:
			// Org owner only when the capability maps to "admin".
			if mappedPermission == "admin" {
				contrib := getOrCreateContrib(orgContribs, gh.Org)
				contrib.OwnerTeams = appendUnique(contrib.OwnerTeams, teamName)
			}
		case ccrn.GithubResourceTypeRepositoryType:
			teamEntry := repoguardv1.GithubTeamWithPermission{
				Team:       teamName,
				Permission: repoguardv1.GithubTeamPermission(mappedPermission),
			}
			contrib := getOrCreateContrib(orgContribs, gh.Org)
			switch gh.Visibility {
			case "public":
				contrib.PublicTeams = appendUnique(contrib.PublicTeams, teamEntry)
			case "private":
				contrib.PrivateTeams = appendUnique(contrib.PrivateTeams, teamEntry)
			case "*":
				contrib.PublicTeams = appendUnique(contrib.PublicTeams, teamEntry)
				contrib.PrivateTeams = appendUnique(contrib.PrivateTeams, teamEntry)
			default:
				log.V(1).Info("unknown visibility in repository_type CCRN", "visibility", gh.Visibility, "org", gh.Org)
			}
		}
		// Non-wildcard repository CCRNs are handled by a different reconciler (team-repository)
	}

	// Patch each GithubOrganization with the contributions
	for orgName, contrib := range orgContribs {
		if err := p.patchOrganization(ctx, asr.Spec.Name, orgName, targetNamespace, contrib); err != nil {
			return err
		}
	}

	return nil
}

// patchOrganization merges one ASR's contribution into a GithubOrganization CR, retrying
// on conflict for concurrent ASR updates. Returns an error if the CR does not exist yet,
// so the lifecycle engine requeues.
func (p *Phase) patchOrganization(
	ctx context.Context,
	asrName string,
	orgName string,
	namespace string,
	contrib *orgContribution,
) error {
	log := p.Log.WithValues("asr", asrName, "org", orgName, "namespace", namespace)

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var org repoguardv1.GithubOrganization
		orgKey := client.ObjectKey{Namespace: namespace, Name: orgName}
		if err := p.Client.Get(ctx, orgKey, &org); err != nil {
			return err
		}

		removeASRContributions(&org, asrName)
		applyContributions(&org, asrName, contrib)
		updateASRAnnotations(&org, asrName, contrib)

		return p.Client.Update(ctx, &org)
	})

	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// GithubOrganization CR not found - return an error so the lifecycle engine requeues.
			log.Info("GithubOrganization not found, will retry", "org", orgName)
			return fmt.Errorf("GithubOrganization %s/%s not found", namespace, orgName)
		}
		p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, audit.ResourceRef{
			Kind:      "GithubOrganization",
			Name:      orgName,
			Namespace: namespace,
			Cluster:   "",
		}, err)
		return fmt.Errorf("failed to patch GithubOrganization %s/%s: %w", namespace, orgName, err)
	}

	p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeSuccess, asrName, audit.ResourceRef{
		Kind:      "GithubOrganization",
		Name:      orgName,
		Namespace: namespace,
		Cluster:   "",
	}, nil)

	log.Info("patched GithubOrganization", "org", orgName)
	return nil
}

// removeASRFromOrg removes all entries contributed by a specific ASR from a GithubOrganization.
// Used during cleanup (ASR deletion).
func (p *Phase) removeASRFromOrg(ctx context.Context, org *repoguardv1.GithubOrganization, asrName string) error {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// Re-fetch to get the latest version
		var freshOrg repoguardv1.GithubOrganization
		orgKey := client.ObjectKeyFromObject(org)
		if err := p.Client.Get(ctx, orgKey, &freshOrg); err != nil {
			if client.IgnoreNotFound(err) == nil {
				// Org deleted, nothing to clean up
				return nil
			}
			return err
		}

		removeASRContributions(&freshOrg, asrName)
		removeASRFromAnnotations(&freshOrg, asrName)

		return p.Client.Update(ctx, &freshOrg)
	})

	if err != nil {
		p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, audit.ResourceRef{
			Kind:      "GithubOrganization",
			Name:      org.Name,
			Namespace: org.Namespace,
			Cluster:   "",
		}, err)
		return err
	}

	p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeSuccess, asrName, audit.ResourceRef{
		Kind:      "GithubOrganization",
		Name:      org.Name,
		Namespace: org.Namespace,
		Cluster:   "",
	}, nil)

	return nil
}

// removeASRContributions removes all entries contributed by a specific ASR from the org spec.
func removeASRContributions(org *repoguardv1.GithubOrganization, asrName string) {
	prevContrib := getStoredContribution(org, asrName)
	if prevContrib == nil {
		return
	}

	for _, team := range prevContrib.OwnerTeams {
		org.Spec.OrganizationOwnerTeams = remove(org.Spec.OrganizationOwnerTeams, team)
	}
	for _, team := range prevContrib.PublicTeams {
		org.Spec.DefaultPublicRepositoryTeams = remove(org.Spec.DefaultPublicRepositoryTeams, team)
	}
	for _, team := range prevContrib.PrivateTeams {
		org.Spec.DefaultPrivateRepositoryTeams = remove(org.Spec.DefaultPrivateRepositoryTeams, team)
	}
}

// applyContributions adds the new contributions to the org spec.
func applyContributions(org *repoguardv1.GithubOrganization, asrName string, contrib *orgContribution) {
	for _, team := range contrib.OwnerTeams {
		org.Spec.OrganizationOwnerTeams = appendUnique(org.Spec.OrganizationOwnerTeams, team)
	}
	for _, team := range contrib.PublicTeams {
		org.Spec.DefaultPublicRepositoryTeams = appendUnique(org.Spec.DefaultPublicRepositoryTeams, team)
	}
	for _, team := range contrib.PrivateTeams {
		org.Spec.DefaultPrivateRepositoryTeams = appendUnique(org.Spec.DefaultPrivateRepositoryTeams, team)
	}
}

// updateASRAnnotations updates the tracking annotations on the GithubOrganization.
func updateASRAnnotations(org *repoguardv1.GithubOrganization, asrName string, contrib *orgContribution) {
	if org.Annotations == nil {
		org.Annotations = make(map[string]string)
	}

	// Store the per-ASR contribution as JSON
	contribJSON, _ := json.Marshal(contrib)
	org.Annotations[annotationASRDefaultTeamsPrefix+asrName] = string(contribJSON)

	// Update the managed-config annotation (comma-separated list of ASR names)
	managedASRs := getManagedASRNames(org)
	if !slices.Contains(managedASRs, asrName) {
		managedASRs = append(managedASRs, asrName)
		slices.Sort(managedASRs)
	}
	org.Annotations[AnnotationASRManagedConfig] = strings.Join(managedASRs, ",")
}

// removeASRFromAnnotations removes the ASR from tracking annotations.
func removeASRFromAnnotations(org *repoguardv1.GithubOrganization, asrName string) {
	if org.Annotations == nil {
		return
	}

	// Remove per-ASR annotation
	delete(org.Annotations, annotationASRDefaultTeamsPrefix+asrName)

	// Update the managed-config annotation
	managedASRs := getManagedASRNames(org)
	managedASRs = remove(managedASRs, asrName)
	if len(managedASRs) == 0 {
		delete(org.Annotations, AnnotationASRManagedConfig)
	} else {
		org.Annotations[AnnotationASRManagedConfig] = strings.Join(managedASRs, ",")
	}
}

// getStoredContribution retrieves the previously stored contribution for an ASR from annotations.
func getStoredContribution(org *repoguardv1.GithubOrganization, asrName string) *orgContribution {
	if org.Annotations == nil {
		return nil
	}

	raw, ok := org.Annotations[annotationASRDefaultTeamsPrefix+asrName]
	if !ok || raw == "" {
		return nil
	}

	var contrib orgContribution
	if err := json.Unmarshal([]byte(raw), &contrib); err != nil {
		return nil
	}
	return &contrib
}

// getManagedASRNames returns the list of ASR names currently managing this org.
func getManagedASRNames(org *repoguardv1.GithubOrganization) []string {
	if org.Annotations == nil {
		return nil
	}
	raw, ok := org.Annotations[AnnotationASRManagedConfig]
	if !ok || raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// hasASRContribution checks if a given ASR has contributed to this org (via annotations).
func hasASRContribution(org *repoguardv1.GithubOrganization, asrName string) bool {
	if org.Annotations == nil {
		return false
	}
	_, ok := org.Annotations[annotationASRDefaultTeamsPrefix+asrName]
	return ok
}

func getOrCreateContrib(m map[string]*orgContribution, orgName string) *orgContribution {
	if c, ok := m[orgName]; ok {
		return c
	}
	c := &orgContribution{}
	m[orgName] = c
	return c
}

func appendUnique[E comparable](s []E, v E) []E {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

func remove[E comparable](s []E, v E) []E {
	return slices.DeleteFunc(s, func(e E) bool { return e == v })
}
