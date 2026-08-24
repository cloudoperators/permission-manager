// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package shared

import (
	"context"
	"fmt"
	"os"

	. "github.com/onsi/gomega"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// greenhouseKubeconfigKey is the Secret data key the controller reads the
// downstream kubeconfig from (internal/utils/klient.go). It is deliberately
// NOT "kubeconfig".
const greenhouseKubeconfigKey = "greenhousekubeconfig"

// ResolveDownstream determines the downstream cluster name and the kubeconfig
// stored in the onboarding Secret. In self-onboard mode the kind cluster is its
// own downstream (internal kubeconfig, name == kind cluster). When
// E2E_DOWNSTREAM_KUBECONFIG is set, that file and E2E_DOWNSTREAM_NAME are used.
func ResolveDownstream(cfg SuiteConfig) (name, kubeconfig string, err error) {
	if path := os.Getenv(EnvDownstreamKubeconfig); path != "" {
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", "", fmt.Errorf("read %s: %w", EnvDownstreamKubeconfig, readErr)
		}
		name = os.Getenv(EnvDownstreamName)
		if name == "" {
			name = cfg.ClusterName
		}
		return name, string(b), nil
	}
	internal, err := InternalKubeconfig(cfg.ClusterName)
	if err != nil {
		return "", "", err
	}
	return cfg.ClusterName, internal, nil
}

// DefaultDiscoveryGroups is the discoveryCache.groups patched onto the main
// downstream so deny-driven (excludeResources) capabilities can expand. The
// denied group "" must be present for secrets to be enumerated and removed.
var DefaultDiscoveryGroups = []string{"", "apps", "batch", "rbac.authorization.k8s.io"}

// OnboardOptions describes a downstream cluster to register with the simulated
// Greenhouse flow.
type OnboardOptions struct {
	Name       string
	Kubeconfig string
	// DiscoveryGroups, when non-nil, is patched onto status.discoveryCache.groups.
	// Leave nil to simulate a cluster whose discovery cache is unpopulated.
	DiscoveryGroups []string
}

// OnboardCluster registers a downstream target: a kubeconfig Secret (same name as
// the Cluster), a Greenhouse Cluster CR, and a status patch (KubeConfigValid=True
// plus optional discoveryCache). Objects live in the given namespace.
func OnboardCluster(ctx context.Context, admin client.Client, namespace string, opts OnboardOptions) error {
	// Secret name MUST equal the Cluster name, data key MUST be greenhousekubeconfig.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: opts.Name, Namespace: namespace},
		Data:       map[string][]byte{greenhouseKubeconfigKey: []byte(opts.Kubeconfig)},
	}
	if err := applyObject(ctx, admin, secret); err != nil {
		return fmt.Errorf("onboard secret: %w", err)
	}
	cluster := &greenhousev1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: opts.Name, Namespace: namespace},
		Spec:       greenhousev1alpha1.ClusterSpec{AccessMode: greenhousev1alpha1.ClusterAccessModeDirect},
	}
	if err := applyObject(ctx, admin, cluster); err != nil {
		return fmt.Errorf("onboard cluster: %w", err)
	}
	return patchClusterStatus(ctx, admin, namespace, opts.Name, opts.DiscoveryGroups)
}

// OnboardDownstream registers the suite's primary downstream target.
func OnboardDownstream(ctx context.Context, admin client.Client, cfg SuiteConfig) error {
	return OnboardCluster(ctx, admin, cfg.ControllerNamespace, OnboardOptions{
		Name:            cfg.DownstreamName,
		Kubeconfig:      cfg.DownstreamKubeconfig,
		DiscoveryGroups: DefaultDiscoveryGroups,
	})
}

// OffboardCluster deletes a cluster's Cluster CR and kubeconfig Secret. Best
// effort; safe when absent. For DeferCleanup of clusters onboarded within a spec.
func OffboardCluster(ctx context.Context, admin client.Client, namespace, name string) {
	Expect(client.IgnoreNotFound(admin.Delete(ctx, &greenhousev1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}))).To(Succeed())
	Expect(client.IgnoreNotFound(admin.Delete(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}))).To(Succeed())
}

// UnreachableKubeconfig returns a valid kubeconfig whose server refuses
// connections (127.0.0.1:1), so RBAC apply fails fast and deterministically.
func UnreachableKubeconfig() string {
	return `apiVersion: v1
kind: Config
clusters:
- name: unreachable
  cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
contexts:
- name: unreachable
  context:
    cluster: unreachable
    user: unreachable
current-context: unreachable
users:
- name: unreachable
  user:
    token: e2e-invalid-token
`
}

// patchClusterStatus sets status.statusConditions[KubeConfigValid]=True and,
// when discoveryGroups is non-nil, status.discoveryCache.groups. Uses
// unstructured to avoid coupling to Greenhouse's status struct layout.
func patchClusterStatus(ctx context.Context, admin client.Client, namespace, name string, discoveryGroups []string) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha1", Kind: "Cluster"})
	u.SetName(name)
	u.SetNamespace(namespace)
	if err := unstructured.SetNestedSlice(u.Object, []any{
		map[string]any{"type": "KubeConfigValid", "status": "True"},
	}, "status", "statusConditions", "conditions"); err != nil {
		return err
	}
	if discoveryGroups != nil {
		groups := make([]any, len(discoveryGroups))
		for i, g := range discoveryGroups {
			groups[i] = g
		}
		if err := unstructured.SetNestedSlice(u.Object, groups, "status", "discoveryCache", "groups"); err != nil {
			return err
		}
	}
	return admin.Status().Patch(ctx, u, client.Merge)
}

// EnsureNamespace creates a namespace on the given client if it does not exist.
func EnsureNamespace(ctx context.Context, c client.Client, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// applyObject upserts the object: created if absent, else its spec is overwritten.
func applyObject(ctx context.Context, c client.Client, obj client.Object) error {
	existing := obj.DeepCopyObject().(client.Object)
	err := c.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if apierrors.IsNotFound(err) {
		return c.Create(ctx, obj)
	}
	if err != nil {
		return err
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	return c.Update(ctx, obj)
}
