// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmapis "github.com/cloudoperators/permission-manager/api"
)

func Test_buildAggregatedRoot(t *testing.T) {
	sel := []metav1.LabelSelector{{MatchLabels: map[string]string{"rbac.authorization.k8s.io/aggregate-to-view": "true"}}}
	root := buildAggregatedRoot("cb", scopeCluster, "READ", sel)

	require.Equal(t, "pm-cb-cluster-read", root.Name)
	require.Nil(t, root.Rules)
	require.NotNil(t, root.AggregationRule)
	require.Len(t, root.AggregationRule.ClusterRoleSelectors, 2)
	require.Equal(t, "cb", root.Labels[pmapis.LabelCB])
	require.Equal(t, "true",
		root.AggregationRule.ClusterRoleSelectors[1].MatchLabels["permission-manager.cloudoperators.dev/aggregate-to-cluster-read"])
}

func Test_buildBaselineChild(t *testing.T) {
	child := buildBaselineChild("cb", scopeNamespace, "OPERATE", []CapabilityRule{
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "list"}},
	})
	require.Equal(t, "pm-cb-namespace-operate-baseline", child.Name)
	require.Equal(t, "true", child.Labels["permission-manager.cloudoperators.dev/aggregate-to-namespace-operate"])
	require.Len(t, child.Rules, 1)
	require.Equal(t, []string{"get", "list"}, child.Rules[0].Verbs)
}

// Test_buildBaselineChild_StructuredRules asserts that multiple mini-rules
// compile to one PolicyRule per distinct (apiGroups, resources) target,
// deterministically ordered by resources.
func Test_buildBaselineChild_StructuredRules(t *testing.T) {
	child := buildBaselineChild("cb", scopeCluster, "READ", []CapabilityRule{
		{APIGroups: []string{""}, Resources: []string{"pods/exec", "pods/portforward", "pods/proxy"}, Verbs: []string{"create"}},
		{APIGroups: []string{""}, Resources: []string{"pods/log"}, Verbs: []string{"get"}},
	})
	require.Len(t, child.Rules, 2)

	// Ordered by resources string: "pods/exec,..." before "pods/log".
	require.Equal(t, []string{"pods/exec", "pods/portforward", "pods/proxy"}, child.Rules[0].Resources)
	require.Equal(t, []string{"create"}, child.Rules[0].Verbs)
	require.Equal(t, []string{"pods/log"}, child.Rules[1].Resources)
	require.Equal(t, []string{"get"}, child.Rules[1].Verbs)
}

func Test_coarseBindingName(t *testing.T) {
	require.Equal(t, "pm-my-asr-my-cb-cluster-read", coarseBindingName("my-asr", "my-cb", scopeCluster, "READ"))
}
