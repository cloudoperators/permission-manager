// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

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

			// The ConfigWarning condition is stamped by the terminal status
			// subroutine, after every subroutine has contributed its warnings.
			_, err = p.EnsureProviderStatus(asr)(context.Background())
			require.NoError(t, err)

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

// Test_EnsureRbacCompiled_ConfigWarningNoChurn asserts the ConfigWarning
// condition keeps its LastTransitionTime across reconciles of unchanged config:
// the condition is finalized once in EnsureProviderStatus, never removed and
// re-added mid-chain, so SetCondition preserves the existing timestamp.
func Test_EnsureRbacCompiled_ConfigWarningNoChurn(t *testing.T) {
	const nsResOnNode = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/v1/nodes"

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:        testSpecName,
			Permissions: []v1alpha1.Permission{{Resource: nsResOnNode, Capability: "read"}},
		},
	}

	reconcile := func() {
		p := &pmk8s.Phase{
			ClusterStore: clusterStoreWithGroups(t, []string{"v1"}),
			AuditLog:     audit.Disabled(),
			Log:          ctrl.Log.WithName("test"),
			CCRNParser:   ccrn.NewParser(nil),
			RemoteDiscovery: func(_ context.Context, _ client.Client, _, _ string) (discovery.DiscoveryInterface, error) {
				return fakeScopeDiscovery{}, nil
			},
		}
		_, err := p.EnsureRbacCompiled(asr)(context.Background())
		require.NoError(t, err)
		_, err = p.EnsureProviderStatus(asr)(context.Background())
		require.NoError(t, err)
	}

	reconcile()
	warn := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ConfigWarningCondition)
	require.NotNil(t, warn)
	require.Equal(t, metav1.ConditionTrue, warn.Status)

	// Back-date the stamp so a spurious re-stamp on the next reconcile is visible
	// even within the same wall-clock second.
	backdated := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
	for i := range asr.Status.Conditions {
		if asr.Status.Conditions[i].Type == string(pmmeta.ConfigWarningCondition) {
			asr.Status.Conditions[i].LastTransitionTime = backdated
		}
	}

	reconcile()
	warn = pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ConfigWarningCondition)
	require.NotNil(t, warn)
	require.Equal(t, backdated, warn.LastTransitionTime,
		"unchanged config must not churn the ConfigWarning LastTransitionTime")

	// A warning→no-warning transition clears the condition.
	asr.Spec.Permissions = []v1alpha1.Permission{
		{Resource: "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/apps/deployments", Capability: "read"},
	}
	reconcile()
	require.Nil(t, pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.ConfigWarningCondition))
}

// fakeDisjointDiscovery serves a cluster-scoped resource only in v2 of its
// group; v1 knows a different resource. Exercises the scope lookup against every
// cached group/version rather than only the first.
type fakeDisjointDiscovery struct {
	discovery.DiscoveryInterface
}

func (fakeDisjointDiscovery) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	switch gv {
	case "disjoint.io/v1":
		return &metav1.APIResourceList{GroupVersion: gv, APIResources: []metav1.APIResource{
			{Name: "oldthings", Namespaced: true},
		}}, nil
	case "disjoint.io/v2":
		return &metav1.APIResourceList{GroupVersion: gv, APIResources: []metav1.APIResource{
			{Name: "things", Namespaced: false},
		}}, nil
	default:
		return &metav1.APIResourceList{GroupVersion: gv}, nil
	}
}

// Test_EnsureRbacCompiled_ScopeMismatch_ResourceOnlyInLaterVersion asserts the
// scope check inspects every cached group/version, catching a cluster-scoped
// resource that lives only in a non-first version.
func Test_EnsureRbacCompiled_ScopeMismatch_ResourceOnlyInLaterVersion(t *testing.T) {
	const nsResOnThing = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/disjoint.io/things"

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:        testSpecName,
			Permissions: []v1alpha1.Permission{{Resource: nsResOnThing, Capability: "read"}},
		},
	}
	p := &pmk8s.Phase{
		ClusterStore: clusterStoreWithGroups(t, []string{"disjoint.io/v1", "disjoint.io/v2"}),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		CCRNParser:   ccrn.NewParser(nil),
		RemoteDiscovery: func(_ context.Context, _ client.Client, _, _ string) (discovery.DiscoveryInterface, error) {
			return fakeDisjointDiscovery{}, nil
		},
	}

	_, err := p.EnsureRbacCompiled(asr)(context.Background())
	require.NoError(t, err)
	warnings := p.ConfigWarnings()
	require.Len(t, warnings, 1)
	require.Equal(t, string(pmmeta.ScopeMismatchReason), warnings[0].Reason)
}

// countingScopeDiscovery counts ServerResourcesForGroupVersion calls per
// group/version while delegating responses to fakeScopeDiscovery.
type countingScopeDiscovery struct {
	discovery.DiscoveryInterface
	calls map[string]int
}

func (c *countingScopeDiscovery) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	c.calls[gv]++
	return fakeScopeDiscovery{}.ServerResourcesForGroupVersion(gv)
}

// Test_EnsureRbacCompiled_ScopeDiscovery_CachedPerGroupVersion asserts the scope
// check fetches each (cluster, group/version) once, even across multiple
// permissions resolving to the same group/version.
func Test_EnsureRbacCompiled_ScopeDiscovery_CachedPerGroupVersion(t *testing.T) {
	const (
		nsResOnNode = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/v1/nodes"
		nsResOnPod  = "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/v1/pods"
	)

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: testASRName, Namespace: testNamespace},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: testSpecName,
			Permissions: []v1alpha1.Permission{
				{Resource: nsResOnNode, Capability: "read"},
				{Resource: nsResOnPod, Capability: "read"},
			},
		},
	}
	counting := &countingScopeDiscovery{calls: map[string]int{}}
	p := &pmk8s.Phase{
		ClusterStore: clusterStoreWithGroups(t, []string{"v1"}),
		AuditLog:     audit.Disabled(),
		Log:          ctrl.Log.WithName("test"),
		CCRNParser:   ccrn.NewParser(nil),
		RemoteDiscovery: func(_ context.Context, _ client.Client, _, _ string) (discovery.DiscoveryInterface, error) {
			return counting, nil
		},
	}

	_, err := p.EnsureRbacCompiled(asr)(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, counting.calls["v1"],
		"each (cluster, group/version) must be discovered once per reconcile")
}
