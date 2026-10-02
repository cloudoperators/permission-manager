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

	pmgithub "github.com/cloudoperators/permission-manager/internal/provider/github"
)

// testGithubInstanceKey is the instance-key label repo-guard stamps on the e2e
// GithubOrganization; PM uses it as the CR-name prefix.
const testGithubInstanceKey = "ghe"

// githubTeamCRName and githubTeamRepoCRName build the expected CR names via the same
// naming code PM uses in production, so the tests track the convention automatically.
func githubTeamCRName(org, team string) string {
	return pmgithub.TeamCRName(testGithubInstanceKey, org, team)
}

func githubTeamRepoCRName(org, team, permission string) string {
	return pmgithub.TeamRepoCRName(testGithubInstanceKey, org, team, permission)
}

// assertGithubTeamExists polls until a GithubTeam CR with the given name exists
// in the specified namespace on the management cluster.
func assertGithubTeamExists(t *testing.T, ctx context.Context, c client.Client, namespace, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubTeam",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("assertGithubTeamExists(%s/%s): %v", namespace, name, err)
	}
}

// assertGithubTeamAbsent polls until a GithubTeam CR with the given name no longer exists.
func assertGithubTeamAbsent(t *testing.T, ctx context.Context, c client.Client, namespace, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubTeam",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return true, nil // Not found = success
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("assertGithubTeamAbsent(%s/%s): %v", namespace, name, err)
	}
}

// assertGithubTeamRepoExists polls until a GithubTeamRepository CR with the given
// name exists in the specified namespace on the management cluster.
func assertGithubTeamRepoExists(t *testing.T, ctx context.Context, c client.Client, namespace, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubTeamRepository",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("assertGithubTeamRepoExists(%s/%s): %v", namespace, name, err)
	}
}

// assertGithubTeamRepoAbsent polls until a GithubTeamRepository CR no longer exists.
func assertGithubTeamRepoAbsent(t *testing.T, ctx context.Context, c client.Client, namespace, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubTeamRepository",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("assertGithubTeamRepoAbsent(%s/%s): %v", namespace, name, err)
	}
}

// assertGithubTeamRepoPermission polls until the named GithubTeamRepository CR
// has the expected permission field value.
func assertGithubTeamRepoPermission(t *testing.T, ctx context.Context, c client.Client, namespace, name, expectedPermission string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubTeamRepository",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}

		permission, found, err := unstructured.NestedString(obj.Object, "spec", "permission")
		if err != nil || !found {
			return false, fmt.Errorf("GithubTeamRepository %s/%s missing spec.permission", namespace, name)
		}
		if permission == expectedPermission {
			return true, nil
		}
		return false, fmt.Errorf("GithubTeamRepository %s/%s permission=%q, want %q", namespace, name, permission, expectedPermission)
	})
	if err != nil {
		t.Fatalf("assertGithubTeamRepoPermission(%s/%s, %q): %v", namespace, name, expectedPermission, err)
	}
}

// assertGithubTeamRepoContains polls until the named GithubTeamRepository CR
// contains the specified repository in its repositories list.
func assertGithubTeamRepoContains(t *testing.T, ctx context.Context, c client.Client, namespace, name, expectedRepo string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubTeamRepository",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}

		repos, found, err := unstructured.NestedStringSlice(obj.Object, "spec", "repositories")
		if err != nil || !found {
			return false, fmt.Errorf("GithubTeamRepository %s/%s missing spec.repositories", namespace, name)
		}
		for _, r := range repos {
			if r == expectedRepo {
				return true, nil
			}
		}
		return false, fmt.Errorf("GithubTeamRepository %s/%s repos=%v, missing %q", namespace, name, repos, expectedRepo)
	})
	if err != nil {
		t.Fatalf("assertGithubTeamRepoContains(%s/%s, %q): %v", namespace, name, expectedRepo, err)
	}
}

// assertGithubTeamStatus polls until the named GithubTeam CR has the expected
// status.teamStatus value.
func assertGithubTeamStatus(t *testing.T, ctx context.Context, c client.Client, namespace, name, expectedStatus string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubTeam",
		})
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, obj); err != nil {
			return false, nil
		}

		status, found, err := unstructured.NestedString(obj.Object, "status", "teamStatus")
		if err != nil || !found {
			return false, fmt.Errorf("GithubTeam %s/%s missing status.teamStatus", namespace, name)
		}
		if status == expectedStatus {
			return true, nil
		}
		return false, fmt.Errorf("GithubTeam %s/%s teamStatus=%q, want %q", namespace, name, status, expectedStatus)
	})
	if err != nil {
		t.Fatalf("assertGithubTeamStatus(%s/%s, %q): %v", namespace, name, expectedStatus, err)
	}
}

// githubProviderReadyTimeout is the timeout for waiting on the GitHub provider pod.
const githubProviderReadyTimeout = 60 * time.Second

// waitForGithubProviderReady waits until the GitHub provider pod is Ready.
func waitForGithubProviderReady(t *testing.T) {
	t.Helper()
	t.Logf("Waiting for GitHub provider pod to be ready (timeout: %s)...", githubProviderReadyTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), githubProviderReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	mgmtCtx := "kind-" + mgmtClusterName
	for {
		select {
		case <-ctx.Done():
			out, _ := runCmdNoFail("kubectl", "--context", mgmtCtx,
				"-n", controllerNamespace, "get", "pods", "-l", "app=pm-controller-github", "-o", "wide")
			logs, _ := runCmdNoFail("kubectl", "--context", mgmtCtx,
				"-n", controllerNamespace, "logs", "-l", "app=pm-controller-github", "--tail=50")
			t.Fatalf("GitHub provider pod not ready within %s\nPods:\n%s\nLogs:\n%s",
				githubProviderReadyTimeout, out, logs)
		case <-ticker.C:
			out, err := runCmdNoFail("kubectl", "--context", mgmtCtx,
				"-n", controllerNamespace,
				"get", "deployment", "pm-controller-github",
				"-o", "jsonpath={.status.readyReplicas}")
			if err == nil && out == "1" {
				t.Log("GitHub provider pod is ready")
				return
			}
		}
	}
}
