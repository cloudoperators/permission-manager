// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
	"github.com/cloudoperators/permission-manager/internal/lifecycle/mocks"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

func Test_EnsureOrphansCleanedUp(t *testing.T) {
	cases := []struct {
		name              string
		invalidConfig     bool
		compiled          []pmk8s.ExportCompiledTarget
		priorTargets      []pmmeta.TargetStatus
		remote            func() *mocks.MockClient
		factoryByCluster  map[string]error
		wantTargetStatus  map[string]pmmeta.TargetStatusValue
		wantProviderEntry bool
	}{
		{
			name:          "should skip when invalid config is set",
			invalidConfig: true,
			priorTargets:  []pmmeta.TargetStatus{{TargetID: testClusterName, Status: pmmeta.TargetStatusReady}},
			remote: func() *mocks.MockClient {
				return mocks.NewMockClient(t)
			},
			wantProviderEntry: true,
			wantTargetStatus:  map[string]pmmeta.TargetStatusValue{testClusterName: pmmeta.TargetStatusReady},
		},
		{
			name:         "should continue when no provider status exists",
			priorTargets: nil,
			remote: func() *mocks.MockClient {
				return mocks.NewMockClient(t)
			},
			wantProviderEntry: true,
			wantTargetStatus:  map[string]pmmeta.TargetStatusValue{},
		},
		{
			name:         "should clean up a target that is no longer compiled",
			compiled:     []pmk8s.ExportCompiledTarget{{ClusterName: testClusterNameA}},
			priorTargets: []pmmeta.TargetStatus{{TargetID: testClusterNameB, Status: pmmeta.TargetStatusReady}},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				return m
			},
			wantProviderEntry: true,
			wantTargetStatus:  map[string]pmmeta.TargetStatusValue{},
		},
		{
			name:     "should prune the orphan but keep the still-compiled target",
			compiled: []pmk8s.ExportCompiledTarget{{ClusterName: testClusterNameA}},
			priorTargets: []pmmeta.TargetStatus{
				{TargetID: testClusterNameA, Status: pmmeta.TargetStatusReady},
				{TargetID: testClusterNameB, Status: pmmeta.TargetStatusError},
			},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				return m
			},
			wantProviderEntry: true,
			wantTargetStatus:  map[string]pmmeta.TargetStatusValue{testClusterNameA: pmmeta.TargetStatusReady},
		},
		{
			name:         "should preserve a target that is still compiled",
			compiled:     []pmk8s.ExportCompiledTarget{{ClusterName: testClusterName}},
			priorTargets: []pmmeta.TargetStatus{{TargetID: testClusterName, Status: pmmeta.TargetStatusReady}},
			remote: func() *mocks.MockClient {
				return mocks.NewMockClient(t)
			},
			wantProviderEntry: true,
			wantTargetStatus:  map[string]pmmeta.TargetStatusValue{testClusterName: pmmeta.TargetStatusReady},
		},
		{
			name:         "should record an error target when the orphan cluster is unreachable",
			compiled:     nil,
			priorTargets: []pmmeta.TargetStatus{{TargetID: testUnreachableClusterName, Status: pmmeta.TargetStatusReady}},
			remote: func() *mocks.MockClient {
				return mocks.NewMockClient(t)
			},
			factoryByCluster:  map[string]error{testUnreachableClusterName: errors.New("unreachable")},
			wantProviderEntry: true,
			wantTargetStatus:  map[string]pmmeta.TargetStatusValue{testUnreachableClusterName: pmmeta.TargetStatusError},
		},
		{
			name:         "should record an error target when orphan delete fails",
			compiled:     nil,
			priorTargets: []pmmeta.TargetStatus{{TargetID: testClusterName, Status: pmmeta.TargetStatusReady}},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("List", mock.Anything, mock.Anything, mock.Anything).Return(
					func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
						if l, ok := list.(*rbacv1.ClusterRoleList); ok {
							l.Items = []rbacv1.ClusterRole{{ObjectMeta: metav1.ObjectMeta{Name: testResource}}}
						}
						return nil
					})
				m.On("Delete", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("conflict"))
				return m
			},
			wantProviderEntry: true,
			wantTargetStatus:  map[string]pmmeta.TargetStatusValue{testClusterName: pmmeta.TargetStatusError},
		},
		{
			name: "should delete orphaned namespace role and rolebinding when namespace is de-referenced",
			compiled: []pmk8s.ExportCompiledTarget{
				{
					ClusterName: testClusterName,
					Roles: []rbacv1.Role{
						{ObjectMeta: metav1.ObjectMeta{Name: testResource, Namespace: "monitoring"}},
					},
					RoleBindings: []rbacv1.RoleBinding{
						{ObjectMeta: metav1.ObjectMeta{Name: testResource, Namespace: "monitoring"}},
					},
				},
			},
			priorTargets: []pmmeta.TargetStatus{
				{
					TargetID: testClusterName,
					Status:   pmmeta.TargetStatusReady,
					AppliedResources: []pmmeta.AppliedResourceRef{
						{Kind: "Role", Namespace: "monitoring", Name: testResource},
						{Kind: "RoleBinding", Namespace: "monitoring", Name: testResource},
						{Kind: "Role", Namespace: "observability", Name: testResource},
						{Kind: "RoleBinding", Namespace: "observability", Name: testResource},
					},
				},
			},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("Delete", mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
					return obj.GetNamespace() == "observability" && obj.GetName() == testResource
				}), mock.Anything).Return(nil).Times(2)
				return m
			},
			wantProviderEntry: true,
			wantTargetStatus:  map[string]pmmeta.TargetStatusValue{testClusterName: pmmeta.TargetStatusReady},
		},
		{
			name: "should delete orphaned clusterrole and clusterrolebinding when narrowing from cluster-wide to namespace-only",
			compiled: []pmk8s.ExportCompiledTarget{
				{
					ClusterName: testClusterName,
					Roles: []rbacv1.Role{
						{ObjectMeta: metav1.ObjectMeta{Name: testResource, Namespace: "monitoring"}},
					},
					RoleBindings: []rbacv1.RoleBinding{
						{ObjectMeta: metav1.ObjectMeta{Name: testResource, Namespace: "monitoring"}},
					},
				},
			},
			priorTargets: []pmmeta.TargetStatus{
				{
					TargetID: testClusterName,
					Status:   pmmeta.TargetStatusReady,
					AppliedResources: []pmmeta.AppliedResourceRef{
						{Kind: "ClusterRole", Name: testResource},
						{Kind: "ClusterRoleBinding", Name: testResource},
					},
				},
			},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("Delete", mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
					return obj.GetName() == testResource && obj.GetNamespace() == ""
				}), mock.Anything).Return(nil).Times(2)
				return m
			},
			wantProviderEntry: true,
			wantTargetStatus:  map[string]pmmeta.TargetStatusValue{testClusterName: pmmeta.TargetStatusReady},
		},
		{
			name: "should skip sub-resource diff when no prior applied resources recorded",
			compiled: []pmk8s.ExportCompiledTarget{
				{
					ClusterName: testClusterName,
					Roles: []rbacv1.Role{
						{ObjectMeta: metav1.ObjectMeta{Name: testResource, Namespace: "monitoring"}},
					},
				},
			},
			priorTargets: []pmmeta.TargetStatus{
				{TargetID: testClusterName, Status: pmmeta.TargetStatusReady},
			},
			remote: func() *mocks.MockClient {
				return mocks.NewMockClient(t) // no calls expected
			},
			wantProviderEntry: true,
			wantTargetStatus:  map[string]pmmeta.TargetStatusValue{testClusterName: pmmeta.TargetStatusReady},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Status: v1alpha1.ApplicationSpecificRoleStatus{
					Providers: map[pmmeta.ProviderType]pmmeta.ProviderStatus{
						pmmeta.ProviderKubernetes: {
							Targets: tc.priorTargets,
						},
					},
				},
			}
			if tc.invalidConfig {
				asr.SetCondition(pmmeta.TrueCondition(
					pmmeta.InvalidConfigCondition,
					pmmeta.InvalidCCRNReason,
					"bad",
				))
			}
			if tc.priorTargets == nil {
				asr.Status.Providers = nil
			}

			remote := tc.remote()
			factory := func(_ context.Context, _ client.Client, clusterName, _ string) (client.Client, error) {
				if err, ok := tc.factoryByCluster[clusterName]; ok {
					return nil, err
				}
				return remote, nil
			}

			p := &pmk8s.Phase{
				Client:       fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
				RemoteClient: factory,
				AuditLog:     audit.Disabled(),
				Log:          ctrl.Log.WithName("test"),
			}
			p.SetCompiled(tc.compiled)

			result, err := p.EnsureOrphansCleanedUp(asr)(context.Background())
			require.NoError(t, err)
			require.Equal(t, lifecycle.Continue(), result)

			_, err = p.EnsureProviderStatus(asr)(context.Background())
			_ = err

			ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
			if !tc.wantProviderEntry {
				require.Nil(t, ps)
				return
			}
			require.NotNil(t, ps)
			got := make(map[string]pmmeta.TargetStatusValue, len(ps.Targets))
			for _, ts := range ps.Targets {
				got[ts.TargetID] = ts.Status
			}
			require.Equal(t, tc.wantTargetStatus, got)
		})
	}
}
