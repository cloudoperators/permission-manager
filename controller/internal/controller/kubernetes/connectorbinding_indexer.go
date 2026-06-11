// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package kubernetes implements the Kubernetes provider reconciler for pm-controller.
package kubernetes

import (
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/cbindex"
	"github.com/cloudoperators/permission-manager/controller/pkg/provider"
)

const (
	// ProviderKubernetes is an alias for the shared provider constant.
	ProviderKubernetes = provider.ProviderKubernetes
)

// ConnectorBindingIndexer maintains an in-memory index of ConnectorBindings
// filtered by the kubernetes provider. It wraps cbindex.Index[string] and
// provides backward-compatible convenience methods.
type ConnectorBindingIndexer struct {
	*cbindex.Index[string]
}

// NewConnectorBindingIndexer creates a new ConnectorBindingIndexer backed by
// the generic cbindex.Index.
func NewConnectorBindingIndexer(log logr.Logger) *ConnectorBindingIndexer {
	return &ConnectorBindingIndexer{
		Index: cbindex.New[string](ProviderKubernetes, func(cb *pmv1.ConnectorBinding) string {
			return cb.Spec.Name
		}, log),
	}
}

// IsKubernetesConnector returns true if the given ConnectorBinding name
// corresponds to a Kubernetes provider.
func (idx *ConnectorBindingIndexer) IsKubernetesConnector(name string) bool {
	return idx.Has(name)
}

// KubernetesBindingNames returns the names of all known Kubernetes ConnectorBindings.
func (idx *ConnectorBindingIndexer) KubernetesBindingNames() []string {
	return idx.BindingNames()
}

// HasAnyKubernetesRef checks if any of the given connector ref names correspond
// to a Kubernetes ConnectorBinding.
func (idx *ConnectorBindingIndexer) HasAnyKubernetesRef(connectorRefs []string) bool {
	return idx.HasAnyRef(connectorRefs)
}

// HandleConnectorBindingEvent processes a ConnectorBinding event, distinguishing
// between deletions and create/update events.
func (idx *ConnectorBindingIndexer) HandleConnectorBindingEvent(cb *pmv1.ConnectorBinding) {
	idx.HandleEvent(cb)
}

// EnqueueASRsForConnectorBinding returns an event handler that maintains the
// index and enqueues ASRs referencing the changed ConnectorBinding.
func (idx *ConnectorBindingIndexer) EnqueueASRsForConnectorBinding(c client.Reader) handler.EventHandler {
	return cbindex.EnqueueASRsForConnectorBinding[string](idx.Index, c)
}

// ConnectorBindingStartupSyncer is a manager.Runnable that explicitly syncs
// the ConnectorBindingIndexer at startup by listing all ConnectorBindings.
type ConnectorBindingStartupSyncer = cbindex.StartupSyncer[string]

// NewConnectorBindingStartupSyncer creates a new startup syncer.
func NewConnectorBindingStartupSyncer(idx *ConnectorBindingIndexer, c client.Reader, log logr.Logger) *ConnectorBindingStartupSyncer {
	return cbindex.NewStartupSyncer[string](idx.Index, c, log)
}
