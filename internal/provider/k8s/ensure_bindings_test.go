// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
	"github.com/cloudoperators/permission-manager/internal/lifecycle/mocks"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

func init() {
	if err := v1alpha1.AddToScheme(scheme.Scheme); err != nil {
		panic(err)
	}
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func cbWithProvider(name, provider string, def []byte) *v1alpha1.ConnectorBinding {
	cb := &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: v1alpha1.ConnectorBindingSpec{
			Name:     name,
			Provider: provider,
		},
	}
	if def != nil {
		cb.Spec.Definition = &runtime.RawExtension{Raw: def}
	}
	return cb
}

func Test_EnsureConnectorBindings(t *testing.T) {
	usableDef := mustMarshal(map[string]any{
		"capabilityMapping": map[string]any{
			"cluster": map[string]any{
				"READ": map[string]any{
					"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"k": "v"}}},
					"rules":                []map[string]any{{"apiGroups": []string{"*"}, "resources": []string{"*"}, "verbs": []string{"get"}}},
				},
			},
		},
	})
	emptyMapDef := mustMarshal(map[string]any{"capabilityMapping": map[string]any{}})
	malformedDef := []byte("{not json")

	cases := []struct {
		name              string
		connectorRefs     []string
		seeded            []client.Object
		wantResult        lifecycle.Result
		wantErr           bool
		wantStashed       int
		wantInvalidConfig bool
		wantReason        pmmeta.ConditionReason
	}{
		{
			name:          "should stash a single usable kubernetes connector binding",
			connectorRefs: []string{"cb-ok"},
			seeded: []client.Object{
				cbWithProvider("cb-ok", string(pmmeta.ProviderKubernetes), usableDef),
			},
			wantResult:  lifecycle.Continue(),
			wantStashed: 1,
		},
		{
			name:          "should skip non-kubernetes connector bindings",
			connectorRefs: []string{"cb-gh", "cb-ok"},
			seeded: []client.Object{
				cbWithProvider("cb-gh", "github", usableDef),
				cbWithProvider("cb-ok", string(pmmeta.ProviderKubernetes), usableDef),
			},
			wantResult:  lifecycle.Continue(),
			wantStashed: 1,
		},
		{
			name:          "should skip refs that fail to get and continue with the remaining",
			connectorRefs: []string{"missing", "cb-ok"},
			seeded: []client.Object{
				cbWithProvider("cb-ok", string(pmmeta.ProviderKubernetes), usableDef),
			},
			wantResult:  lifecycle.Continue(),
			wantStashed: 1,
		},
		{
			name:              "should break when no kubernetes connector binding is referenced",
			connectorRefs:     []string{"cb-gh"},
			seeded:            []client.Object{cbWithProvider("cb-gh", "github", usableDef)},
			wantResult:        lifecycle.Break(),
			wantErr:           true,
			wantInvalidConfig: true,
		},
		{
			name:              "should break when every referenced binding fails to get",
			connectorRefs:     []string{"missing"},
			seeded:            nil,
			wantResult:        lifecycle.Break(),
			wantErr:           true,
			wantInvalidConfig: true,
		},
		{
			name:          "should break when every kubernetes binding is unparseable",
			connectorRefs: []string{"cb-bad"},
			seeded: []client.Object{
				cbWithProvider("cb-bad", string(pmmeta.ProviderKubernetes), malformedDef),
			},
			wantResult:        lifecycle.Break(),
			wantErr:           true,
			wantStashed:       1,
			wantInvalidConfig: true,
		},
		{
			name:          "should break when every kubernetes binding has an empty capability mapping",
			connectorRefs: []string{"cb-empty"},
			seeded: []client.Object{
				cbWithProvider("cb-empty", string(pmmeta.ProviderKubernetes), emptyMapDef),
			},
			wantResult:        lifecycle.Break(),
			wantErr:           true,
			wantStashed:       1,
			wantInvalidConfig: true,
		},
		{
			name:          "should break with InvalidConnectorBinding when every binding name is label-unsafe",
			connectorRefs: []string{"cb-badname"},
			seeded: []client.Object{
				&v1alpha1.ConnectorBinding{
					ObjectMeta: metav1.ObjectMeta{Name: "cb-badname", Namespace: testNamespace},
					Spec: v1alpha1.ConnectorBindingSpec{
						Name:       "Bad Name",
						Provider:   string(pmmeta.ProviderKubernetes),
						Definition: &runtime.RawExtension{Raw: usableDef},
					},
				},
			},
			wantResult:        lifecycle.Break(),
			wantErr:           true,
			wantStashed:       1,
			wantInvalidConfig: true,
			wantReason:        pmmeta.InvalidConnectorBindingReason,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Spec: v1alpha1.ApplicationSpecificRoleSpec{
					ConnectorRefs: tc.connectorRefs,
				},
			}

			local := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.seeded...).Build()

			p := &pmk8s.Phase{
				Client: local,
				RemoteClient: func(_ context.Context, _ client.Client, _, _ string) (client.Client, error) {
					return mocks.NewMockClient(t), nil
				},
				AuditLog: audit.Disabled(),
				Log:      ctrl.Log.WithName("test"),
			}

			result, err := p.EnsureConnectorBindings(asr)(context.Background())
			require.Equal(t, tc.wantResult, result)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
			if tc.wantInvalidConfig {
				require.NotNil(t, invalid)
				wantReason := tc.wantReason
				if wantReason == "" {
					wantReason = pmmeta.MissingConnectorBindingReason
				}
				require.Equal(t, string(wantReason), invalid.Reason)
			} else {
				require.Nil(t, invalid)
			}
		})
	}
}

func Test_EnsureConnectorBindings_Revoke_On_Break(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec:       v1alpha1.ApplicationSpecificRoleSpec{ConnectorRefs: []string{"missing"}},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[pmmeta.ProviderType]pmmeta.ProviderStatus{
				pmmeta.ProviderKubernetes: {
					Targets: []pmmeta.TargetStatus{{TargetID: testClusterName, Status: pmmeta.TargetStatusReady}},
				},
			},
		},
	}
	asr.SetCondition(pmmeta.TrueCondition(
		pmmeta.ProvisionedCondition,
		pmmeta.ProvisioningSucceededReason,
		"stale",
	))

	remote := mocks.NewMockClient(t)
	remote.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	p := &pmk8s.Phase{
		Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
		RemoteClient: func(_ context.Context, _ client.Client, _, _ string) (client.Client, error) {
			return remote, nil
		},
		AuditLog: audit.Disabled(),
		Log:      ctrl.Log.WithName("test"),
	}

	result, err := p.EnsureConnectorBindings(asr)(context.Background())
	require.Error(t, err)
	require.Equal(t, lifecycle.Break(), result)

	ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
	require.NotNil(t, ps)
	require.Empty(t, ps.Targets)

	require.Nil(t, pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ProvisionedCondition))
}

func Test_EnsureConnectorBindings_Preserve_Unreachable(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec:       v1alpha1.ApplicationSpecificRoleSpec{ConnectorRefs: []string{"missing"}},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[pmmeta.ProviderType]pmmeta.ProviderStatus{
				pmmeta.ProviderKubernetes: {
					Targets: []pmmeta.TargetStatus{{TargetID: testUnreachableClusterName, Status: pmmeta.TargetStatusReady}},
				},
			},
		},
	}

	p := &pmk8s.Phase{
		Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
		RemoteClient: func(_ context.Context, _ client.Client, _, _ string) (client.Client, error) {
			return nil, errors.New("unreachable")
		},
		AuditLog: audit.Disabled(),
		Log:      ctrl.Log.WithName("test"),
	}

	result, err := p.EnsureConnectorBindings(asr)(context.Background())
	require.Error(t, err)
	require.Equal(t, lifecycle.Break(), result)

	ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
	require.NotNil(t, ps)
	require.Len(t, ps.Targets, 1)
	require.Equal(t, testUnreachableClusterName, ps.Targets[0].TargetID)
	require.Equal(t, pmmeta.TargetStatusError, ps.Targets[0].Status)
}

func Test_EnsureConnectorBindings_ResourceOnlyASR(t *testing.T) {
	const resourceCCRN = "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/apps/deployments"
	const coarseCCRN = "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/remote-cluster"
	const githubCCRN = "urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/github.com/myorg"

	emptyMapDef := mustMarshal(map[string]any{"capabilityMapping": map[string]any{}})

	cases := []struct {
		name        string
		permissions []v1alpha1.Permission
		wantResult  lifecycle.Result
	}{
		{
			name:        "resource-only ASR accepts empty-mapping CB",
			permissions: []v1alpha1.Permission{{Resource: resourceCCRN, Capability: "read"}},
			wantResult:  lifecycle.Continue(),
		},
		{
			name:        "mixed ASR still requires non-empty mapping",
			permissions: []v1alpha1.Permission{{Resource: resourceCCRN, Capability: "read"}, {Resource: coarseCCRN, Capability: "read"}},
			wantResult:  lifecycle.Break(),
		},
		{
			name:        "github plus resource CCRN accepts empty-mapping CB",
			permissions: []v1alpha1.Permission{{Resource: githubCCRN, Capability: "read"}, {Resource: resourceCCRN, Capability: "read"}},
			wantResult:  lifecycle.Continue(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Spec: v1alpha1.ApplicationSpecificRoleSpec{
					ConnectorRefs: []string{"cb-empty"},
					Permissions:   tc.permissions,
				},
			}
			local := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(
				cbWithProvider("cb-empty", string(pmmeta.ProviderKubernetes), emptyMapDef),
			).Build()

			p := &pmk8s.Phase{
				Client: local,
				RemoteClient: func(_ context.Context, _ client.Client, _, _ string) (client.Client, error) {
					return mocks.NewMockClient(t), nil
				},
				AuditLog:   audit.Disabled(),
				Log:        ctrl.Log.WithName("test"),
				CCRNParser: ccrn.NewParser(nil),
			}

			result, _ := p.EnsureConnectorBindings(asr)(context.Background())
			require.Equal(t, tc.wantResult, result)
		})
	}
}

func Test_EnsureSharedClusterRoles_CreatesRootAndChild(t *testing.T) {
	remote := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	cb := newCBRaw(t, map[string]any{
		"cluster": map[string]any{
			"READ": map[string]any{
				"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"rbac.authorization.k8s.io/aggregate-to-view": "true"}}},
				"rules":                []map[string]any{{"apiGroups": []string{""}, "resources": []string{"nodes"}, "verbs": []string{"get", "list", "watch"}}},
			},
			"MANAGE": map[string]any{"clusterRoleName": "cluster-admin"},
		},
	})

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: testSpecName,
			Permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: "READ"},
				{Resource: validClusterCCRN, Capability: "MANAGE"},
			},
		},
	}

	p := &pmk8s.Phase{
		Client:       remote,
		ClusterStore: newClusterStore(t, testClusterName),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		CCRNParser:   ccrn.NewParser(nil),
		RemoteClient: func(_ context.Context, _ client.Client, _, _ string) (client.Client, error) { return remote, nil },
	}
	p.SetCBs([]*v1alpha1.ConnectorBinding{cb})

	_, err := p.EnsureSharedClusterRoles(asr)(context.Background())
	require.NoError(t, err)

	var list rbacv1.ClusterRoleList
	require.NoError(t, remote.List(context.Background(), &list))
	names := map[string]bool{}
	for _, cr := range list.Items {
		names[cr.Name] = true
	}
	require.True(t, names["pm-s-cb-cluster-read"], "root created")
	require.True(t, names["pm-s-cb-cluster-read-baseline"], "child created")
	require.False(t, names["pm-s-cb-cluster-manage"], "MANAGE direct binding creates no shared role")
}

// Test_EnsureSharedClusterRoles_PreservesAggregatedRootRules asserts the
// reconcile never clears the aggregated root's Rules. The Kubernetes
// aggregation controller owns that field; overwriting it with the root's empty
// rules on every reconcile would flap access for every ASR using the root.
func Test_EnsureSharedClusterRoles_PreservesAggregatedRootRules(t *testing.T) {
	aggregated := []rbacv1.PolicyRule{{
		APIGroups: []string{""},
		Resources: []string{"pods"},
		Verbs:     []string{"get", "list", "watch"},
	}}
	root := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pm-s-cb-cluster-read",
			Labels: map[string]string{
				pmapis.LabelManagedByKey: pmapis.ManagedByPMController,
				pmapis.LabelCB:           testCBName,
			},
		},
		AggregationRule: &rbacv1.AggregationRule{ClusterRoleSelectors: []metav1.LabelSelector{
			{MatchLabels: map[string]string{pmapis.LabelDomain + "aggregate-to-cluster-read": "true"}},
		}},
		// Populated by the aggregation controller in a real cluster.
		Rules: aggregated,
	}
	remote := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(root).Build()

	cb := newCBRaw(t, map[string]any{
		"cluster": map[string]any{
			"READ": map[string]any{
				"clusterRoleSelectors": []map[string]any{{"matchLabels": map[string]string{"rbac.authorization.k8s.io/aggregate-to-view": "true"}}},
				"rules":                []map[string]any{{"apiGroups": []string{""}, "resources": []string{"nodes"}, "verbs": []string{"get", "list", "watch"}}},
			},
		},
	})
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:        testSpecName,
			Permissions: []v1alpha1.Permission{{Resource: validClusterCCRN, Capability: "READ"}},
		},
	}

	p := &pmk8s.Phase{
		Client:       remote,
		ClusterStore: newClusterStore(t, testClusterName),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		CCRNParser:   ccrn.NewParser(nil),
		RemoteClient: func(_ context.Context, _ client.Client, _, _ string) (client.Client, error) { return remote, nil },
	}
	p.SetCBs([]*v1alpha1.ConnectorBinding{cb})

	_, err := p.EnsureSharedClusterRoles(asr)(context.Background())
	require.NoError(t, err)

	var got rbacv1.ClusterRole
	require.NoError(t, remote.Get(context.Background(), client.ObjectKey{Name: "pm-s-cb-cluster-read"}, &got))
	require.Equal(t, aggregated, got.Rules, "aggregated root rules must survive the reconcile")
	require.NotNil(t, got.AggregationRule, "root keeps its aggregationRule")
}
