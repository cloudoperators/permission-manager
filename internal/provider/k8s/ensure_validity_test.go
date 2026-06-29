// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
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
				AuditLog: audit.Disabled(),
				Log:      ctrl.Log.WithName("test"),
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
		AuditLog: audit.Disabled(),
		Log:      ctrl.Log.WithName("test"),
	}

	result, err := p.EnsureASRValidity(asr)(context.Background())
	require.NoError(t, err)
	require.Equal(t, lifecycle.Continue(), result)
	require.Nil(t, pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition))
}
