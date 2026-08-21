// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"context"
	"fmt"
	"os"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	greenhouseKubeconfigKey = "greenhousekubeconfig"

	// apiServerOverrideAnnotation lets a developer running the controller
	// outside the kind docker network point the remote client at a host-
	// reachable address (e.g. https://127.0.0.1:53097) while keeping the
	// kubeconfig's CA + auth intact. Honored only when the controller is
	// started with DEV_API_SERVER_OVERRIDE set to a truthy value; production
	// deployments must not set the env var, so a stray annotation on a Secret
	// cannot redirect credentials to an attacker-controlled endpoint.
	apiServerOverrideAnnotation = "greenhouse.cloudoperators.dev/api-server-override"
	apiServerOverrideEnv        = "DEV_API_SERVER_OVERRIDE"
)

// restConfigForCluster reads the named cluster's greenhousekubeconfig Secret from
// the management cluster and returns a *rest.Config for it. Shared by
// NewRemoteClient and NewRemoteDiscoveryClient so both reach the remote cluster
// through the same credential path and dev host override.
func restConfigForCluster(ctx context.Context, c client.Client, clusterName, namespace string) (*rest.Config, error) {
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

	if allow, err := strconv.ParseBool(os.Getenv(apiServerOverrideEnv)); err == nil && allow {
		if override := secret.Annotations[apiServerOverrideAnnotation]; override != "" {
			ctrl.LoggerFrom(ctx).Info("api server override active",
				"cluster", clusterName, "from", restConfig.Host, "to", override)
			restConfig.Host = override
		}
	}

	return restConfig, nil
}

// NewRemoteClient builds a controller-runtime client for the named cluster
// by reading its greenhousekubeconfig Secret from the management cluster.
// Additional scheme registration functions can be passed to extend the default
// scheme (clientgoscheme + rbacv1).
func NewRemoteClient(ctx context.Context, c client.Client, clusterName, namespace string, addToScheme ...func(*runtime.Scheme) error) (client.Client, error) {
	restConfig, err := restConfigForCluster(ctx, c, clusterName, namespace)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	for _, fn := range append([]func(*runtime.Scheme) error{clientgoscheme.AddToScheme, rbacv1.AddToScheme}, addToScheme...) {
		if err := fn(scheme); err != nil {
			return nil, fmt.Errorf("scheme registration: %w", err)
		}
	}

	return client.New(restConfig, client.Options{Scheme: scheme})
}

// NewRemoteDiscoveryClient builds a discovery client for the named cluster from
// the same greenhousekubeconfig Secret NewRemoteClient uses. Used to enumerate a
// denied API group's resources live during deny-driven RBAC expansion.
func NewRemoteDiscoveryClient(ctx context.Context, c client.Client, clusterName, namespace string) (discovery.DiscoveryInterface, error) {
	restConfig, err := restConfigForCluster(ctx, c, clusterName, namespace)
	if err != nil {
		return nil, err
	}
	return discovery.NewDiscoveryClientForConfig(restConfig)
}
