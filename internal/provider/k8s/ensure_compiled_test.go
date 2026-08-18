// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"encoding/json"
	"testing"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

const (
	testSpecName    = "test-group"
	testCapability  = "READ"
	wildcardCluster = "remote-*"
)

func newClusterStore(t *testing.T, names ...string) cache.Store {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, n := range names {
		require.NoError(t, store.Add(&greenhousev1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: n, Namespace: testNamespace},
		}))
	}
	return store
}

func newCB(t *testing.T, name string, verbs ...string) *v1alpha1.ConnectorBinding {
	t.Helper()
	return newCBRaw(t, name, map[string]any{
		testCapability: []map[string]any{
			{"apiGroups": []string{"*"}, "resources": []string{"*"}, "verbs": verbs},
		},
	})
}

// newCBRaw builds a kubernetes ConnectorBinding from a raw capabilityMapping,
// so tests can supply the structured (apiGroups/resources/verbs) rule form.
func newCBRaw(t *testing.T, name string, capabilityMapping map[string]any) *v1alpha1.ConnectorBinding {
	t.Helper()
	def := map[string]any{"capabilityMapping": capabilityMapping}
	rawBytes, err := json.Marshal(def)
	require.NoError(t, err)
	return &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: v1alpha1.ConnectorBindingSpec{
			Name:       name,
			Provider:   "kubernetes",
			Definition: &runtime.RawExtension{Raw: rawBytes},
		},
	}
}

func Test_EnsureRbacCompiled(t *testing.T) {
	cases := []struct {
		name             string
		permissions      []v1alpha1.Permission
		cbs              []*v1alpha1.ConnectorBinding
		invalidCCRNs     []string
		clusters         []string
		wantTargets      int
		wantClusterRoles map[string]int
		wantRoles        map[string]int
		wantUnknownCap   bool
		wantNoTargets    bool
	}{
		{
			name: "should compile a cluster-scoped pair for a cluster ccrn",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: testCapability},
			},
			cbs:              []*v1alpha1.ConnectorBinding{newCB(t, "cb", "get", "list")},
			clusters:         []string{testClusterName},
			wantTargets:      1,
			wantClusterRoles: map[string]int{testClusterName: 1},
		},
		{
			name: "should compile a namespace-scoped pair for a namespace ccrn",
			permissions: []v1alpha1.Permission{
				{Resource: validNamespaceCCRN, Capability: testCapability},
			},
			cbs:         []*v1alpha1.ConnectorBinding{newCB(t, "cb", "get")},
			clusters:    []string{testClusterName},
			wantTargets: 1,
			wantRoles:   map[string]int{testClusterName: 1},
		},
		{
			name: "should compile cluster and namespace pairs on the same target",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: testCapability},
				{Resource: validNamespaceCCRN, Capability: testCapability},
			},
			cbs:              []*v1alpha1.ConnectorBinding{newCB(t, "cb", "get")},
			clusters:         []string{testClusterName},
			wantTargets:      1,
			wantClusterRoles: map[string]int{testClusterName: 1},
			wantRoles:        map[string]int{testClusterName: 1},
		},
		{
			name: "should fan out a wildcard cluster pattern across matching clusters",
			permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/" + wildcardCluster, Capability: testCapability},
			},
			cbs:              []*v1alpha1.ConnectorBinding{newCB(t, "cb", "get")},
			clusters:         []string{testClusterNameA, testClusterNameB, "other"},
			wantTargets:      2,
			wantClusterRoles: map[string]int{testClusterNameA: 1, testClusterNameB: 1},
		},
		{
			name: "should skip ccrns already marked invalid",
			permissions: []v1alpha1.Permission{
				{Resource: malformedCCRN, Capability: testCapability},
				{Resource: validClusterCCRN, Capability: testCapability},
			},
			cbs:              []*v1alpha1.ConnectorBinding{newCB(t, "cb", "get")},
			invalidCCRNs:     []string{malformedCCRN},
			clusters:         []string{testClusterName},
			wantTargets:      1,
			wantClusterRoles: map[string]int{testClusterName: 1},
		},
		{
			name: "should mark invalid when a capability has no connector mapping",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: "WRITE"},
			},
			cbs:            []*v1alpha1.ConnectorBinding{newCB(t, "cb", "get")},
			clusters:       []string{testClusterName},
			wantTargets:    0,
			wantUnknownCap: true,
		},
		{
			name: "should mark invalid when a cluster pattern matches nothing",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: testCapability},
			},
			cbs:           []*v1alpha1.ConnectorBinding{newCB(t, "cb", "get")},
			clusters:      []string{"other"},
			wantTargets:   0,
			wantNoTargets: true,
		},
		{
			name:        "should produce no targets when permissions are empty",
			permissions: nil,
			cbs:         nil,
			clusters:    []string{testClusterName},
			wantTargets: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Spec: v1alpha1.ApplicationSpecificRoleSpec{
					Name:        testSpecName,
					Permissions: tc.permissions,
				},
			}

			p := &pmk8s.Phase{
				ClusterStore: newClusterStore(t, tc.clusters...),
				AuditLog:     audit.Disabled(),
				Log:          ctrl.Log.WithName("test"),
				CCRNParser:   ccrn.NewParser(nil),
			}
			p.SetCBs(tc.cbs)
			p.SetInvalidCCRNs(tc.invalidCCRNs)

			result, err := p.EnsureRbacCompiled(asr)(context.Background())
			require.NoError(t, err)
			require.Equal(t, lifecycle.Continue(), result)

			compiled := p.Compiled()
			require.Len(t, compiled, tc.wantTargets)

			byCluster := make(map[string]pmk8s.ExportCompiledTarget, len(compiled))
			for _, ct := range compiled {
				byCluster[ct.ClusterName] = ct
			}
			for cluster, want := range tc.wantClusterRoles {
				got, ok := byCluster[cluster]
				require.True(t, ok)
				require.Len(t, got.ClusterRoles, want)
				require.Len(t, got.ClusterRoleBindings, want)
			}
			for cluster, want := range tc.wantRoles {
				got, ok := byCluster[cluster]
				require.True(t, ok)
				require.Len(t, got.Roles, want)
				require.Len(t, got.RoleBindings, want)
			}

			invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
			if tc.wantUnknownCap {
				require.NotNil(t, invalid)
				require.Equal(t, string(pmmeta.UnknownCapabilityReason), invalid.Reason)
			}
			if tc.wantNoTargets {
				require.NotNil(t, invalid)
				require.Equal(t, string(pmmeta.NoTargetsResolvedReason), invalid.Reason)
			}
			if !tc.wantUnknownCap && !tc.wantNoTargets {
				require.Nil(t, invalid)
			}
		})
	}
}

func Test_EnsureRbacCompiled_Verb_Union(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: testSpecName,
			Permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: testCapability},
			},
		},
	}

	p := &pmk8s.Phase{
		ClusterStore: newClusterStore(t, testClusterName),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		CCRNParser:   ccrn.NewParser(nil),
	}
	p.SetCBs([]*v1alpha1.ConnectorBinding{
		newCB(t, "cb-a", "get", "list"),
		newCB(t, "cb-b", "list", "watch"),
	})

	_, err := p.EnsureRbacCompiled(asr)(context.Background())
	require.NoError(t, err)

	compiled := p.Compiled()
	require.Len(t, compiled, 1)
	require.Len(t, compiled[0].ClusterRoles, 1)
	require.Equal(t, []string{"get", "list", "watch"}, compiled[0].ClusterRoles[0].Rules[0].Verbs)
}

// Test_EnsureRbacCompiled_StructuredRules asserts a capability made of multiple
// mini-rules (e.g. sub-resource access) compiles to one PolicyRule per distinct
// (apiGroups, resources) target, deterministically ordered.
func Test_EnsureRbacCompiled_StructuredRules(t *testing.T) {
	operate := newCBRaw(t, "cb-operate", map[string]any{
		"OPERATE": []map[string]any{
			{
				"apiGroups": []string{""},
				"resources": []string{"pods/exec", "pods/portforward", "pods/proxy"},
				"verbs":     []string{"create"},
			},
			{
				"apiGroups": []string{""},
				"resources": []string{"pods/log"},
				"verbs":     []string{"get"},
			},
		},
	})

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: testSpecName,
			Permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: "OPERATE"},
			},
		},
	}

	p := &pmk8s.Phase{
		ClusterStore: newClusterStore(t, testClusterName),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		CCRNParser:   ccrn.NewParser(nil),
	}
	p.SetCBs([]*v1alpha1.ConnectorBinding{operate})

	_, err := p.EnsureRbacCompiled(asr)(context.Background())
	require.NoError(t, err)

	compiled := p.Compiled()
	require.Len(t, compiled, 1)
	require.Len(t, compiled[0].ClusterRoles, 1)

	rules := compiled[0].ClusterRoles[0].Rules
	require.Len(t, rules, 2)

	// Ordered by resources string: "pods/exec,..." before "pods/log".
	require.Equal(t, []string{""}, rules[0].APIGroups)
	require.Equal(t, []string{"pods/exec", "pods/portforward", "pods/proxy"}, rules[0].Resources)
	require.Equal(t, []string{"create"}, rules[0].Verbs)

	require.Equal(t, []string{""}, rules[1].APIGroups)
	require.Equal(t, []string{"pods/log"}, rules[1].Resources)
	require.Equal(t, []string{"get"}, rules[1].Verbs)
}

// Test_EnsureRbacCompiled_MergesRules asserts that referencing two structured
// capabilities on the same cluster produces a single ClusterRole whose rules
// union the distinct targets, with verbs merged per target.
func Test_EnsureRbacCompiled_MergesRules(t *testing.T) {
	read := newCBRaw(t, "cb-read", map[string]any{
		"READ": []map[string]any{
			{"apiGroups": []string{"*"}, "resources": []string{"*"}, "verbs": []string{"get", "list", "watch"}},
		},
	})
	operate := newCBRaw(t, "cb-operate", map[string]any{
		"OPERATE": []map[string]any{
			{
				"apiGroups": []string{""},
				"resources": []string{"pods/exec"},
				"verbs":     []string{"create"},
			},
		},
	})

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: testSpecName,
			Permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: "READ"},
				{Resource: validClusterCCRN, Capability: "OPERATE"},
			},
		},
	}

	p := &pmk8s.Phase{
		ClusterStore: newClusterStore(t, testClusterName),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		CCRNParser:   ccrn.NewParser(nil),
	}
	p.SetCBs([]*v1alpha1.ConnectorBinding{read, operate})

	_, err := p.EnsureRbacCompiled(asr)(context.Background())
	require.NoError(t, err)

	compiled := p.Compiled()
	require.Len(t, compiled, 1)
	require.Len(t, compiled[0].ClusterRoles, 1)

	rules := compiled[0].ClusterRoles[0].Rules
	require.Len(t, rules, 2)

	// "*" sorts before "pods/exec".
	require.Equal(t, []string{"*"}, rules[0].Resources)
	require.Equal(t, []string{"get", "list", "watch"}, rules[0].Verbs)

	require.Equal(t, []string{"pods/exec"}, rules[1].Resources)
	require.Equal(t, []string{"create"}, rules[1].Verbs)
}
