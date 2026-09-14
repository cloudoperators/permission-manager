// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
)

// fieldManagerPrefix prefixes PM's per-ASR SSA field managers.
const fieldManagerPrefix = "permission-manager/"

// fieldManagerMaxLen is the apiserver's managedFields manager limit.
const fieldManagerMaxLen = 128

// orgFieldManager keys on the ASR's namespace/name, not spec.name (shared spec.name
// must not share a manager). Long ns/name hash to stay under the 128-byte limit.
func orgFieldManager(asrNamespace, asrName string) string {
	mgr := fieldManagerPrefix + asrNamespace + "/" + asrName
	if len(mgr) <= fieldManagerMaxLen {
		return mgr
	}
	sum := sha256.Sum256([]byte(asrNamespace + "/" + asrName))
	return fieldManagerPrefix + hex.EncodeToString(sum[:])
}

// orgContribution represents what an ASR contributes to a GithubOrganization.
type orgContribution struct {
	PublicTeams   []repoguardv1.GithubTeamWithPermission
	PrivateTeams  []repoguardv1.GithubTeamWithPermission
	InternalTeams []repoguardv1.GithubTeamWithPermission
	OwnerTeams    []string
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
			case "internal":
				contrib.InternalTeams = appendUnique(contrib.InternalTeams, teamEntry)
			case "*":
				contrib.PublicTeams = appendUnique(contrib.PublicTeams, teamEntry)
				contrib.PrivateTeams = appendUnique(contrib.PrivateTeams, teamEntry)
				contrib.InternalTeams = appendUnique(contrib.InternalTeams, teamEntry)
			default:
				log.V(1).Info("unknown visibility in repository_type CCRN", "visibility", gh.Visibility, "org", gh.Org)
			}
		}
		// Non-wildcard repository CCRNs are handled by a different reconciler (team-repository)
	}

	// Patch each GithubOrganization with the contributions
	for orgName, contrib := range orgContribs {
		if err := p.patchOrganization(ctx, asr.Namespace, asr.Name, orgName, targetNamespace, contrib); err != nil {
			return err
		}
	}

	// Release orgs this ASR manages but no longer contributes to.
	return p.pruneStaleOrgs(ctx, asr, targetNamespace, orgContribs)
}

// pruneStaleOrgs releases this ASR's entries on managed orgs missing from the desired set;
// without it, dropped orgs keep stale entries until the ASR is deleted.
func (p *Phase) pruneStaleOrgs(
	ctx context.Context,
	asr *v1alpha1.ApplicationSpecificRole,
	targetNamespace string,
	desired map[string]*orgContribution,
) error {

	var orgList repoguardv1.GithubOrganizationList
	if err := p.Client.List(ctx, &orgList); err != nil {
		return fmt.Errorf("failed to list GithubOrganizations: %w", err)
	}
	for i := range orgList.Items {
		org := &orgList.Items[i]
		if !orgManagedByASR(org, asr.Namespace, asr.Name) {
			continue
		}
		if _, ok := desired[org.Name]; ok && org.Namespace == targetNamespace {
			continue // just applied above
		}
		if err := p.removeASRFromOrg(ctx, org, asr.Namespace, asr.Name); err != nil {
			return fmt.Errorf("failed to release org %s/%s: %w", org.Namespace, org.Name, err)
		}
	}
	return nil
}

// patchOrganization applies one ASR's contribution via SSA under its field manager. The
// Get only detects a missing CR (Helm owns creation); the apiserver merges/prunes per ASR.
func (p *Phase) patchOrganization(
	ctx context.Context,
	asrNamespace string,
	asrName string,
	orgName string,
	namespace string,
	contrib *orgContribution,
) error {

	log := p.Log.WithValues("asr", asrName, "org", orgName, "namespace", namespace)

	var org repoguardv1.GithubOrganization
	orgKey := client.ObjectKey{Namespace: namespace, Name: orgName}
	if err := p.Client.Get(ctx, orgKey, &org); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Not found: requeue rather than let SSA create it (Helm owns creation).
			log.Info("GithubOrganization not found, will retry", "org", orgName)
			return fmt.Errorf("GithubOrganization %s/%s not found", namespace, orgName)
		}
		p.auditOrgFailure(ctx, asrName, orgName, namespace, err)
		return fmt.Errorf("failed to get GithubOrganization %s/%s: %w", namespace, orgName, err)
	}

	applyObj := newOrgApplyObject(namespace, orgName, contrib)
	if err := p.Client.Apply(ctx, client.ApplyConfigurationFromUnstructured(applyObj), client.FieldOwner(orgFieldManager(asrNamespace, asrName)), client.ForceOwnership); err != nil {
		p.auditOrgFailure(ctx, asrName, orgName, namespace, err)
		return fmt.Errorf("failed to apply GithubOrganization %s/%s: %w", namespace, orgName, err)
	}

	p.auditOrgSuccess(ctx, asrName, orgName, namespace)
	log.Info("applied GithubOrganization", "org", orgName)
	return nil
}

// removeASRFromOrg releases one ASR's entries on cleanup by applying an empty contribution
// under its field manager; SSA prunes only this ASR's entries. Missing org is a no-op.
func (p *Phase) removeASRFromOrg(ctx context.Context, org *repoguardv1.GithubOrganization, asrNamespace, asrName string) error {
	orgKey := client.ObjectKeyFromObject(org)
	if err := p.Client.Get(ctx, orgKey, &repoguardv1.GithubOrganization{}); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Org deleted, nothing to clean up.
			return nil
		}
		p.auditOrgFailure(ctx, asrName, org.Name, org.Namespace, err)
		return err
	}

	applyObj := newOrgApplyObject(org.Namespace, org.Name, &orgContribution{})
	if err := p.Client.Apply(ctx, client.ApplyConfigurationFromUnstructured(applyObj), client.FieldOwner(orgFieldManager(asrNamespace, asrName)), client.ForceOwnership); err != nil {
		p.auditOrgFailure(ctx, asrName, org.Name, org.Namespace, err)
		return err
	}

	p.auditOrgSuccess(ctx, asrName, org.Name, org.Namespace)
	return nil
}

// newOrgApplyObject builds the SSA payload: only this ASR's team-list fields (no identity/
// status). Each list is always sent (empty [] when none) so SSA releases dropped entries.
func newOrgApplyObject(namespace, orgName string, contrib *orgContribution) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(repoguardv1.GroupVersion.String())
	u.SetKind("GithubOrganization")
	u.SetName(orgName)
	u.SetNamespace(namespace)
	u.Object["spec"] = map[string]any{
		"organizationOwnerTeams":         stringsToUnstructured(contrib.OwnerTeams),
		"defaultPublicRepositoryTeams":   teamsToUnstructured(contrib.PublicTeams),
		"defaultPrivateRepositoryTeams":  teamsToUnstructured(contrib.PrivateTeams),
		"defaultInternalRepositoryTeams": teamsToUnstructured(contrib.InternalTeams),
	}
	return u
}

// stringsToUnstructured returns a non-nil slice so an empty list serializes as [], not dropped.
func stringsToUnstructured(in []string) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}

// teamsToUnstructured is the []GithubTeamWithPermission analogue of stringsToUnstructured.
func teamsToUnstructured(in []repoguardv1.GithubTeamWithPermission) []any {
	out := make([]any, 0, len(in))
	for _, t := range in {
		entry := map[string]any{"team": t.Team}
		if t.Permission != "" { // omit when empty, matching the CRD's omitempty
			entry["permission"] = string(t.Permission)
		}
		out = append(out, entry)
	}
	return out
}

// auditOrgSuccess and auditOrgFailure record a GithubOrganization apply outcome.
func (p *Phase) auditOrgSuccess(ctx context.Context, asrName, orgName, namespace string) {
	p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeSuccess, asrName, audit.ResourceRef{
		Kind:      "GithubOrganization",
		Name:      orgName,
		Namespace: namespace,
	}, nil)
}

func (p *Phase) auditOrgFailure(ctx context.Context, asrName, orgName, namespace string, err error) {
	p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, audit.ResourceRef{
		Kind:      "GithubOrganization",
		Name:      orgName,
		Namespace: namespace,
	}, err)
}

// orgManagedByASR reports whether the ASR has entries on the org, via its SSA field manager.
func orgManagedByASR(org *repoguardv1.GithubOrganization, asrNamespace, asrName string) bool {
	mgr := orgFieldManager(asrNamespace, asrName)
	for _, mf := range org.GetManagedFields() {
		if mf.Manager == mgr {
			return true
		}
	}
	return false
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
