// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"

	authorizationv1 "k8s.io/api/authorization/v1"
)

var _ = Describe("ASR provisioning: kubernetes", func() {
	It("binds a cluster CCRN to the shared aggregated ClusterRole when an ASR is created", func() {
		ctx := context.Background()
		const asrName = "provision-reader"
		sharedRole := sharedClusterRoleName(scopeCluster)
		binding := coarseBindingName(asrName, scopeCluster)
		resource := "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/" + remoteClusterName

		createASR(asrName, []string{connectorName}, []map[string]string{
			{"resource": resource, "capability": "READ"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		assertSharedRootClusterRole(ctx, sharedRole)
		assertClusterRoleBindingExists(ctx, binding, sharedRole, asrName)
	})

	It("binds a namespace CCRN to the shared ClusterRole with a namespaced RoleBinding and no per-ASR Role", func() {
		ctx := context.Background()
		const asrName = "provision-ns-reader"
		sharedRole := sharedClusterRoleName(scopeNamespace)
		binding := coarseBindingName(asrName, scopeNamespace)
		resource := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/%s", remoteClusterName, remoteTestNamespace)

		createASR(asrName, []string{connectorName}, []map[string]string{
			{"resource": resource, "capability": "READ"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		assertSharedRootClusterRole(ctx, sharedRole)
		assertRoleBindingExists(ctx, remoteTestNamespace, binding, sharedRole, asrName)
		assertRoleAbsent(ctx, remoteTestNamespace, perASRRoleName(asrName))
	})

	It("removes the per-ASR binding when the ASR is deleted", func() {
		ctx := context.Background()
		const asrName = "deprovision-reader"
		sharedRole := sharedClusterRoleName(scopeCluster)
		binding := coarseBindingName(asrName, scopeCluster)
		resource := "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/" + remoteClusterName

		createASR(asrName, []string{connectorName}, []map[string]string{
			{"resource": resource, "capability": "READ"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		assertClusterRoleBindingExists(ctx, binding, sharedRole, asrName)

		assertASRDeleted(ctx, asrName)
		// The shared ClusterRole is owned by the ConnectorBinding and stays; only
		// the per-ASR binding is removed.
		assertClusterRoleBindingGone(ctx, binding, defaultAssertTimeout)
	})

	It("cleans up the orphaned cluster binding when the ASR narrows to a namespace CCRN", func() {
		ctx := context.Background()
		const asrName = "scope-change-reader"
		clusterBinding := coarseBindingName(asrName, scopeCluster)
		clusterRole := sharedClusterRoleName(scopeCluster)
		nsBinding := coarseBindingName(asrName, scopeNamespace)
		nsRole := sharedClusterRoleName(scopeNamespace)
		clusterResource := "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/" + remoteClusterName
		namespaceResource := fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/%s", remoteClusterName, remoteTestNamespace)

		createASR(asrName, []string{connectorName}, []map[string]string{
			{"resource": clusterResource, "capability": "READ"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		assertClusterRoleBindingExists(ctx, clusterBinding, clusterRole, asrName)

		updateASRPermissions(asrName, []map[string]string{
			{"resource": namespaceResource, "capability": "READ"},
		})

		assertRoleBindingExists(ctx, remoteTestNamespace, nsBinding, nsRole, asrName)
		assertClusterRoleBindingGone(ctx, clusterBinding, resourcePropagationTimeout)
	})
})

var _ = Describe("ASR effective permissions: kubernetes", func() {
	nsResource := func() string {
		return fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/%s", remoteClusterName, remoteTestNamespace)
	}

	It("grants cluster READ effective get on pods but not secrets", func() {
		ctx := context.Background()
		const asrName = "effective-cluster-reader"
		resource := "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/" + remoteClusterName

		createASR(asrName, []string{connectorName}, []map[string]string{
			{"resource": resource, "capability": "READ"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		assertAccessEventually(ctx, asrName, authorizationv1.ResourceAttributes{Verb: "get", Resource: "pods"}, true)
		assertAccessEventually(ctx, asrName, authorizationv1.ResourceAttributes{Verb: "get", Resource: "secrets"}, false)
	})

	It("grants namespace READ effective get on pods but not secrets", func() {
		ctx := context.Background()
		const asrName = "effective-ns-reader"

		createASR(asrName, []string{connectorName}, []map[string]string{
			{"resource": nsResource(), "capability": "READ"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		assertAccessEventually(ctx, asrName, authorizationv1.ResourceAttributes{Namespace: remoteTestNamespace, Verb: "get", Resource: "pods"}, true)
		assertAccessEventually(ctx, asrName, authorizationv1.ResourceAttributes{Namespace: remoteTestNamespace, Verb: "get", Resource: "secrets"}, false)
	})

	It("grants namespace OPERATE effective create on deployments but not get on secrets", func() {
		ctx := context.Background()
		const asrName = "effective-ns-operator"

		createASR(asrName, []string{connectorName}, []map[string]string{
			{"resource": nsResource(), "capability": "OPERATE"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		assertAccessEventually(ctx, asrName, authorizationv1.ResourceAttributes{Namespace: remoteTestNamespace, Group: "apps", Verb: "create", Resource: "deployments"}, true)
		assertAccessEventually(ctx, asrName, authorizationv1.ResourceAttributes{Namespace: remoteTestNamespace, Verb: "get", Resource: "secrets"}, false)
	})

	It("grants namespace MANAGE effective get on secrets", func() {
		ctx := context.Background()
		const asrName = "effective-ns-manager"

		createASR(asrName, []string{connectorName}, []map[string]string{
			{"resource": nsResource(), "capability": "MANAGE"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		assertAccessEventually(ctx, asrName, authorizationv1.ResourceAttributes{Namespace: remoteTestNamespace, Verb: "get", Resource: "secrets"}, true)
	})
})
