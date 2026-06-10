//go:build e2e

// Package e2e_wrappers provides convenience wrappers that resolve short cluster
// names to full names and typed clients, matching the API the onboarding/offboarding
// tests were written against.
package e2e

import (
	"context"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

// The following functions shadow the assertion_helpers and suite_helpers functions
// for the onboarding/offboarding tests that pass (t, ctx, shortClusterName, resourceName)
// instead of (t, ctx, client.Client, resourceName, subjectGroup).
//
// They are intentionally NOT named the same as the real helpers — the test files
// that need them call these specific signatures.

// waitForClusterRoleByName waits for a ClusterRole to appear on a cluster (identified by short name).
func waitForClusterRoleByName(t *testing.T, ctx context.Context, shortClusterName, crName string) {
	t.Helper()
	name := fullClusterName(shortClusterName)
	deadline, ok := ctx.Deadline()
	timeout := resourcePropagationTimeout
	if ok {
		timeout = time.Until(deadline)
	}
	waitForClusterRole(t, name, crName, timeout)
}

// waitForClusterRoleAbsentByName waits for a ClusterRole to disappear from a cluster.
func waitForClusterRoleAbsentByName(t *testing.T, ctx context.Context, shortClusterName, crName string) {
	t.Helper()
	name := fullClusterName(shortClusterName)
	deadline, ok := ctx.Deadline()
	timeout := resourcePropagationTimeout
	if ok {
		timeout = time.Until(deadline)
	}
	waitForClusterRoleAbsent(t, name, crName, timeout)
}

// assertClusterRoleBindingByName verifies a CRB exists on a cluster (by short name).
// Uses empty subjectGroup (doesn't verify subject, just existence).
func assertClusterRoleBindingByName(t *testing.T, ctx context.Context, shortClusterName, crbName string) {
	t.Helper()
	name := fullClusterName(shortClusterName)
	cl := crClientForCluster(t, name)
	assertClusterRoleBindingExists(t, ctx, cl, crbName, "")
}

// assertClusterRoleAbsentByName verifies a ClusterRole is gone from a cluster (by short name).
func assertClusterRoleAbsentByName(t *testing.T, ctx context.Context, shortClusterName, crName string) {
	t.Helper()
	name := fullClusterName(shortClusterName)
	cl := crClientForCluster(t, name)
	assertClusterRoleAbsent(t, ctx, cl, crName)
}

// assertClusterRoleByName verifies a ClusterRole exists on a cluster (by short name).
func assertClusterRoleByName(t *testing.T, ctx context.Context, shortClusterName, crName string) {
	t.Helper()
	name := fullClusterName(shortClusterName)
	cl := crClientForCluster(t, name)
	assertClusterRoleExists(t, ctx, cl, crName)
}

// assertRoleBindingByName verifies a RoleBinding exists in a namespace on a cluster (by short name).
func assertRoleBindingByName(t *testing.T, ctx context.Context, shortClusterName, namespace, rbName string) {
	t.Helper()
	name := fullClusterName(shortClusterName)
	cl := crClientForCluster(t, name)
	assertRoleBindingExists(t, ctx, cl, namespace, rbName, "")
}

// assertRoleBindingAbsentByName verifies a RoleBinding is gone from a namespace on a cluster.
func assertRoleBindingAbsentByName(t *testing.T, ctx context.Context, shortClusterName, namespace, rbName string) {
	t.Helper()
	name := fullClusterName(shortClusterName)
	cl := crClientForCluster(t, name)
	assertRoleBindingAbsent(t, ctx, cl, namespace, rbName)
}

// updateASRObj updates an ASR on the management cluster via typed client.
func updateASRObj(t *testing.T, asr *pmv1.ApplicationSpecificRole) {
	t.Helper()
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	if err := mgmtClient.Update(context.Background(), asr); err != nil {
		t.Fatalf("failed to update ASR %s: %v", asr.Name, err)
	}
	t.Logf("Updated ASR %q", asr.Name)
}

// getASR fetches the current ASR state from the management cluster.
func getASR(t *testing.T, asrName string) *pmv1.ApplicationSpecificRole {
	t.Helper()
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	var asr pmv1.ApplicationSpecificRole
	key := client.ObjectKey{Namespace: controllerNamespace, Name: asrName}
	if err := mgmtClient.Get(context.Background(), key, &asr); err != nil {
		t.Fatalf("failed to get ASR %s: %v", asrName, err)
	}
	return &asr
}
