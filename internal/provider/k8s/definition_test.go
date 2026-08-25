// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

// Test_UnionRulesForCapability asserts that capability lookup is
// case-insensitive and an unmapped capability returns found=false.
func Test_UnionRulesForCapability(t *testing.T) {
	cbFull := newCBRaw(t, "full", map[string]any{
		"READ": []map[string]any{
			{"apiGroups": []string{"*"}, "resources": []string{"*"}, "verbs": []string{"get", "list"}},
		},
		"WRITE": []map[string]any{
			{"apiGroups": []string{"apps"}, "resources": []string{"deployments"}, "verbs": []string{"update"}},
		},
	})
	cbs := []*v1alpha1.ConnectorBinding{cbFull}

	// Case-insensitive lookup.
	readRules, ok := pmk8s.UnionRulesForCapability(cbs, "read")
	require.True(t, ok)
	require.Equal(t, []pmk8s.CapabilityRule{{
		APIGroups: []string{"*"},
		Resources: []string{"*"},
		Verbs:     []string{"get", "list"},
	}}, readRules)

	// All three fields explicit.
	writeRules, ok := pmk8s.UnionRulesForCapability(cbs, "WRITE")
	require.True(t, ok)
	require.Equal(t, []pmk8s.CapabilityRule{{
		APIGroups: []string{"apps"},
		Resources: []string{"deployments"},
		Verbs:     []string{"update"},
	}}, writeRules)

	// Unmapped capability.
	_, ok = pmk8s.UnionRulesForCapability(cbs, "ADMIN")
	require.False(t, ok)
}

// Test_ParseConnectorDefinition_ValidatesRules asserts that a rule missing
// apiGroups, resources, or verbs is rejected at parse time.
func Test_ParseConnectorDefinition_ValidatesRules(t *testing.T) {
	cases := []struct {
		name string
		rule map[string]any
	}{
		{
			name: "missing apiGroups",
			rule: map[string]any{"resources": []string{"pods"}, "verbs": []string{"get"}},
		},
		{
			name: "missing resources",
			rule: map[string]any{"apiGroups": []string{"*"}, "verbs": []string{"get"}},
		},
		{
			name: "missing verbs",
			rule: map[string]any{"apiGroups": []string{"*"}, "resources": []string{"*"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cb := newCBRaw(t, "cb", map[string]any{
				"READ": []map[string]any{tc.rule},
			})
			_, found := pmk8s.UnionRulesForCapability([]*v1alpha1.ConnectorBinding{cb}, "READ")
			require.False(t, found, "expected CB with invalid rule to be skipped")
		})
	}
}

// Test_ParseConnectorDefinition_ExcludeResources asserts the validation rules
// specific to deny-driven rules: a valid excludeResources rule parses, while a
// non-wildcard resources list or a selector missing fields is rejected.
func Test_ParseConnectorDefinition_ExcludeResources(t *testing.T) {
	valid := newCBRaw(t, "cb", map[string]any{
		"READ": []map[string]any{{
			"apiGroups": []string{"*"},
			"resources": []string{"*"},
			"verbs":     []string{"get", "list", "watch"},
			"excludeResources": []map[string]any{
				{"apiGroups": []string{""}, "resources": []string{"secrets"}},
			},
		}},
	})
	rules, found := pmk8s.UnionRulesForCapability([]*v1alpha1.ConnectorBinding{valid}, "READ")
	require.True(t, found)
	require.Len(t, rules, 1)
	require.Equal(t, []pmk8s.ResourceSelector{{APIGroups: []string{""}, Resources: []string{"secrets"}}}, rules[0].ExcludeResources)

	invalid := []struct {
		name string
		rule map[string]any
	}{
		{
			name: "excludeResources with non-wildcard resources",
			rule: map[string]any{
				"apiGroups":        []string{"*"},
				"resources":        []string{"pods"},
				"verbs":            []string{"get"},
				"excludeResources": []map[string]any{{"apiGroups": []string{""}, "resources": []string{"secrets"}}},
			},
		},
		{
			name: "selector missing apiGroups",
			rule: map[string]any{
				"apiGroups":        []string{"*"},
				"resources":        []string{"*"},
				"verbs":            []string{"get"},
				"excludeResources": []map[string]any{{"resources": []string{"secrets"}}},
			},
		},
		{
			name: "selector missing resources",
			rule: map[string]any{
				"apiGroups":        []string{"*"},
				"resources":        []string{"*"},
				"verbs":            []string{"get"},
				"excludeResources": []map[string]any{{"apiGroups": []string{""}}},
			},
		},
		{
			name: "selector with wildcard apiGroups",
			rule: map[string]any{
				"apiGroups":        []string{"*"},
				"resources":        []string{"*"},
				"verbs":            []string{"get"},
				"excludeResources": []map[string]any{{"apiGroups": []string{"*"}, "resources": []string{"secrets"}}},
			},
		},
		{
			name: "selector with wildcard resources",
			rule: map[string]any{
				"apiGroups":        []string{"*"},
				"resources":        []string{"*"},
				"verbs":            []string{"get"},
				"excludeResources": []map[string]any{{"apiGroups": []string{""}, "resources": []string{"*"}}},
			},
		},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			cb := newCBRaw(t, "cb", map[string]any{"READ": []map[string]any{tc.rule}})
			_, found := pmk8s.UnionRulesForCapability([]*v1alpha1.ConnectorBinding{cb}, "READ")
			require.False(t, found, "expected CB with invalid excludeResources rule to be skipped")
		})
	}
}
