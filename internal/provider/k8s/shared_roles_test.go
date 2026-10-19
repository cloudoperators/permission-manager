// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmapis "github.com/cloudoperators/permission-manager/api"
)

func Test_buildAggregatedRoot(t *testing.T) {
	sel := []metav1.LabelSelector{{MatchLabels: map[string]string{"rbac.authorization.k8s.io/aggregate-to-view": "true"}}}
	root := buildAggregatedRoot("cb", scopeCluster, "READ", sel)

	require.Equal(t, "pm-s-cb-cluster-read", root.Name)
	require.Nil(t, root.Rules)
	require.NotNil(t, root.AggregationRule)
	require.Len(t, root.AggregationRule.ClusterRoleSelectors, 3)
	require.Equal(t, "cb", root.Labels[pmapis.LabelCB])
	require.Equal(t, "true",
		root.AggregationRule.ClusterRoleSelectors[1].MatchLabels["permission-manager.cloudoperators.dev/aggregate-to-cluster-read"])
}

func Test_buildBaselineChild(t *testing.T) {
	child := buildBaselineChild("cb", scopeNamespace, "OPERATE", []CapabilityRule{
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "list"}},
	})
	require.Equal(t, "pm-s-cb-namespace-operate-baseline", child.Name)
	require.Equal(t, "true", child.Labels[cbAggregateToLabel("cb", scopeNamespace, "OPERATE")])
	require.Len(t, child.Rules, 1)
	require.Equal(t, []string{"get", "list"}, child.Rules[0].Verbs)
}

// Test_CBPrivateAggregation asserts that two CBs defining the same scope/cap do
// not cross-pollinate: each baseline carries only its own CB-private label, and
// a CB's root selects the shared label plus its own private label, never the
// other CB's private label.
func Test_CBPrivateAggregation(t *testing.T) {
	const scope, capability = scopeCluster, "READ"
	shared := aggregateToLabel(scope, capability)
	labelA := cbAggregateToLabel("cb-a", scope, capability)
	labelB := cbAggregateToLabel("cb-b", scope, capability)
	require.NotEqual(t, labelA, labelB, "CB-private labels must differ per CB")

	childA := buildBaselineChild("cb-a", scope, capability, nil)
	require.Equal(t, "true", childA.Labels[labelA], "baseline carries its own CB-private label")
	require.NotContains(t, childA.Labels, shared, "baseline must not carry the shared team-extension label")
	require.NotContains(t, childA.Labels, labelB, "baseline must not carry another CB's label")

	rootA := buildAggregatedRoot("cb-a", scope, capability, []metav1.LabelSelector{
		{MatchLabels: map[string]string{"rbac.authorization.k8s.io/aggregate-to-view": "true"}},
	})
	selected := map[string]bool{}
	for _, sel := range rootA.AggregationRule.ClusterRoleSelectors {
		for k := range sel.MatchLabels {
			selected[k] = true
		}
	}
	require.True(t, selected[shared], "root selects the shared team-extension label")
	require.True(t, selected[labelA], "root selects its own CB-private label")
	require.True(t, selected["rbac.authorization.k8s.io/aggregate-to-view"], "root keeps the CB selectors")
	require.False(t, selected[labelB], "root must never select another CB's label")
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
	name := coarseBindingName("my-ns", "my-asr", "my-cb", scopeCluster, "READ")
	require.True(t, strings.HasPrefix(name, "pm-a-my-asr-my-cb-cluster-read-"),
		"readable prefix preserved, got %q", name)
}

// Test_coarseBindingName_FieldBoundary asserts the ShortHash suffix keeps two
// coarse bindings distinct when their readable parts collide but the ASR/CB
// field boundary differs, so one ASR's binding cannot silently overwrite
// another's.
func Test_coarseBindingName_FieldBoundary(t *testing.T) {
	require.NotEqual(t,
		coarseBindingName("ns", "a-b", "c", scopeCluster, "READ"),
		coarseBindingName("ns", "a", "b-c", scopeCluster, "READ"))
}

// Test_NamePrefixesDisjoint asserts shared and per-ASR names carry disjoint
// prefixes and never collide, even for an adversarial ASR name chosen to mimic
// a shared-role name tail.
func Test_NamePrefixesDisjoint(t *testing.T) {
	shared := sharedRoleName("cb", scopeNamespace, "MANAGE")
	require.Equal(t, "pm-s-cb-namespace-manage", shared)
	require.True(t, strings.HasPrefix(shared, sharedNamePrefix))

	// An ASR literally named "cb-namespace-manage" compiles a per-ASR role that,
	// under a shared prefix, would read like the shared role above.
	perASR := perASRNamePrefix + "cb-namespace-manage"
	require.True(t, strings.HasPrefix(perASR, perASRNamePrefix))
	require.NotEqual(t, shared, perASR)
	require.False(t, strings.HasPrefix(perASR, sharedNamePrefix))
	require.False(t, strings.HasPrefix(shared, perASRNamePrefix))
}
