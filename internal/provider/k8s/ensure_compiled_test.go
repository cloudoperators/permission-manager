// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"encoding/json"
	"testing"

	greenhousemetav1alpha1 "github.com/cloudoperators/greenhouse/api/meta/v1alpha1"
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
	testCBName      = "cb"
	wildcardCluster = "remote-*"
)

func newClusterStore(t *testing.T, names ...string) cache.Store {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, n := range names {
		require.NoError(t, store.Add(newCluster(n, true)))
	}
	return store
}

// newCluster builds a greenhouse Cluster with a Ready condition set to
// True/False per ready.
func newCluster(name string, ready bool) *greenhousev1alpha1.Cluster {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	c := &greenhousev1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
	}
	c.Status.SetConditions(greenhousemetav1alpha1.Condition{
		Type:   greenhousemetav1alpha1.ReadyCondition,
		Status: status,
	})
	return c
}

func newCB(t *testing.T, verbs ...string) *v1alpha1.ConnectorBinding {
	t.Helper()
	rule := map[string]any{"apiGroups": []string{"*"}, "resources": []string{"*"}, "verbs": verbs}
	return newCBRaw(t, map[string]any{
		"cluster": map[string]any{
			testCapability: map[string]any{
				"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"k": "v"}}},
				"rules":                []map[string]any{rule},
			},
		},
		"namespace": map[string]any{
			testCapability: map[string]any{
				"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"k": "v"}}},
				"rules":                []map[string]any{rule},
			},
		},
	})
}

// newCBRaw builds a kubernetes ConnectorBinding from a raw capabilityMapping,
// so tests can supply the structured (apiGroups/resources/verbs) rule form.
func newCBRaw(t *testing.T, capabilityMapping map[string]any) *v1alpha1.ConnectorBinding {
	t.Helper()
	def := map[string]any{"capabilityMapping": capabilityMapping}
	rawBytes, err := json.Marshal(def)
	require.NoError(t, err)
	return &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: testCBName, Namespace: testNamespace},
		Spec: v1alpha1.ConnectorBindingSpec{
			Name:       testCBName,
			Provider:   "kubernetes",
			Definition: &runtime.RawExtension{Raw: rawBytes},
		},
	}
}

func Test_EnsureRbacCompiled(t *testing.T) {
	const clusterResourceRead = "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/apps/deployments"

	cases := []struct {
		name           string
		permissions    []v1alpha1.Permission
		cbs            []*v1alpha1.ConnectorBinding
		invalidCCRNs   []string
		clusters       []string
		wantTargets    int
		wantUnknownCap bool
		wantNoTargets  bool
		assert         func(t *testing.T, byCluster map[string]pmk8s.ExportCompiledTarget)
	}{
		{
			name: "cluster ccrn compiles to a coarse ClusterRoleBinding",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: testCapability},
			},
			cbs:         []*v1alpha1.ConnectorBinding{newCB(t, "get", "list")},
			clusters:    []string{testClusterName},
			wantTargets: 1,
			assert: func(t *testing.T, byCluster map[string]pmk8s.ExportCompiledTarget) {
				c := byCluster[testClusterName]
				require.Empty(t, c.ClusterRoles)
				require.Len(t, c.CoarseClusterRoleBindings, 1)
				require.Equal(t, "pm-a-test-asr-cb-cluster-read", c.CoarseClusterRoleBindings[0].Name)
				require.Equal(t, "pm-s-cb-cluster-read", c.CoarseClusterRoleBindings[0].RoleRef.Name)
			},
		},
		{
			name: "namespace ccrn compiles to a coarse RoleBinding to a ClusterRole",
			permissions: []v1alpha1.Permission{
				{Resource: validNamespaceCCRN, Capability: testCapability},
			},
			cbs:         []*v1alpha1.ConnectorBinding{newCB(t, "get")},
			clusters:    []string{testClusterName},
			wantTargets: 1,
			assert: func(t *testing.T, byCluster map[string]pmk8s.ExportCompiledTarget) {
				c := byCluster[testClusterName]
				require.Empty(t, c.Roles)
				require.Len(t, c.CoarseRoleBindings, 1)
				require.Equal(t, "demo", c.CoarseRoleBindings[0].Namespace)
				require.Equal(t, "ClusterRole", c.CoarseRoleBindings[0].RoleRef.Kind)
				require.Equal(t, "pm-s-cb-namespace-read", c.CoarseRoleBindings[0].RoleRef.Name)
			},
		},
		{
			name: "cluster and namespace coarse bindings on the same target",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: testCapability},
				{Resource: validNamespaceCCRN, Capability: testCapability},
			},
			cbs:         []*v1alpha1.ConnectorBinding{newCB(t, "get")},
			clusters:    []string{testClusterName},
			wantTargets: 1,
			assert: func(t *testing.T, byCluster map[string]pmk8s.ExportCompiledTarget) {
				c := byCluster[testClusterName]
				require.Len(t, c.CoarseClusterRoleBindings, 1)
				require.Len(t, c.CoarseRoleBindings, 1)
			},
		},
		{
			name: "resource ccrn compiles a per-ASR ClusterRole",
			permissions: []v1alpha1.Permission{
				{Resource: clusterResourceRead, Capability: "READ"},
			},
			clusters:    []string{testClusterName},
			wantTargets: 1,
			assert: func(t *testing.T, byCluster map[string]pmk8s.ExportCompiledTarget) {
				c := byCluster[testClusterName]
				require.Len(t, c.ClusterRoles, 1)
				require.Equal(t, "pm-a-test-asr", c.ClusterRoles[0].Name)
				require.Empty(t, c.CoarseClusterRoleBindings)
			},
		},
		{
			name: "fans out a wildcard cluster pattern across matching clusters",
			permissions: []v1alpha1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/" + wildcardCluster, Capability: testCapability},
			},
			cbs:         []*v1alpha1.ConnectorBinding{newCB(t, "get")},
			clusters:    []string{testClusterNameA, testClusterNameB, "other"},
			wantTargets: 2,
			assert: func(t *testing.T, byCluster map[string]pmk8s.ExportCompiledTarget) {
				require.Len(t, byCluster[testClusterNameA].CoarseClusterRoleBindings, 1)
				require.Len(t, byCluster[testClusterNameB].CoarseClusterRoleBindings, 1)
			},
		},
		{
			name: "skips ccrns already marked invalid",
			permissions: []v1alpha1.Permission{
				{Resource: malformedCCRN, Capability: testCapability},
				{Resource: validClusterCCRN, Capability: testCapability},
			},
			cbs:          []*v1alpha1.ConnectorBinding{newCB(t, "get")},
			invalidCCRNs: []string{malformedCCRN},
			clusters:     []string{testClusterName},
			wantTargets:  1,
			assert: func(t *testing.T, byCluster map[string]pmk8s.ExportCompiledTarget) {
				require.Len(t, byCluster[testClusterName].CoarseClusterRoleBindings, 1)
			},
		},
		{
			name: "marks invalid when a capability has no connector mapping",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: "WRITE"},
			},
			cbs:            []*v1alpha1.ConnectorBinding{newCB(t, "get")},
			clusters:       []string{testClusterName},
			wantTargets:    0,
			wantUnknownCap: true,
		},
		{
			name: "marks invalid when a cluster pattern matches nothing",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: testCapability},
			},
			cbs:           []*v1alpha1.ConnectorBinding{newCB(t, "get")},
			clusters:      []string{"other"},
			wantTargets:   0,
			wantNoTargets: true,
		},
		{
			name:        "produces no targets when permissions are empty",
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
			if tc.assert != nil {
				tc.assert(t, byCluster)
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

// Test_EnsureRbacCompiled_PartialSuccess covers mixed valid/invalid clusters:
// Ready clusters compile while missing or not-ready ones are skipped without
// hard-failing the ASR, unless nothing usable resolves.
func Test_EnsureRbacCompiled_PartialSuccess(t *testing.T) {
	const readyCluster = "ready-1"
	const notReadyCluster = "not-ready-1"
	const missingCluster = "missing-1"

	ccrnFor := func(name string) string {
		return "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/" + name
	}

	cases := []struct {
		name         string
		clusters     []*greenhousev1alpha1.Cluster
		permClusters []string
		wantCompiled []string
		wantInvalid  bool
		wantReason   pmmeta.ConditionReason
	}{
		{
			name:         "valid cluster compiles while a missing one is skipped",
			clusters:     []*greenhousev1alpha1.Cluster{newCluster(readyCluster, true)},
			permClusters: []string{readyCluster, missingCluster},
			wantCompiled: []string{readyCluster},
			wantInvalid:  false,
		},
		{
			name:         "valid cluster compiles while a not-ready one is skipped",
			clusters:     []*greenhousev1alpha1.Cluster{newCluster(readyCluster, true), newCluster(notReadyCluster, false)},
			permClusters: []string{readyCluster, notReadyCluster},
			wantCompiled: []string{readyCluster},
			wantInvalid:  false,
		},
		{
			name:         "all missing clusters yield NoTargetsResolved",
			clusters:     nil,
			permClusters: []string{missingCluster},
			wantCompiled: nil,
			wantInvalid:  true,
			wantReason:   pmmeta.NoTargetsResolvedReason,
		},
		{
			name:         "all not-ready clusters yield NoTargetsResolved",
			clusters:     []*greenhousev1alpha1.Cluster{newCluster(notReadyCluster, false)},
			permClusters: []string{notReadyCluster},
			wantCompiled: nil,
			wantInvalid:  true,
			wantReason:   pmmeta.NoTargetsResolvedReason,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := cache.NewStore(cache.MetaNamespaceKeyFunc)
			for _, c := range tc.clusters {
				require.NoError(t, store.Add(c))
			}

			perms := make([]v1alpha1.Permission, 0, len(tc.permClusters))
			for _, name := range tc.permClusters {
				perms = append(perms, v1alpha1.Permission{Resource: ccrnFor(name), Capability: testCapability})
			}
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: testSpecName, Permissions: perms},
			}

			p := &pmk8s.Phase{
				ClusterStore: store,
				AuditLog:     audit.Disabled(),
				Log:          ctrl.Log.WithName("test"),
				CCRNParser:   ccrn.NewParser(nil),
			}
			p.SetCBs([]*v1alpha1.ConnectorBinding{newCB(t, "get")})

			_, err := p.EnsureRbacCompiled(asr)(context.Background())
			require.NoError(t, err)

			var compiledNames []string
			for _, ct := range p.Compiled() {
				compiledNames = append(compiledNames, ct.ClusterName)
			}
			require.ElementsMatch(t, tc.wantCompiled, compiledNames)

			invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
			if tc.wantInvalid {
				require.NotNil(t, invalid)
				require.Equal(t, string(tc.wantReason), invalid.Reason)
			} else {
				require.Nil(t, invalid)
			}
		})
	}
}

// Test_EnsureRbacCompiled_InvalidCapabilityKeepsSibling asserts that an
// unsupported capability on a resource CCRN invalidates only that permission:
// a valid READ on the same resource string still compiles, while the ASR is
// still flagged InvalidConfig for the rejected capability.
func Test_EnsureRbacCompiled_InvalidCapabilityKeepsSibling(t *testing.T) {
	const resourceCCRN = "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/apps/deployments"
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: testSpecName,
			Permissions: []v1alpha1.Permission{
				{Resource: resourceCCRN, Capability: "READ"},
				{Resource: resourceCCRN, Capability: "admin"},
			},
		},
	}

	p := &pmk8s.Phase{
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		ClusterStore: newClusterStore(t, testClusterName),
		CCRNParser:   ccrn.NewParser(nil),
	}

	// The validator flags the unsupported capability without poisoning the resource.
	_, err := p.EnsureASRValidity(asr)(context.Background())
	require.NoError(t, err)
	invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
	require.NotNil(t, invalid)
	require.Equal(t, string(pmmeta.InvalidCCRNReason), invalid.Reason)

	// The compiler still produces the valid READ grant; the bad capability contributes nothing.
	_, err = p.EnsureRbacCompiled(asr)(context.Background())
	require.NoError(t, err)
	compiled := p.Compiled()
	require.Len(t, compiled, 1)
	require.Len(t, compiled[0].ClusterRoles, 1)
	rules := compiled[0].ClusterRoles[0].Rules
	require.Len(t, rules, 1)
	require.Equal(t, []string{"apps"}, rules[0].APIGroups)
	require.Equal(t, []string{"deployments"}, rules[0].Resources)
	require.Equal(t, []string{"get", "list", "watch"}, rules[0].Verbs)
}

func Test_EnsureRbacCompiled_ResourceCCRNs(t *testing.T) {
	const (
		clusterResourceRead  = "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/apps/deployments"
		clusterResourceWrite = "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/apps/deployments"
		nsResourceRead       = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/v1/pods:log"
	)

	t.Run("cluster-resource READ compiles a ClusterRole with get/list/watch and no CB", func(t *testing.T) {
		asr := &v1alpha1.ApplicationSpecificRole{
			ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
			Spec: v1alpha1.ApplicationSpecificRoleSpec{
				Name:        testSpecName,
				Permissions: []v1alpha1.Permission{{Resource: clusterResourceRead, Capability: "READ"}},
			},
		}
		p := &pmk8s.Phase{
			AuditLog:     audit.Disabled(),
			Log:          ctrl.Log.WithName("test"),
			ClusterStore: newClusterStore(t, testClusterName),
			CCRNParser:   ccrn.NewParser(nil),
		}
		result, err := p.EnsureRbacCompiled(asr)(context.Background())
		require.NoError(t, err)
		require.Equal(t, lifecycle.Continue(), result)

		compiled := p.Compiled()
		require.Len(t, compiled, 1)
		require.Len(t, compiled[0].ClusterRoles, 1)
		rules := compiled[0].ClusterRoles[0].Rules
		require.Len(t, rules, 1)
		require.Equal(t, []string{"apps"}, rules[0].APIGroups)
		require.Equal(t, []string{"deployments"}, rules[0].Resources)
		require.Equal(t, []string{"get", "list", "watch"}, rules[0].Verbs)
	})

	t.Run("READ+WRITE on same target union into one PolicyRule", func(t *testing.T) {
		asr := &v1alpha1.ApplicationSpecificRole{
			ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
			Spec: v1alpha1.ApplicationSpecificRoleSpec{
				Name: testSpecName,
				Permissions: []v1alpha1.Permission{
					{Resource: clusterResourceRead, Capability: "READ"},
					{Resource: clusterResourceWrite, Capability: "WRITE"},
				},
			},
		}
		p := &pmk8s.Phase{
			AuditLog:     audit.Disabled(),
			Log:          ctrl.Log.WithName("test"),
			ClusterStore: newClusterStore(t, testClusterName),
			CCRNParser:   ccrn.NewParser(nil),
		}
		_, err := p.EnsureRbacCompiled(asr)(context.Background())
		require.NoError(t, err)
		compiled := p.Compiled()
		require.Len(t, compiled, 1)
		require.Len(t, compiled[0].ClusterRoles, 1)
		rules := compiled[0].ClusterRoles[0].Rules
		require.Len(t, rules, 1)
		require.Equal(t, []string{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"}, rules[0].Verbs)
	})

	t.Run("namespace-resource with subresource compiles a Role with resource/subresource", func(t *testing.T) {
		asr := &v1alpha1.ApplicationSpecificRole{
			ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
			Spec: v1alpha1.ApplicationSpecificRoleSpec{
				Name:        testSpecName,
				Permissions: []v1alpha1.Permission{{Resource: nsResourceRead, Capability: "READ"}},
			},
		}
		p := &pmk8s.Phase{
			AuditLog:     audit.Disabled(),
			Log:          ctrl.Log.WithName("test"),
			ClusterStore: newClusterStore(t, testClusterName),
			CCRNParser:   ccrn.NewParser(nil),
		}
		_, err := p.EnsureRbacCompiled(asr)(context.Background())
		require.NoError(t, err)
		compiled := p.Compiled()
		require.Len(t, compiled, 1)
		require.Len(t, compiled[0].Roles, 1)
		require.Equal(t, testNamespace, compiled[0].Roles[0].Namespace)
		rules := compiled[0].Roles[0].Rules
		require.Len(t, rules, 1)
		require.Equal(t, []string{""}, rules[0].APIGroups)
		require.Equal(t, []string{"pods/log"}, rules[0].Resources)
	})

	t.Run("comma resource CCRN is dropped without colliding with a sibling READ", func(t *testing.T) {
		// A comma in a resource token is rejected at parse; it must not reach the
		// comma-joined rule merge key and leak verbs onto the real resources.
		const commaCCRN = "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/v1/pods,secrets"
		asr := &v1alpha1.ApplicationSpecificRole{
			ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
			Spec: v1alpha1.ApplicationSpecificRoleSpec{
				Name: testSpecName,
				Permissions: []v1alpha1.Permission{
					{Resource: commaCCRN, Capability: "READ"},
					{Resource: clusterResourceRead, Capability: "READ"},
				},
			},
		}
		p := &pmk8s.Phase{
			AuditLog:     audit.Disabled(),
			Log:          ctrl.Log.WithName("test"),
			ClusterStore: newClusterStore(t, testClusterName),
			CCRNParser:   ccrn.NewParser(nil),
		}
		_, err := p.EnsureRbacCompiled(asr)(context.Background())
		require.NoError(t, err)
		compiled := p.Compiled()
		require.Len(t, compiled, 1)
		require.Len(t, compiled[0].ClusterRoles, 1)
		rules := compiled[0].ClusterRoles[0].Rules
		require.Len(t, rules, 1)
		require.Equal(t, []string{"apps"}, rules[0].APIGroups)
		require.Equal(t, []string{"deployments"}, rules[0].Resources)
		require.Equal(t, []string{"get", "list", "watch"}, rules[0].Verbs)
	})
}
