// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
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
// Returns missing org CRs so the caller can aggregate across ConnectorBindings before setting
// the InvalidConfig condition once.
func (p *Phase) reconcileOrganizations(
	ctx context.Context,
	asr *v1alpha1.ApplicationSpecificRole,
	cbDef *GithubConnectorDefinition,
	targetNamespace string,
) ([]string, error) {

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
				contrib.PublicTeams = upsertTeamPermission(contrib.PublicTeams, teamEntry)
			case "private":
				contrib.PrivateTeams = upsertTeamPermission(contrib.PrivateTeams, teamEntry)
			case "internal":
				contrib.InternalTeams = upsertTeamPermission(contrib.InternalTeams, teamEntry)
			case "*":
				contrib.PublicTeams = upsertTeamPermission(contrib.PublicTeams, teamEntry)
				contrib.PrivateTeams = upsertTeamPermission(contrib.PrivateTeams, teamEntry)
				contrib.InternalTeams = upsertTeamPermission(contrib.InternalTeams, teamEntry)
			default:
				log.V(1).Info("unknown visibility in repository_type CCRN", "visibility", gh.Visibility, "org", gh.Org)
			}
		}
		// Non-wildcard repository CCRNs are handled by a different reconciler (team-repository)
	}

	// Patch each org. A missing org CR is a permanent misconfig (repo-guard owns
	// creation), surfaced as InvalidConfig rather than a requeue error; the
	// GithubOrganization create watch re-enqueues the ASR once the CR appears.
	var (
		errs        []error
		missingOrgs []string
	)
	for orgName, contrib := range orgContribs {
		err := p.patchOrganization(ctx, asr.Namespace, asr.Name, orgName, targetNamespace, contrib)
		if err == nil {
			continue
		}
		if notFound, ok := errors.AsType[*githubOrgNotFoundError](err); ok {
			missingOrgs = append(missingOrgs, fmt.Sprintf("%s/%s", notFound.namespace, notFound.name))
			continue
		}
		errs = append(errs, err)
	}

	// Release orgs this ASR manages but no longer contributes to.
	if err := p.pruneStaleOrgs(ctx, asr, targetNamespace, orgContribs); err != nil {
		errs = append(errs, err)
	}

	return missingOrgs, errors.Join(errs...)
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

// SetOrgConfigValidity sets InvalidConfig (mirrored on Ready) for missing orgs, or clears it
// when none. Pass orgs missing across all bindings.
func SetOrgConfigValidity(asr *v1alpha1.ApplicationSpecificRole, missingOrgs []string) {
	if len(missingOrgs) == 0 {
		asr.ClearInvalidConfigIfReason(pmmeta.MissingGithubOrganizationReason)
		// Drop the Ready we set so it is re-derived downstream.
		if r := asr.GetReadyCondition(); r != nil && r.Reason == string(pmmeta.MissingGithubOrganizationReason) {
			asr.RemoveCondition(pmmeta.ReadyCondition)
		}
		return
	}
	slices.Sort(missingOrgs)
	msg := fmt.Sprintf("GithubOrganization %s not found; ensure repo-guard has created it", strings.Join(missingOrgs, ", "))
	asr.SetCondition(pmmeta.TrueCondition(pmmeta.InvalidConfigCondition, pmmeta.MissingGithubOrganizationReason, msg))
	asr.SetCondition(pmmeta.FalseCondition(pmmeta.ReadyCondition, pmmeta.MissingGithubOrganizationReason, msg))
}

// githubOrgNotFoundError marks a missing GithubOrganization CR; the caller surfaces
// it as InvalidConfig instead of requeuing (repo-guard owns creation).
type githubOrgNotFoundError struct {
	namespace, name string
}

func (e *githubOrgNotFoundError) Error() string {
	return fmt.Sprintf("GithubOrganization %s/%s not found", e.namespace, e.name)
}

// patchOrganization applies one ASR's contribution via SSA under its per-ASR field manager.
// The Get only detects a missing CR (Helm owns creation); a missing CR returns
// githubOrgNotFoundError, which the caller surfaces as InvalidConfig (no requeue).
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
			// Not found: surface as InvalidConfig; do not let SSA create it (Helm owns creation).
			log.Info("GithubOrganization not found, surfacing InvalidConfig", "org", orgName)
			return &githubOrgNotFoundError{namespace: namespace, name: orgName}
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

// githubPermissionRank orders GitHub repo permissions weak→strong.
var githubPermissionRank = map[repoguardv1.GithubTeamPermission]int{
	"pull": 1, "triage": 2, "push": 3, "maintain": 4, "admin": 5,
}

// upsertTeamPermission adds t, or keeps the stronger permission if the team is already present.
// The team-list fields are listType=map keyed by team, so at most one entry per team.
func upsertTeamPermission(s []repoguardv1.GithubTeamWithPermission, t repoguardv1.GithubTeamWithPermission) []repoguardv1.GithubTeamWithPermission {
	for i := range s {
		if s[i].Team == t.Team {
			if githubPermissionRank[t.Permission] > githubPermissionRank[s[i].Permission] {
				s[i].Permission = t.Permission
			}
			return s
		}
	}
	return append(s, t)
}
