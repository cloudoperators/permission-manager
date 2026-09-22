// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"errors"
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

func Test_EnsureProviderStatus(t *testing.T) {
	cases := []struct {
		name                     string
		unmappedCaps             []string
		invalidCCRNs             []string
		unmatchedClusterPatterns []string
		notReadyClusters         []string
		provisionStatuses        []pmk8s.ExportClusterStatus
		orphanStatuses           []pmk8s.ExportClusterStatus
		provisionErrs            []error
		orphanErrs               []error
		priorReady               bool
		wantResult               lifecycle.Result
		wantErr                  bool
		wantDegraded             bool
		wantTargets              map[string]pmmeta.TargetStatusValue
	}{
		{
			name:              "should write a healthy target when provision succeeded",
			provisionStatuses: []pmk8s.ExportClusterStatus{{Name: testClusterName}},
			wantResult:        lifecycle.Continue(),
			wantTargets:       map[string]pmmeta.TargetStatusValue{testClusterName: pmmeta.TargetStatusReady},
		},
		{
			name:              "should write an error target when provision failed and break with aggregate",
			provisionStatuses: []pmk8s.ExportClusterStatus{{Name: testClusterName, Err: errors.New("apply failed")}},
			provisionErrs:     []error{errors.New("apply failed")},
			wantResult:        lifecycle.Break(),
			wantErr:           true,
			wantTargets:       map[string]pmmeta.TargetStatusValue{testClusterName: pmmeta.TargetStatusError},
		},
		{
			name:           "should write orphan status alongside provision status",
			orphanStatuses: []pmk8s.ExportClusterStatus{{Name: testClusterNameB, Err: errors.New("stale")}},
			orphanErrs:     []error{errors.New("stale")},
			wantResult:     lifecycle.Break(),
			wantErr:        true,
			wantTargets:    map[string]pmmeta.TargetStatusValue{testClusterNameB: pmmeta.TargetStatusError},
		},
		{
			name:         "should write a degraded status when unmapped capabilities exist",
			unmappedCaps: []string{"WRITE"},
			wantResult:   lifecycle.Continue(),
			wantDegraded: true,
		},
		{
			name:         "should write a degraded status when invalid ccrns exist",
			invalidCCRNs: []string{malformedCCRN},
			wantResult:   lifecycle.Continue(),
			wantDegraded: true,
		},
		{
			name:                     "should write an error target for a missing cluster alongside applied ones",
			provisionStatuses:        []pmk8s.ExportClusterStatus{{Name: testClusterName}},
			unmatchedClusterPatterns: []string{testClusterNameB},
			wantResult:               lifecycle.Continue(),
			wantTargets: map[string]pmmeta.TargetStatusValue{
				testClusterName:  pmmeta.TargetStatusReady,
				testClusterNameB: pmmeta.TargetStatusError,
			},
		},
		{
			name:              "should write a pending target for a not-ready cluster alongside applied ones",
			provisionStatuses: []pmk8s.ExportClusterStatus{{Name: testClusterName}},
			notReadyClusters:  []string{testClusterNameB},
			wantResult:        lifecycle.Continue(),
			wantTargets: map[string]pmmeta.TargetStatusValue{
				testClusterName:  pmmeta.TargetStatusReady,
				testClusterNameB: pmmeta.TargetStatusPending,
			},
		},
		{
			name:                     "should combine multiple degraded reasons into the message",
			unmappedCaps:             []string{"WRITE"},
			invalidCCRNs:             []string{malformedCCRN},
			unmatchedClusterPatterns: []string{wildcardCluster},
			wantResult:               lifecycle.Continue(),
			wantDegraded:             true,
		},
		{
			name:          "should aggregate provision and orphan errors",
			provisionErrs: []error{errors.New("apply failed")},
			orphanErrs:    []error{errors.New("revoke failed")},
			wantResult:    lifecycle.Break(),
			wantErr:       true,
		},
		{
			name:       "should continue cleanly with no accumulators",
			wantResult: lifecycle.Continue(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
			}

			p := &pmk8s.Phase{
				AuditLog: audit.Disabled(),
				Log:      ctrl.Log.WithName("test"),
			}
			p.SetUnmappedCaps(tc.unmappedCaps)
			p.SetInvalidCCRNs(tc.invalidCCRNs)
			p.SetUnmatchedClusterPatterns(tc.unmatchedClusterPatterns)
			p.SetNotReadyClusters(tc.notReadyClusters)
			p.SetProvisionStatuses(tc.provisionStatuses)
			p.SetOrphanStatuses(tc.orphanStatuses)
			p.SetProvisionErrs(tc.provisionErrs)
			p.SetOrphanErrs(tc.orphanErrs)

			result, err := p.EnsureProviderStatus(asr)(context.Background())
			require.Equal(t, tc.wantResult, result)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
			if tc.wantDegraded {
				require.NotNil(t, ps)
				require.False(t, ps.Ready)
				require.NotEmpty(t, ps.Message)
				return
			}
			if len(tc.wantTargets) == 0 {
				return
			}
			require.NotNil(t, ps)
			got := make(map[string]pmmeta.TargetStatusValue, len(ps.Targets))
			for _, ts := range ps.Targets {
				got[ts.TargetID] = ts.Status
			}
			for id, want := range tc.wantTargets {
				require.Equal(t, want, got[id])
			}
		})
	}
}

func Test_EnsureProviderStatus_Ready_Mirror(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
	}
	asr.SetCondition(pmmeta.TrueCondition(
		pmmeta.ProvisionedCondition,
		pmmeta.ProvisioningSucceededReason,
		"ok",
	))

	p := &pmk8s.Phase{
		AuditLog: audit.Disabled(),
		Log:      ctrl.Log.WithName("test"),
	}

	result, err := p.EnsureProviderStatus(asr)(context.Background())
	require.NoError(t, err)
	require.Equal(t, lifecycle.Continue(), result)

	ready := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ReadyCondition)
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionTrue, ready.Status)
}

func Test_EnsureProviderStatus_Invalid_Override(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
	}
	asr.SetCondition(pmmeta.TrueCondition(
		pmmeta.InvalidConfigCondition,
		pmmeta.InvalidCCRNReason,
		"bad",
	))
	asr.SetCondition(pmmeta.TrueCondition(
		pmmeta.ProvisionedCondition,
		pmmeta.ProvisioningSucceededReason,
		"ok",
	))

	p := &pmk8s.Phase{
		AuditLog: audit.Disabled(),
		Log:      ctrl.Log.WithName("test"),
	}

	result, err := p.EnsureProviderStatus(asr)(context.Background())
	require.NoError(t, err)
	require.Equal(t, lifecycle.Continue(), result)

	ready := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ReadyCondition)
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
}

func Test_EnsureProviderStatus_OrphanError_PreservesAppliedResources(t *testing.T) {
	priorRefs := []pmmeta.AppliedResourceRef{
		{Kind: "Role", Namespace: "monitoring", Name: testResource},
		{Kind: "Role", Namespace: "observability", Name: testResource},
	}
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[pmmeta.ProviderType]pmmeta.ProviderStatus{
				pmmeta.ProviderKubernetes: {
					Targets: []pmmeta.TargetStatus{
						{TargetID: testClusterName, Status: pmmeta.TargetStatusReady, AppliedResources: priorRefs},
					},
				},
			},
		},
	}

	p := &pmk8s.Phase{
		AuditLog: audit.Disabled(),
		Log:      ctrl.Log.WithName("test"),
	}
	p.SetOrphanStatuses([]pmk8s.ExportClusterStatus{{Name: testClusterName, Err: errors.New("delete failed")}})
	p.SetOrphanErrs([]error{errors.New("delete failed")})

	_, _ = p.EnsureProviderStatus(asr)(context.Background())

	ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
	require.NotNil(t, ps)
	require.Len(t, ps.Targets, 1)
	target := ps.Targets[0]
	require.Equal(t, pmmeta.TargetStatusError, target.Status)
	require.Equal(t, priorRefs, target.AppliedResources, "prior applied resources must survive a failed orphan delete")
}

// A cluster going not-Ready keeps its prior applied-resource tracking.
func Test_EnsureProviderStatus_NotReady_PreservesAppliedResources(t *testing.T) {
	priorRefs := []pmmeta.AppliedResourceRef{
		{Kind: "ClusterRole", Name: testResource},
		{Kind: "ClusterRoleBinding", Name: testResource},
	}
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[pmmeta.ProviderType]pmmeta.ProviderStatus{
				pmmeta.ProviderKubernetes: {
					Targets: []pmmeta.TargetStatus{
						{TargetID: testClusterNameB, Status: pmmeta.TargetStatusReady, AppliedResources: priorRefs},
					},
				},
			},
		},
	}

	p := &pmk8s.Phase{
		AuditLog: audit.Disabled(),
		Log:      ctrl.Log.WithName("test"),
	}
	p.SetNotReadyClusters([]string{testClusterNameB})

	_, _ = p.EnsureProviderStatus(asr)(context.Background())

	ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
	require.NotNil(t, ps)
	require.Len(t, ps.Targets, 1)
	target := ps.Targets[0]
	require.Equal(t, pmmeta.TargetStatusPending, target.Status)
	require.Equal(t, priorRefs, target.AppliedResources, "prior applied resources must survive a not-Ready blip")
}
