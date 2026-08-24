// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/e2e/shared"
)

// The kubernetes provider runs in its own Ordered top-level container so it can
// be distributed to a separate Ginkgo process from the github provider (the two
// providers do not share cluster state). Every spec fuzzes its ASR name and
// registers cleanup immediately, so a passing run leaves the cluster as found.
var _ = Describe("kubernetes provider", Ordered, func() {
	ctx := context.Background()

	// rbacName is the RBAC object name the provider derives from the ASR name.
	rbacName := func(asrName string) string { return "pm-" + asrName }

	// applyASR creates the ASR against the admin cluster and schedules its
	// finalizer-safe deletion regardless of outcome.
	applyASR := func(name, specName string, perms []shared.Perm) {
		asr := shared.NewASR(cfg.ControllerNamespace, name, specName,
			[]string{shared.K8sConnectorBinding}, perms)
		Expect(adminC.Create(ctx, asr)).To(Succeed())
		DeferCleanup(func() {
			shared.DeleteASR(ctx, adminC, cfg.ControllerNamespace, name)
		})
	}

	Context("happy paths", func() {
		It("provisions a ClusterRole for a cluster-scoped CCRN", func() {
			name := shared.ASRName("k8s-cluster-read")
			specName := "E2E_K8S_CLUSTER_READ"
			applyASR(name, specName, []shared.Perm{
				{Resource: shared.ClusterCCRN(cfg.DownstreamName), Capability: "READ"},
			})

			shared.ExpectASRReady(ctx, adminC, cfg.ControllerNamespace, name)
			cr := shared.ExpectClusterRole(ctx, downstream, rbacName(name))
			shared.ExpectManagedRBACLabels(cr, name)
			shared.ExpectRuleGrants(cr.Rules, "", "pods", shared.ReadVerbs...)
			shared.ExpectClusterRoleBinding(ctx, downstream, rbacName(name), rbacName(name), specName)
		})

		It("provisions a namespaced Role for a namespace-scoped CCRN", func() {
			name := shared.ASRName("k8s-ns-write")
			specName := "E2E_K8S_NS_WRITE"
			applyASR(name, specName, []shared.Perm{
				{Resource: shared.NamespaceCCRN(cfg.DownstreamName, shared.TargetNamespace), Capability: "WRITE"},
			})

			shared.ExpectASRReady(ctx, adminC, cfg.ControllerNamespace, name)
			role := shared.ExpectRole(ctx, downstream, shared.TargetNamespace, rbacName(name))
			shared.ExpectManagedRBACLabels(role, name)
			shared.ExpectRuleGrants(role.Rules, "", "pods", shared.WriteVerbs...)
			shared.ExpectRoleBinding(ctx, downstream, shared.TargetNamespace, rbacName(name), specName)
			// A namespace-scoped ASR must not create a cluster-scoped role.
			shared.ExpectClusterRoleAbsent(ctx, downstream, rbacName(name))
		})

		It("grants full verbs including rbac roles for an ADMIN capability", func() {
			name := shared.ASRName("k8s-cluster-admin")
			specName := "E2E_K8S_CLUSTER_ADMIN"
			applyASR(name, specName, []shared.Perm{
				{Resource: shared.ClusterCCRN(cfg.DownstreamName), Capability: "ADMIN"},
			})

			shared.ExpectASRReady(ctx, adminC, cfg.ControllerNamespace, name)
			cr := shared.ExpectClusterRole(ctx, downstream, rbacName(name))
			shared.ExpectRuleGrants(cr.Rules, "", "pods", shared.WriteVerbs...)
			shared.ExpectRuleGrants(cr.Rules, "rbac.authorization.k8s.io", "roles", shared.WriteVerbs...)
		})

		It("provisions both a ClusterRole and a Role for a multi-permission ASR", func() {
			name := shared.ASRName("k8s-multi")
			specName := "E2E_K8S_MULTI"
			applyASR(name, specName, []shared.Perm{
				{Resource: shared.ClusterCCRN(cfg.DownstreamName), Capability: "READ"},
				{Resource: shared.NamespaceCCRN(cfg.DownstreamName, shared.TargetNamespace), Capability: "WRITE"},
			})

			shared.ExpectASRReady(ctx, adminC, cfg.ControllerNamespace, name)
			cr := shared.ExpectClusterRole(ctx, downstream, rbacName(name))
			shared.ExpectRuleGrants(cr.Rules, "", "pods", shared.ReadVerbs...)
			role := shared.ExpectRole(ctx, downstream, shared.TargetNamespace, rbacName(name))
			shared.ExpectRuleGrants(role.Rules, "", "pods", shared.WriteVerbs...)
		})

		It("resolves a cluster wildcard CCRN to the onboarded cluster", func() {
			name := shared.ASRName("k8s-wildcard")
			applyASR(name, "E2E_K8S_WILDCARD", []shared.Perm{
				{Resource: shared.ClusterWildcardCCRN("pm-e2e*"), Capability: "READ"},
			})

			shared.ExpectASRReady(ctx, adminC, cfg.ControllerNamespace, name)
			shared.ExpectClusterRole(ctx, downstream, rbacName(name))
		})

		It("excludes secrets for a deny-driven capability", func() {
			name := shared.ASRName("k8s-read-except-secrets")
			applyASR(name, "E2E_K8S_READ_EXCEPT_SECRETS", []shared.Perm{
				{Resource: shared.ClusterCCRN(cfg.DownstreamName), Capability: "READ_EXCEPT_SECRETS"},
			})

			shared.ExpectASRReady(ctx, adminC, cfg.ControllerNamespace, name)
			cr := shared.ExpectClusterRole(ctx, downstream, rbacName(name))
			// Core resources are enumerated with secrets removed; verbs are read-only.
			shared.ExpectRuleGrants(cr.Rules, "", "configmaps", shared.ReadVerbs...)
			shared.ExpectRuleIncludesResource(cr.Rules, "", "pods")
			shared.ExpectRuleExcludesResource(cr.Rules, "", "secrets")
		})
	})

	Context("fail paths", func() {
		It("reports InvalidCCRN for an unsupported CCRN type", func() {
			name := shared.ASRName("k8s-invalid-ccrn")
			applyASR(name, "E2E_K8S_INVALID_CCRN", []shared.Perm{
				{Resource: shared.InvalidTypeCCRN(cfg.DownstreamName, shared.TargetNamespace), Capability: "READ"},
			})

			shared.ExpectInvalidConfig(ctx, adminC, cfg.ControllerNamespace, name, pmmeta.InvalidCCRNReason)
		})

		It("reports InvalidCCRN for a namespace wildcard CCRN", func() {
			name := shared.ASRName("k8s-ns-wildcard")
			applyASR(name, "E2E_K8S_NS_WILDCARD", []shared.Perm{
				{Resource: shared.NamespaceWildcardCCRN(cfg.DownstreamName, "e2e-*"), Capability: "READ"},
			})

			shared.ExpectInvalidConfig(ctx, adminC, cfg.ControllerNamespace, name, pmmeta.InvalidCCRNReason)
		})

		It("reports MissingConnectorBinding when the referenced binding does not exist", func() {
			name := shared.ASRName("k8s-missing-cb")
			asr := shared.NewASR(cfg.ControllerNamespace, name, "E2E_K8S_MISSING_CB",
				[]string{"does-not-exist"}, []shared.Perm{
					{Resource: shared.ClusterCCRN(cfg.DownstreamName), Capability: "READ"},
				})
			Expect(adminC.Create(ctx, asr)).To(Succeed())
			DeferCleanup(func() {
				shared.DeleteASR(ctx, adminC, cfg.ControllerNamespace, name)
			})

			shared.ExpectInvalidConfig(ctx, adminC, cfg.ControllerNamespace, name, pmmeta.MissingConnectorBindingReason)
		})

		It("reports UnknownCapability for a capability absent from the mapping", func() {
			name := shared.ASRName("k8s-unknown-cap")
			applyASR(name, "E2E_K8S_UNKNOWN_CAP", []shared.Perm{
				{Resource: shared.ClusterCCRN(cfg.DownstreamName), Capability: "TELEPORT"},
			})

			shared.ExpectInvalidConfig(ctx, adminC, cfg.ControllerNamespace, name, pmmeta.UnknownCapabilityReason)
		})

		It("reports NoTargetsResolved when a cluster pattern matches nothing", func() {
			name := shared.ASRName("k8s-no-targets")
			applyASR(name, "E2E_K8S_NO_TARGETS", []shared.Perm{
				{Resource: shared.ClusterCCRN("ghost-cluster"), Capability: "READ"},
			})

			shared.ExpectInvalidConfig(ctx, adminC, cfg.ControllerNamespace, name, pmmeta.NoTargetsResolvedReason)
		})

		It("reports Ready=True/NoTargets when no permission resolves to a k8s target", func() {
			// A k8s-bound ASR whose only permission is a github CCRN compiles zero
			// kubernetes rules — a benign, provisioned state (not InvalidConfig).
			name := shared.ASRName("k8s-benign-no-targets")
			applyASR(name, "E2E_K8S_BENIGN_NO_TARGETS", []shared.Perm{
				{Resource: shared.OrgCCRN(shared.GithubInstance, shared.GithubOrg), Capability: "READ"},
			})

			shared.ExpectReadyNoTargets(ctx, adminC, cfg.ControllerNamespace, name)
		})

		It("reports DiscoveryUnavailable when a deny-driven capability targets a cluster without a discovery cache", func() {
			nodisc := "pm-nodisc-" + shared.RandSuffix()
			Expect(shared.OnboardCluster(ctx, adminC, cfg.ControllerNamespace, shared.OnboardOptions{
				Name:       nodisc,
				Kubeconfig: cfg.DownstreamKubeconfig,
				// DiscoveryGroups nil: expansion fails closed.
			})).To(Succeed())
			DeferCleanup(func() { shared.OffboardCluster(ctx, adminC, cfg.ControllerNamespace, nodisc) })

			name := shared.ASRName("k8s-no-discovery")
			applyASR(name, "E2E_K8S_NO_DISCOVERY", []shared.Perm{
				{Resource: shared.ClusterCCRN(nodisc), Capability: "READ_EXCEPT_SECRETS"},
			})

			shared.ExpectInvalidConfig(ctx, adminC, cfg.ControllerNamespace, name, pmmeta.DiscoveryUnavailableReason)
		})

		It("reports ProvisioningFailed when the only target is unreachable", func() {
			bad := "pm-unreachable-" + shared.RandSuffix()
			Expect(shared.OnboardCluster(ctx, adminC, cfg.ControllerNamespace, shared.OnboardOptions{
				Name:       bad,
				Kubeconfig: shared.UnreachableKubeconfig(),
			})).To(Succeed())
			DeferCleanup(func() { shared.OffboardCluster(ctx, adminC, cfg.ControllerNamespace, bad) })

			name := shared.ASRName("k8s-prov-failed")
			applyASR(name, "E2E_K8S_PROV_FAILED", []shared.Perm{
				{Resource: shared.ClusterCCRN(bad), Capability: "READ"},
			})

			shared.ExpectReadyFalseReason(ctx, adminC, cfg.ControllerNamespace, name, string(pmmeta.ProvisioningFailedReason))
		})

		It("reports PartialProvisioning when some targets apply and others fail", func() {
			suffix := shared.RandSuffix()
			good := "pm-partial-" + suffix + "-good"
			bad := "pm-partial-" + suffix + "-bad"
			Expect(shared.OnboardCluster(ctx, adminC, cfg.ControllerNamespace, shared.OnboardOptions{
				Name: good, Kubeconfig: cfg.DownstreamKubeconfig, DiscoveryGroups: shared.DefaultDiscoveryGroups,
			})).To(Succeed())
			DeferCleanup(func() { shared.OffboardCluster(ctx, adminC, cfg.ControllerNamespace, good) })
			Expect(shared.OnboardCluster(ctx, adminC, cfg.ControllerNamespace, shared.OnboardOptions{
				Name: bad, Kubeconfig: shared.UnreachableKubeconfig(),
			})).To(Succeed())
			DeferCleanup(func() { shared.OffboardCluster(ctx, adminC, cfg.ControllerNamespace, bad) })

			name := shared.ASRName("k8s-partial")
			applyASR(name, "E2E_K8S_PARTIAL", []shared.Perm{
				{Resource: shared.ClusterWildcardCCRN("pm-partial-" + suffix + "-*"), Capability: "READ"},
			})

			shared.ExpectReadyFalseReason(ctx, adminC, cfg.ControllerNamespace, name, string(pmmeta.PartialProvisioningReason))
		})
	})

	Context("offboarding", func() {
		It("revokes provisioned RBAC when the ASR is deleted", func() {
			name := shared.ASRName("k8s-offboard")
			asr := shared.NewASR(cfg.ControllerNamespace, name, "E2E_K8S_OFFBOARD",
				[]string{shared.K8sConnectorBinding}, []shared.Perm{
					{Resource: shared.ClusterCCRN(cfg.DownstreamName), Capability: "READ"},
				})
			Expect(adminC.Create(ctx, asr)).To(Succeed())

			shared.ExpectASRReady(ctx, adminC, cfg.ControllerNamespace, name)
			shared.ExpectClusterRole(ctx, downstream, rbacName(name))

			// Graceful delete: let the controller's finalizer cleanup revoke RBAC.
			shared.DeleteASR(ctx, adminC, cfg.ControllerNamespace, name)
			shared.ExpectClusterRoleAbsent(ctx, downstream, rbacName(name))
		})

		It("cleans up orphaned cluster RBAC when the ASR narrows to a namespace CCRN", func() {
			name := shared.ASRName("k8s-scope-narrow")
			applyASR(name, "E2E_K8S_SCOPE_NARROW", []shared.Perm{
				{Resource: shared.ClusterCCRN(cfg.DownstreamName), Capability: "READ"},
			})

			shared.ExpectASRReady(ctx, adminC, cfg.ControllerNamespace, name)
			shared.ExpectClusterRole(ctx, downstream, rbacName(name))
			// Status tracks the cluster-scoped resources for this target.
			shared.ExpectTargetAppliedKinds(ctx, adminC, cfg.ControllerNamespace, name,
				cfg.DownstreamName, "ClusterRole", "ClusterRoleBinding")

			// Narrow from cluster scope to a single namespace: the namespaced Role
			// appears and the now-orphaned cluster-scoped RBAC is removed.
			shared.UpdateASRPermissions(ctx, adminC, cfg.ControllerNamespace, name, []shared.Perm{
				{Resource: shared.NamespaceCCRN(cfg.DownstreamName, shared.TargetNamespace), Capability: "READ"},
			})

			shared.ExpectRole(ctx, downstream, shared.TargetNamespace, rbacName(name))
			shared.ExpectClusterRoleAbsent(ctx, downstream, rbacName(name))
			// Status is pruned: the target now tracks only the namespaced resources.
			shared.ExpectTargetAppliedKinds(ctx, adminC, cfg.ControllerNamespace, name,
				cfg.DownstreamName, "Role", "RoleBinding")
		})
	})
})
