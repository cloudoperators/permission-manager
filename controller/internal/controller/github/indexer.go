// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/cbindex"
)

// GithubIndexer maintains an in-memory index of ConnectorBindings
// filtered by the "github" provider. It wraps cbindex.Index[ConnectorBindingSpec]
// and provides GitHub-specific convenience methods like FindGithubRef and GetBinding.
type GithubIndexer struct {
	*cbindex.Index[pmv1.ConnectorBindingSpec]
}

// NewGithubIndexer creates a new GithubIndexer backed by the generic cbindex.Index.
func NewGithubIndexer(log logr.Logger) *GithubIndexer {
	return &GithubIndexer{
		Index: cbindex.New[pmv1.ConnectorBindingSpec](ProviderGithub, func(cb *pmv1.ConnectorBinding) pmv1.ConnectorBindingSpec {
			return cb.Spec
		}, log),
	}
}

// IsGithubConnector returns true if the given ConnectorBinding name
// corresponds to the GitHub provider.
func (idx *GithubIndexer) IsGithubConnector(name string) bool {
	return idx.Has(name)
}

// GithubBindingNames returns the names of all known GitHub ConnectorBindings.
func (idx *GithubIndexer) GithubBindingNames() []string {
	return idx.BindingNames()
}

// GetBinding returns the ConnectorBindingSpec for a github binding by name.
// Returns false if not found.
func (idx *GithubIndexer) GetBinding(name string) (pmv1.ConnectorBindingSpec, bool) {
	return idx.Get(name)
}

// HasAnyGithubRef checks if any of the given connector ref names correspond
// to a GitHub ConnectorBinding.
func (idx *GithubIndexer) HasAnyGithubRef(connectorRefs []string) bool {
	return idx.HasAnyRef(connectorRefs)
}

// FindGithubRef returns the first connector ref name that corresponds to a GitHub
// ConnectorBinding, along with its spec. Returns empty string and false if none found.
func (idx *GithubIndexer) FindGithubRef(connectorRefs []string) (string, pmv1.ConnectorBindingSpec, bool) {
	return idx.FindRef(connectorRefs)
}

// EnqueueASRsForConnectorBinding returns an event handler that maintains the
// index and enqueues ASRs referencing the changed ConnectorBinding.
func (idx *GithubIndexer) EnqueueASRsForConnectorBinding(c client.Reader) handler.EventHandler {
	return cbindex.EnqueueASRsForConnectorBinding[pmv1.ConnectorBindingSpec](idx.Index, c)
}
