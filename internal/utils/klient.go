// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"context"
	"fmt"
	"os"
	"strconv"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	"github.com/cloudoperators/greenhouse/pkg/lifecycle"
)

const (
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

// restConfigForCluster uses greenhouse lifecycle.NewRemoteKubeCfg to build a rest.Config for the named cluster
// and applies the apiServerOverrideAnnotation if present and allowed by the DEV_API_SERVER_OVERRIDE env var.
func restConfigForCluster(ctx context.Context, c client.Client, clusterName, namespace string) (*rest.Config, error) {
	cluster := &greenhousev1alpha1.Cluster{}
	if err := c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: namespace}, cluster); err != nil {
		return nil, fmt.Errorf("cluster %q: %w", clusterName, err)
	}

	restConfig, err := lifecycle.NewRemoteKubeCfg(ctx, c, cluster)
	if err != nil {
		return nil, fmt.Errorf("remote kubeconfig for cluster %q: %w", clusterName, err)
	}

	if allow, err := strconv.ParseBool(os.Getenv(apiServerOverrideEnv)); err == nil && allow {
		if override := cluster.Annotations[apiServerOverrideAnnotation]; override != "" {
			ctrl.LoggerFrom(ctx).Info("api server override active",
				"cluster", clusterName, "from", restConfig.Host, "to", override)
			restConfig.Host = override
		}
	}

	return restConfig, nil
}

// NewRemoteClient builds a controller-runtime client for the named cluster
// using the greenhouse lifecycle.NewRemoteKubeCfg.
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

// NewRemoteDiscoveryClient builds a discovery client for the named cluster using the greenhouse lifecycle.NewRemoteKubeCfg.
// Used to enumerate a denied API group's resources live during deny-driven RBAC expansion.
func NewRemoteDiscoveryClient(ctx context.Context, c client.Client, clusterName, namespace string) (discovery.DiscoveryInterface, error) {
	restConfig, err := restConfigForCluster(ctx, c, clusterName, namespace)
	if err != nil {
		return nil, err
	}
	return discovery.NewDiscoveryClientForConfig(restConfig)
}
