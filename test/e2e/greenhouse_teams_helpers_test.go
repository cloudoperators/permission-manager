// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// assertTeamExists polls until a Team CR with the given name exists
// in the specified namespace on the management cluster.
func assertTeamExists(t *testing.T, ctx context.Context, c client.Client, namespace, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "greenhouse.sap",
			Version: "v1alpha1",
			Kind:    "Team",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("assertTeamExists(%s/%s): %v", namespace, name, err)
	}
}

// assertTeamAbsent polls until a Team CR with the given name no longer exists.
func assertTeamAbsent(t *testing.T, ctx context.Context, c client.Client, namespace, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "greenhouse.sap",
			Version: "v1alpha1",
			Kind:    "Team",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("assertTeamAbsent(%s/%s): %v", namespace, name, err)
	}
}

// assertTeamMappedIDPGroup polls until the named Team CR has the expected
// spec.mappedIdPGroup value.
func assertTeamMappedIDPGroup(t *testing.T, ctx context.Context, c client.Client, namespace, name, expectedGroup string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "greenhouse.sap",
			Version: "v1alpha1",
			Kind:    "Team",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}

		group, found, err := unstructured.NestedString(obj.Object, "spec", "mappedIdPGroup")
		if err != nil || !found {
			return false, fmt.Errorf("Team %s/%s missing spec.mappedIdPGroup", namespace, name)
		}
		if group == expectedGroup {
			return true, nil
		}
		return false, fmt.Errorf("Team %s/%s mappedIdPGroup=%q, want %q", namespace, name, group, expectedGroup)
	})
	if err != nil {
		t.Fatalf("assertTeamMappedIDPGroup(%s/%s, %q): %v", namespace, name, expectedGroup, err)
	}
}

// assertTeamRoleBindingExists polls until a TeamRoleBinding CR with the given
// name exists in the specified namespace on the management cluster.
func assertTeamRoleBindingExists(t *testing.T, ctx context.Context, c client.Client, namespace, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "greenhouse.sap",
			Version: "v1alpha2",
			Kind:    "TeamRoleBinding",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("assertTeamRoleBindingExists(%s/%s): %v", namespace, name, err)
	}
}

// assertTeamRoleBindingAbsent polls until a TeamRoleBinding CR no longer exists.
func assertTeamRoleBindingAbsent(t *testing.T, ctx context.Context, c client.Client, namespace, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "greenhouse.sap",
			Version: "v1alpha2",
			Kind:    "TeamRoleBinding",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("assertTeamRoleBindingAbsent(%s/%s): %v", namespace, name, err)
	}
}

// assertTeamRoleBindingTeamRoleRef polls until the named TeamRoleBinding CR
// has the expected spec.teamRoleRef value.
func assertTeamRoleBindingTeamRoleRef(t *testing.T, ctx context.Context, c client.Client, namespace, name, expectedRef string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "greenhouse.sap",
			Version: "v1alpha2",
			Kind:    "TeamRoleBinding",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}

		ref, found, err := unstructured.NestedString(obj.Object, "spec", "teamRoleRef")
		if err != nil || !found {
			return false, fmt.Errorf("TeamRoleBinding %s/%s missing spec.teamRoleRef", namespace, name)
		}
		if ref == expectedRef {
			return true, nil
		}
		return false, fmt.Errorf("TeamRoleBinding %s/%s teamRoleRef=%q, want %q", namespace, name, ref, expectedRef)
	})
	if err != nil {
		t.Fatalf("assertTeamRoleBindingTeamRoleRef(%s/%s, %q): %v", namespace, name, expectedRef, err)
	}
}

// assertTeamRoleBindingCluster polls until the named TeamRoleBinding CR
// has the expected spec.clusterSelector.clusterName value.
func assertTeamRoleBindingCluster(t *testing.T, ctx context.Context, c client.Client, namespace, name, expectedCluster string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "greenhouse.sap",
			Version: "v1alpha2",
			Kind:    "TeamRoleBinding",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}

		cluster, found, err := unstructured.NestedString(obj.Object, "spec", "clusterSelector", "clusterName")
		if err != nil || !found {
			return false, fmt.Errorf("TeamRoleBinding %s/%s missing spec.clusterSelector.clusterName", namespace, name)
		}
		if cluster == expectedCluster {
			return true, nil
		}
		return false, fmt.Errorf("TeamRoleBinding %s/%s clusterSelector.clusterName=%q, want %q", namespace, name, cluster, expectedCluster)
	})
	if err != nil {
		t.Fatalf("assertTeamRoleBindingCluster(%s/%s, %q): %v", namespace, name, expectedCluster, err)
	}
}

// assertTeamRoleExists polls until a TeamRole CR with the given name exists
// in the specified namespace on the management cluster.
func assertTeamRoleExists(t *testing.T, ctx context.Context, c client.Client, namespace, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "greenhouse.sap",
			Version: "v1alpha1",
			Kind:    "TeamRole",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("assertTeamRoleExists(%s/%s): %v", namespace, name, err)
	}
}

// greenhouseTeamsProviderReadyTimeout is the timeout for waiting on the greenhouse-teams provider pod.
const greenhouseTeamsProviderReadyTimeout = 60 * time.Second

// waitForGreenhouseTeamsProviderReady waits until the greenhouse-teams provider pod is Ready.
func waitForGreenhouseTeamsProviderReady(t *testing.T) {
	t.Helper()
	t.Logf("Waiting for greenhouse-teams provider pod to be ready (timeout: %s)...", greenhouseTeamsProviderReadyTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), greenhouseTeamsProviderReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	mgmtCtx := "kind-" + mgmtClusterName
	for {
		select {
		case <-ctx.Done():
			out, _ := runCmdNoFail("kubectl", "--context", mgmtCtx,
				"-n", controllerNamespace, "get", "pods", "-l", "app=pm-controller-greenhouse-teams", "-o", "wide")
			logs, _ := runCmdNoFail("kubectl", "--context", mgmtCtx,
				"-n", controllerNamespace, "logs", "-l", "app=pm-controller-greenhouse-teams", "--tail=50")
			t.Fatalf("greenhouse-teams provider pod not ready within %s\nPods:\n%s\nLogs:\n%s",
				greenhouseTeamsProviderReadyTimeout, out, logs)
		case <-ticker.C:
			out, err := runCmdNoFail("kubectl", "--context", mgmtCtx,
				"-n", controllerNamespace,
				"get", "deployment", "pm-controller-greenhouse-teams",
				"-o", "jsonpath={.status.readyReplicas}")
			if err == nil && out == "1" {
				t.Log("greenhouse-teams provider pod is ready")
				return
			}
		}
	}
}
