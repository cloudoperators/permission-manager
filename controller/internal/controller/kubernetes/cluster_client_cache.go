package kubernetes

import (
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CachedClientEntry holds a cached client along with the Secret resourceVersion
// that was used to create it.
type CachedClientEntry struct {
	Client          client.Client
	ResourceVersion string
}

// ClusterClientCache is a thread-safe cache for cluster clients keyed by
// cluster name and validated against a resource version.
type ClusterClientCache struct {
	mu    sync.RWMutex
	cache map[string]*CachedClientEntry
}

// NewClusterClientCache creates a new empty ClusterClientCache.
func NewClusterClientCache() *ClusterClientCache {
	return &ClusterClientCache{
		cache: make(map[string]*CachedClientEntry),
	}
}

// Get returns the cached client entry for the named cluster, or false if not found.
func (c *ClusterClientCache) Get(name string) (CachedClientEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.cache[name]
	if !ok {
		return CachedClientEntry{}, false
	}
	return *entry, true
}

// Put stores a client in the cache with its associated resource version.
func (c *ClusterClientCache) Put(name string, cl client.Client, resourceVersion string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[name] = &CachedClientEntry{
		Client:          cl,
		ResourceVersion: resourceVersion,
	}
}

// Invalidate removes the cached entry for the named cluster.
func (c *ClusterClientCache) Invalidate(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, name)
}
