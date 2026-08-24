// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package shared

import (
	"fmt"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1alpha1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// Scheme returns a runtime.Scheme registering every type the suite reads or
// writes: core/rbac, the Permission Manager CRDs, the Greenhouse Cluster type,
// and the repo-guard github CRs. It mirrors the controller's scheme init in
// cmd/main.go so client decoding matches the controller exactly.
func Scheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))
	utilruntime.Must(pmv1alpha1.AddToScheme(scheme))
	utilruntime.Must(greenhousev1alpha1.AddToScheme(scheme))
	utilruntime.Must(repoguardv1.AddToScheme(scheme))
	return scheme
}

// clientFromKubeconfig builds a controller-runtime client from raw kubeconfig
// bytes using the suite scheme.
func clientFromKubeconfig(kubeconfig string) (client.Client, error) {
	restCfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	return client.New(restCfg, client.Options{Scheme: Scheme()})
}

// AdminClient builds a client against the management (kind) cluster hosting
// Greenhouse and the controller. Specs create ASRs/ConnectorBindings here.
func AdminClient(cfg SuiteConfig) (client.Client, error) {
	return clientFromKubeconfig(cfg.AdminKubeconfig)
}

// DownstreamClient builds a client against the downstream target where RBAC is
// provisioned. In self-onboard mode this is the same kind cluster as the admin
// client; with a real downstream it points at that cluster. Specs use it
// identically in both modes so no test code changes when a real kubeconfig is
// supplied.
func DownstreamClient(cfg SuiteConfig) (client.Client, error) {
	if selfOnboard() {
		return AdminClient(cfg)
	}
	return clientFromKubeconfig(cfg.DownstreamKubeconfig)
}
