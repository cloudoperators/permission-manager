// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// greenhouseKubeconfigKey is the preferred key in the Secret data.
	greenhouseKubeconfigKey = "greenhousekubeconfig"
	// kubeconfigKey is the fallback key in the Secret data.
	kubeconfigKey = "kubeconfig"
)

// ClientBuilderFunc is the signature for building a controller-runtime client
// from a rest.Config. This is injectable for testing.
type ClientBuilderFunc func(cfg *rest.Config, opts client.Options) (client.Client, error)

// namespaceCacheTTL is how long namespace lists are cached per cluster.
const namespaceCacheTTL = 60 * time.Second

// GreenhouseClusterClientFactory builds controller-runtime clients for remote
// clusters by reading kubeconfig Secrets from a management cluster. It composes
// a ClusterClientCache and a NamespaceCache, each with their own mutex, to avoid
// cross-blocking between client and namespace cache operations.
type GreenhouseClusterClientFactory struct {
	// ManagementClient is used to read Secrets from the management cluster.
	ManagementClient client.Client
	// Namespace is the namespace where cluster Secrets live.
	Namespace string
	// ClientBuilder builds a controller-runtime client from a rest.Config.
	// If nil, defaults to client.New with the RBAC scheme.
	ClientBuilder ClientBuilderFunc
	// Now is an injectable clock for testing. Defaults to time.Now.
	// Deprecated: this field is kept for backward compatibility; the clock is
	// forwarded to the NamespaceCache at initialization.
	Now func() time.Time

	clientCache *ClusterClientCache
	nsCache     *NamespaceCache
}

// initCaches lazily initializes the composed caches if they have not been set.
func (f *GreenhouseClusterClientFactory) initCaches() {
	if f.clientCache == nil {
		f.clientCache = NewClusterClientCache()
	}
	if f.nsCache == nil {
		clock := f.Now
		if clock == nil {
			clock = time.Now
		}
		f.nsCache = NewNamespaceCache(clock)
	}
}

// ClientForCluster returns a cached or newly-created client for the named cluster.
// It reads the cluster's kubeconfig Secret, preferring the "greenhousekubeconfig"
// key over "kubeconfig". Returns ClusterUnreachableError on any failure.
func (f *GreenhouseClusterClientFactory) ClientForCluster(ctx context.Context, clusterName string) (ClusterClient, error) {
	f.initCaches()

	// Read the Secret
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: clusterName, Namespace: f.Namespace}
	if err := f.ManagementClient.Get(ctx, key, secret); err != nil {
		return nil, &ClusterUnreachableError{
			ClusterName: clusterName,
			Reason:      fmt.Sprintf("secret not found: %v", err),
		}
	}

	// Check cache - if we have a cached client with the same resourceVersion, return it.
	if entry, ok := f.clientCache.Get(clusterName); ok && entry.ResourceVersion == secret.ResourceVersion {
		return entry.Client, nil
	}

	// Extract kubeconfig bytes - prefer greenhousekubeconfig, fall back to kubeconfig
	kubeconfigBytes, ok := secret.Data[greenhouseKubeconfigKey]
	if !ok {
		kubeconfigBytes, ok = secret.Data[kubeconfigKey]
		if !ok {
			return nil, &ClusterUnreachableError{
				ClusterName: clusterName,
				Reason:      fmt.Sprintf("secret %q has neither %q nor %q key", clusterName, greenhouseKubeconfigKey, kubeconfigKey),
			}
		}
	}

	// Parse kubeconfig into rest.Config
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, &ClusterUnreachableError{
			ClusterName: clusterName,
			Reason:      fmt.Sprintf("malformed kubeconfig: %v", err),
		}
	}

	// Security: reject kubeconfigs with exec credential plugins or auth providers.
	// These could execute arbitrary code in the controller pod if a Secret is compromised.
	if restConfig.ExecProvider != nil {
		log.FromContext(ctx).Info("rejecting kubeconfig with exec credential plugin",
			"cluster", clusterName, "command", restConfig.ExecProvider.Command)
		return nil, &ClusterUnreachableError{
			ClusterName: clusterName,
			Reason:      "kubeconfig uses exec credential plugin (forbidden)",
		}
	}
	if restConfig.AuthProvider != nil {
		log.FromContext(ctx).Info("rejecting kubeconfig with auth provider",
			"cluster", clusterName, "provider", restConfig.AuthProvider.Name)
		return nil, &ClusterUnreachableError{
			ClusterName: clusterName,
			Reason:      "kubeconfig uses auth provider plugin (forbidden)",
		}
	}

	// Build the client
	builder := f.ClientBuilder
	if builder == nil {
		builder = defaultClientBuilder
	}

	scheme := buildRemoteScheme()
	c, err := builder(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, &ClusterUnreachableError{
			ClusterName: clusterName,
			Reason:      fmt.Sprintf("failed to create client: %v", err),
		}
	}

	// Cache the client
	f.clientCache.Put(clusterName, c, secret.ResourceVersion)

	return c, nil
}

// InvalidateCache removes the cached client and namespace list for a cluster,
// forcing the next calls to re-read the Secret and rebuild the client.
func (f *GreenhouseClusterClientFactory) InvalidateCache(clusterName string) {
	f.initCaches()
	f.clientCache.Invalidate(clusterName)
	f.nsCache.Invalidate(clusterName)
}

// InvalidateNamespaceCache removes the cached namespace list for a cluster,
// forcing the next NamespacesForCluster call to re-list. Implements
// NamespaceCacheInvalidator.
func (f *GreenhouseClusterClientFactory) InvalidateNamespaceCache(clusterName string) {
	f.initCaches()
	f.nsCache.Invalidate(clusterName)
}

// NamespacesForCluster lists namespaces on a remote cluster, caching the result
// for namespaceCacheTTL. On cache miss, it calls ClientForCluster then lists
// all Namespaces on the cluster.
func (f *GreenhouseClusterClientFactory) NamespacesForCluster(ctx context.Context, clusterName string) ([]string, error) {
	f.initCaches()

	// Check namespace cache
	if names, ok := f.nsCache.Get(clusterName); ok {
		return names, nil
	}

	// Cache miss: get a client and list namespaces
	clusterClient, err := f.ClientForCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}

	var nsList corev1.NamespaceList
	if err := clusterClient.List(ctx, &nsList); err != nil {
		return nil, fmt.Errorf("failed to list namespaces on cluster %q: %w", clusterName, err)
	}

	names := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		names = append(names, ns.Name)
	}

	// Store in namespace cache
	f.nsCache.Put(clusterName, names)

	return names, nil
}

// buildRemoteScheme creates a scheme suitable for managing RBAC resources on
// remote clusters.
func buildRemoteScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = rbacv1.AddToScheme(scheme)
	return scheme
}

// defaultClientBuilder is the production client builder.
func defaultClientBuilder(cfg *rest.Config, opts client.Options) (client.Client, error) {
	return client.New(cfg, opts)
}
