// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

// connectorName is the ConnectorBinding spec.name from
// testdata/setup/connectorbinding-k8s.yaml. It is the token embedded in shared
// ClusterRole and binding names.
const connectorName = "e2e-k8s"

const (
	scopeCluster   = "cluster"
	scopeNamespace = "namespace"
)

// sharedClusterRoleName returns the aggregated root ClusterRole a coarse READ
// capability binds to, e.g. "pm-e2e-k8s-cluster-read".
func sharedClusterRoleName(scope string) string {
	return "pm-" + connectorName + "-" + scope + "-read"
}

// coarseBindingName returns the per-ASR binding emitted for a coarse READ
// capability, e.g. "pm-provision-reader-e2e-k8s-cluster-read".
func coarseBindingName(asrName, scope string) string {
	return "pm-" + asrName + "-" + connectorName + "-" + scope + "-read"
}

// assertSharedRootClusterRole waits for the aggregated root ClusterRole to
// appear and verifies it is aggregation-only (aggregationRule set) and
// controller-managed.
func assertSharedRootClusterRole(ctx context.Context, name string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var cr rbacv1.ClusterRole
		g.Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, &cr)).To(Succeed())
		g.Expect(cr.Labels).To(HaveKeyWithValue(managedByLabel, managedByController))
		g.Expect(cr.AggregationRule).NotTo(BeNil(), "root ClusterRole %q has no aggregationRule", name)
	}).WithTimeout(resourcePropagationTimeout).WithPolling(pollInterval).
		Should(Succeed(), "root ClusterRole %q did not appear on %s", name, remoteContext)
}

// assertRoleBindingExists verifies a namespaced RoleBinding exists, is
// controller-managed, binds the subject group, and references the shared
// ClusterRole roleName via a RoleRef of Kind ClusterRole.
func assertRoleBindingExists(ctx context.Context, namespace, name, roleName, subjectGroup string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var rb rbacv1.RoleBinding
		g.Expect(remoteClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &rb)).To(Succeed())
		g.Expect(rb.Labels).To(HaveKeyWithValue(managedByLabel, managedByController))
		g.Expect(rb.RoleRef.Kind).To(Equal("ClusterRole"))
		g.Expect(rb.RoleRef.Name).To(Equal(roleName))
		g.Expect(rb.Subjects).To(ContainElement(And(
			HaveField("Kind", "Group"), HaveField("Name", subjectGroup))))
	}).WithTimeout(defaultAssertTimeout).WithPolling(assertPollInterval).Should(Succeed())
}

// assertClusterRoleBindingExists verifies a ClusterRoleBinding exists, is
// controller-managed, binds the subject group, and references the ClusterRole
// roleName.
func assertClusterRoleBindingExists(ctx context.Context, name, roleName, subjectGroup string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var crb rbacv1.ClusterRoleBinding
		g.Expect(remoteClient.Get(ctx, types.NamespacedName{Name: name}, &crb)).To(Succeed())
		g.Expect(crb.Labels).To(HaveKeyWithValue(managedByLabel, managedByController))
		g.Expect(crb.RoleRef.Kind).To(Equal("ClusterRole"))
		g.Expect(crb.RoleRef.Name).To(Equal(roleName))
		g.Expect(crb.Subjects).To(ContainElement(And(
			HaveField("Kind", "Group"), HaveField("Name", subjectGroup))))
	}).WithTimeout(defaultAssertTimeout).WithPolling(assertPollInterval).Should(Succeed())
}

// assertClusterRoleBindingGone waits until the named ClusterRoleBinding is
// removed from the remote cluster.
func assertClusterRoleBindingGone(ctx context.Context, name string, timeout time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		err := remoteClient.Get(ctx, types.NamespacedName{Name: name}, &rbacv1.ClusterRoleBinding{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "ClusterRoleBinding %q still on %s (err: %v)", name, remoteContext, err)
	}).WithTimeout(timeout).WithPolling(pollInterval).Should(Succeed())
}

// assertRoleAbsent verifies no namespaced Role of the given name is created,
// confirming coarse namespace capabilities bind to a shared ClusterRole rather
// than a per-ASR Role.
func assertRoleAbsent(ctx context.Context, namespace, name string) {
	GinkgoHelper()
	Consistently(func(g Gomega) {
		err := remoteClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &rbacv1.Role{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "unexpected per-ASR Role %q in namespace %q on %s", name, namespace, remoteContext)
	}).WithTimeout(5 * time.Second).WithPolling(assertPollInterval).Should(Succeed())
}
