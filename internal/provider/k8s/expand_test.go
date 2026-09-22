// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"testing"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
)

// fakeDiscovery implements only the discovery method expandRuleForCluster uses
// for denied-group enumeration; the embedded nil interface panics on any other
// call, keeping the fake honest about what the code under test touches.
type fakeDiscovery struct {
	discovery.DiscoveryInterface
	resources map[string]*metav1.APIResourceList
	resErr    map[string]error
}

func (f *fakeDiscovery) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	if f.resErr != nil {
		if err := f.resErr[gv]; err != nil {
			return nil, err
		}
	}
	if l, ok := f.resources[gv]; ok {
		return l, nil
	}
	return nil, errors.New("group version not found: " + gv)
}

func resourceList(gv string, names ...string) *metav1.APIResourceList {
	l := &metav1.APIResourceList{GroupVersion: gv}
	for _, n := range names {
		l.APIResources = append(l.APIResources, metav1.APIResource{Name: n})
	}
	return l
}

func clusterWithGroups(name string, groups ...string) *greenhousev1alpha1.Cluster { //nolint:unparam
	c := &greenhousev1alpha1.Cluster{}
	c.Name = name
	if groups != nil {
		c.Status.DiscoveryCache = &greenhousev1alpha1.DiscoveryCache{Groups: groups}
	}
	return c
}

// discoWith builds a fake discovery client with resource lists for denied-group expansion.
func discoWith() *fakeDiscovery {
	return &fakeDiscovery{
		resources: map[string]*metav1.APIResourceList{
			"v1":                     resourceList("v1", "pods", "configmaps", "secrets", "secrets/status"),
			"core.gardener.cloud/v1": resourceList("core.gardener.cloud/v1", "shoots", "internalsecrets"),
		},
	}
}

func Test_expandRuleForCluster_passthrough(t *testing.T) {
	rule := CapabilityRule{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get"}}
	out, err := expandRuleForCluster(rule, clusterWithGroups("c", "apps/v1"), nil)
	require.NoError(t, err)
	require.Equal(t, []CapabilityRule{rule}, out)
}

func Test_expandRuleForCluster_wildcardWithExclusions(t *testing.T) {
	rule := CapabilityRule{
		APIGroups: []string{"*"},
		Resources: []string{"*"},
		Verbs:     []string{"get", "list", "watch"},
		ExcludeResources: []ResourceSelector{
			{APIGroups: []string{""}, Resources: []string{"secrets"}},
			{APIGroups: []string{"core.gardener.cloud"}, Resources: []string{"internalsecrets"}},
		},
	}
	cluster := clusterWithGroups("c", "v1", "apps/v1", "core.gardener.cloud/v1")
	out, err := expandRuleForCluster(rule, cluster, discoWith())
	require.NoError(t, err)

	// rest grant first (non-denied groups), then denied groups sorted.
	require.Len(t, out, 3)

	require.Equal(t, []string{"apps"}, out[0].APIGroups)
	require.Equal(t, []string{"*"}, out[0].Resources)
	require.Equal(t, []string{"get", "list", "watch"}, out[0].Verbs)

	// core "" enumerated: secrets and its subresource secrets/status dropped.
	require.Equal(t, []string{""}, out[1].APIGroups)
	require.Equal(t, []string{"configmaps", "pods"}, out[1].Resources)
	require.Equal(t, []string{"get", "list", "watch"}, out[1].Verbs)

	require.Equal(t, []string{"core.gardener.cloud"}, out[2].APIGroups)
	require.Equal(t, []string{"shoots"}, out[2].Resources)
}

func Test_expandRuleForCluster_specificAPIGroupsIntersect(t *testing.T) {
	rule := CapabilityRule{
		APIGroups: []string{"", "apps", "batch"}, // batch not discovered → dropped
		Resources: []string{"*"},
		Verbs:     []string{"get"},
		ExcludeResources: []ResourceSelector{
			{APIGroups: []string{""}, Resources: []string{"secrets"}},
		},
	}
	cluster := clusterWithGroups("c", "v1", "apps/v1", "core.gardener.cloud/v1")
	out, err := expandRuleForCluster(rule, cluster, discoWith())
	require.NoError(t, err)

	require.Len(t, out, 2)
	require.Equal(t, []string{"apps"}, out[0].APIGroups) // core.gardener.cloud not requested
	require.Equal(t, []string{"*"}, out[0].Resources)
	require.Equal(t, []string{""}, out[1].APIGroups)
	require.Equal(t, []string{"configmaps", "pods"}, out[1].Resources)
}

func Test_expandRuleForCluster_failClosed(t *testing.T) {
	rule := CapabilityRule{
		APIGroups:        []string{"*"},
		Resources:        []string{"*"},
		Verbs:            []string{"get"},
		ExcludeResources: []ResourceSelector{{APIGroups: []string{""}, Resources: []string{"secrets"}}},
	}

	t.Run("nil discovery cache", func(t *testing.T) {
		out, err := expandRuleForCluster(rule, clusterWithGroups("c"), discoWith())
		require.Error(t, err)
		require.Nil(t, out)
	})

	t.Run("empty groups", func(t *testing.T) {
		cluster := &greenhousev1alpha1.Cluster{}
		cluster.Name = "c"
		cluster.Status.DiscoveryCache = &greenhousev1alpha1.DiscoveryCache{Groups: nil}
		out, err := expandRuleForCluster(rule, cluster, discoWith())
		require.Error(t, err)
		require.Nil(t, out)
	})

	t.Run("empty cache entry", func(t *testing.T) {
		out, err := expandRuleForCluster(rule, clusterWithGroups("c", "", "apps/v1"), discoWith())
		require.Error(t, err)
		require.Nil(t, out)
	})

	t.Run("ServerResourcesForGroupVersion error", func(t *testing.T) {
		d := discoWith()
		d.resErr = map[string]error{"v1": errors.New("boom")}
		out, err := expandRuleForCluster(rule, clusterWithGroups("c", "v1", "apps/v1"), d)
		require.Error(t, err)
		require.Nil(t, out)
	})
}

func Test_isExcluded(t *testing.T) {
	denied := map[string]struct{}{"secrets": {}}
	require.True(t, isExcluded("secrets", denied))
	require.True(t, isExcluded("secrets/status", denied))
	require.False(t, isExcluded("secretstores", denied)) // prefix without "/" is not a subresource
	require.False(t, isExcluded("configmaps", denied))

	// A "*" entry denies every resource in the group (defensive backstop).
	all := map[string]struct{}{"*": {}}
	require.True(t, isExcluded("configmaps", all))
	require.True(t, isExcluded("secrets", all))
}
