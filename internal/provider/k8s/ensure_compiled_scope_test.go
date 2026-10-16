// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"testing"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

// clusterStoreWithGroups returns a cluster store holding a single Ready test
// cluster whose discovery cache advertises the given group/version strings.
func clusterStoreWithGroups(t *testing.T, groups []string) cache.Store {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	c := newCluster(testClusterName, true)
	if groups != nil {
		c.Status.DiscoveryCache = &greenhousev1alpha1.DiscoveryCache{Groups: groups}
	}
	require.NoError(t, store.Add(c))
	return store
}

// fakeScopeDiscovery reports resource scope (Namespaced) per group/version, so
// the compile-phase kind-vs-scope check can be exercised against a target
// cluster's discovery rather than the management cluster's RESTMapper.
type fakeScopeDiscovery struct {
	discovery.DiscoveryInterface
}

func (fakeScopeDiscovery) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	switch gv {
	case "v1":
		return &metav1.APIResourceList{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "nodes", Namespaced: false},
			{Name: "pods", Namespaced: true},
		}}, nil
	case "apps/v1":
		return &metav1.APIResourceList{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{
			{Name: "deployments", Namespaced: true},
		}}, nil
	case "multi.io/v1", "multi.io/v2":
		return &metav1.APIResourceList{GroupVersion: gv, APIResources: []metav1.APIResource{
			{Name: "gadgets", Namespaced: false},
		}}, nil
	default:
		return &metav1.APIResourceList{GroupVersion: gv}, nil
	}
}

func Test_EnsureRbacCompiled_ScopeMismatchWarning(t *testing.T) {
	const (
		nsResOnNode     = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/v1/nodes"
		nsResOnDeploy   = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/apps/deployments"
		nsResOnCRD      = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/example.io/widgets"
		nsResOnMultiVer = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/multi.io/gadgets"
	)
	groups := []string{"v1", "apps/v1", "multi.io/v1", "multi.io/v2"}

	cases := []struct {
		name         string
		permissions  []v1alpha1.Permission
		wantWarnings int
	}{
		{
			name:         "namespace-resource on cluster-scoped kind warns",
			permissions:  []v1alpha1.Permission{{Resource: nsResOnNode, Capability: "read"}},
			wantWarnings: 1,
		},
		{
			name:         "namespace-resource on namespaced kind does not warn",
			permissions:  []v1alpha1.Permission{{Resource: nsResOnDeploy, Capability: "read"}},
			wantWarnings: 0,
		},
		{
			name:         "resource unknown to target discovery fails open",
			permissions:  []v1alpha1.Permission{{Resource: nsResOnCRD, Capability: "read"}},
			wantWarnings: 0,
		},
		{
			name:         "namespace-resource on multi-version cluster-scoped kind warns",
			permissions:  []v1alpha1.Permission{{Resource: nsResOnMultiVer, Capability: "read"}},
			wantWarnings: 1,
		},
		{
			name: "read and write on same cluster-scoped CCRN warn once",
			permissions: []v1alpha1.Permission{
				{Resource: nsResOnNode, Capability: "read"},
				{Resource: nsResOnNode, Capability: "write"},
			},
			wantWarnings: 1,
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
				ClusterStore: clusterStoreWithGroups(t, groups),
				AuditLog:     audit.Disabled(),
				Log:          ctrl.Log.WithName("test"),
				CCRNParser:   ccrn.NewParser(nil),
				RemoteDiscovery: func(_ context.Context, _ client.Client, _, _ string) (discovery.DiscoveryInterface, error) {
					return fakeScopeDiscovery{}, nil
				},
			}

			_, err := p.EnsureRbacCompiled(asr)(context.Background())
			require.NoError(t, err)

			warnings := p.ConfigWarnings()
			require.Len(t, warnings, tc.wantWarnings)
			for _, w := range warnings {
				require.Equal(t, string(pmmeta.ScopeMismatchReason), w.Reason)
			}

			warn := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ConfigWarningCondition)
			if tc.wantWarnings > 0 {
				require.NotNil(t, warn)
				require.Equal(t, metav1.ConditionTrue, warn.Status)
			} else {
				require.Nil(t, warn)
			}
		})
	}
}

// fakeNoDiscovery always fails to build, so the compile-phase scope check must
// fail open (no warning) rather than block.
func Test_EnsureRbacCompiled_ScopeMismatch_FailsOpenWithoutDiscovery(t *testing.T) {
	const nsResOnNode = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/v1/nodes"

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:        testSpecName,
			Permissions: []v1alpha1.Permission{{Resource: nsResOnNode, Capability: "read"}},
		},
	}

	// Cluster has the group cached, but no discovery factory is configured.
	p := &pmk8s.Phase{
		ClusterStore: clusterStoreWithGroups(t, []string{"v1"}),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		CCRNParser:   ccrn.NewParser(nil),
	}

	_, err := p.EnsureRbacCompiled(asr)(context.Background())
	require.NoError(t, err)
	require.Empty(t, p.ConfigWarnings())
}
