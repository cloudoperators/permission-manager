// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	rbacv1 "k8s.io/api/rbac/v1"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

func (p *Phase) SetCompiled(targets []ExportCompiledTarget) {
	out := make([]compiledTarget, 0, len(targets))
	for _, t := range targets {
		out = append(out, compiledTarget{
			clusterName:               t.ClusterName,
			clusterRoles:              t.ClusterRoles,
			clusterRoleBindings:       t.ClusterRoleBindings,
			roles:                     t.Roles,
			roleBindings:              t.RoleBindings,
			coarseClusterRoleBindings: t.CoarseClusterRoleBindings,
			coarseRoleBindings:        t.CoarseRoleBindings,
		})
	}
	p.compiled = out
}

func (p *Phase) SetCBs(cbs []*v1alpha1.ConnectorBinding) {
	p.cbs = cbs
}

// ASRLabelValue exposes asrLabelValue to the external test package.
func ASRLabelValue(asrName string) string {
	return asrLabelValue(asrName)
}

func (p *Phase) SetInvalidCCRNs(ccrns []string) {
	p.invalidCCRNs = ccrns
}

func (p *Phase) SetUnmappedCaps(caps []string) {
	p.unmappedCaps = caps
}

func (p *Phase) SetUnmatchedClusterPatterns(patterns []string) {
	p.unmatchedClusterPatterns = patterns
}

func (p *Phase) SetNotReadyClusters(clusters []string) {
	p.notReadyClusters = clusters
}

func (p *Phase) SetProvisionStatuses(entries []ExportClusterStatus) {
	out := make([]clusterStatus, 0, len(entries))
	for _, e := range entries {
		out = append(out, clusterStatus{name: e.Name, err: e.Err, appliedResources: e.AppliedResources})
	}
	p.provisionStatuses = out
}

func (p *Phase) SetOrphanStatuses(entries []ExportClusterStatus) {
	out := make([]clusterStatus, 0, len(entries))
	for _, e := range entries {
		out = append(out, clusterStatus{name: e.Name, err: e.Err, appliedResources: e.AppliedResources})
	}
	p.orphanStatuses = out
}

func (p *Phase) SetProvisionErrs(errs []error) {
	p.provisionErrs = errs
}

func (p *Phase) SetOrphanErrs(errs []error) {
	p.orphanErrs = errs
}

type ExportClusterStatus struct {
	Name             string
	Err              error
	AppliedResources []pmmeta.AppliedResourceRef
}

func (p *Phase) Compiled() []ExportCompiledTarget {
	out := make([]ExportCompiledTarget, 0, len(p.compiled))
	for _, t := range p.compiled {
		out = append(out, ExportCompiledTarget{
			ClusterName:               t.clusterName,
			ClusterRoles:              t.clusterRoles,
			ClusterRoleBindings:       t.clusterRoleBindings,
			Roles:                     t.roles,
			RoleBindings:              t.roleBindings,
			CoarseClusterRoleBindings: t.coarseClusterRoleBindings,
			CoarseRoleBindings:        t.coarseRoleBindings,
		})
	}
	return out
}

type ExportCompiledTarget struct {
	ClusterName               string
	ClusterRoles              []rbacv1.ClusterRole
	ClusterRoleBindings       []rbacv1.ClusterRoleBinding
	Roles                     []rbacv1.Role
	RoleBindings              []rbacv1.RoleBinding
	CoarseClusterRoleBindings []rbacv1.ClusterRoleBinding
	CoarseRoleBindings        []rbacv1.RoleBinding
}
