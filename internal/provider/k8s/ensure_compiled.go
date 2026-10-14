// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
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
//   - p.unmatchedClusterPatterns — patterns that matched no existing cluster
//   - p.notReadyClusters         — clusters that exist but are not Ready=True
//
// Writes InvalidConfig=True (mirrored on Ready=False) with reason
// UnknownCapability when unmapped capabilities exist, or NoTargetsResolved when
// no usable target resolved at all; clears each reason when it no longer holds.
// Missing/not-ready clusters alongside usable ones are skipped, not fatal.
func (p *Phase) EnsureRbacCompiled(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(ctx context.Context) (lifecycle.Result, error) {
		invalid := stringSet(p.invalidCCRNs)
		unmappedSeen := make(map[string]struct{})
		unmatchedSeen := make(map[string]struct{})
		notReadySeen := make(map[string]struct{})
		degradedSeen := make(map[string]struct{})
		parser := p.CCRNParser
		if parser == nil {
			parser = ccrn.NewParser(nil)
		}

		// per-reconcile discovery caches for deny-driven expansion: a cluster's
		// discovery client is built at most once, and a cluster whose discovery
		// failed is not retried within this reconcile.
		discoByCluster := make(map[string]discovery.DiscoveryInterface)
		discoFailed := make(map[string]struct{})

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

			var rules []CapabilityRule
			if parsed.K8sResource != nil {
				rules = resourceRulesFor(parsed.K8sResource, perm.Capability)
				if len(rules) == 0 {
					continue
				}
			} else {
				r, found := UnionRulesForCapability(p.cbs, perm.Capability)
				if !found {
					capability := perm.Capability
					if _, seen := unmappedSeen[capability]; !seen {
						unmappedSeen[capability] = struct{}{}
						p.unmappedCaps = append(p.unmappedCaps, capability)
					}
					continue
				}
				rules = r
			}

			matches := matchClusterPattern(p.ClusterStore, parsed.ClusterName, asr.Namespace)
			for _, name := range matches.notReady {
				if _, seen := notReadySeen[name]; !seen {
					notReadySeen[name] = struct{}{}
					p.notReadyClusters = append(p.notReadyClusters, name)
				}
			}
			if len(matches.ready) == 0 {
				// A pattern that matched no existing cluster at all is unmatched;
				// one that matched only not-ready clusters is already recorded above.
				if len(matches.notReady) == 0 {
					if _, seen := unmatchedSeen[parsed.ClusterName]; !seen {
						unmatchedSeen[parsed.ClusterName] = struct{}{}
						p.unmatchedClusterPatterns = append(p.unmatchedClusterPatterns, parsed.ClusterName)
					}
				}
				continue
			}

			for _, clusterName := range matches.ready {
				effectiveRules := rules
				if rulesNeedExpansion(rules) {
					expanded, err := p.expandRulesForCluster(ctx, rules, clusterName, asr.Namespace, discoByCluster, discoFailed)
					if err != nil {
						p.Log.Error(err, "deny-driven expansion failed, skipping cluster",
							"namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
						if _, seen := degradedSeen[clusterName]; !seen {
							degradedSeen[clusterName] = struct{}{}
							p.discoveryDegraded = append(p.discoveryDegraded, clusterName)
						}
						continue
					}
					effectiveRules = expanded
				}

				s := ensureScope(clusterName)
				if parsed.Namespace == "" {
					for _, r := range effectiveRules {
						s.clusterRules.add(r)
					}
				} else {
					nsSet, ok := s.namespaceRules[parsed.Namespace]
					if !ok {
						nsSet = newPolicyRuleSet()
						s.namespaceRules[parsed.Namespace] = nsSet
					}
					for _, r := range effectiveRules {
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

		// Invalid config only when nothing usable resolved; otherwise skips are
		// a partial result, not a hard error.
		if len(p.compiled) == 0 && (len(p.unmatchedClusterPatterns) > 0 || len(p.notReadyClusters) > 0) {
			msg := "no Ready target clusters resolved: " + skippedSummary(p.unmatchedClusterPatterns, p.notReadyClusters)
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

		if len(p.discoveryDegraded) > 0 {
			msg := fmt.Sprintf("discovery unavailable for cluster(s), deny rules cannot be safely expanded: %v", p.discoveryDegraded)
			asr.SetCondition(pmmeta.TrueCondition(
				pmmeta.InvalidConfigCondition,
				pmmeta.DiscoveryUnavailableReason,
				msg,
			))
			asr.SetCondition(pmmeta.FalseCondition(
				pmmeta.ReadyCondition,
				pmmeta.DiscoveryUnavailableReason,
				msg,
			))
		} else {
			asr.ClearInvalidConfigIfReason(pmmeta.DiscoveryUnavailableReason)
		}

		return lifecycle.Continue(), nil
	}
}

// resourceVerbsFor returns the hardcoded verb set for a resource-scoped CCRN
// capability. The capabilityMapping is intentionally not consulted. WRITE
// includes deletecollection for parity with the built-in edit/admin roles.
func resourceVerbsFor(capability string) []string {
	switch strings.ToUpper(capability) {
	case "READ":
		return []string{"get", "list", "watch"}
	case "WRITE":
		return []string{"create", "update", "patch", "delete", "deletecollection"}
	default:
		return nil
	}
}

// resourceRulesFor builds the single CapabilityRule a resource CCRN grants: its
// apiGroup/resource[/subresource] target with the hardcoded verb set. Returns
// nil for an unknown capability (the validator rejects those as InvalidCCRN).
func resourceRulesFor(res *ccrn.K8sResource, capability string) []CapabilityRule {
	verbs := resourceVerbsFor(capability)
	if len(verbs) == 0 {
		return nil
	}
	resource := res.Resource
	if res.SubResource != "" {
		resource = res.Resource + "/" + res.SubResource
	}
	return []CapabilityRule{{
		APIGroups: []string{res.APIGroup},
		Resources: []string{resource},
		Verbs:     verbs,
	}}
}

// rulesNeedExpansion reports whether any rule is deny-driven (has
// ExcludeResources) and therefore requires per-cluster discovery expansion.
func rulesNeedExpansion(rules []CapabilityRule) bool {
	for _, r := range rules {
		if len(r.ExcludeResources) > 0 {
			return true
		}
	}
	return false
}

// expandRulesForCluster resolves the cluster and its discovery client, then
// expands every rule for that cluster. Returns an error (fail-closed) if the
// cluster is missing from the store or discovery is unavailable.
func (p *Phase) expandRulesForCluster(ctx context.Context, rules []CapabilityRule, clusterName, namespace string, discoByCluster map[string]discovery.DiscoveryInterface, discoFailed map[string]struct{}) ([]CapabilityRule, error) {
	cluster, ok := getClusterFromStore(p.ClusterStore, namespace, clusterName)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found in store", clusterName)
	}
	disco, err := p.discoveryFor(ctx, clusterName, namespace, discoByCluster, discoFailed)
	if err != nil {
		return nil, err
	}
	var out []CapabilityRule
	for _, r := range rules {
		expanded, err := expandRuleForCluster(r, cluster, disco)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// discoveryFor returns a discovery client for the cluster, building it at most
// once per reconcile and short-circuiting clusters whose build already failed.
func (p *Phase) discoveryFor(ctx context.Context, clusterName, namespace string, discoByCluster map[string]discovery.DiscoveryInterface, discoFailed map[string]struct{}) (discovery.DiscoveryInterface, error) {
	if _, bad := discoFailed[clusterName]; bad {
		return nil, fmt.Errorf("discovery previously failed for cluster %q", clusterName)
	}
	if d, ok := discoByCluster[clusterName]; ok {
		return d, nil
	}
	if p.RemoteDiscovery == nil {
		discoFailed[clusterName] = struct{}{}
		return nil, errors.New("no discovery client factory configured")
	}
	d, err := p.RemoteDiscovery(ctx, p.Client, clusterName, namespace)
	if err != nil {
		discoFailed[clusterName] = struct{}{}
		return nil, fmt.Errorf("build discovery client for cluster %q: %w", clusterName, err)
	}
	discoByCluster[clusterName] = d
	return d, nil
}

// getClusterFromStore looks up a greenhouse Cluster object by "namespace/name".
func getClusterFromStore(store cache.Store, namespace, name string) (*greenhousev1alpha1.Cluster, bool) {
	obj, exists, err := store.GetByKey(namespace + "/" + name)
	if err != nil || !exists {
		return nil, false
	}
	cluster, ok := obj.(*greenhousev1alpha1.Cluster)
	return cluster, ok
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

// clusterMatches partitions resolved clusters by Ready condition: ready are
// usable targets, notReady exist but must be skipped (not torn down).
type clusterMatches struct {
	ready    []string
	notReady []string
}

// matchClusterPattern resolves a pattern against the ClusterStore, partitioning
// by Ready condition. Exact names use the "namespace/name" key; wildcards
// glob-match. Both slices empty means the pattern matched no existing cluster.
func matchClusterPattern(store cache.Store, pattern, namespace string) clusterMatches {
	var res clusterMatches
	if !ccrn.IsWildcard(pattern) {
		cluster, ok := getClusterFromStore(store, namespace, pattern)
		if !ok {
			return res
		}
		if isClusterReady(cluster) {
			res.ready = append(res.ready, pattern)
		} else {
			res.notReady = append(res.notReady, pattern)
		}
		return res
	}

	for _, obj := range store.List() {
		cluster, ok := obj.(*greenhousev1alpha1.Cluster)
		if !ok {
			continue
		}
		if cluster.Namespace != namespace {
			continue
		}
		if !ccrn.MatchGlob(pattern, cluster.Name) {
			continue
		}
		if isClusterReady(cluster) {
			res.ready = append(res.ready, cluster.Name)
		} else {
			res.notReady = append(res.notReady, cluster.Name)
		}
	}
	sort.Strings(res.ready)
	sort.Strings(res.notReady)
	return res
}

// isClusterReady reports whether the Cluster's Ready condition is True.
func isClusterReady(cluster *greenhousev1alpha1.Cluster) bool {
	return cluster.Status.IsReadyTrue()
}

// skippedSummary renders skipped clusters for status messages; empty categories
// are omitted.
func skippedSummary(unmatched, notReady []string) string {
	parts := make([]string, 0, 2)
	if len(unmatched) > 0 {
		parts = append(parts, fmt.Sprintf("missing cluster(s): %v", unmatched))
	}
	if len(notReady) > 0 {
		parts = append(parts, fmt.Sprintf("not-ready cluster(s): %v", notReady))
	}
	return strings.Join(parts, "; ")
}

// stringSet builds a set from a slice.
func stringSet(ss []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		out[s] = struct{}{}
	}
	return out
}
