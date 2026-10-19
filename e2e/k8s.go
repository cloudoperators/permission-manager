// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/cloudoperators/permission-manager/internal/naming"
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
// capability binds to, e.g. "pm-s-e2e-k8s-cluster-read".
func sharedClusterRoleName(scope string) string {
	return "pm-s-" + connectorName + "-" + scope + "-read"
}

// coarseBindingName returns the per-ASR binding emitted for a coarse READ
// capability, e.g. "pm-a-provision-reader-e2e-k8s-cluster-read-<hash>". The
// ShortHash suffix mirrors the controller's coarseBindingName over the full
// (ASR namespace, ASR name, CB, scope, capability) identity.
func coarseBindingName(asrName, scope string) string {
	const capability = "read"
	h := naming.ShortHash(orgNamespace, asrName, connectorName, scope, capability)
	name := "pm-a-" + asrName + "-" + connectorName + "-" + scope + "-" + capability + "-" + h
	return naming.TruncateWithHash(name, naming.MaxK8sNameLength)
}

// perASRRoleName returns the per-ASR ClusterRole/Role name the controller emits
// for resource CCRNs, e.g. "pm-a-provision-ns-reader".
func perASRRoleName(asrName string) string {
	return "pm-a-" + asrName
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

// assertAccessEventually posts a SubjectAccessReview to the remote cluster for
// the subject group and verifies the effective decision matches allowed. This
// checks what RBAC actually grants, not just that objects exist: aggregation and
// baseline rules both feed into the authorizer's answer. The group is the ASR's
// spec.name, the subject the controller binds.
func assertAccessEventually(ctx context.Context, group string, attrs authorizationv1.ResourceAttributes, allowed bool) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		sar := &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				Groups:             []string{group},
				ResourceAttributes: &attrs,
			},
		}
		g.Expect(remoteClient.Create(ctx, sar)).To(Succeed())
		g.Expect(sar.Status.Allowed).To(Equal(allowed),
			"SubjectAccessReview for group %q on %s/%s (verb %q): allowed=%t, want %t (reason: %s)",
			group, attrs.Resource, attrs.Subresource, attrs.Verb, sar.Status.Allowed, allowed, sar.Status.Reason)
	}).WithTimeout(resourcePropagationTimeout).WithPolling(pollInterval).Should(Succeed())
}
