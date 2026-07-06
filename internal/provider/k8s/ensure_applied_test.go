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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
	"github.com/cloudoperators/permission-manager/internal/lifecycle/mocks"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

var (
	errNotFoundCR = apierrors.NewNotFound(schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: "clusterroles"}, testResource)
	errNotFoundRB = apierrors.NewNotFound(schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: "rolebindings"}, testResource)
)

func clusterScopedTarget(clusterName string) pmk8s.ExportCompiledTarget {
	return pmk8s.ExportCompiledTarget{
		ClusterName: clusterName,
		ClusterRoles: []rbacv1.ClusterRole{{
			ObjectMeta: metav1.ObjectMeta{Name: testResource},
			Rules:      []rbacv1.PolicyRule{{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"get"}}},
		}},
		ClusterRoleBindings: []rbacv1.ClusterRoleBinding{{
			ObjectMeta: metav1.ObjectMeta{Name: testResource},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: testResource},
			Subjects:   []rbacv1.Subject{{Kind: "Group", Name: testSpecName, APIGroup: "rbac.authorization.k8s.io"}},
		}},
	}
}

func namespaceScopedTarget(clusterName string) pmk8s.ExportCompiledTarget {
	return pmk8s.ExportCompiledTarget{
		ClusterName: clusterName,
		Roles: []rbacv1.Role{{
			ObjectMeta: metav1.ObjectMeta{Name: testResource, Namespace: testNamespace},
			Rules:      []rbacv1.PolicyRule{{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"get"}}},
		}},
		RoleBindings: []rbacv1.RoleBinding{{
			ObjectMeta: metav1.ObjectMeta{Name: testResource, Namespace: testNamespace},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: testResource},
			Subjects:   []rbacv1.Subject{{Kind: "Group", Name: testSpecName, APIGroup: "rbac.authorization.k8s.io"}},
		}},
	}
}

func Test_EnsureRbacApplied(t *testing.T) {
	cases := []struct {
		name                  string
		invalidConfig         bool
		compiled              []pmk8s.ExportCompiledTarget
		factoryByCluster      map[string]error
		remote                func() *mocks.MockClient
		wantProvisionedStatus metav1.ConditionStatus
		wantReason            pmmeta.ConditionReason
		wantRemoved           bool
	}{
		{
			name:          "should skip and remove prior provisioned when invalid config is set",
			invalidConfig: true,
			compiled:      []pmk8s.ExportCompiledTarget{clusterScopedTarget(testClusterName)},
			remote: func() *mocks.MockClient {
				return mocks.NewMockClient(t)
			},
			wantRemoved: true,
		},
		{
			name:     "should report no targets when compiled is empty",
			compiled: nil,
			remote: func() *mocks.MockClient {
				return mocks.NewMockClient(t)
			},
			wantProvisionedStatus: metav1.ConditionTrue,
			wantReason:            pmmeta.NoTargetsReason,
		},
		{
			name:     "should create cluster-scoped pair when nothing exists on the remote",
			compiled: []pmk8s.ExportCompiledTarget{clusterScopedTarget(testClusterName)},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errNotFoundCR)
				m.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)
				return m
			},
			wantProvisionedStatus: metav1.ConditionTrue,
			wantReason:            pmmeta.ProvisioningSucceededReason,
		},
		{
			name:     "should create namespace-scoped pair when nothing exists on the remote",
			compiled: []pmk8s.ExportCompiledTarget{namespaceScopedTarget(testClusterName)},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errNotFoundRB)
				m.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)
				return m
			},
			wantProvisionedStatus: metav1.ConditionTrue,
			wantReason:            pmmeta.ProvisioningSucceededReason,
		},
		{
			name:     "should patch existing cluster-scoped rbac",
			compiled: []pmk8s.ExportCompiledTarget{clusterScopedTarget(testClusterName)},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
				m.On("Patch", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
				return m
			},
			wantProvisionedStatus: metav1.ConditionTrue,
			wantReason:            pmmeta.ProvisioningSucceededReason,
		},
		{
			name:     "should recreate role binding when role ref changes",
			compiled: []pmk8s.ExportCompiledTarget{namespaceScopedTarget(testClusterName)},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
					func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
						if rb, ok := obj.(*rbacv1.RoleBinding); ok {
							rb.RoleRef = rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "stale"}
						}
						return nil
					})
				m.On("Patch", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
				m.On("Delete", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				m.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				return m
			},
			wantProvisionedStatus: metav1.ConditionTrue,
			wantReason:            pmmeta.ProvisioningSucceededReason,
		},
		{
			name:             "should report provisioning failed when factory cannot reach the only target",
			compiled:         []pmk8s.ExportCompiledTarget{clusterScopedTarget(testUnreachableClusterName)},
			factoryByCluster: map[string]error{testUnreachableClusterName: errors.New("unreachable")},
			remote: func() *mocks.MockClient {
				return mocks.NewMockClient(t)
			},
			wantProvisionedStatus: metav1.ConditionFalse,
			wantReason:            pmmeta.ProvisioningFailedReason,
		},
		{
			name: "should report partial provisioning when only some targets fail",
			compiled: []pmk8s.ExportCompiledTarget{
				clusterScopedTarget(testClusterNameA),
				clusterScopedTarget(testUnreachableClusterName),
			},
			factoryByCluster: map[string]error{testUnreachableClusterName: errors.New("unreachable")},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errNotFoundCR)
				m.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)
				return m
			},
			wantProvisionedStatus: metav1.ConditionFalse,
			wantReason:            pmmeta.PartialProvisioningReason,
		},
		{
			name:     "should report provisioning failed when apply errors on the only target",
			compiled: []pmk8s.ExportCompiledTarget{clusterScopedTarget(testClusterName)},
			remote: func() *mocks.MockClient {
				m := mocks.NewMockClient(t)
				m.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("boom"))
				return m
			},
			wantProvisionedStatus: metav1.ConditionFalse,
			wantReason:            pmmeta.ProvisioningFailedReason,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
				Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: testSpecName},
			}
			if tc.invalidConfig {
				asr.SetCondition(pmmeta.TrueCondition(
					pmmeta.InvalidConfigCondition,
					pmmeta.InvalidCCRNReason,
					"bad",
				))
				asr.SetCondition(pmmeta.TrueCondition(
					pmmeta.ProvisionedCondition,
					pmmeta.ProvisioningSucceededReason,
					"stale",
				))
			}

			remote := tc.remote()
			factory := func(_ context.Context, _ client.Client, clusterName, _ string) (client.Client, error) {
				if err, ok := tc.factoryByCluster[clusterName]; ok {
					return nil, err
				}
				return remote, nil
			}

			p := &pmk8s.Phase{
				RemoteClient: factory,
				AuditLog:     audit.Disabled(),
				Log:          ctrl.Log.WithName("test"),
			}
			p.SetCompiled(tc.compiled)

			result, err := p.EnsureRbacApplied(asr)(context.Background())
			require.NoError(t, err)
			require.Equal(t, lifecycle.Continue(), result)

			provisioned := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ProvisionedCondition)
			if tc.wantRemoved {
				require.Nil(t, provisioned)
				return
			}
			require.NotNil(t, provisioned)
			require.Equal(t, tc.wantProvisionedStatus, provisioned.Status)
			require.Equal(t, string(tc.wantReason), provisioned.Reason)
		})
	}
}

func Test_EnsureRbacApplied_AppliedResources(t *testing.T) {
	t.Run("should record applied resources in provider status on successful apply", func(t *testing.T) {
		asr := &v1alpha1.ApplicationSpecificRole{
			ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
			Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: testSpecName},
		}
		remote := mocks.NewMockClient(t)
		remote.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errNotFoundCR)
		remote.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		p := &pmk8s.Phase{
			RemoteClient: func(_ context.Context, _ client.Client, _, _ string) (client.Client, error) {
				return remote, nil
			},
			AuditLog: audit.Disabled(),
			Log:      ctrl.Log.WithName("test"),
		}
		p.SetCompiled([]pmk8s.ExportCompiledTarget{clusterScopedTarget(testClusterName)})

		_, _ = p.EnsureRbacApplied(asr)(context.Background())
		_, _ = p.EnsureProviderStatus(asr)(context.Background())

		ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
		require.NotNil(t, ps)
		require.Len(t, ps.Targets, 1)
		target := ps.Targets[0]
		require.Equal(t, testClusterName, target.TargetID)
		require.Len(t, target.AppliedResources, 2)
		require.Equal(t, "ClusterRole", target.AppliedResources[0].Kind)
		require.Equal(t, testResource, target.AppliedResources[0].Name)
		require.Equal(t, "ClusterRoleBinding", target.AppliedResources[1].Kind)
		require.Equal(t, testResource, target.AppliedResources[1].Name)
	})

	t.Run("should preserve prior applied resources in status when apply fails", func(t *testing.T) {
		priorRefs := []pmmeta.AppliedResourceRef{
			{Kind: "ClusterRole", Name: testResource},
			{Kind: "ClusterRoleBinding", Name: testResource},
		}
		asr := &v1alpha1.ApplicationSpecificRole{
			ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
			Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: testSpecName},
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
		remote := mocks.NewMockClient(t)
		remote.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("api error"))

		p := &pmk8s.Phase{
			RemoteClient: func(_ context.Context, _ client.Client, _, _ string) (client.Client, error) {
				return remote, nil
			},
			AuditLog: audit.Disabled(),
			Log:      ctrl.Log.WithName("test"),
		}
		p.SetCompiled([]pmk8s.ExportCompiledTarget{clusterScopedTarget(testClusterName)})

		_, _ = p.EnsureRbacApplied(asr)(context.Background())
		_, _ = p.EnsureProviderStatus(asr)(context.Background())

		ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
		require.NotNil(t, ps)
		require.Len(t, ps.Targets, 1)
		target := ps.Targets[0]
		require.Equal(t, pmmeta.TargetStatusError, target.Status)
		require.Equal(t, priorRefs, target.AppliedResources, "prior applied resources must be preserved on error")
	})
}
