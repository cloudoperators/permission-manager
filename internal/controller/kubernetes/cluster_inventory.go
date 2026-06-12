// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"sync"

	"github.com/go-logr/logr"

	"github.com/cloudoperators/permission-manager/pkg/ccrn"
)

// ClusterInventory maintains a map of known Greenhouse clusters.
// It provides thread-safe lookups and glob-based pattern matching for
// determining which clusters match an ASR's CCRN patterns.
type ClusterInventory struct {
	mu       sync.RWMutex
	clusters map[string]struct{}
	log      logr.Logger
}

// NewClusterInventory creates a new ClusterInventory.
func NewClusterInventory(log logr.Logger) *ClusterInventory {
	return &ClusterInventory{
		clusters: make(map[string]struct{}),
		log:      log.WithName("cluster-inventory"),
	}
}

// Update adds or updates a cluster in the inventory.
func (inv *ClusterInventory) Update(name string) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.clusters[name] = struct{}{}
	inv.log.V(1).Info("updated cluster in inventory", "cluster", name)
}

// Remove removes a cluster from the inventory.
func (inv *ClusterInventory) Remove(name string) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	delete(inv.clusters, name)
	inv.log.V(1).Info("removed cluster from inventory", "cluster", name)
}

// Has returns true if the cluster is known in the inventory.
func (inv *ClusterInventory) Has(name string) bool {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	_, ok := inv.clusters[name]
	return ok
}

// Names returns all known cluster names.
func (inv *ClusterInventory) Names() []string {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	names := make([]string, 0, len(inv.clusters))
	for name := range inv.clusters {
		names = append(names, name)
	}
	return names
}

// MatchingNames returns cluster names that match the given pattern.
// For exact names (no wildcard), it fast-paths via Has(). For a bare "*",
// it returns all cluster names. For prefix globs (e.g. "nw-a-*"), it iterates
// and matches each cluster name.
func (inv *ClusterInventory) MatchingNames(pattern string) []string {
	// Fast path: exact match (no wildcard)
	if !ccrn.IsWildcard(pattern) {
		if inv.Has(pattern) {
			return []string{pattern}
		}
		return nil
	}
	// Bare wildcard: all clusters
	if pattern == "*" {
		return inv.Names()
	}
	// Prefix glob: iterate
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	var matches []string
	for name := range inv.clusters {
		if ccrn.MatchGlob(pattern, name) {
			matches = append(matches, name)
		}
	}
	return matches
}
