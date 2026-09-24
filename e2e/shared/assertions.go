// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package shared

import (
	"context"
	"time"

	. "github.com/onsi/gomega"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	pmv1alpha1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// Default polling budget for eventual assertions.
const (
	pollTimeout  = 90 * time.Second
	pollInterval = 2 * time.Second
)

// getASR fetches the named ASR; returns nil on NotFound so Eventually can retry.
func getASR(ctx context.Context, c client.Client, ns, name string) *pmv1alpha1.ApplicationSpecificRole {
	asr := &pmv1alpha1.ApplicationSpecificRole{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, asr); err != nil {
		return nil
	}
	return asr
}

// condition returns the named condition, or nil.
func condition(asr *pmv1alpha1.ApplicationSpecificRole, t pmmeta.ConditionType) *metav1.Condition {
	if asr == nil {
		return nil
	}
	return pmmeta.GetConditionByType(asr.Status.Conditions, t)
}

// ExpectASRReady waits until the ASR reports Ready=True with reason
// ProvisioningSucceeded and the kubernetes provider status is Ready — the
// kubernetes happy-path terminal state.
func ExpectASRReady(ctx context.Context, c client.Client, ns, name string) {
	Eventually(func(g Gomega) {
		asr := getASR(ctx, c, ns, name)
		g.Expect(asr).NotTo(BeNil(), "ASR %s/%s not found", ns, name)

		ready := condition(asr, pmmeta.ReadyCondition)
		g.Expect(ready).NotTo(BeNil(), "Ready condition missing")
		g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), "Ready status")
		g.Expect(ready.Reason).To(Equal(string(pmmeta.ProvisioningSucceededReason)))

		ps, ok := asr.Status.Providers[pmmeta.ProviderKubernetes]
		g.Expect(ok).To(BeTrue(), "kubernetes provider status missing")
		g.Expect(ps.Ready).To(BeTrue(), "kubernetes provider not Ready")
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed())
}

// ExpectInvalidConfig waits until the ASR carries an InvalidConfig=True
// condition and a Ready=False condition, both with the given reason — the
// kubernetes fail-path contract.
func ExpectInvalidConfig(ctx context.Context, c client.Client, ns, name string, reason pmmeta.ConditionReason) {
	Eventually(func(g Gomega) {
		asr := getASR(ctx, c, ns, name)
		g.Expect(asr).NotTo(BeNil(), "ASR %s/%s not found", ns, name)

		invalid := condition(asr, pmmeta.InvalidConfigCondition)
		g.Expect(invalid).NotTo(BeNil(), "InvalidConfig condition missing")
		g.Expect(invalid.Status).To(Equal(metav1.ConditionTrue), "InvalidConfig status")
		g.Expect(invalid.Reason).To(Equal(string(reason)))

		ready := condition(asr, pmmeta.ReadyCondition)
		g.Expect(ready).NotTo(BeNil(), "Ready condition missing")
		g.Expect(ready.Status).To(Equal(metav1.ConditionFalse), "Ready status")
		g.Expect(ready.Reason).To(Equal(string(reason)))
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed())
}

// ExpectReadyFalseReason waits until the ASR's Ready condition is False with the
// given reason. The github provider surfaces hard failures this way (reason
// FailingCreation) rather than via InvalidConfig.
func ExpectReadyFalseReason(ctx context.Context, c client.Client, ns, name, reason string) {
	Eventually(func(g Gomega) {
		asr := getASR(ctx, c, ns, name)
		g.Expect(asr).NotTo(BeNil(), "ASR %s/%s not found", ns, name)

		ready := condition(asr, pmmeta.ReadyCondition)
		g.Expect(ready).NotTo(BeNil(), "Ready condition missing")
		g.Expect(ready.Status).To(Equal(metav1.ConditionFalse), "Ready status")
		g.Expect(ready.Reason).To(Equal(reason))
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed())
}

// ExpectReadyNoTargets waits until the ASR reports the benign Ready=True /
// NoTargets state (a k8s-bound ASR that compiled zero kubernetes rules).
func ExpectReadyNoTargets(ctx context.Context, c client.Client, ns, name string) {
	Eventually(func(g Gomega) {
		asr := getASR(ctx, c, ns, name)
		g.Expect(asr).NotTo(BeNil(), "ASR %s/%s not found", ns, name)

		ready := condition(asr, pmmeta.ReadyCondition)
		g.Expect(ready).NotTo(BeNil(), "Ready condition missing")
		g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), "Ready status")
		g.Expect(ready.Reason).To(Equal(string(pmmeta.NoTargetsReason)))
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed())
}

// ExpectClusterRole waits until a ClusterRole with the given name exists and is
// managed by the pm-controller for this ASR (labels + subject not checked here;
// see ExpectManagedRBACLabels).
func ExpectClusterRole(ctx context.Context, c client.Client, name string) *rbacv1.ClusterRole {
	cr := &rbacv1.ClusterRole{}
	Eventually(func() error {
		return c.Get(ctx, types.NamespacedName{Name: name}, cr)
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed(),
		"ClusterRole %s not created", name)
	return cr
}

// ExpectClusterRoleAbsent waits until the named ClusterRole no longer exists.
func ExpectClusterRoleAbsent(ctx context.Context, c client.Client, name string) {
	Eventually(func() bool {
		err := c.Get(ctx, types.NamespacedName{Name: name}, &rbacv1.ClusterRole{})
		return apierrors.IsNotFound(err)
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(BeTrue(),
		"ClusterRole %s still present", name)
}

// ExpectRole waits until a namespaced Role with the given name exists.
func ExpectRole(ctx context.Context, c client.Client, namespace, name string) *rbacv1.Role {
	role := &rbacv1.Role{}
	Eventually(func() error {
		return c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, role)
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed(),
		"Role %s/%s not created", namespace, name)
	return role
}

// ExpectManagedRBACLabels asserts the object carries the pm-controller
// managed-by and source-ASR labels the kubernetes provider applies.
func ExpectManagedRBACLabels(obj client.Object, asrName string) {
	labels := obj.GetLabels()
	Expect(labels).To(HaveKeyWithValue(pmapis.LabelManagedByKey, pmapis.ManagedByPMController))
	Expect(labels).To(HaveKeyWithValue(pmapis.LabelASR, asrName))
}

// ruleCovers reports whether a PolicyRule grants the given (apiGroup, resource);
// "*" in either field matches anything.
func ruleCovers(r rbacv1.PolicyRule, apiGroup, resource string) bool {
	return containsOrWildcard(r.APIGroups, apiGroup) && containsOrWildcard(r.Resources, resource)
}

func containsOrWildcard(ss []string, want string) bool {
	for _, s := range ss {
		if s == want || s == "*" {
			return true
		}
	}
	return false
}

// ExpectRuleGrants asserts a rule covering (apiGroup, resource) exists and its
// verbs are exactly the expected set.
func ExpectRuleGrants(rules []rbacv1.PolicyRule, apiGroup, resource string, verbs ...string) {
	for _, r := range rules {
		if ruleCovers(r, apiGroup, resource) {
			Expect(r.Verbs).To(ConsistOf(verbs), "verbs for %s/%s", apiGroup, resource)
			return
		}
	}
	Expect(rules).To(ContainElement(WithTransform(
		func(r rbacv1.PolicyRule) bool { return ruleCovers(r, apiGroup, resource) }, BeTrue())),
		"no rule grants %s/%s", apiGroup, resource)
}

// ExpectRuleIncludesResource asserts some rule grants (apiGroup, resource).
func ExpectRuleIncludesResource(rules []rbacv1.PolicyRule, apiGroup, resource string) {
	Expect(rules).To(ContainElement(WithTransform(
		func(r rbacv1.PolicyRule) bool { return ruleCovers(r, apiGroup, resource) }, BeTrue())),
		"expected a rule granting %s/%s", apiGroup, resource)
}

// ExpectRuleExcludesResource asserts no rule grants (apiGroup, resource) — the
// deny-driven (excludeResources) contract.
func ExpectRuleExcludesResource(rules []rbacv1.PolicyRule, apiGroup, resource string) {
	for _, r := range rules {
		Expect(ruleCovers(r, apiGroup, resource)).To(BeFalse(),
			"rule unexpectedly grants %s/%s: %+v", apiGroup, resource, r)
	}
}

// ExpectTargetAppliedKinds waits until the kubernetes provider's status for the
// given target is Ready and its AppliedResources are exactly the given kinds.
// Proves status pruning: after a scope change, orphaned kinds drop off.
func ExpectTargetAppliedKinds(ctx context.Context, c client.Client, ns, name, targetID string, kinds ...string) {
	Eventually(func(g Gomega) {
		asr := getASR(ctx, c, ns, name)
		g.Expect(asr).NotTo(BeNil(), "ASR %s/%s not found", ns, name)
		ps, ok := asr.Status.Providers[pmmeta.ProviderKubernetes]
		g.Expect(ok).To(BeTrue(), "kubernetes provider status missing")

		var target *pmmeta.TargetStatus
		for i := range ps.Targets {
			if ps.Targets[i].TargetID == targetID {
				target = &ps.Targets[i]
				break
			}
		}
		g.Expect(target).NotTo(BeNil(), "target %s missing from status", targetID)
		g.Expect(target.Status).To(Equal(pmmeta.TargetStatusReady))

		got := make([]string, 0, len(target.AppliedResources))
		for _, r := range target.AppliedResources {
			got = append(got, r.Kind)
		}
		g.Expect(got).To(ConsistOf(kinds), "applied kinds for target %s", targetID)
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed())
}

// ExpectClusterRoleBinding waits until the named ClusterRoleBinding exists, is
// managed by the pm-controller, references the given ClusterRole, and binds the
// expected Group subject (the ASR's spec.name).
func ExpectClusterRoleBinding(ctx context.Context, c client.Client, name, roleName, subjectGroup string) {
	Eventually(func(g Gomega) {
		var crb rbacv1.ClusterRoleBinding
		g.Expect(c.Get(ctx, types.NamespacedName{Name: name}, &crb)).To(Succeed())
		g.Expect(crb.Labels).To(HaveKeyWithValue(pmapis.LabelManagedByKey, pmapis.ManagedByPMController))
		g.Expect(crb.RoleRef.Kind).To(Equal("ClusterRole"))
		g.Expect(crb.RoleRef.Name).To(Equal(roleName))
		g.Expect(crb.Subjects).To(ContainElement(And(
			HaveField("Kind", "Group"), HaveField("Name", subjectGroup))))
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed())
}

// ExpectRoleBinding waits until the namespaced RoleBinding exists, is managed by
// the pm-controller, and binds the expected Group subject.
func ExpectRoleBinding(ctx context.Context, c client.Client, namespace, name, subjectGroup string) {
	Eventually(func(g Gomega) {
		var rb rbacv1.RoleBinding
		g.Expect(c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &rb)).To(Succeed())
		g.Expect(rb.Labels).To(HaveKeyWithValue(pmapis.LabelManagedByKey, pmapis.ManagedByPMController))
		g.Expect(rb.Subjects).To(ContainElement(And(
			HaveField("Kind", "Group"), HaveField("Name", subjectGroup))))
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed())
}

// ExpectGithubTeam waits until a GithubTeam CR with the given name exists in the
// namespace.
func ExpectGithubTeam(ctx context.Context, c client.Client, namespace, name string) {
	Eventually(func() error {
		return c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &repoguardv1.GithubTeam{})
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed(),
		"GithubTeam %s/%s not created", namespace, name)
}

// ExpectGithubProviderReady waits until the ASR's github provider status is
// Ready (all github targets reached a terminal-good state).
func ExpectGithubProviderReady(ctx context.Context, c client.Client, ns, name string) {
	Eventually(func(g Gomega) {
		asr := getASR(ctx, c, ns, name)
		g.Expect(asr).NotTo(BeNil(), "ASR %s/%s not found", ns, name)
		ps, ok := asr.Status.Providers[pmmeta.ProviderGithub]
		g.Expect(ok).To(BeTrue(), "github provider status missing")
		g.Expect(ps.Ready).To(BeTrue(), "github provider not Ready")
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed())
}

// ExpectGithubTeamRepository waits until a GithubTeamRepository CR exists with the
// given mapped permission (pull/push/admin) and a non-empty repository list.
func ExpectGithubTeamRepository(ctx context.Context, c client.Client, ns, name, permission string) {
	Eventually(func(g Gomega) {
		tr := &repoguardv1.GithubTeamRepository{}
		g.Expect(c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, tr)).To(Succeed())
		g.Expect(string(tr.Spec.Permission)).To(Equal(permission), "team-repo permission")
		g.Expect(tr.Spec.Repository).NotTo(BeEmpty(), "team-repo repository list")
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed())
}

// ExpectGithubOrgOwnerTeam waits until the GithubOrganization CR lists the team
// among its organizationOwnerTeams (the org-ADMIN contribution).
func ExpectGithubOrgOwnerTeam(ctx context.Context, c client.Client, ns, org, team string) {
	Eventually(func(g Gomega) {
		o := &repoguardv1.GithubOrganization{}
		g.Expect(c.Get(ctx, types.NamespacedName{Namespace: ns, Name: org}, o)).To(Succeed())
		g.Expect(o.Spec.OrganizationOwnerTeams).To(ContainElement(team))
	}).WithTimeout(pollTimeout).WithPolling(pollInterval).Should(Succeed())
}
