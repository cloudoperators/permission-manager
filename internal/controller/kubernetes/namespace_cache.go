// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"sync"
	"time"
)

// namespaceCacheEntry holds a cached namespace list with a fetch timestamp.
type namespaceCacheEntry struct {
	names     []string
	fetchedAt time.Time
}

// NamespaceCache is a thread-safe TTL-based cache for namespace lists keyed
// by cluster name. It uses an injectable clock for testability.
type NamespaceCache struct {
	mu    sync.RWMutex
	cache map[string]*namespaceCacheEntry
	clock func() time.Time
	ttl   time.Duration
}

// NewNamespaceCache creates a new NamespaceCache with the given clock function.
// The TTL defaults to namespaceCacheTTL (60s).
func NewNamespaceCache(clock func() time.Time) *NamespaceCache {
	return &NamespaceCache{
		cache: make(map[string]*namespaceCacheEntry),
		clock: clock,
		ttl:   namespaceCacheTTL,
	}
}

// Get returns the cached namespace list for the named cluster. Returns false
// on cache miss or if the entry has expired past the TTL.
func (c *NamespaceCache) Get(name string) ([]string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.cache[name]
	if !ok {
		return nil, false
	}
	if c.clock().Sub(entry.fetchedAt) >= c.ttl {
		return nil, false
	}
	return entry.names, true
}

// Put stores a namespace list in the cache, recording the current time
// from the injectable clock.
func (c *NamespaceCache) Put(name string, namespaces []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[name] = &namespaceCacheEntry{
		names:     namespaces,
		fetchedAt: c.clock(),
	}
}

// Invalidate removes the cached entry for the named cluster.
func (c *NamespaceCache) Invalidate(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, name)
}
