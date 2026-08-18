// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
		parser := p.CCRNParser
		if parser == nil {
			parser = ccrn.NewParser(nil)
		}

		// per-cluster scratch: cluster name → { cluster rules, ns → rules }
		type scope struct {
			clusterRules   *policyRuleSet
			namespaceRules map[string]*policyRuleSet
		}
		scopes := make(map[string]*scope)
		ensureScope := func(name string) *scope {
			s, ok := scopes[name]
			if !ok {
				s = &scope{
					clusterRules:   newPolicyRuleSet(),
					namespaceRules: make(map[string]*policyRuleSet),
				}
				scopes[name] = s
			}
			return s
		}

		for _, perm := range asr.Spec.Permissions {
			if _, bad := invalid[perm.Resource]; bad {
				continue
			}
			parsed, err := parser.Parse(perm.Resource)
			if err != nil || !parsed.IsKubernetes() {
				continue
			}

			rules, found := UnionRulesForCapability(p.cbs, perm.Capability)
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
					for _, r := range rules {
						s.clusterRules.add(r)
					}
				} else {
					nsSet, ok := s.namespaceRules[parsed.Namespace]
					if !ok {
						nsSet = newPolicyRuleSet()
						s.namespaceRules[parsed.Namespace] = nsSet
					}
					for _, r := range rules {
						nsSet.add(r)
					}
				}
			}
		}

		// Flatten into p.compiled. Rule sets materialize to deterministically
		// ordered PolicyRules.
		for clusterName, s := range scopes {
			target := compiledTarget{clusterName: clusterName}
			if !s.clusterRules.empty() {
				cr, crb := buildClusterScopedRBAC(asr.Name, asr.Spec.Name, s.clusterRules)
				target.clusterRoles = append(target.clusterRoles, *cr)
				target.clusterRoleBindings = append(target.clusterRoleBindings, *crb)
			}
			for _, ns := range sortedKeys(s.namespaceRules) {
				role, rb := buildNamespaceScopedRBAC(asr.Name, asr.Spec.Name, ns, s.namespaceRules[ns])
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
func buildClusterScopedRBAC(asrName, specName string, rules *policyRuleSet) (*rbacv1.ClusterRole, *rbacv1.ClusterRoleBinding) {
	resourceName := resourceNamePrefix + asrName
	labels := map[string]string{
		pmapis.LabelManagedByKey: pmapis.ManagedByPMController,
		pmapis.LabelASR:          asrName,
	}
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Labels: labels},
		Rules:      rules.toPolicyRules(),
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
func buildNamespaceScopedRBAC(asrName, specName, ns string, rules *policyRuleSet) (*rbacv1.Role, *rbacv1.RoleBinding) {
	resourceName := resourceNamePrefix + asrName
	labels := map[string]string{
		pmapis.LabelManagedByKey: pmapis.ManagedByPMController,
		pmapis.LabelASR:          asrName,
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: ns, Labels: labels},
		Rules:      rules.toPolicyRules(),
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

// policyRuleSet accumulates CapabilityRules, unioning verbs across rules that
// share the same (apiGroups, resources) target so each distinct target
// materializes to exactly one PolicyRule.
type policyRuleSet struct {
	verbs     map[ruleKey]map[string]struct{}
	groups    map[ruleKey][]string
	resources map[ruleKey][]string
}

// ruleKey identifies a PolicyRule target by its sorted apiGroups and resources.
type ruleKey struct {
	apiGroups string
	resources string
}

func newPolicyRuleSet() *policyRuleSet {
	return &policyRuleSet{
		verbs:     make(map[ruleKey]map[string]struct{}),
		groups:    make(map[ruleKey][]string),
		resources: make(map[ruleKey][]string),
	}
}

// add merges one rule into the set. Rules with no verbs are ignored, since an
// empty verb list grants nothing.
func (s *policyRuleSet) add(r CapabilityRule) {
	if len(r.Verbs) == 0 {
		return
	}
	groups := sortedUnique(r.APIGroups)
	resources := sortedUnique(r.Resources)
	key := ruleKey{apiGroups: strings.Join(groups, ","), resources: strings.Join(resources, ",")}
	vs, ok := s.verbs[key]
	if !ok {
		vs = make(map[string]struct{})
		s.verbs[key] = vs
		s.groups[key] = groups
		s.resources[key] = resources
	}
	for _, v := range r.Verbs {
		vs[v] = struct{}{}
	}
}

func (s *policyRuleSet) empty() bool { return len(s.verbs) == 0 }

// toPolicyRules materializes the set into deterministically ordered PolicyRules
// (by resources, then apiGroups), with verbs sorted within each rule.
func (s *policyRuleSet) toPolicyRules() []rbacv1.PolicyRule {
	keys := make([]ruleKey, 0, len(s.verbs))
	for k := range s.verbs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].resources != keys[j].resources {
			return keys[i].resources < keys[j].resources
		}
		return keys[i].apiGroups < keys[j].apiGroups
	})
	rules := make([]rbacv1.PolicyRule, 0, len(keys))
	for _, k := range keys {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: s.groups[k],
			Resources: s.resources[k],
			Verbs:     sortedKeys(s.verbs[k]),
		})
	}
	return rules
}

// sortedUnique returns the input sorted with duplicates removed.
func sortedUnique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
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
