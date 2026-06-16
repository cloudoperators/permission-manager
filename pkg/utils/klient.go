// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const greenhouseKubeconfigKey = "greenhousekubeconfig"

// NewRemoteClient builds a controller-runtime client for the named cluster
// by reading its greenhousekubeconfig Secret from the management cluster.
// Additional scheme registration functions can be passed to extend the default
// scheme (clientgoscheme + rbacv1).
func NewRemoteClient(ctx context.Context, c client.Client, clusterName, namespace string, addToScheme ...func(*runtime.Scheme) error) (client.Client, error) {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: namespace}, secret); err != nil {
		return nil, fmt.Errorf("kubeconfig secret: %w", err)
	}

	kubeconfigBytes, ok := secret.Data[greenhouseKubeconfigKey]
	if !ok {
		return nil, fmt.Errorf("secret %q missing %s key", clusterName, greenhouseKubeconfigKey)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, fmt.Errorf("malformed kubeconfig: %w", err)
	}

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = rbacv1.AddToScheme(scheme)
	for _, fn := range addToScheme {
		if err := fn(scheme); err != nil {
			return nil, fmt.Errorf("scheme registration: %w", err)
		}
	}

	return client.New(restConfig, client.Options{Scheme: scheme})
}
