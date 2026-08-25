// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"fmt"
	"sort"
	"strings"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	"k8s.io/client-go/discovery"
)

// expandRuleForCluster turns a single CapabilityRule into the concrete rules it
// grants on one cluster.
//
// A rule without ExcludeResources is passed through unchanged. A deny-driven rule
// (ExcludeResources set; validate guarantees resources: ["*"]) is expanded
// against the cluster's discovered API groups: the requested groups are split
// into "denied" groups (named in ExcludeResources) and "the rest". The rest are
// granted as one wildcard rule (resources: ["*"]) so the ClusterRole only
// references groups that exist on the cluster. Each denied group is instead
// enumerated live and granted its resources minus the excluded ones.
//
// It is fail-closed: any inability to compute the deny-safe grant (missing
// discovery cache, discovery lookup failure) returns an error and NO rules, so a
// denied resource can never leak through a partial expansion.
func expandRuleForCluster(rule CapabilityRule, cluster *greenhousev1alpha1.Cluster, disco discovery.DiscoveryInterface) ([]CapabilityRule, error) {
	if len(rule.ExcludeResources) == 0 {
		return []CapabilityRule{rule}, nil
	}

	if cluster.Status.DiscoveryCache == nil || len(cluster.Status.DiscoveryCache.Groups) == 0 {
		return nil, fmt.Errorf("discovery cache unavailable for cluster %q", cluster.Name)
	}
	allGroups := cluster.Status.DiscoveryCache.Groups

	// Resolve which groups this rule applies to. "*" means every discovered
	// group; an explicit list is intersected with discovery so we never emit a
	// grant for a group the cluster doesn't serve.
	requested := allGroups
	if !wildcard(rule.APIGroups) {
		requested = intersect(rule.APIGroups, allGroups)
	}

	// deniedByGroup maps an API group to the set of resource names to exclude.
	deniedByGroup := make(map[string]map[string]struct{})
	for _, sel := range rule.ExcludeResources {
		for _, g := range sel.APIGroups {
			set := deniedByGroup[g]
			if set == nil {
				set = make(map[string]struct{})
				deniedByGroup[g] = set
			}
			for _, r := range sel.Resources {
				set[r] = struct{}{}
			}
		}
	}

	var rest, denied []string
	for _, g := range requested {
		if _, ok := deniedByGroup[g]; ok {
			denied = append(denied, g)
		} else {
			rest = append(rest, g)
		}
	}

	var out []CapabilityRule
	if len(rest) > 0 {
		out = append(out, CapabilityRule{
			APIGroups: sortedUnique(rest),
			Resources: []string{"*"},
			Verbs:     rule.Verbs,
		})
	}

	if len(denied) == 0 {
		return out, nil
	}

	// Only denied groups need a live resource lookup, and the cache holds group
	// names only, so resolve each group's preferred version first.
	prefGV, err := preferredGroupVersions(disco)
	if err != nil {
		return nil, err
	}
	sort.Strings(denied)
	for _, g := range denied {
		gv := prefGV[g]
		if g == "" {
			gv = "v1" // core group is served at /api/v1
		}
		if gv == "" {
			return nil, fmt.Errorf("no preferred version for API group %q on cluster %q", g, cluster.Name)
		}
		list, err := disco.ServerResourcesForGroupVersion(gv)
		if err != nil {
			return nil, fmt.Errorf("discover resources for %q on cluster %q: %w", gv, cluster.Name, err)
		}
		remaining := make([]string, 0, len(list.APIResources))
		seen := make(map[string]struct{}, len(list.APIResources))
		for _, res := range list.APIResources {
			if isExcluded(res.Name, deniedByGroup[g]) {
				continue
			}
			if _, dup := seen[res.Name]; dup {
				continue
			}
			seen[res.Name] = struct{}{}
			remaining = append(remaining, res.Name)
		}
		if len(remaining) > 0 {
			out = append(out, CapabilityRule{
				APIGroups: []string{g},
				Resources: sortedUnique(remaining),
				Verbs:     rule.Verbs,
			})
		}
	}
	return out, nil
}

// preferredGroupVersions builds a map of API group name to its preferred
// group/version string (e.g. "apps" -> "apps/v1") from a single ServerGroups
// call. The core group appears as "" with GroupVersion "v1".
func preferredGroupVersions(disco discovery.DiscoveryInterface) (map[string]string, error) {
	groups, err := disco.ServerGroups()
	if err != nil {
		return nil, fmt.Errorf("discover server groups: %w", err)
	}
	out := make(map[string]string, len(groups.Groups))
	for _, g := range groups.Groups {
		out[g.Name] = g.PreferredVersion.GroupVersion
	}
	return out, nil
}

// isExcluded reports whether a resource name is denied. A denied name matches the
// resource itself and any of its subresources (e.g. excluding "secrets" also
// drops "secrets/status").
func isExcluded(name string, denied map[string]struct{}) bool {
	// A "*" entry denies every resource in the group. Validation rejects
	// wildcard selectors, so this is a defensive backstop against configs that
	// bypass it (e.g. older persisted state) — treating "*" as exclude-all keeps
	// it fail-closed rather than silently granting access.
	if _, ok := denied["*"]; ok {
		return true
	}
	if _, ok := denied[name]; ok {
		return true
	}
	for e := range denied {
		if strings.HasPrefix(name, e+"/") {
			return true
		}
	}
	return false
}

// intersect returns the elements of want that are present in have, preserving
// have's order.
func intersect(want, have []string) []string {
	wantSet := make(map[string]struct{}, len(want))
	for _, w := range want {
		wantSet[w] = struct{}{}
	}
	var out []string
	for _, h := range have {
		if _, ok := wantSet[h]; ok {
			out = append(out, h)
		}
	}
	return out
}
