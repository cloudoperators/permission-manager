// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// dumpOnFailure is designed to be registered via t.Cleanup(). It checks
// t.Failed() and, if the test failed, dumps controller pod logs, RBAC state
// on consumer clusters, and ASR status from the management cluster.
//
// Usage:
//
//	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, consumerClients) })
func dumpOnFailure(t *testing.T, mgmtClient client.Client, consumerClients map[string]kubernetes.Interface) {
	t.Helper()

	if !t.Failed() {
		return
	}

	t.Log("=== FAILURE DIAGNOSTICS ===")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Controller pod logs
	dumpControllerLogs(t)

	// 2. ASR status on management cluster
	dumpASRStatus(t, ctx, mgmtClient)

	// 3. RBAC state on each consumer cluster
	for clusterName, k8sClient := range consumerClients {
		dumpClusterRBAC(t, ctx, k8sClient, clusterName)
	}

	t.Log("=== END FAILURE DIAGNOSTICS ===")
}

// dumpControllerLogs fetches and logs recent controller pod logs.
func dumpControllerLogs(t *testing.T) {
	t.Helper()

	t.Log("--- Controller Pod Logs (last 100 lines) ---")
	out, err := runCmdNoFail("kubectl", "--context", "kind-"+mgmtClusterName,
		"-n", controllerNamespace, "logs", "-l", "app=pm-controller", "--tail=100")
	if err != nil {
		t.Logf("  [ERROR] Failed to fetch controller logs: %v", err)
		return
	}
	t.Log(out)
}

// dumpASRStatus lists all ASRs in the controller namespace and logs their status.
func dumpASRStatus(t *testing.T, ctx context.Context, mgmtClient client.Client) {
	t.Helper()

	t.Log("--- ASR Status (management cluster) ---")

	var asrList pmv1.ApplicationSpecificRoleList
	if err := mgmtClient.List(ctx, &asrList, client.InNamespace(controllerNamespace)); err != nil {
		t.Logf("  [ERROR] Failed to list ASRs: %v", err)
		return
	}

	if len(asrList.Items) == 0 {
		t.Log("  No ASRs found")
		return
	}

	for _, asr := range asrList.Items {
		t.Logf("  ASR: %s/%s", asr.Namespace, asr.Name)

		// Conditions
		if len(asr.Status.Conditions) > 0 {
			for _, cond := range asr.Status.Conditions {
				t.Logf("    Condition: type=%s status=%s reason=%s message=%q",
					cond.Type, cond.Status, cond.Reason, cond.Message)
			}
		} else {
			t.Log("    No conditions")
		}

		// Provider status
		if len(asr.Status.Providers) > 0 {
			for providerName, ps := range asr.Status.Providers {
				t.Logf("    Provider %s: ready=%v message=%q lastReconciled=%s",
					providerName, ps.Ready, ps.Message, ps.LastReconciled.Format(time.RFC3339))
				for _, ts := range ps.Targets {
					t.Logf("      Target: id=%s status=%s message=%q lastUpdated=%s",
						ts.TargetID, ts.Status, ts.Message, ts.LastUpdated.Format(time.RFC3339))
				}
			}
		} else {
			t.Log("    No provider status entries")
		}
	}
}

// dumpClusterRBAC logs all pm-controller-managed ClusterRoles, ClusterRoleBindings,
// and RoleBindings on the given consumer cluster.
func dumpClusterRBAC(t *testing.T, ctx context.Context, k8sClient kubernetes.Interface, clusterName string) {
	t.Helper()

	t.Logf("--- RBAC State on %s ---", clusterName)

	// ClusterRoles with pm-controller label
	labelSelector := "app.kubernetes.io/managed-by=pm-controller"

	crList, err := k8sClient.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		t.Logf("  [ERROR] Failed to list ClusterRoles: %v", err)
	} else if len(crList.Items) == 0 {
		t.Logf("  No pm-controller-managed ClusterRoles found")
	} else {
		for _, cr := range crList.Items {
			verbs := formatRules(cr.Rules)
			t.Logf("  ClusterRole: %s (rules: %s)", cr.Name, verbs)
		}
	}

	// ClusterRoleBindings with pm-controller label
	crbList, err := k8sClient.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		t.Logf("  [ERROR] Failed to list ClusterRoleBindings: %v", err)
	} else if len(crbList.Items) == 0 {
		t.Logf("  No pm-controller-managed ClusterRoleBindings found")
	} else {
		for _, crb := range crbList.Items {
			subjects := formatSubjects(crb.Subjects)
			t.Logf("  ClusterRoleBinding: %s -> %s (subjects: %s)", crb.Name, crb.RoleRef.Name, subjects)
		}
	}

	// RoleBindings with pm-controller label across all namespaces
	rbList, err := k8sClient.RbacV1().RoleBindings("").List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		t.Logf("  [ERROR] Failed to list RoleBindings: %v", err)
	} else if len(rbList.Items) == 0 {
		t.Logf("  No pm-controller-managed RoleBindings found")
	} else {
		for _, rb := range rbList.Items {
			subjects := formatSubjects(rb.Subjects)
			t.Logf("  RoleBinding: %s/%s -> %s (subjects: %s)", rb.Namespace, rb.Name, rb.RoleRef.Name, subjects)
		}
	}
}

// dumpASRDetail fetches and logs a single ASR's full status. Useful for
// targeted debugging in individual test assertions.
func dumpASRDetail(t *testing.T, ctx context.Context, mgmtClient client.Client, namespace, asrName string) {
	t.Helper()

	var asr pmv1.ApplicationSpecificRole
	key := types.NamespacedName{Namespace: namespace, Name: asrName}
	if err := mgmtClient.Get(ctx, key, &asr); err != nil {
		t.Logf("  [DEBUG] Failed to fetch ASR %s/%s: %v", namespace, asrName, err)
		return
	}

	t.Logf("  [DEBUG] ASR %s/%s: generation=%d, connectorRefs=%v, permissions=%d",
		namespace, asrName, asr.Generation, asr.Spec.ConnectorRefs, len(asr.Spec.Permissions))
	for _, cond := range asr.Status.Conditions {
		t.Logf("  [DEBUG]   condition: type=%s status=%s reason=%s", cond.Type, cond.Status, cond.Reason)
	}
	for providerName, ps := range asr.Status.Providers {
		for _, ts := range ps.Targets {
			t.Logf("  [DEBUG]   provider=%s target=%s status=%s msg=%q", providerName, ts.TargetID, ts.Status, ts.Message)
		}
	}
}

// formatRules produces a compact string representation of PolicyRules for logging.
func formatRules(rules []rbacv1.PolicyRule) string {
	if len(rules) == 0 {
		return "<none>"
	}
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		parts = append(parts, fmt.Sprintf("{apiGroups:%v resources:%v verbs:%v}",
			r.APIGroups, r.Resources, r.Verbs))
	}
	return strings.Join(parts, ", ")
}

// formatSubjects produces a compact string representation of RBAC subjects for logging.
func formatSubjects(subjects []rbacv1.Subject) string {
	if len(subjects) == 0 {
		return "<none>"
	}
	parts := make([]string, 0, len(subjects))
	for _, s := range subjects {
		parts = append(parts, fmt.Sprintf("%s:%s", s.Kind, s.Name))
	}
	return strings.Join(parts, ", ")
}
