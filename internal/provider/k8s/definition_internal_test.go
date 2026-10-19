// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

func rawDef(t *testing.T, mapping map[string]any) *runtime.RawExtension {
	t.Helper()
	b, err := json.Marshal(map[string]any{"capabilityMapping": mapping})
	require.NoError(t, err)
	return &runtime.RawExtension{Raw: b}
}

func Test_ParseConnectorDefinition_ClosedVocabulary(t *testing.T) {
	ok := rawDef(t, map[string]any{
		"cluster": map[string]any{
			"MANAGE": map[string]any{"clusterRoleName": "cluster-admin"},
			"READ": map[string]any{
				"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"rbac.authorization.k8s.io/aggregate-to-view": "true"}}},
				"rules":                []map[string]any{{"apiGroups": []string{""}, "resources": []string{"nodes"}, "verbs": []string{"get", "list", "watch"}}},
			},
		},
		"namespace": map[string]any{
			"OPERATE": map[string]any{
				"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"x": "y"}}},
			},
		},
	})
	def, err := ParseConnectorDefinition(ok)
	require.NoError(t, err)
	require.Len(t, def.CapabilityMapping.Cluster, 2)
	require.Equal(t, "cluster-admin", def.CapabilityMapping.Cluster["MANAGE"].ClusterRoleName)

	cases := []struct {
		name    string
		mapping map[string]any
	}{
		{"unknown cluster capability", map[string]any{"cluster": map[string]any{"OPERATE": map[string]any{"clusterRoleName": "x"}}}},
		{"clusterRoleName outside cluster MANAGE", map[string]any{"cluster": map[string]any{"READ": map[string]any{"clusterRoleName": "x"}}}},
		{"clusterRoleName in namespace", map[string]any{"namespace": map[string]any{"MANAGE": map[string]any{"clusterRoleName": "x"}}}},
		{"direct and shared together", map[string]any{"cluster": map[string]any{"MANAGE": map[string]any{"clusterRoleName": "x", "rules": []map[string]any{{"apiGroups": []string{""}, "resources": []string{"nodes"}, "verbs": []string{"get"}}}}}}},
		{"shared without selectors", map[string]any{"namespace": map[string]any{"READ": map[string]any{"rules": []map[string]any{{"apiGroups": []string{""}, "resources": []string{"pods"}, "verbs": []string{"get"}}}}}}},
		{"empty entry", map[string]any{"namespace": map[string]any{"READ": map[string]any{}}}},
		{"rule missing verbs", map[string]any{"namespace": map[string]any{"READ": map[string]any{"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"x": "y"}}}, "rules": []map[string]any{{"apiGroups": []string{""}, "resources": []string{"pods"}}}}}}},
		{"non-canonical lowercase cluster key", map[string]any{"cluster": map[string]any{"read": map[string]any{"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"x": "y"}}}}}}},
		{"non-canonical lowercase namespace key", map[string]any{"namespace": map[string]any{"operate": map[string]any{"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"x": "y"}}}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConnectorDefinition(rawDef(t, tc.mapping))
			require.Error(t, err)
		})
	}
}

func Test_ValidateConnectorBinding_Name(t *testing.T) {
	okDef := rawDef(t, map[string]any{
		"cluster": map[string]any{"MANAGE": map[string]any{"clusterRoleName": "cluster-admin"}},
	})
	cases := []struct {
		name    string
		cbName  string
		wantErr bool
	}{
		{"valid dns label", "kubernetes-prod", false},
		{"uppercase and space rejected", "Kubernetes Production", true},
		{"too long rejected", strings.Repeat("a", 64), true},
		{"leading hyphen rejected", "-x", true},
		{"trailing hyphen rejected", "x-", true},
		{"empty rejected", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cb := &v1alpha1.ConnectorBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "cb"},
				Spec:       v1alpha1.ConnectorBindingSpec{Name: tc.cbName, Provider: "kubernetes", Definition: okDef},
			}
			_, err := ValidateConnectorBinding(cb)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_resolveCapability(t *testing.T) {
	mk := func(name string, mapping map[string]any) *v1alpha1.ConnectorBinding {
		return &v1alpha1.ConnectorBinding{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       v1alpha1.ConnectorBindingSpec{Name: name, Provider: "kubernetes", Definition: rawDef(t, mapping)},
		}
	}
	cb1 := mk("cb1", map[string]any{"cluster": map[string]any{"READ": map[string]any{"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"a": "b"}}}}}})
	cb2 := mk("cb2", map[string]any{"cluster": map[string]any{"READ": map[string]any{"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"c": "d"}}}}}})
	cbs := []*v1alpha1.ConnectorBinding{cb1, cb2}

	got, ok := resolveCapability(cbs, scopeCluster, "read")
	require.True(t, ok)
	require.Len(t, got, 2)
	require.Equal(t, "cb1", got[0].cbName)

	_, ok = resolveCapability(cbs, scopeNamespace, "READ")
	require.False(t, ok)
}
