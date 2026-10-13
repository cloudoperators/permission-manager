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

// adminPermissionLabel: repo-guard's per-org label, "admin" or "admin-ondemand".
const adminPermissionLabel = "repo-guard.cloudoperators.dev/admin-permission"

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

	// ownerCandidatePerms: org-level mapped perms, resolved to owners per-org at
	// apply time against the org's admin-permission label.
	ownerCandidatePerms []string
	// ownerTeamName is the team added as owner when a candidate perm matches.
	ownerTeamName string
}

// reconcileOrganizations patches org-level permissions and returns desired org keys plus missing
// org CRs. Pruning is the caller's job (see PruneStaleOrgs).
func (p *Phase) reconcileOrganizations(
	ctx context.Context,
	asr *v1alpha1.ApplicationSpecificRole,
	cbDef *GithubConnectorDefinition,
	targetNamespace string,
) (desired []client.ObjectKey, problems GithubConfigProblems, err error) {

	log := p.Log.WithValues("asr", asr.Name)

	// Collect org-level contributions from ASR permissions
	orgContribs := make(map[string]*orgContribution) // keyed by org name

	teamName := asr.Spec.Name
	for _, p := range parseGithubPerms(asr) {
		gh := p.gh

		mappedPermission, ok := cbDef.CapabilityMapping[p.capability]
		if !ok {
			// An unmapped cap on a repository_type CCRN would be dropped silently; surface it.
			// Other resource types are reported on the team path (provider status).
			if gh.ResourceType == ccrn.GithubResourceTypeRepositoryType {
				problems.UnmappedCaps = appendUnique(problems.UnmappedCaps, p.capability)
			}
			log.V(1).Info("capability not in mapping, skipping", "capability", p.capability)
			continue
		}

		switch gh.ResourceType {
		case ccrn.GithubResourceTypeOrganization:
			// Admin-level caps are owner candidates; resolved per-org at apply time.
			if isAdminPermission(mappedPermission) {
				contrib := getOrCreateContrib(orgContribs, gh.Org)
				contrib.ownerCandidatePerms = appendUnique(contrib.ownerCandidatePerms, mappedPermission)
				contrib.ownerTeamName = teamName
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

	// Patch each org. A missing org CR is surfaced as InvalidConfig, not a requeue error — the
	// create watch re-enqueues the ASR once it appears.
	var errs []error
	desired = make([]client.ObjectKey, 0, len(orgContribs))
	for orgName, contrib := range orgContribs {
		desired = append(desired, client.ObjectKey{Namespace: targetNamespace, Name: orgName})
		err := p.patchOrganization(ctx, asr.Namespace, asr.Name, orgName, targetNamespace, contrib)
		if err == nil {
			continue
		}
		if notFound, ok := errors.AsType[*githubOrgNotFoundError](err); ok {
			problems.MissingOrgs = append(problems.MissingOrgs, fmt.Sprintf("%s/%s", notFound.namespace, notFound.name))
			continue
		}
		errs = append(errs, err)
	}

	return desired, problems, errors.Join(errs...)
}

// PruneStaleOrgs releases this ASR's entries on managed orgs absent from desired. Caller passes
// desired aggregated across all bindings (the field manager is per-ASR, not per-binding).
func (p *Phase) PruneStaleOrgs(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole, desired []client.ObjectKey) error {
	want := make(map[client.ObjectKey]struct{}, len(desired))
	for _, k := range desired {
		want[k] = struct{}{}
	}
	var orgList repoguardv1.GithubOrganizationList
	if err := p.Client.List(ctx, &orgList); err != nil {
		return fmt.Errorf("failed to list GithubOrganizations: %w", err)
	}
	for i := range orgList.Items {
		org := &orgList.Items[i]
		if !orgManagedByASR(org, asr.Namespace, asr.Name) {
			continue
		}
		if _, ok := want[client.ObjectKeyFromObject(org)]; ok {
			continue // still desired
		}
		if err := p.removeASRFromOrg(ctx, org, asr.Namespace, asr.Name); err != nil {
			return fmt.Errorf("failed to release org %s/%s: %w", org.Namespace, org.Name, err)
		}
	}
	return nil
}

// githubConfigReasons are the InvalidConfig reasons the github provider owns (set at most one, clear all).
var githubConfigReasons = []pmmeta.ConditionReason{
	pmmeta.MissingGithubOrganizationReason,
	pmmeta.InvalidGithubCCRNReason,
	pmmeta.UnmappedGithubCapabilityReason,
}

// SetGithubConfigValidity sets one InvalidConfig condition (mirrored on Ready) for the highest-
// precedence problem (missing org > invalid CCRN > unmapped capability), or clears it when none.
// Pass problems aggregated across all bindings.
func SetGithubConfigValidity(asr *v1alpha1.ApplicationSpecificRole, problems GithubConfigProblems) {
	reason, msg := githubConfigVerdict(problems)
	if reason == "" {
		asr.ClearInvalidConfigIfReason(githubConfigReasons...)
		// Drop the Ready mirror if it still carries one of our reasons.
		if r := asr.GetReadyCondition(); r != nil {
			for _, gr := range githubConfigReasons {
				if r.Reason == string(gr) {
					asr.RemoveCondition(pmmeta.ReadyCondition)
					break
				}
			}
		}
		return
	}
	asr.SetCondition(pmmeta.TrueCondition(pmmeta.InvalidConfigCondition, reason, msg))
	asr.SetCondition(pmmeta.FalseCondition(pmmeta.ReadyCondition, reason, msg))
}

// githubConfigVerdict picks the highest-precedence problem and builds its reason + message.
func githubConfigVerdict(p GithubConfigProblems) (reason pmmeta.ConditionReason, msg string) {
	switch {
	case len(p.MissingOrgs) > 0:
		v := sortedUnique(p.MissingOrgs)
		return pmmeta.MissingGithubOrganizationReason,
			fmt.Sprintf("GithubOrganization %s not found; ensure repo-guard has created it", strings.Join(v, ", "))
	case len(p.InvalidCCRNs) > 0:
		v := sortedUnique(p.InvalidCCRNs)
		return pmmeta.InvalidGithubCCRNReason,
			fmt.Sprintf("wildcard repository CCRN not supported; use a repository_type CCRN for all-repo access (%s)", strings.Join(v, ", "))
	case len(p.UnmappedCaps) > 0:
		v := sortedUnique(p.UnmappedCaps)
		return pmmeta.UnmappedGithubCapabilityReason,
			fmt.Sprintf("capability %s on a repository_type CCRN is not mapped in the ConnectorBinding", strings.Join(v, ", "))
	default:
		return "", ""
	}
}

// sortedUnique returns a sorted, de-duplicated copy of in.
func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
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

	// Resolve org ownership against the org's admin-permission label, before building
	// the apply payload from contrib.
	if slices.Contains(contrib.ownerCandidatePerms, p.orgAdminPermission(&org)) {
		contrib.OwnerTeams = appendUnique(contrib.OwnerTeams, contrib.ownerTeamName)
	}

	applyObj := newOrgApplyObject(namespace, orgName, org.ResourceVersion, contrib)
	if err := p.Client.Apply(ctx, client.ApplyConfigurationFromUnstructured(applyObj), client.FieldOwner(orgFieldManager(asrNamespace, asrName)), client.ForceOwnership); err != nil {
		p.auditOrgFailure(ctx, asrName, orgName, namespace, err)
		return fmt.Errorf("failed to apply GithubOrganization %s/%s: %w", namespace, orgName, err)
	}

	p.auditOrgSuccess(ctx, asrName, orgName, namespace)
	log.Info("applied GithubOrganization", "org", orgName)
	return nil
}

// removeASRFromOrg releases one ASR's entries on cleanup via an empty apply under its
// field manager. Missing org is a no-op.
func (p *Phase) removeASRFromOrg(ctx context.Context, org *repoguardv1.GithubOrganization, asrNamespace, asrName string) error {
	var fresh repoguardv1.GithubOrganization
	orgKey := client.ObjectKeyFromObject(org)
	if err := p.Client.Get(ctx, orgKey, &fresh); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Org deleted, nothing to clean up.
			return nil
		}
		p.auditOrgFailure(ctx, asrName, org.Name, org.Namespace, err)
		return err
	}

	applyObj := newOrgApplyObject(org.Namespace, org.Name, fresh.ResourceVersion, &orgContribution{})
	if err := p.Client.Apply(ctx, client.ApplyConfigurationFromUnstructured(applyObj), client.FieldOwner(orgFieldManager(asrNamespace, asrName)), client.ForceOwnership); err != nil {
		p.auditOrgFailure(ctx, asrName, org.Name, org.Namespace, err)
		return err
	}

	p.auditOrgSuccess(ctx, asrName, org.Name, org.Namespace)
	return nil
}

// newOrgApplyObject builds the SSA payload: only this ASR's team-list fields (no identity/
// status). Each list is always sent (empty [] when none) so SSA releases dropped entries.
// resourceVersion is a precondition so a delete between Get and apply fails, not recreates.
func newOrgApplyObject(namespace, orgName, resourceVersion string, contrib *orgContribution) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(repoguardv1.GroupVersion.String())
	u.SetKind("GithubOrganization")
	u.SetName(orgName)
	u.SetNamespace(namespace)
	u.SetResourceVersion(resourceVersion)
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

// isAdminPermission reports whether a mapped permission can grant org ownership.
func isAdminPermission(p string) bool {
	return p == repoguardv1.GithubTeamPermissionAdmin || p == repoguardv1.GithubTeamPermissionAdminOndemand
}

// orgAdminPermission returns the org's admin role from its label; missing defaults to admin,
// an unrecognized value is returned as-is so it matches no owner candidate.
func (p *Phase) orgAdminPermission(org *repoguardv1.GithubOrganization) string {
	switch v := org.Labels[adminPermissionLabel]; v {
	case "", repoguardv1.GithubTeamPermissionAdmin:
		return repoguardv1.GithubTeamPermissionAdmin
	case repoguardv1.GithubTeamPermissionAdminOndemand:
		return repoguardv1.GithubTeamPermissionAdminOndemand
	default:
		p.Log.Info("unknown admin-permission label, not granting owner access", "value", v, "org", org.Name)
		return v
	}
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
