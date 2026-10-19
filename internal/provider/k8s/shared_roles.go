// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmapis "github.com/cloudoperators/permission-manager/api"
)

// sharedRoleLabels are the ownership labels on every shared ClusterRole,
// keyed to the ConnectorBinding so per-CB orphan cleanup can find them.
func sharedRoleLabels(cbName string) map[string]string {
	return map[string]string{
		pmapis.LabelManagedByKey: pmapis.ManagedByPMController,
		pmapis.LabelCB:           cbName,
	}
}

// buildAggregatedRoot returns the root ClusterRole for a capability. It carries
// only an AggregationRule: the CB selectors plus the auto-derived aggregation
// label that selects the baseline child. Its Rules are owned by the Kubernetes
// aggregation controller and must stay empty.
func buildAggregatedRoot(cbName, scope, capability string, selectors []metav1.LabelSelector) *rbacv1.ClusterRole {
	rules := make([]metav1.LabelSelector, 0, len(selectors)+1)
	rules = append(rules, selectors...)
	rules = append(rules, metav1.LabelSelector{
		MatchLabels: map[string]string{aggregateToLabel(scope, capability): "true"},
	})
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   sharedRoleName(cbName, scope, capability),
			Labels: sharedRoleLabels(cbName),
		},
		AggregationRule: &rbacv1.AggregationRule{ClusterRoleSelectors: rules},
	}
}

// buildBaselineChild returns the baseline child ClusterRole carrying the
// CB-defined rules and the auto-derived aggregation label so the root selects it.
func buildBaselineChild(cbName, scope, capability string, rules []CapabilityRule) *rbacv1.ClusterRole {
	labels := sharedRoleLabels(cbName)
	labels[aggregateToLabel(scope, capability)] = "true"
	set := newPolicyRuleSet()
	for _, r := range rules {
		set.add(r)
	}
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   baselineChildName(cbName, scope, capability),
			Labels: labels,
		},
		Rules: set.toPolicyRules(),
	}
}

// coarseBindingName returns a per-ASR binding name unique per CB/scope/capability
// so one ASR can hold several coarse bindings on one cluster without collision,
// e.g. "pm-a-my-asr-my-cb-cluster-read".
func coarseBindingName(asrName, cbName, scope, capability string) string {
	return perASRNamePrefix + asrName + "-" + cbName + "-" + scope + "-" + strings.ToLower(capability)
}

// buildCoarseClusterRoleBinding binds the ASR's subject group to a ClusterRole
// (a shared pm role, or an external role such as cluster-admin). The binding is
// per-ASR and carries the asr label for per-ASR orphan cleanup.
func buildCoarseClusterRoleBinding(asrName, asrNamespace, specName, bindingName, roleName string) rbacv1.ClusterRoleBinding {
	return rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: bindingName,
			Labels: map[string]string{
				pmapis.LabelManagedByKey:    pmapis.ManagedByPMController,
				pmapis.LabelASR:             asrLabelValue(asrName),
				pmapis.LabelSourceNamespace: asrNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     kindClusterRole,
			Name:     roleName,
		},
		Subjects: []rbacv1.Subject{{Kind: "Group", Name: specName, APIGroup: "rbac.authorization.k8s.io"}},
	}
}

// buildCoarseRoleBinding binds the ASR's subject group to a ClusterRole within a
// single namespace, confining the shared role's rules to that namespace.
func buildCoarseRoleBinding(asrName, asrNamespace, specName, ns, bindingName, clusterRoleName string) rbacv1.RoleBinding {
	return rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingName,
			Namespace: ns,
			Labels: map[string]string{
				pmapis.LabelManagedByKey:    pmapis.ManagedByPMController,
				pmapis.LabelASR:             asrLabelValue(asrName),
				pmapis.LabelSourceNamespace: asrNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     kindClusterRole,
			Name:     clusterRoleName,
		},
		Subjects: []rbacv1.Subject{{Kind: "Group", Name: specName, APIGroup: "rbac.authorization.k8s.io"}},
	}
}
