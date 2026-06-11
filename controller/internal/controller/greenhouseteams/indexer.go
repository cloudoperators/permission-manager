// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package greenhouseteams implements the greenhouse-teams provider controller.
package greenhouseteams

import (
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/cbindex"
	"github.com/cloudoperators/permission-manager/controller/pkg/provider"
)

const (
	// ProviderGreenhouseTeams is the provider name for greenhouse-teams ConnectorBindings.
	ProviderGreenhouseTeams = provider.ProviderGreenhouseTeams
)

// ConnectorBindingIndexer maintains an in-memory index of ConnectorBindings
// filtered by the greenhouse-teams provider. It wraps cbindex.Index[string]
// and provides backward-compatible convenience methods.
type ConnectorBindingIndexer struct {
	*cbindex.Index[string]
}

// NewConnectorBindingIndexer creates a new ConnectorBindingIndexer backed by
// the generic cbindex.Index.
func NewConnectorBindingIndexer(log logr.Logger) *ConnectorBindingIndexer {
	return &ConnectorBindingIndexer{
		Index: cbindex.New[string](ProviderGreenhouseTeams, func(cb *pmv1.ConnectorBinding) string {
			return cb.Spec.Name
		}, log),
	}
}

// IsProviderConnector returns true if the given ConnectorBinding name
// corresponds to a greenhouse-teams provider.
func (idx *ConnectorBindingIndexer) IsProviderConnector(name string) bool {
	return idx.Has(name)
}

// BindingNames returns the names of all known greenhouse-teams ConnectorBindings.
func (idx *ConnectorBindingIndexer) BindingNames() []string {
	return idx.Index.BindingNames()
}

// HasAnyProviderRef checks if any of the given connector ref names correspond
// to a greenhouse-teams ConnectorBinding.
func (idx *ConnectorBindingIndexer) HasAnyProviderRef(connectorRefs []string) bool {
	return idx.HasAnyRef(connectorRefs)
}

// HandleConnectorBindingEvent processes a ConnectorBinding event by updating the index.
// Returns true if the binding was added/updated as a greenhouse-teams connector.
func (idx *ConnectorBindingIndexer) HandleConnectorBindingEvent(cb *pmv1.ConnectorBinding) bool {
	idx.Update(cb)
	return idx.IsProviderConnector(cb.Name)
}

// EnqueueASRsForConnectorBinding returns an event handler that maintains the
// index and enqueues ASRs referencing the changed ConnectorBinding.
func (idx *ConnectorBindingIndexer) EnqueueASRsForConnectorBinding(c client.Reader) handler.EventHandler {
	return cbindex.EnqueueASRsForConnectorBinding[string](idx.Index, c)
}

// ConnectorBindingStartupSyncer is a manager.Runnable that explicitly syncs
// the ConnectorBinding index at startup by listing all ConnectorBindings.
type ConnectorBindingStartupSyncer = cbindex.StartupSyncer[string]

// NewConnectorBindingStartupSyncer creates a new startup syncer for the
// greenhouse-teams ConnectorBinding index.
func NewConnectorBindingStartupSyncer(idx *ConnectorBindingIndexer, c client.Reader, log logr.Logger) *ConnectorBindingStartupSyncer {
	return cbindex.NewStartupSyncer[string](idx.Index, c, log)
}
