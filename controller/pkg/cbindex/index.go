// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package cbindex provides a generic, thread-safe in-memory index for ConnectorBindings
// filtered by provider. It eliminates duplication across provider-specific indexer
// implementations (github, greenhouse-teams, kubernetes).
package cbindex

import (
	"sync"

	"github.com/go-logr/logr"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

// ExtractFunc extracts the indexed value T from a ConnectorBinding.
type ExtractFunc[T any] func(cb *pmv1.ConnectorBinding) T

// Index is a generic, thread-safe in-memory index of ConnectorBindings
// filtered by a specific provider. It maps ConnectorBinding metadata.name to
// a value T extracted via the provided ExtractFunc.
type Index[T any] struct {
	mu       sync.RWMutex
	bindings map[string]T
	provider string
	extract  ExtractFunc[T]
	log      logr.Logger
}

// New creates a new Index for the given provider.
// The extract function determines what value is stored for each indexed ConnectorBinding.
func New[T any](provider string, extract ExtractFunc[T], log logr.Logger) *Index[T] {
	return &Index[T]{
		bindings: make(map[string]T),
		provider: provider,
		extract:  extract,
		log:      log.WithName("cbindex-" + provider),
	}
}

// Has returns true if the given ConnectorBinding name is in the index.
func (idx *Index[T]) Has(name string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	_, ok := idx.bindings[name]
	return ok
}

// Get returns the indexed value for a ConnectorBinding name.
// Returns the zero value and false if not found.
func (idx *Index[T]) Get(name string) (T, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	val, ok := idx.bindings[name]
	return val, ok
}

// BindingNames returns the names of all indexed ConnectorBindings.
func (idx *Index[T]) BindingNames() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	names := make([]string, 0, len(idx.bindings))
	for name := range idx.bindings {
		names = append(names, name)
	}
	return names
}

// HasAnyRef checks if any of the given connector ref names are in the index.
func (idx *Index[T]) HasAnyRef(connectorRefs []string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, ref := range connectorRefs {
		if _, ok := idx.bindings[ref]; ok {
			return true
		}
	}
	return false
}

// FindRef returns the first connector ref name that is in the index,
// along with its value. Returns empty string, zero value, and false if none found.
func (idx *Index[T]) FindRef(connectorRefs []string) (string, T, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, ref := range connectorRefs {
		if val, ok := idx.bindings[ref]; ok {
			return ref, val, true
		}
	}
	var zero T
	return "", zero, false
}

// Update adds or updates a ConnectorBinding in the index based on its provider.
// If the CB's provider matches, it is indexed. If not, it is removed (handles provider changes).
func (idx *Index[T]) Update(cb *pmv1.ConnectorBinding) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if cb.Spec.Provider == idx.provider {
		idx.bindings[cb.Name] = idx.extract(cb)
		idx.log.V(1).Info("indexed ConnectorBinding", "name", cb.Name)
	} else {
		// If it was previously indexed but provider changed, remove it.
		delete(idx.bindings, cb.Name)
	}
}

// Remove removes a ConnectorBinding from the index by name.
func (idx *Index[T]) Remove(name string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.bindings, name)
	idx.log.V(1).Info("removed ConnectorBinding from index", "name", name)
}

// Sync rebuilds the index from a full list of ConnectorBindings.
// Only bindings matching the provider are indexed.
func (idx *Index[T]) Sync(bindings []pmv1.ConnectorBinding) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.bindings = make(map[string]T)
	for i := range bindings {
		cb := &bindings[i]
		if cb.Spec.Provider == idx.provider {
			idx.bindings[cb.Name] = idx.extract(cb)
		}
	}
	idx.log.Info("synced ConnectorBinding index", "count", len(idx.bindings))
}

// HandleEvent processes a ConnectorBinding event, distinguishing between
// deletions (DeletionTimestamp set) and create/update events.
func (idx *Index[T]) HandleEvent(cb *pmv1.ConnectorBinding) {
	if cb.DeletionTimestamp != nil {
		idx.Remove(cb.Name)
	} else {
		idx.Update(cb)
	}
}

// Provider returns the provider name this index filters on.
func (idx *Index[T]) Provider() string {
	return idx.provider
}
