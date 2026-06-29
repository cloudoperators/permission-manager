// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"sort"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
)

// EnsureRbacCompiled translates ASR permissions into per-cluster RBAC objects
// in p.compiled. Reads p.cbs and p.invalidCCRNs from prior subroutines.
//
// Records degraded reasons on the Phase:
//   - p.unmappedCaps             — capabilities no CB defines
//   - p.unmatchedClusterPatterns — patterns that matched zero clusters
//
// Writes InvalidConfig=True (mirrored on Ready=False) with reason
// UnknownCapability or NoTargetsResolved when those accumulators are non-empty;
// clears each reason when its accumulator is empty.
func (p *Phase) EnsureRbacCompiled(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(_ context.Context) (lifecycle.Result, error) {
		invalid := stringSet(p.invalidCCRNs)
		unmappedSeen := make(map[string]struct{})
		unmatchedSeen := make(map[string]struct{})

		// per-cluster scratch: cluster name → { cluster verbs, ns → verbs }
		type scope struct {
			clusterVerbs   map[string]struct{}
			namespaceVerbs map[string]map[string]struct{}
		}
		scopes := make(map[string]*scope)
		ensureScope := func(name string) *scope {
			s, ok := scopes[name]
			if !ok {
				s = &scope{
					clusterVerbs:   make(map[string]struct{}),
					namespaceVerbs: make(map[string]map[string]struct{}),
				}
				scopes[name] = s
			}
			return s
		}

		for _, perm := range asr.Spec.Permissions {
			if _, bad := invalid[perm.Resource]; bad {
				continue
			}
			parsed, err := ccrn.Parse(perm.Resource)
			if err != nil || !parsed.IsKubernetes() {
				continue
			}

			verbs, found := UnionVerbsForCapability(p.cbs, perm.Capability)
			if !found {
				capability := perm.Capability
				if _, seen := unmappedSeen[capability]; !seen {
					unmappedSeen[capability] = struct{}{}
					p.unmappedCaps = append(p.unmappedCaps, capability)
				}
				continue
			}

			matches := matchClusterPattern(p.ClusterStore, parsed.ClusterName, asr.Namespace)
			if len(matches) == 0 {
				if _, seen := unmatchedSeen[parsed.ClusterName]; !seen {
					unmatchedSeen[parsed.ClusterName] = struct{}{}
					p.unmatchedClusterPatterns = append(p.unmatchedClusterPatterns, parsed.ClusterName)
				}
				continue
			}

			for _, clusterName := range matches {
				s := ensureScope(clusterName)
				if parsed.Namespace == "" {
					for _, v := range verbs {
						s.clusterVerbs[v] = struct{}{}
					}
				} else {
					if _, ok := s.namespaceVerbs[parsed.Namespace]; !ok {
						s.namespaceVerbs[parsed.Namespace] = make(map[string]struct{})
					}
					for _, v := range verbs {
						s.namespaceVerbs[parsed.Namespace][v] = struct{}{}
					}
				}
			}
		}

		// Flatten into p.compiled. Verb sets are sorted at materialization
		// for deterministic output.
		for clusterName, s := range scopes {
			target := compiledTarget{clusterName: clusterName}
			if len(s.clusterVerbs) > 0 {
				cr, crb := buildClusterScopedRBAC(asr.Name, asr.Spec.Name, s.clusterVerbs)
				target.clusterRoles = append(target.clusterRoles, *cr)
				target.clusterRoleBindings = append(target.clusterRoleBindings, *crb)
			}
			for _, ns := range sortedKeys(s.namespaceVerbs) {
				role, rb := buildNamespaceScopedRBAC(asr.Name, asr.Spec.Name, ns, s.namespaceVerbs[ns])
				target.roles = append(target.roles, *role)
				target.roleBindings = append(target.roleBindings, *rb)
			}
			p.compiled = append(p.compiled, target)
		}

		if len(p.unmappedCaps) > 0 {
			msg := fmt.Sprintf("unmapped capabilities: %v", p.unmappedCaps)
			asr.SetCondition(pmmeta.TrueCondition(
				pmmeta.InvalidConfigCondition,
				pmmeta.UnknownCapabilityReason,
				msg,
			))
			asr.SetCondition(pmmeta.FalseCondition(
				pmmeta.ReadyCondition,
				pmmeta.UnknownCapabilityReason,
				msg,
			))
		} else {
			asr.ClearInvalidConfigIfReason(pmmeta.UnknownCapabilityReason)
		}

		if len(p.unmatchedClusterPatterns) > 0 {
			msg := fmt.Sprintf("cluster pattern(s) matched no clusters: %v", p.unmatchedClusterPatterns)
			asr.SetCondition(pmmeta.TrueCondition(
				pmmeta.InvalidConfigCondition,
				pmmeta.NoTargetsResolvedReason,
				msg,
			))
			asr.SetCondition(pmmeta.FalseCondition(
				pmmeta.ReadyCondition,
				pmmeta.NoTargetsResolvedReason,
				msg,
			))
		} else {
			asr.ClearInvalidConfigIfReason(pmmeta.NoTargetsResolvedReason)
		}

		return lifecycle.Continue(), nil
	}
}

// buildClusterScopedRBAC materializes a cluster-wide ClusterRole + ClusterRoleBinding.
func buildClusterScopedRBAC(asrName, specName string, verbs map[string]struct{}) (*rbacv1.ClusterRole, *rbacv1.ClusterRoleBinding) {
	resourceName := resourceNamePrefix + asrName
	labels := map[string]string{
		pmapis.LabelManagedByKey: pmapis.ManagedByPMController,
		pmapis.LabelASR:          asrName,
	}
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Labels: labels},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"*"},
			Resources: []string{"*"},
			Verbs:     sortedKeys(verbs),
		}},
	}
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Labels: labels},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     kindClusterRole,
			Name:     resourceName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:     "Group",
			Name:     specName,
			APIGroup: "rbac.authorization.k8s.io",
		}},
	}
	return cr, crb
}

// buildNamespaceScopedRBAC materializes a namespace-scoped Role + RoleBinding.
func buildNamespaceScopedRBAC(asrName, specName, ns string, verbs map[string]struct{}) (*rbacv1.Role, *rbacv1.RoleBinding) {
	resourceName := resourceNamePrefix + asrName
	labels := map[string]string{
		pmapis.LabelManagedByKey: pmapis.ManagedByPMController,
		pmapis.LabelASR:          asrName,
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: ns, Labels: labels},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"*"},
			Resources: []string{"*"},
			Verbs:     sortedKeys(verbs),
		}},
	}
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: ns, Labels: labels},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     kindRole,
			Name:     resourceName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:     "Group",
			Name:     specName,
			APIGroup: "rbac.authorization.k8s.io",
		}},
	}
	return role, rb
}

// sortedKeys returns map keys in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// matchClusterPattern resolves a cluster pattern against the ClusterStore.
// Exact names are looked up by "namespace/name" key; wildcards iterate and
// glob-match.
func matchClusterPattern(store cache.Store, pattern, namespace string) []string {
	if !ccrn.IsWildcard(pattern) {
		if _, exists, err := store.GetByKey(namespace + "/" + pattern); err == nil && exists {
			return []string{pattern}
		}
		return nil
	}

	var matches []string
	for _, obj := range store.List() {
		cluster, ok := obj.(*greenhousev1alpha1.Cluster)
		if !ok {
			continue
		}
		if cluster.Namespace != namespace {
			continue
		}
		if ccrn.MatchGlob(pattern, cluster.Name) {
			matches = append(matches, cluster.Name)
		}
	}
	sort.Strings(matches)
	return matches
}

// stringSet builds a set from a slice.
func stringSet(ss []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		out[s] = struct{}{}
	}
	return out
}
