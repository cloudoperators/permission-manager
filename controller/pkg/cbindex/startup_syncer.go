package cbindex

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

// StartupSyncer is a manager.Runnable that explicitly syncs a ConnectorBinding
// index at startup by listing all ConnectorBindings from the API server.
//
// This makes the startup contract explicit: rather than relying solely on
// controller-runtime's Add events to seed the index (which depends on event
// ordering), the syncer lists all existing ConnectorBindings and rebuilds
// the index before any reconciliation occurs.
//
// Usage: add to the manager via mgr.Add(&StartupSyncer{...})
// — it runs once when the manager starts (after cache sync).
type StartupSyncer[T any] struct {
	Index  *Index[T]
	Client client.Reader
	Log    logr.Logger
}

// NewStartupSyncer creates a new StartupSyncer for the given index.
func NewStartupSyncer[T any](idx *Index[T], c client.Reader, log logr.Logger) *StartupSyncer[T] {
	return &StartupSyncer[T]{
		Index:  idx,
		Client: c,
		Log:    log.WithName("cb-startup-syncer-" + idx.Provider()),
	}
}

// Start lists all ConnectorBindings and syncs the index.
// It satisfies the manager.Runnable interface.
func (s *StartupSyncer[T]) Start(ctx context.Context) error {
	var cbList pmv1.ConnectorBindingList
	if err := s.Client.List(ctx, &cbList); err != nil {
		return fmt.Errorf("failed to list ConnectorBindings for startup sync: %w", err)
	}

	s.Index.Sync(cbList.Items)
	s.Log.Info("completed ConnectorBinding startup sync", "count", len(cbList.Items))
	return nil
}
