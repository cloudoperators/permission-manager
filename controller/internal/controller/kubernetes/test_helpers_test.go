// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

// failingClient is a test helper that returns a configured error for all
// mutating operations. Used to simulate cluster-level failures.
type failingClient struct {
	client.Client
	err error
}

func (f *failingClient) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return f.err
}

func (f *failingClient) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return f.err
}

func (f *failingClient) Create(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
	return f.err
}

func (f *failingClient) Update(_ context.Context, _ client.Object, _ ...client.UpdateOption) error {
	return f.err
}

func (f *failingClient) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
	return f.err
}

func (f *failingClient) Delete(_ context.Context, _ client.Object, _ ...client.DeleteOption) error {
	return f.err
}

func (f *failingClient) DeleteAllOf(_ context.Context, _ client.Object, _ ...client.DeleteAllOfOption) error {
	return f.err
}

func (f *failingClient) Scheme() *runtime.Scheme {
	return runtime.NewScheme()
}

func (f *failingClient) RESTMapper() meta.RESTMapper {
	return nil
}

func (f *failingClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}

func (f *failingClient) IsObjectNamespaced(_ runtime.Object) (bool, error) {
	return false, nil
}

func (f *failingClient) Status() client.SubResourceWriter {
	return &failingSubResourceWriter{err: f.err}
}

func (f *failingClient) SubResource(_ string) client.SubResourceClient {
	return nil
}

type failingSubResourceWriter struct {
	err error
}

func (w *failingSubResourceWriter) Create(_ context.Context, _ client.Object, _ client.Object, _ ...client.SubResourceCreateOption) error {
	return w.err
}

func (w *failingSubResourceWriter) Update(_ context.Context, _ client.Object, _ ...client.SubResourceUpdateOption) error {
	return w.err
}

func (w *failingSubResourceWriter) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
	return w.err
}

// StaticNamespaceLister is a test helper that returns pre-configured namespace lists.
type StaticNamespaceLister struct {
	Namespaces map[string][]string // clusterName -> namespace list
	Err        map[string]error    // optional per-cluster errors
}

// NamespacesForCluster returns the pre-configured namespace list for the given cluster.
func (l *StaticNamespaceLister) NamespacesForCluster(_ context.Context, clusterName string) ([]string, error) {
	if l.Err != nil {
		if err, ok := l.Err[clusterName]; ok {
			return nil, err
		}
	}
	if l.Namespaces == nil {
		return nil, nil
	}
	return l.Namespaces[clusterName], nil
}

// StaticClusterClientFactory is a test helper that returns pre-configured clients
// for known clusters. Unknown clusters return an error.
type StaticClusterClientFactory struct {
	Clients map[string]ClusterClient
}

// ClientForCluster returns the pre-configured client for the given cluster.
func (f *StaticClusterClientFactory) ClientForCluster(_ context.Context, clusterName string) (ClusterClient, error) {
	c, ok := f.Clients[clusterName]
	if !ok {
		return nil, &ClusterUnreachableError{ClusterName: clusterName}
	}
	return c, nil
}

// wrapWithSSAInterceptor wraps a fake client with an interceptor that converts
// SSA Apply patches on the status subresource to MergePatch-like updates.
// This is needed because the controller-runtime fake client does not support
// Apply patches (SSA). The interceptor simulates SSA behavior for testing.
func wrapWithSSAInterceptor(underlying client.WithWatch) client.Client {
	funcs := interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if patch.Type() == types.ApplyPatchType {
				// Convert the unstructured Apply patch to a regular status update
				u, ok := obj.(*unstructured.Unstructured)
				if !ok {
					// Fall through to underlying for non-unstructured objects
					return underlying.Status().Patch(ctx, obj, patch, opts...)
				}

				// Get the providers from the patch
				statusMap, _, _ := unstructured.NestedMap(u.Object, "status", "providers")
				if statusMap == nil {
					return nil
				}

				// Get the existing ASR
				nn := types.NamespacedName{Name: u.GetName(), Namespace: u.GetNamespace()}
				existing := &pmv1.ApplicationSpecificRole{}
				if err := underlying.Get(ctx, nn, existing); err != nil {
					return err
				}

				// Merge providers
				if existing.Status.Providers == nil {
					existing.Status.Providers = make(map[string]pmv1.ProviderStatus)
				}

				for providerKey, rawValue := range statusMap {
					providerMap, ok := rawValue.(map[string]interface{})
					if !ok {
						continue
					}

					ps := pmv1.ProviderStatus{}
					if ready, ok := providerMap["ready"].(bool); ok {
						ps.Ready = ready
					}
					if msg, ok := providerMap["message"].(string); ok {
						ps.Message = msg
					}
					if lr, ok := providerMap["lastReconciled"].(string); ok {
						parsed, _ := time.Parse(metav1.RFC3339Micro, lr)
						ps.LastReconciled = metav1.NewTime(parsed)
					}
					if targets, ok := providerMap["targets"].([]interface{}); ok {
						for _, rawTarget := range targets {
							targetMap, ok := rawTarget.(map[string]interface{})
							if !ok {
								continue
							}
							ts := pmv1.TargetStatus{}
							if id, ok := targetMap["targetID"].(string); ok {
								ts.TargetID = id
							}
							if s, ok := targetMap["status"].(string); ok {
								ts.Status = s
							}
							if m, ok := targetMap["message"].(string); ok {
								ts.Message = m
							}
							if lu, ok := targetMap["lastUpdated"].(string); ok {
								parsed, _ := time.Parse(metav1.RFC3339Micro, lu)
								ts.LastUpdated = metav1.NewTime(parsed)
							}
							ps.Targets = append(ps.Targets, ts)
						}
					}
					existing.Status.Providers[providerKey] = ps
				}

				return underlying.Status().Update(ctx, existing)
			}

			// For non-Apply patches (like MergePatch for removal), pass through
			return underlying.Status().Patch(ctx, obj, patch, opts...)
		},
	}

	return interceptor.NewClient(underlying, funcs)
}
