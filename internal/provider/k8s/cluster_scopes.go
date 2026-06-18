// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"strings"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
)

// capabilityVerbs maps ASR permission capabilities to Kubernetes RBAC verbs.
var capabilityVerbs = map[string][]string{
	"READ":    {"get", "list", "watch"},
	"WRITE":   {"get", "list", "watch", "create", "update", "patch", "delete"},
	"OPERATE": {"exec", "port-forward", "proxy"},
	"SECURE":  {"bind", "escalate", "impersonate"},
	"*":       {"*"},
}

// ClusterScope holds the resolved RBAC verb sets for a single cluster.
type ClusterScope struct {
	// ClusterVerbs are verbs for cluster-wide RBAC.
	ClusterVerbs map[string]struct{}
	// NamespaceVerbs maps namespace name to its verb set.
	NamespaceVerbs map[string]map[string]struct{}
}

// parsedPerm is the result of parsing a single ASR permission entry.
type parsedPerm struct {
	clusterPattern string
	namespace      string // empty for cluster-scoped
	verbs          []string
}

// buildClusterScopes parses permissions, matches cluster patterns against the
// store, and returns a map of cluster name → ClusterScope plus any unmapped capabilities.
func (p *Phase) buildClusterScopes(asr *v1alpha1.ApplicationSpecificRole) (scopes map[string]*ClusterScope, unmappedCaps []string) {
	perms, unmappedCaps := parsePermissions(asr)
	return buildScopes(p.matchClusters(perms, asr.Namespace)), unmappedCaps
}

// parsePermissions iterates asr.Spec.Permissions, parses each CCRN, maps
// capabilities to verbs, and returns only kubernetes entries.
// Unmapped capabilities are collected and returned separately.
func parsePermissions(asr *v1alpha1.ApplicationSpecificRole) (perms []parsedPerm, unmappedCaps []string) {
	unmappedSeen := make(map[string]struct{})

	for _, p := range asr.Spec.Permissions {
		parsed, err := ccrn.Parse(p.Resource)
		if err != nil || !parsed.IsKubernetes() {
			continue
		}

		verbs, ok := capabilityVerbs[strings.ToUpper(p.Capability)]
		if !ok {
			capability := strings.ToUpper(p.Capability)
			if _, seen := unmappedSeen[capability]; !seen {
				unmappedSeen[capability] = struct{}{}
				unmappedCaps = append(unmappedCaps, capability)
			}
			continue
		}

		perms = append(perms, parsedPerm{
			clusterPattern: parsed.ClusterName,
			namespace:      parsed.Namespace,
			verbs:          verbs,
		})
	}

	return perms, unmappedCaps
}

// matchClusters resolves each parsedPerm's cluster pattern against the
// ClusterStore and returns one entry per matched cluster name.
func (p *Phase) matchClusters(perms []parsedPerm, asrNamespace string) []parsedPerm {
	var resolved []parsedPerm

	for _, perm := range perms {
		for _, name := range p.matchPattern(perm.clusterPattern, asrNamespace) {
			resolved = append(resolved, parsedPerm{
				clusterPattern: name,
				namespace:      perm.namespace,
				verbs:          perm.verbs,
			})
		}
	}

	return resolved
}

// matchPattern returns cluster names from the store that match the given pattern.
// Cluster objects are keyed as "namespace/name" in the informer store.
func (p *Phase) matchPattern(pattern, namespace string) []string {
	if !ccrn.IsWildcard(pattern) {
		if _, exists, err := p.ClusterStore.GetByKey(namespace + "/" + pattern); err == nil && exists {
			return []string{pattern}
		}
		return nil
	}

	var matches []string
	for _, obj := range p.ClusterStore.List() {
		cluster, ok := obj.(*greenhousev1alpha1.Cluster)
		if !ok {
			continue
		}
		if ccrn.MatchGlob(pattern, cluster.Name) {
			matches = append(matches, cluster.Name)
		}
	}
	return matches
}

// buildScopes groups resolved permissions by cluster name into ClusterScope.
func buildScopes(perms []parsedPerm) map[string]*ClusterScope {
	scopes := make(map[string]*ClusterScope)

	for _, perm := range perms {
		scope, exists := scopes[perm.clusterPattern]
		if !exists {
			scope = &ClusterScope{
				ClusterVerbs:   make(map[string]struct{}),
				NamespaceVerbs: make(map[string]map[string]struct{}),
			}
			scopes[perm.clusterPattern] = scope
		}

		if perm.namespace == "" {
			for _, v := range perm.verbs {
				scope.ClusterVerbs[v] = struct{}{}
			}
		} else {
			if _, ok := scope.NamespaceVerbs[perm.namespace]; !ok {
				scope.NamespaceVerbs[perm.namespace] = make(map[string]struct{})
			}
			for _, v := range perm.verbs {
				scope.NamespaceVerbs[perm.namespace][v] = struct{}{}
			}
		}
	}

	return scopes
}

// expandNamespaceScopes resolves all namespace keys in scope.NamespaceVerbs:
//   - cluster-wide ("*") → promoted to ClusterVerbs
//   - wildcard patterns  → expanded against namespaces on the remote cluster
//   - exact names        → passed through unchanged
func expandNamespaceScopes(ctx context.Context, c client.Client, scope *ClusterScope) error {
	expandClusterWideNamespace(scope)

	if !hasWildcardNamespaces(scope) {
		return nil
	}

	namespaces, err := listRemoteNamespaces(ctx, c)
	if err != nil {
		return err
	}

	return expandWildcardNamespaces(scope, namespaces)
}

// expandClusterWideNamespace promotes the special "*" namespace key's verbs
// into ClusterVerbs and removes the key — granting cluster-wide access.
func expandClusterWideNamespace(scope *ClusterScope) {
	verbs, ok := scope.NamespaceVerbs["*"]
	if !ok {
		return
	}
	for v := range verbs {
		scope.ClusterVerbs[v] = struct{}{}
	}
	delete(scope.NamespaceVerbs, "*")
}

// hasWildcardNamespaces returns true if any namespace key in the scope is a wildcard pattern.
func hasWildcardNamespaces(scope *ClusterScope) bool {
	for ns := range scope.NamespaceVerbs {
		if ccrn.IsWildcard(ns) {
			return true
		}
	}
	return false
}

// listRemoteNamespaces lists all namespaces on the remote cluster.
func listRemoteNamespaces(ctx context.Context, c client.Client) ([]string, error) {
	var nsList corev1.NamespaceList
	if err := c.List(ctx, &nsList); err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	names := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		names = append(names, ns.Name)
	}
	return names, nil
}

// expandWildcardNamespaces replaces wildcard namespace keys in scope.NamespaceVerbs
// with the concrete namespace names they match. Exact keys are left untouched.
func expandWildcardNamespaces(scope *ClusterScope, namespaces []string) error {
	var wildcardKeys []string
	for ns := range scope.NamespaceVerbs {
		if ccrn.IsWildcard(ns) {
			wildcardKeys = append(wildcardKeys, ns)
		}
	}

	for _, ns := range wildcardKeys {
		verbs := scope.NamespaceVerbs[ns]
		delete(scope.NamespaceVerbs, ns)
		for _, realNS := range namespaces {
			if !ccrn.MatchGlob(ns, realNS) {
				continue
			}
			if _, ok := scope.NamespaceVerbs[realNS]; !ok {
				scope.NamespaceVerbs[realNS] = make(map[string]struct{})
			}
			for v := range verbs {
				scope.NamespaceVerbs[realNS][v] = struct{}{}
			}
		}
	}
	return nil
}
