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
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

const (
	testSpecName    = "test-group"
	testCapability  = "READ"
	wildcardCluster = "remote-*"
)

func newClusterStore(t *testing.T, namespace string, names ...string) cache.Store {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, n := range names {
		require.NoError(t, store.Add(&greenhousev1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: n, Namespace: namespace},
		}))
	}
	return store
}

func newCB(name string, verbs ...string) *v1alpha1.ConnectorBinding {
	def := map[string]any{
		"capabilityMapping": map[string][]string{
			testCapability: verbs,
		},
	}
	rawBytes, _ := json.Marshal(def)
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
			cbs:              []*v1alpha1.ConnectorBinding{newCB("cb", "get", "list")},
			clusters:         []string{testClusterName},
			wantTargets:      1,
			wantClusterRoles: map[string]int{testClusterName: 1},
		},
		{
			name: "should compile a namespace-scoped pair for a namespace ccrn",
			permissions: []v1alpha1.Permission{
				{Resource: validNamespaceCCRN, Capability: testCapability},
			},
			cbs:         []*v1alpha1.ConnectorBinding{newCB("cb", "get")},
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
			cbs:              []*v1alpha1.ConnectorBinding{newCB("cb", "get")},
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
			cbs:              []*v1alpha1.ConnectorBinding{newCB("cb", "get")},
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
			cbs:              []*v1alpha1.ConnectorBinding{newCB("cb", "get")},
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
			cbs:            []*v1alpha1.ConnectorBinding{newCB("cb", "get")},
			clusters:       []string{testClusterName},
			wantTargets:    0,
			wantUnknownCap: true,
		},
		{
			name: "should mark invalid when a cluster pattern matches nothing",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: testCapability},
			},
			cbs:           []*v1alpha1.ConnectorBinding{newCB("cb", "get")},
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
				ClusterStore: newClusterStore(t, testNamespace, tc.clusters...),
				AuditLog:     audit.Disabled(),
				Log:          ctrl.Log.WithName("test"),
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
		ClusterStore: newClusterStore(t, testNamespace, testClusterName),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
	}
	p.SetCBs([]*v1alpha1.ConnectorBinding{
		newCB("cb-a", "get", "list"),
		newCB("cb-b", "list", "watch"),
	})

	_, err := p.EnsureRbacCompiled(asr)(context.Background())
	require.NoError(t, err)

	compiled := p.Compiled()
	require.Len(t, compiled, 1)
	require.Len(t, compiled[0].ClusterRoles, 1)
	require.Equal(t, []string{"get", "list", "watch"}, compiled[0].ClusterRoles[0].Rules[0].Verbs)
}
