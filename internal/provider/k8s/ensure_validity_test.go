// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

const (
	validClusterCCRN      = "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/remote-cluster"
	validNamespaceCCRN    = "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo"
	wildcardNamespaceCCRN = "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/*"
	malformedCCRN         = "not-a-urn"
)

func Test_EnsureASRValidity(t *testing.T) {
	cases := []struct {
		name              string
		permissions       []v1alpha1.Permission
		wantInvalidConfig bool
		wantReadyFalse    bool
		wantReason        pmmeta.ConditionReason
	}{
		{
			name: "should pass when all kubernetes ccrns are valid",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: "read"},
				{Resource: validNamespaceCCRN, Capability: "write"},
			},
		},
		{
			name: "should mark invalid when a ccrn fails to parse",
			permissions: []v1alpha1.Permission{
				{Resource: malformedCCRN, Capability: "read"},
			},
			wantInvalidConfig: true,
			wantReadyFalse:    true,
			wantReason:        pmmeta.InvalidCCRNReason,
		},
		{
			name: "should mark invalid when a kubernetes ccrn has a wildcard namespace",
			permissions: []v1alpha1.Permission{
				{Resource: wildcardNamespaceCCRN, Capability: "read"},
			},
			wantInvalidConfig: true,
			wantReadyFalse:    true,
			wantReason:        pmmeta.InvalidCCRNReason,
		},
		{
			name: "should mark invalid when one of many ccrns is invalid",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: "read"},
				{Resource: malformedCCRN, Capability: "read"},
				{Resource: validNamespaceCCRN, Capability: "write"},
			},
			wantInvalidConfig: true,
			wantReadyFalse:    true,
			wantReason:        pmmeta.InvalidCCRNReason,
		},
		{
			name:        "should pass when permissions are empty",
			permissions: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Spec:       v1alpha1.ApplicationSpecificRoleSpec{Permissions: tc.permissions},
			}

			p := &pmk8s.Phase{
				AuditLog:   audit.Disabled(),
				Log:        ctrl.Log.WithName("test"),
				CCRNParser: ccrn.NewParser(nil),
			}

			result, err := p.EnsureASRValidity(asr)(context.Background())
			require.NoError(t, err)
			require.Equal(t, lifecycle.Continue(), result)

			invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
			ready := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ReadyCondition)

			if tc.wantInvalidConfig {
				require.NotNil(t, invalid)
				require.Equal(t, metav1.ConditionTrue, invalid.Status)
				require.Equal(t, string(tc.wantReason), invalid.Reason)
			} else {
				require.Nil(t, invalid)
			}

			if tc.wantReadyFalse {
				require.NotNil(t, ready)
				require.Equal(t, metav1.ConditionFalse, ready.Status)
				require.Equal(t, string(tc.wantReason), ready.Reason)
			}
		})
	}
}

func Test_EnsureASRValidity_Remove_Stale_Condition(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Permissions: []v1alpha1.Permission{{Resource: validClusterCCRN, Capability: "read"}},
		},
	}
	asr.SetCondition(pmmeta.TrueCondition(
		pmmeta.InvalidConfigCondition,
		pmmeta.InvalidCCRNReason,
		"invalid CCRNs: [stale]",
	))

	p := &pmk8s.Phase{
		AuditLog:   audit.Disabled(),
		Log:        ctrl.Log.WithName("test"),
		CCRNParser: ccrn.NewParser(nil),
	}

	result, err := p.EnsureASRValidity(asr)(context.Background())
	require.NoError(t, err)
	require.Equal(t, lifecycle.Continue(), result)
	require.Nil(t, pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition))
}

func Test_EnsureASRValidity_CustomCCRNGroup(t *testing.T) {
	customGroupCluster := "urn:ccrn:cluster.k8s.ccrn.mycompany.dev/v1/remote-cluster"
	customGroupNamespace := "urn:ccrn:namespace.k8s.ccrn.mycompany.dev/v1/remote-cluster/demo"

	cases := []struct {
		name              string
		permissions       []v1alpha1.Permission
		wantInvalidConfig bool
	}{
		{
			name: "custom group cluster CCRN accepted",
			permissions: []v1alpha1.Permission{
				{Resource: customGroupCluster, Capability: "read"},
			},
		},
		{
			name: "custom group namespace CCRN accepted",
			permissions: []v1alpha1.Permission{
				{Resource: customGroupNamespace, Capability: "write"},
			},
		},
		{
			name: "custom group alongside default group",
			permissions: []v1alpha1.Permission{
				{Resource: validClusterCCRN, Capability: "read"},
				{Resource: customGroupCluster, Capability: "read"},
			},
		},
		{
			name: "unknown group rejected without custom parser",
			permissions: []v1alpha1.Permission{
				{Resource: customGroupCluster, Capability: "read"},
			},
			wantInvalidConfig: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Spec:       v1alpha1.ApplicationSpecificRoleSpec{Permissions: tc.permissions},
			}

			p := &pmk8s.Phase{
				AuditLog: audit.Disabled(),
				Log:      ctrl.Log.WithName("test"),
			}
			// Use a custom parser with the extra group for acceptance cases;
			// use only the default domain for the rejection case.
			if !tc.wantInvalidConfig {
				p.CCRNParser = ccrn.NewParser([]string{"ccrn.mycompany.dev"})
			} else {
				p.CCRNParser = ccrn.NewParser(nil)
			}

			result, err := p.EnsureASRValidity(asr)(context.Background())
			require.NoError(t, err)
			require.Equal(t, lifecycle.Continue(), result)

			invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
			if tc.wantInvalidConfig {
				require.NotNil(t, invalid)
				require.Equal(t, metav1.ConditionTrue, invalid.Status)
			} else {
				require.Nil(t, invalid)
			}
		})
	}
}

func Test_EnsureASRValidity_ResourceCCRNHardRules(t *testing.T) {
	const (
		apigroupWildcard  = "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/*/deployments"
		subresOnWildcard  = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/v1/*:log"
		wildcardNamespace = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/*/apps/deployments"
		validResourceCCRN = "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/apps/deployments"
	)
	cases := []struct {
		name              string
		permissions       []v1alpha1.Permission
		wantInvalidConfig bool
	}{
		{
			name:              "wildcard apiGroup is invalid",
			permissions:       []v1alpha1.Permission{{Resource: apigroupWildcard, Capability: "read"}},
			wantInvalidConfig: true,
		},
		{
			name:              "subresource on wildcard resource is invalid",
			permissions:       []v1alpha1.Permission{{Resource: subresOnWildcard, Capability: "read"}},
			wantInvalidConfig: true,
		},
		{
			name:              "wildcard namespace is invalid",
			permissions:       []v1alpha1.Permission{{Resource: wildcardNamespace, Capability: "read"}},
			wantInvalidConfig: true,
		},
		{
			name:              "capability other than read/write is invalid",
			permissions:       []v1alpha1.Permission{{Resource: validResourceCCRN, Capability: "manage"}},
			wantInvalidConfig: true,
		},
		{
			name:              "valid resource ccrn passes",
			permissions:       []v1alpha1.Permission{{Resource: validResourceCCRN, Capability: "write"}},
			wantInvalidConfig: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Spec:       v1alpha1.ApplicationSpecificRoleSpec{Permissions: tc.permissions},
			}
			p := &pmk8s.Phase{
				AuditLog:   audit.Disabled(),
				Log:        ctrl.Log.WithName("test"),
				CCRNParser: ccrn.NewParser(nil),
			}
			_, err := p.EnsureASRValidity(asr)(context.Background())
			require.NoError(t, err)
			invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
			if tc.wantInvalidConfig {
				require.NotNil(t, invalid)
				require.Equal(t, metav1.ConditionTrue, invalid.Status)
				require.Equal(t, string(pmmeta.InvalidCCRNReason), invalid.Reason)
			} else {
				require.Nil(t, invalid)
			}
		})
	}
}

func newTestRESTMapper() meta.RESTMapper {
	m := meta.NewDefaultRESTMapper(nil)
	m.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Node"}, meta.RESTScopeRoot)
	m.Add(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, meta.RESTScopeNamespace)
	return m
}

func Test_EnsureASRValidity_Warnings(t *testing.T) {
	const (
		nsResOnNode        = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/v1/nodes"
		nsResOnDeploy      = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/apps/deployments"
		nsResOnCRD         = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/example.io/widgets"
		clusterWriteDeploy = "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/apps/deployments"
	)
	cases := []struct {
		name        string
		permissions []v1alpha1.Permission
		wantWarning bool
	}{
		{
			name:        "namespace-resource on cluster-scoped kind warns",
			permissions: []v1alpha1.Permission{{Resource: nsResOnNode, Capability: "read"}},
			wantWarning: true,
		},
		{
			name:        "namespace-resource on namespaced kind does not warn",
			permissions: []v1alpha1.Permission{{Resource: nsResOnDeploy, Capability: "read"}},
			wantWarning: false,
		},
		{
			name:        "unknown kind fails open without warning",
			permissions: []v1alpha1.Permission{{Resource: nsResOnCRD, Capability: "read"}},
			wantWarning: false,
		},
		{
			name:        "write without read warns",
			permissions: []v1alpha1.Permission{{Resource: clusterWriteDeploy, Capability: "write"}},
			wantWarning: true,
		},
		{
			name: "write with read does not warn",
			permissions: []v1alpha1.Permission{
				{Resource: clusterWriteDeploy, Capability: "read"},
				{Resource: clusterWriteDeploy, Capability: "write"},
			},
			wantWarning: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Spec:       v1alpha1.ApplicationSpecificRoleSpec{Permissions: tc.permissions},
			}
			p := &pmk8s.Phase{
				AuditLog:   audit.Disabled(),
				Log:        ctrl.Log.WithName("test"),
				CCRNParser: ccrn.NewParser(nil),
				RESTMapper: newTestRESTMapper(),
			}
			_, err := p.EnsureASRValidity(asr)(context.Background())
			require.NoError(t, err)

			require.Nil(t, pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition))
			require.Nil(t, pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ReadyCondition))

			warn := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ConfigWarningCondition)
			if tc.wantWarning {
				require.NotNil(t, warn)
				require.Equal(t, metav1.ConditionTrue, warn.Status)
			} else {
				require.Nil(t, warn)
			}
		})
	}
}
