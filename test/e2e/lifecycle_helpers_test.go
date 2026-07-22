// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// Ensure unused import guard.
var _ = time.Second

// fullClusterName resolves a short cluster name (e.g., "consumer-1") to the
// full kind cluster name (e.g., "pm-e2e-consumer-1").
func fullClusterName(short string) string {
	if strings.HasPrefix(short, clusterPrefix) {
		return short
	}
	return clusterPrefix + short
}

// clientForCluster is a shorthand that resolves short cluster names.
func clientForCluster(t *testing.T, shortName string) client.Client {
	t.Helper()
	return crClientForCluster(t, fullClusterName(shortName))
}

// --- Struct-based ASR creation for onboarding/offboarding tests ---

// createASRObj creates an ASR from a typed struct on the management cluster.
func createASRObj(t *testing.T, asr *pmv1.ApplicationSpecificRole) {
	t.Helper()
	mgmtClient := crClientForCluster(t, mgmtClusterName)
	if err := mgmtClient.Create(context.Background(), asr); err != nil {
		t.Fatalf("failed to create ASR %s: %v", asr.Name, err)
	}
	t.Logf("Created ASR %q", asr.Name)
}

// --- Cluster lifecycle helpers for offboarding tests ---

// deleteClusterCR deletes a Greenhouse Cluster CR from the management cluster.
func deleteClusterCR(t *testing.T, shortName string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	name := fullClusterName(shortName)
	runCmd(t, "kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"delete", "cluster.greenhouse.sap", name, "--ignore-not-found")
	t.Logf("Deleted Cluster CR %q", name)
}

// recreateClusterCR re-creates a Greenhouse Cluster CR and its kubeconfig Secret.
func recreateClusterCR(t *testing.T, shortName string) {
	t.Helper()
	name := fullClusterName(shortName)
	mgmtCtx := "kind-" + mgmtClusterName

	// Re-create kubeconfig Secret
	internalKubeconfig := getInternalKubeconfig(t, name)
	cmd := exec.Command("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"create", "secret", "generic", name,
		"--from-literal=kubeconfig="+internalKubeconfig,
		"--dry-run=client", "-o", "yaml")
	var yamlOut bytes.Buffer
	cmd.Stdout = &yamlOut
	cmd.Stderr = &yamlOut
	if err := cmd.Run(); err != nil {
		t.Logf("Warning: failed to generate secret yaml for %s: %v", name, err)
		return
	}
	applyCmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(yamlOut.String())
	var applyOut bytes.Buffer
	applyCmd.Stdout = &applyOut
	applyCmd.Stderr = &applyOut
	if err := applyCmd.Run(); err != nil {
		t.Logf("Warning: failed to apply kubeconfig secret for %s: %v", name, err)
	}

	// Re-create Cluster CR
	clusterYAML := fmt.Sprintf(`apiVersion: greenhouse.sap/v1alpha1
kind: Cluster
metadata:
  name: %s
  namespace: %s
spec:
  accessMode: direct`, name, controllerNamespace)

	applyCmd2 := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	applyCmd2.Stdin = strings.NewReader(clusterYAML)
	var out2 bytes.Buffer
	applyCmd2.Stdout = &out2
	applyCmd2.Stderr = &out2
	if err := applyCmd2.Run(); err != nil {
		t.Logf("Warning: failed to re-create Cluster CR %s: %v", name, err)
		return
	}

	// Patch status KubeConfigValid=True
	statusPatch := `{"status":{"statusConditions":{"conditions":[{"type":"KubeConfigValid","status":"True"}]}}}`
	_, err := runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"patch", "cluster.greenhouse.sap", name,
		"--type=merge", "--subresource=status",
		"-p", statusPatch)
	if err != nil {
		t.Logf("Warning: failed to patch Cluster CR status for %s", name)
	}

	t.Logf("Re-created Cluster CR %q with KubeConfigValid=True", name)
}

// invalidateClusterKubeConfig sets KubeConfigValid=False on a Cluster CR.
func invalidateClusterKubeConfig(t *testing.T, shortName string) {
	t.Helper()
	name := fullClusterName(shortName)
	mgmtCtx := "kind-" + mgmtClusterName

	statusPatch := `{"status":{"statusConditions":{"conditions":[{"type":"KubeConfigValid","status":"False"}]}}}`
	runCmd(t, "kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"patch", "cluster.greenhouse.sap", name,
		"--type=merge", "--subresource=status",
		"-p", statusPatch)

	t.Logf("Invalidated Cluster CR %q (KubeConfigValid=False)", name)
}

// updateASRPermissions updates an existing ASR's permissions.
func updateASRPermissions(t *testing.T, asrName string, permissions []pmv1.Permission) {
	t.Helper()
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	var asr pmv1.ApplicationSpecificRole
	key := client.ObjectKey{Namespace: controllerNamespace, Name: asrName}
	if err := mgmtClient.Get(context.Background(), key, &asr); err != nil {
		t.Fatalf("failed to get ASR %s: %v", asrName, err)
	}

	patch := client.MergeFrom(asr.DeepCopy())
	asr.Spec.Permissions = permissions
	if err := mgmtClient.Patch(context.Background(), &asr, patch); err != nil {
		t.Fatalf("failed to update ASR %s permissions: %v", asrName, err)
	}
	t.Logf("Updated ASR %q permissions", asrName)
}

// asrHasFinalizer checks if an ASR has the RBAC cleanup finalizer.
func asrHasFinalizer(t *testing.T, asrName string) bool {
	t.Helper()
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	var asr pmv1.ApplicationSpecificRole
	key := client.ObjectKey{Namespace: controllerNamespace, Name: asrName}
	if err := mgmtClient.Get(context.Background(), key, &asr); err != nil {
		t.Fatalf("failed to get ASR %s: %v", asrName, err)
	}

	for _, f := range asr.Finalizers {
		if f == "permission-manager.cloudoperators.dev/kubernetes" {
			return true
		}
	}
	return false
}

// asrExists checks if an ASR still exists.
func asrExists(t *testing.T, asrName string) bool {
	t.Helper()
	mgmtClient := crClientForCluster(t, mgmtClusterName)

	var asr pmv1.ApplicationSpecificRole
	key := client.ObjectKey{Namespace: controllerNamespace, Name: asrName}
	err := mgmtClient.Get(context.Background(), key, &asr)
	return err == nil
}
