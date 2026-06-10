package kubernetes

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NamespaceLister lists namespaces on a remote cluster. Implementations
// should cache results internally.
type NamespaceLister interface {
	NamespacesForCluster(ctx context.Context, clusterName string) ([]string, error)
}

// NamespaceCacheInvalidator is an optional interface that NamespaceLister
// implementations may satisfy to allow cache invalidation on demand. The
// reconciler uses this to flush stale namespace lists when a force-reconcile
// annotation is detected.
type NamespaceCacheInvalidator interface {
	InvalidateNamespaceCache(clusterName string)
}

// ClusterClient wraps the controller-runtime client.Client interface for
// operations on a remote cluster. This abstraction allows swapping in fake
// clients for testing while using real per-cluster clients in production.
type ClusterClient interface {
	client.Client
}

// ClusterClientFactory creates a ClusterClient for a given cluster name.
// Returns an error if the cluster is unreachable.
type ClusterClientFactory interface {
	// ClientForCluster returns a client for the named cluster.
	// Returns an error if the cluster is unreachable or the client cannot be created.
	ClientForCluster(ctx context.Context, clusterName string) (ClusterClient, error)
}

// ClusterUnreachableError indicates that a cluster could not be reached.
type ClusterUnreachableError struct {
	ClusterName string
	Reason      string
}

func (e *ClusterUnreachableError) Error() string {
	msg := "cluster unreachable: " + e.ClusterName
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg
}
