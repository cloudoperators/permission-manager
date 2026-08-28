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

// fakeCompileDiscovery is a minimal discovery.DiscoveryInterface for the compile
// test; only ServerGroups and ServerResourcesForGroupVersion are exercised.
type fakeCompileDiscovery struct {
	discovery.DiscoveryInterface
}

func (fakeCompileDiscovery) ServerGroups() (*metav1.APIGroupList, error) {
	return &metav1.APIGroupList{Groups: []metav1.APIGroup{
		{Name: "", PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "v1"}},
		{Name: "apps", PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "apps/v1"}},
	}}, nil
}

func (fakeCompileDiscovery) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	switch gv {
	case "v1":
		return &metav1.APIResourceList{GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "pods"}, {Name: "secrets"},
		}}, nil
	default:
		return &metav1.APIResourceList{GroupVersion: gv}, nil
	}
}

func clusterStoreWithGroups(t *testing.T, name string, groups []string) cache.Store {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	c := newCluster(name, true)
	if groups != nil {
		c.Status.DiscoveryCache = &greenhousev1alpha1.DiscoveryCache{Groups: groups}
	}
	require.NoError(t, store.Add(c))
	return store
}

func newExcludeCB(t *testing.T) *v1alpha1.ConnectorBinding {
	t.Helper()
	return newCBRaw(t, "cb", map[string]any{
		testCapability: []map[string]any{{
			"apiGroups": []string{"*"},
			"resources": []string{"*"},
			"verbs":     []string{"get", "list", "watch"},
			"excludeResources": []map[string]any{
				{"apiGroups": []string{""}, "resources": []string{"secrets"}},
			},
		}},
	})
}

func Test_EnsureRbacCompiled_ExcludeResources(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:        testSpecName,
			Permissions: []v1alpha1.Permission{{Resource: validClusterCCRN, Capability: testCapability}},
		},
	}

	p := &pmk8s.Phase{
		ClusterStore: clusterStoreWithGroups(t, testClusterName, []string{"", "apps"}),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		CCRNParser:   ccrn.NewParser(nil),
		RemoteDiscovery: func(_ context.Context, _ client.Client, _, _ string) (discovery.DiscoveryInterface, error) {
			return fakeCompileDiscovery{}, nil
		},
	}
	p.SetCBs([]*v1alpha1.ConnectorBinding{newExcludeCB(t)})

	_, err := p.EnsureRbacCompiled(asr)(context.Background())
	require.NoError(t, err)

	compiled := p.Compiled()
	require.Len(t, compiled, 1)
	require.Len(t, compiled[0].ClusterRoles, 1)

	rules := compiled[0].ClusterRoles[0].Rules
	// Two rules: the wildcard-rest grant for "apps", and the enumerated core
	// group with secrets removed. toPolicyRules sorts by resources then apiGroups.
	require.Len(t, rules, 2)

	// Sorted: resources ["*"] < ["pods"], so the rest grant comes first.
	require.Equal(t, []string{"apps"}, rules[0].APIGroups)
	require.Equal(t, []string{"*"}, rules[0].Resources)
	require.Equal(t, []string{"get", "list", "watch"}, rules[0].Verbs)

	require.Equal(t, []string{""}, rules[1].APIGroups)
	require.Equal(t, []string{"pods"}, rules[1].Resources)
	require.Equal(t, []string{"get", "list", "watch"}, rules[1].Verbs)

	require.Nil(t, pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition))
}

func Test_EnsureRbacCompiled_ExcludeResources_FailClosed(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:        testSpecName,
			Permissions: []v1alpha1.Permission{{Resource: validClusterCCRN, Capability: testCapability}},
		},
	}

	// Cluster has no discovery cache → deny rule cannot be safely expanded.
	p := &pmk8s.Phase{
		ClusterStore: clusterStoreWithGroups(t, testClusterName, nil),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		CCRNParser:   ccrn.NewParser(nil),
		RemoteDiscovery: func(_ context.Context, _ client.Client, _, _ string) (discovery.DiscoveryInterface, error) {
			return fakeCompileDiscovery{}, nil
		},
	}
	p.SetCBs([]*v1alpha1.ConnectorBinding{newExcludeCB(t)})

	_, err := p.EnsureRbacCompiled(asr)(context.Background())
	require.NoError(t, err)

	require.Empty(t, p.Compiled())
	invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
	require.NotNil(t, invalid)
	require.Equal(t, string(pmmeta.DiscoveryUnavailableReason), invalid.Reason)
}
