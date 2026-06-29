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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

const (
	testNamespace              = "demo"
	testASRName                = "test-asr"
	testClusterName            = "remote-cluster"
	testClusterNameA           = "remote-cluster-a"
	testClusterNameB           = "remote-cluster-b"
	testUnreachableClusterName = "unreachable-cluster"
	testResource               = "pm-test-asr"
)

func Test_EnsureRbacRemoved(t *testing.T) {
	cases := []struct {
		name              string
		priorTargets      []pmmeta.TargetStatus
		remote            func() *mocks.MockClient
		factoryErr        error
		factoryByCluster  map[string]error
		wantResult        lifecycle.Result
		wantErr           bool
		wantProviderEntry bool
	}{
		{
			name:         "should clear provider status when no prior targets exist",
			priorTargets: nil,
			remote: func() *mocks.MockClient {
				return mocks.NewMockClient(t)
			},
			wantResult:        lifecycle.Continue(),
			wantProviderEntry: false,
		},
		{
			name:         "should clear provider status when no rbac is left to delete",
			priorTargets: []pmmeta.TargetStatus{{TargetID: testClusterName, Status: pmmeta.TargetStatusReady}},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				return m
			},
			wantResult:        lifecycle.Continue(),
			wantProviderEntry: false,
		},
		{
			name:         "should delete cluster-scoped and namespace-scoped rbac pairs on the remote cluster",
			priorTargets: []pmmeta.TargetStatus{{TargetID: testClusterName, Status: pmmeta.TargetStatusReady}},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("List", mock.Anything, mock.Anything, mock.Anything).Return(
					func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
						switch l := list.(type) {
						case *rbacv1.ClusterRoleList:
							l.Items = []rbacv1.ClusterRole{{ObjectMeta: metav1.ObjectMeta{Name: testResource}}}
						case *rbacv1.ClusterRoleBindingList:
							l.Items = []rbacv1.ClusterRoleBinding{{ObjectMeta: metav1.ObjectMeta{Name: testResource}}}
						case *rbacv1.RoleList:
							l.Items = []rbacv1.Role{{ObjectMeta: metav1.ObjectMeta{Name: testResource, Namespace: testNamespace}}}
						case *rbacv1.RoleBindingList:
							l.Items = []rbacv1.RoleBinding{{ObjectMeta: metav1.ObjectMeta{Name: testResource, Namespace: testNamespace}}}
						}
						return nil
					})
				m.On("Delete", mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(4)
				return m
			},
			wantResult:        lifecycle.Continue(),
			wantProviderEntry: false,
		},
		{
			name:         "should ignore not-found error on delete",
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
				m.On("Delete", mock.Anything, mock.Anything, mock.Anything).Return(
					apierrors.NewNotFound(schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: "clusterroles"}, testResource))
				return m
			},
			wantResult:        lifecycle.Continue(),
			wantProviderEntry: false,
		},
		{
			name:         "should break and preserve status when delete fails on the remote cluster",
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
			wantResult:        lifecycle.Break(),
			wantErr:           true,
			wantProviderEntry: true,
		},
		{
			name:         "should break with wrapped error when list fails",
			priorTargets: []pmmeta.TargetStatus{{TargetID: testClusterName, Status: pmmeta.TargetStatusReady}},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("List", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("list failed"))
				return m
			},
			wantResult:        lifecycle.Break(),
			wantErr:           true,
			wantProviderEntry: true,
		},
		{
			name:         "should break with aggregated error when remote client factory fails",
			priorTargets: []pmmeta.TargetStatus{{TargetID: testClusterName, Status: pmmeta.TargetStatusReady}},
			remote: func() *mocks.MockClient {
				return mocks.NewMockClient(t)
			},
			factoryErr:        errors.New("cluster unreachable"),
			wantResult:        lifecycle.Break(),
			wantErr:           true,
			wantProviderEntry: true,
		},
		{
			name: "should break and preserve status when one of many targets is unreachable",
			priorTargets: []pmmeta.TargetStatus{
				{TargetID: testClusterName, Status: pmmeta.TargetStatusReady},
				{TargetID: testUnreachableClusterName, Status: pmmeta.TargetStatusReady},
			},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				return m
			},
			factoryByCluster:  map[string]error{testUnreachableClusterName: errors.New("unreachable")},
			wantResult:        lifecycle.Break(),
			wantErr:           true,
			wantProviderEntry: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testASRName,
					Namespace: testNamespace,
				},
				Status: v1alpha1.ApplicationSpecificRoleStatus{
					Providers: map[pmmeta.ProviderType]pmmeta.ProviderStatus{
						pmmeta.ProviderKubernetes: {
							Targets: tc.priorTargets,
						},
					},
				},
			}

			local := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
			remote := tc.remote()

			factory := func(_ context.Context, _ client.Client, clusterName, _ string) (client.Client, error) {
				if tc.factoryErr != nil {
					return nil, tc.factoryErr
				}
				if err, ok := tc.factoryByCluster[clusterName]; ok {
					return nil, err
				}
				return remote, nil
			}

			p := &pmk8s.Phase{
				Client:       local,
				RemoteClient: factory,
				AuditLog:     audit.Disabled(),
				Log:          ctrl.Log.WithName("test"),
			}

			result, err := p.EnsureRbacRemoved(asr)(context.Background())
			require.Equal(t, tc.wantResult, result)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			_, hasKubernetes := asr.Status.Providers[pmmeta.ProviderKubernetes]
			require.Equal(t, tc.wantProviderEntry, hasKubernetes)
		})
	}
}

func Test_EnsureRbacRemoved_ListError(t *testing.T) {
	cases := []struct {
		name        string
		failOn      func(list client.ObjectList) bool
		wantWrapped string
	}{
		{
			name:        "ClusterRoleList",
			failOn:      func(l client.ObjectList) bool { _, ok := l.(*rbacv1.ClusterRoleList); return ok },
			wantWrapped: "ClusterRole",
		},
		{
			name:        "ClusterRoleBindingList",
			failOn:      func(l client.ObjectList) bool { _, ok := l.(*rbacv1.ClusterRoleBindingList); return ok },
			wantWrapped: "ClusterRoleBinding",
		},
		{
			name:        "RoleList",
			failOn:      func(l client.ObjectList) bool { _, ok := l.(*rbacv1.RoleList); return ok },
			wantWrapped: "Role",
		},
		{
			name:        "RoleBindingList",
			failOn:      func(l client.ObjectList) bool { _, ok := l.(*rbacv1.RoleBindingList); return ok },
			wantWrapped: "RoleBinding",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Status: v1alpha1.ApplicationSpecificRoleStatus{
					Providers: map[pmmeta.ProviderType]pmmeta.ProviderStatus{
						pmmeta.ProviderKubernetes: {
							Targets: []pmmeta.TargetStatus{{TargetID: testClusterName, Status: pmmeta.TargetStatusReady}},
						},
					},
				},
			}

			remote := mocks.NewMockClient(t)
			remote.On("List", mock.Anything, mock.Anything, mock.Anything).Return(
				func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
					if tc.failOn(list) {
						return errors.New("boom")
					}
					return nil
				})

			p := &pmk8s.Phase{
				Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
				RemoteClient: func(_ context.Context, _ client.Client, _, _ string) (client.Client, error) {
					return remote, nil
				},
				AuditLog: audit.Disabled(),
				Log:      ctrl.Log.WithName("test"),
			}

			result, err := p.EnsureRbacRemoved(asr)(context.Background())
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantWrapped)
			require.Equal(t, lifecycle.Break(), result)
			_, has := asr.Status.Providers[pmmeta.ProviderKubernetes]
			require.True(t, has)
		})
	}
}

func Test_EnsureRbacRemoved_MultiTarget(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[pmmeta.ProviderType]pmmeta.ProviderStatus{
				pmmeta.ProviderKubernetes: {
					Targets: []pmmeta.TargetStatus{
						{TargetID: testClusterNameA, Status: pmmeta.TargetStatusReady},
						{TargetID: testClusterNameB, Status: pmmeta.TargetStatusReady},
					},
				},
			},
		},
	}

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

	result, err := p.EnsureRbacRemoved(asr)(context.Background())
	require.NoError(t, err)
	require.Equal(t, lifecycle.Continue(), result)
	_, has := asr.Status.Providers[pmmeta.ProviderKubernetes]
	require.False(t, has)
}

func Test_EnsureRbacRemoved_Aggregate_Fail(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[pmmeta.ProviderType]pmmeta.ProviderStatus{
				pmmeta.ProviderKubernetes: {
					Targets: []pmmeta.TargetStatus{{TargetID: testClusterName, Status: pmmeta.TargetStatusReady}},
				},
			},
		},
	}

	remote := mocks.NewMockClient(t)
	remote.On("List", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			switch l := list.(type) {
			case *rbacv1.ClusterRoleList:
				l.Items = []rbacv1.ClusterRole{{ObjectMeta: metav1.ObjectMeta{Name: "cr-fails"}}}
			case *rbacv1.RoleList:
				l.Items = []rbacv1.Role{{ObjectMeta: metav1.ObjectMeta{Name: "role-ok", Namespace: testNamespace}}}
			}
			return nil
		})
	remote.On("Delete", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
			if obj.GetName() == "cr-fails" {
				return errors.New("conflict")
			}
			return nil
		})

	p := &pmk8s.Phase{
		Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
		RemoteClient: func(_ context.Context, _ client.Client, _, _ string) (client.Client, error) {
			return remote, nil
		},
		AuditLog: audit.Disabled(),
		Log:      ctrl.Log.WithName("test"),
	}

	result, err := p.EnsureRbacRemoved(asr)(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "cr-fails")
	require.Equal(t, lifecycle.Break(), result)
	_, has := asr.Status.Providers[pmmeta.ProviderKubernetes]
	require.True(t, has)

	remote.AssertCalled(t, "Delete", mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		return obj.GetName() == "role-ok"
	}), mock.Anything)
}
