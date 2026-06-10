package github

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
	"github.com/cloudoperators/permission-manager/controller/pkg/ccrn"
)

const (
	// DefaultOrgRequeueDelay is the requeue delay when a GithubOrganization CR is not found.
	DefaultOrgRequeueDelay = 30 * time.Second

	// AnnotationASRManagedConfig tracks which ASR names have contributed to this org.
	AnnotationASRManagedConfig = "repo-guard.cloudoperators.dev/asr-managed-config"

	// annotationASRDefaultTeamsPrefix is the prefix for per-ASR annotation tracking contributed entries.
	annotationASRDefaultTeamsPrefix = "repo-guard.cloudoperators.dev/asr-default-teams-"
)

// orgContribution represents what an ASR contributes to a GithubOrganization.
type orgContribution struct {
	PublicTeams  []GithubTeamWithPermission `json:"publicTeams,omitempty"`
	PrivateTeams []GithubTeamWithPermission `json:"privateTeams,omitempty"`
	OwnerTeams   []string                   `json:"ownerTeams,omitempty"`
}

// reconcileOrganizations processes all permissions in the ASR that target GithubOrganization resources.
// It groups permissions by organization and patches each GithubOrganization CR.
func (r *GithubReconciler) reconcileOrganizations(
	ctx context.Context,
	asr *pmv1.ApplicationSpecificRole,
	cbDef *GithubConnectorDefinition,
	targetNamespace string,
) (ctrl.Result, error) {
	log := r.Log.WithValues("asr", asr.Name)

	// Collect org-level contributions from ASR permissions
	orgContribs := make(map[string]*orgContribution) // keyed by org name

	for _, perm := range asr.Spec.Permissions {
		resource := perm.Resource
		// Ensure the resource has the urn:ccrn: prefix for parsing
		if !strings.HasPrefix(resource, urnCCRNPrefix) {
			resource = urnCCRNPrefix + resource
		}

		parsed, err := ccrn.Parse(resource)
		if err != nil || !parsed.IsGithub() {
			continue
		}

		gh := parsed.Github

		// Map capability to permission using ConnectorBinding's capabilityMapping
		mappedPermission, ok := cbDef.CapabilityMapping[perm.Capability]
		if !ok {
			log.V(1).Info("capability not in mapping, skipping", "capability", perm.Capability)
			continue
		}

		teamName := asr.Spec.Name

		switch gh.ResourceType {
		case ccrn.GithubResourceTypeOrganization:
			// Organization CCRN: add to organizationOwnerTeams only if mapped permission is "admin"
			if mappedPermission == "admin" {
				contrib := getOrCreateContrib(orgContribs, gh.Org)
				contrib.OwnerTeams = appendUniqueString(contrib.OwnerTeams, teamName)
			}
		case ccrn.GithubResourceTypeRepositoryType:
			// repository_type CCRN: add to default teams based on visibility
			visibility := gh.Visibility
			teamEntry := GithubTeamWithPermission{
				Name:       teamName,
				Permission: mappedPermission,
			}

			contrib := getOrCreateContrib(orgContribs, gh.Org)
			switch visibility {
			case "public":
				contrib.PublicTeams = appendUniqueTeam(contrib.PublicTeams, teamEntry)
			case "private":
				contrib.PrivateTeams = appendUniqueTeam(contrib.PrivateTeams, teamEntry)
			case "*":
				contrib.PublicTeams = appendUniqueTeam(contrib.PublicTeams, teamEntry)
				contrib.PrivateTeams = appendUniqueTeam(contrib.PrivateTeams, teamEntry)
			default:
				log.V(1).Info("unknown visibility in repository_type CCRN", "visibility", visibility, "resource", perm.Resource)
			}
		}
		// Non-wildcard repository CCRNs are handled by a different reconciler (team-repository)
	}

	// Patch each GithubOrganization with the contributions
	for orgName, contrib := range orgContribs {
		result, err := r.patchOrganization(ctx, asr.Spec.Name, orgName, targetNamespace, contrib)
		if err != nil {
			return result, err
		}
		if result.RequeueAfter > 0 {
			return result, nil
		}
	}

	return ctrl.Result{}, nil
}

// patchOrganization applies the contribution from a single ASR to a GithubOrganization CR.
// It uses RetryOnConflict to handle concurrent updates from multiple ASRs.
func (r *GithubReconciler) patchOrganization(
	ctx context.Context,
	asrName string,
	orgName string,
	namespace string,
	contrib *orgContribution,
) (ctrl.Result, error) {
	log := r.Log.WithValues("asr", asrName, "org", orgName, "namespace", namespace)

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// Fetch the GithubOrganization CR
		var org GithubOrganization
		orgKey := client.ObjectKey{Namespace: namespace, Name: orgName}
		if err := r.Get(ctx, orgKey, &org); err != nil {
			return err
		}

		// Remove stale entries from this ASR before adding new ones
		removeASRContributions(&org, asrName)

		// Add new contributions
		applyContributions(&org, asrName, contrib)

		// Update the ASR tracking annotations
		updateASRAnnotations(&org, asrName, contrib)

		// Persist the update
		return r.Update(ctx, &org)
	})

	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// GithubOrganization CR not found - requeue with delay
			log.Info("GithubOrganization not found, requeueing", "org", orgName)
			return ctrl.Result{RequeueAfter: DefaultOrgRequeueDelay}, nil
		}
		r.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, audit.ResourceRef{
			Kind:      "GithubOrganization",
			Name:      orgName,
			Namespace: namespace,
			Cluster:   "",
		}, err)
		return ctrl.Result{}, fmt.Errorf("failed to patch GithubOrganization %s/%s: %w", namespace, orgName, err)
	}

	r.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeSuccess, asrName, audit.ResourceRef{
		Kind:      "GithubOrganization",
		Name:      orgName,
		Namespace: namespace,
		Cluster:   "",
	}, nil)

	log.Info("patched GithubOrganization", "org", orgName)
	return ctrl.Result{}, nil
}

// removeASRFromOrg removes all entries contributed by a specific ASR from a GithubOrganization.
// Used during cleanup (ASR deletion).
func (r *GithubReconciler) removeASRFromOrg(ctx context.Context, org *GithubOrganization, asrName string) error {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// Re-fetch to get the latest version
		var freshOrg GithubOrganization
		orgKey := client.ObjectKeyFromObject(org)
		if err := r.Get(ctx, orgKey, &freshOrg); err != nil {
			if client.IgnoreNotFound(err) == nil {
				// Org deleted, nothing to clean up
				return nil
			}
			return err
		}

		removeASRContributions(&freshOrg, asrName)
		removeASRFromAnnotations(&freshOrg, asrName)

		return r.Update(ctx, &freshOrg)
	})

	if err != nil {
		r.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, audit.ResourceRef{
			Kind:      "GithubOrganization",
			Name:      org.Name,
			Namespace: org.Namespace,
			Cluster:   "",
		}, err)
		return err
	}

	r.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeSuccess, asrName, audit.ResourceRef{
		Kind:      "GithubOrganization",
		Name:      org.Name,
		Namespace: org.Namespace,
		Cluster:   "",
	}, nil)

	return nil
}

// removeASRContributions removes all entries contributed by a specific ASR from the org spec.
func removeASRContributions(org *GithubOrganization, asrName string) {
	// Get what this ASR previously contributed
	prevContrib := getStoredContribution(org, asrName)
	if prevContrib == nil {
		return
	}

	// Remove owner teams
	for _, team := range prevContrib.OwnerTeams {
		org.Spec.OrganizationOwnerTeams = removeString(org.Spec.OrganizationOwnerTeams, team)
	}

	// Remove public teams
	for _, team := range prevContrib.PublicTeams {
		org.Spec.DefaultPublicRepositoryTeams = removeTeam(org.Spec.DefaultPublicRepositoryTeams, team)
	}

	// Remove private teams
	for _, team := range prevContrib.PrivateTeams {
		org.Spec.DefaultPrivateRepositoryTeams = removeTeam(org.Spec.DefaultPrivateRepositoryTeams, team)
	}
}

// applyContributions adds the new contributions to the org spec.
func applyContributions(org *GithubOrganization, asrName string, contrib *orgContribution) {
	// Add owner teams
	for _, team := range contrib.OwnerTeams {
		org.Spec.OrganizationOwnerTeams = appendUniqueString(org.Spec.OrganizationOwnerTeams, team)
	}

	// Add public teams
	for _, team := range contrib.PublicTeams {
		org.Spec.DefaultPublicRepositoryTeams = appendUniqueTeam(org.Spec.DefaultPublicRepositoryTeams, team)
	}

	// Add private teams
	for _, team := range contrib.PrivateTeams {
		org.Spec.DefaultPrivateRepositoryTeams = appendUniqueTeam(org.Spec.DefaultPrivateRepositoryTeams, team)
	}
}

// updateASRAnnotations updates the tracking annotations on the GithubOrganization.
func updateASRAnnotations(org *GithubOrganization, asrName string, contrib *orgContribution) {
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
		sort.Strings(managedASRs)
	}
	org.Annotations[AnnotationASRManagedConfig] = strings.Join(managedASRs, ",")
}

// removeASRFromAnnotations removes the ASR from tracking annotations.
func removeASRFromAnnotations(org *GithubOrganization, asrName string) {
	if org.Annotations == nil {
		return
	}

	// Remove per-ASR annotation
	delete(org.Annotations, annotationASRDefaultTeamsPrefix+asrName)

	// Update the managed-config annotation
	managedASRs := getManagedASRNames(org)
	managedASRs = removeString(managedASRs, asrName)
	if len(managedASRs) == 0 {
		delete(org.Annotations, AnnotationASRManagedConfig)
	} else {
		org.Annotations[AnnotationASRManagedConfig] = strings.Join(managedASRs, ",")
	}
}

// getStoredContribution retrieves the previously stored contribution for an ASR from annotations.
func getStoredContribution(org *GithubOrganization, asrName string) *orgContribution {
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
func getManagedASRNames(org *GithubOrganization) []string {
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
func hasASRContribution(org *GithubOrganization, asrName string) bool {
	if org.Annotations == nil {
		return false
	}
	_, ok := org.Annotations[annotationASRDefaultTeamsPrefix+asrName]
	return ok
}

// --- Helper functions ---

func getOrCreateContrib(m map[string]*orgContribution, orgName string) *orgContribution {
	if c, ok := m[orgName]; ok {
		return c
	}
	c := &orgContribution{}
	m[orgName] = c
	return c
}

func appendUniqueString(slice []string, s string) []string {
	for _, existing := range slice {
		if existing == s {
			return slice
		}
	}
	return append(slice, s)
}

func appendUniqueTeam(slice []GithubTeamWithPermission, team GithubTeamWithPermission) []GithubTeamWithPermission {
	for _, existing := range slice {
		if existing.Name == team.Name && existing.Permission == team.Permission {
			return slice
		}
	}
	return append(slice, team)
}

func removeString(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

func removeTeam(slice []GithubTeamWithPermission, team GithubTeamWithPermission) []GithubTeamWithPermission {
	result := make([]GithubTeamWithPermission, 0, len(slice))
	for _, item := range slice {
		if item.Name != team.Name || item.Permission != team.Permission {
			result = append(result, item)
		}
	}
	return result
}

