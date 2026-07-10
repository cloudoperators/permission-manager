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

func waitForClusterRole(ctx context.Context, name string, timeout time.Duration) *rbacv1.ClusterRole {
	GinkgoHelper()
	var cr rbacv1.ClusterRole
	Eventually(func() error {
		return remoteClient.Get(ctx, types.NamespacedName{Name: name}, &cr)
	}).WithTimeout(timeout).WithPolling(pollInterval).
		Should(Succeed(), "ClusterRole %q did not appear on %s", name, remoteContext)
	return &cr
}

func waitForRole(ctx context.Context, namespace, name string, timeout time.Duration) {
	GinkgoHelper()
	Eventually(func() error {
		return remoteClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &rbacv1.Role{})
	}).WithTimeout(timeout).WithPolling(pollInterval).
		Should(Succeed(), "Role %q in namespace %q did not appear on %s", name, namespace, remoteContext)
}

func assertRoleBindingExists(ctx context.Context, namespace, name, subjectGroup string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var rb rbacv1.RoleBinding
		g.Expect(remoteClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &rb)).To(Succeed())
		g.Expect(rb.Labels).To(HaveKeyWithValue(managedByLabel, managedByController))
		if subjectGroup != "" {
			g.Expect(rb.Subjects).To(ContainElement(And(
				HaveField("Kind", "Group"), HaveField("Name", subjectGroup))))
		}
	}).WithTimeout(defaultAssertTimeout).WithPolling(assertPollInterval).Should(Succeed())
}

func assertClusterScopedRBACGone(ctx context.Context, name string, timeout time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		err := remoteClient.Get(ctx, types.NamespacedName{Name: name}, &rbacv1.ClusterRole{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "ClusterRole %q still on %s (err: %v)", name, remoteContext, err)
		err = remoteClient.Get(ctx, types.NamespacedName{Name: name}, &rbacv1.ClusterRoleBinding{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "ClusterRoleBinding %q still on %s (err: %v)", name, remoteContext, err)
	}).WithTimeout(timeout).WithPolling(pollInterval).Should(Succeed())
}

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
