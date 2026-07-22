// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ASR provisioning: kubernetes", func() {
	It("provisions a cluster-scoped ClusterRole on the remote cluster when an ASR is created", func() {
		ctx := context.Background()
		const asrName = "provision-reader"
		expectedCR := "pm-" + asrName
		resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", remoteClusterName)

		createASR(asrName, []string{"e2e-k8s"}, []map[string]string{
			{"resource": resource, "capability": "READ"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		cr := waitForClusterRole(ctx, expectedCR, resourcePropagationTimeout)
		Expect(cr.Rules).NotTo(BeEmpty(), "ClusterRole %q has no rules", expectedCR)
		// READ maps to get, list, watch.
		Expect(cr.Rules[0].Verbs).To(ContainElements("get", "list", "watch"))

		assertClusterRoleBindingExists(ctx, expectedCR, expectedCR, asrName)
	})

	It("provisions a namespace-scoped Role and RoleBinding for a namespace CCRN", func() {
		ctx := context.Background()
		const asrName = "provision-ns-reader"
		resource := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/%s", remoteClusterName, remoteTestNamespace)

		createASR(asrName, []string{"e2e-k8s"}, []map[string]string{
			{"resource": resource, "capability": "READ"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		waitForRole(ctx, remoteTestNamespace, "pm-"+asrName, resourcePropagationTimeout)
		assertRoleBindingExists(ctx, remoteTestNamespace, "pm-"+asrName, asrName)
	})

	It("removes the remote RBAC when the ASR is deleted", func() {
		ctx := context.Background()
		const asrName = "deprovision-reader"
		expectedCR := "pm-" + asrName
		resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", remoteClusterName)

		createASR(asrName, []string{"e2e-k8s"}, []map[string]string{
			{"resource": resource, "capability": "READ"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		waitForClusterRole(ctx, expectedCR, resourcePropagationTimeout)

		assertASRDeleted(ctx, asrName)
		assertClusterScopedRBACGone(ctx, expectedCR, defaultAssertTimeout)
	})

	It("cleans up orphaned cluster RBAC when the ASR narrows to a namespace CCRN", func() {
		ctx := context.Background()
		const asrName = "scope-change-reader"
		expectedName := "pm-" + asrName
		clusterResource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", remoteClusterName)
		namespaceResource := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/%s", remoteClusterName, remoteTestNamespace)

		createASR(asrName, []string{"e2e-k8s"}, []map[string]string{
			{"resource": clusterResource, "capability": "READ"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		waitForClusterRole(ctx, expectedName, resourcePropagationTimeout)

		updateASRPermissions(asrName, []map[string]string{
			{"resource": namespaceResource, "capability": "READ"},
		})

		waitForRole(ctx, remoteTestNamespace, expectedName, resourcePropagationTimeout)
		assertRoleBindingExists(ctx, remoteTestNamespace, expectedName, asrName)
		assertClusterScopedRBACGone(ctx, expectedName, resourcePropagationTimeout)
	})
})
