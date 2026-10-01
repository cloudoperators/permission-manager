// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// TestEdge_Operational_CBCapabilityMappingUpdate creates a custom GitHub
// ConnectorBinding with a specific capabilityMapping, creates an ASR that
// references it, verifies the GithubTeamRepository CR uses the mapped
// permission, then patches the CB's capabilityMapping and verifies the
// GithubTeamRepository CR is updated with the new permission value.
func TestEdge_Operational_CBCapabilityMappingUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-cb-remap"
	cbName := "test-github-remap"

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	mgmtCtx := "kind-" + mgmtClusterName

	// Step 1: Create a custom GitHub ConnectorBinding with read->pull, write->push.
	definition := map[string]interface{}{
		"capabilityMapping": map[string]string{
			"read":  "pull",
			"write": "push",
		},
		"defaultTargetNamespace": controllerNamespace,
	}
	defJSON, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("failed to marshal definition: %v", err)
	}

	cbYAML := fmt.Sprintf(`apiVersion: permission-manager.cloudoperators.dev/v1
kind: ConnectorBinding
metadata:
  name: %s
  namespace: %s
spec:
  name: %s
  provider: github
  definition: %s`, cbName, controllerNamespace, cbName, string(defJSON))

	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(cbYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create custom GitHub ConnectorBinding: %v\n%s", err, out.String())
	}
	t.Logf("Created ConnectorBinding %q with capabilityMapping read->pull", cbName)

	t.Cleanup(func() {
		deleteConnectorBindingCR(t, cbName)
		deleteASR(t, asrName)
	})

	// Step 2: Create ASR referencing both the custom github CB and e2e-k8s, with
	// a github repo permission using capability "read".
	createASR(t, asrName, []string{cbName, "e2e-k8s"}, []map[string]string{
		{"resource": "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/e2e-org/test-repo", "capability": "read"},
		{"resource": clusterCCRN(consumerClusters[0].Name), "capability": "READ"},
	})

	// Step 3: Wait for the GithubTeamRepository to appear with spec.permission = "pull".
	gtrName := githubTeamRepoCRName("e2e-org", asrName, "pull")
	assertGithubTeamRepoExists(t, ctx, mgmtClient, controllerNamespace, gtrName)
	assertGithubTeamRepoPermission(t, ctx, mgmtClient, controllerNamespace, gtrName, "pull")
	t.Log("GithubTeamRepository created with permission=pull")

	// Step 4: Patch the CB's capabilityMapping to change read->triage.
	newDefinition := map[string]interface{}{
		"capabilityMapping": map[string]string{
			"read":  "triage",
			"write": "push",
		},
		"defaultTargetNamespace": controllerNamespace,
	}
	newDefJSON, err := json.Marshal(newDefinition)
	if err != nil {
		t.Fatalf("failed to marshal new definition: %v", err)
	}

	patchPayload := fmt.Sprintf(`{"spec":{"definition":%s}}`, string(newDefJSON))
	runCmd(t, "kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"patch", "connectorbinding", cbName,
		"--type=merge", "-p", patchPayload)
	t.Log("Patched ConnectorBinding capabilityMapping: read -> triage")

	// Step 5: Force reconcile to propagate the change.
	forceReconcile(t, asrName)

	// Step 6: Verify the GithubTeamRepository CR is updated/recreated with permission = "triage".
	// The old "pull" GTR might be deleted and a new "triage" one created, or the existing one updated.
	// We check for a GTR with permission "triage" using a list-by-label approach.
	newGtrName := githubTeamRepoCRName("e2e-org", asrName, "triage")
	err = pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		// Check if a GTR with the new name and triage permission exists
		var list unstructured.UnstructuredList
		list.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubTeamRepositoryList",
		})
		if listErr := mgmtClient.List(ctx, &list, client.InNamespace(controllerNamespace),
			client.MatchingLabels{"permission-manager.cloudoperators.dev/asr": asrName}); listErr != nil {
			return false, listErr
		}
		for _, item := range list.Items {
			perm, _, _ := unstructured.NestedString(item.Object, "spec", "permission")
			if perm == "triage" {
				return true, nil
			}
		}
		// Also check by exact name
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "repo-guard.cloudoperators.dev",
			Version: "v1",
			Kind:    "GithubTeamRepository",
		})
		key := types.NamespacedName{Namespace: controllerNamespace, Name: newGtrName}
		if getErr := mgmtClient.Get(ctx, key, obj); getErr == nil {
			perm, _, _ := unstructured.NestedString(obj.Object, "spec", "permission")
			if perm == "triage" {
				return true, nil
			}
		}
		return false, fmt.Errorf("no GithubTeamRepository with permission=triage found yet")
	})
	if err != nil {
		t.Fatalf("GithubTeamRepository was not updated to permission=triage: %v", err)
	}

	t.Log("SUCCESS: CB capabilityMapping update propagated to GithubTeamRepository (pull -> triage)")
}

// TestEdge_Operational_DeprecatedFinalizerMigration creates an ASR with READ on
// consumer-1, waits for the controller to add its finalizer, then manually adds
// the deprecated finalizer. After forcing a reconcile, it verifies the deprecated
// finalizer is removed and only the current finalizer remains.
func TestEdge_Operational_DeprecatedFinalizerMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-finalizer-migrate"
	consumer1 := consumerClusters[0].Name
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	kubeClient := kubeclientForCluster(t, consumer1)
	clients := map[string]kubernetes.Interface{consumer1: kubeClient}
	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, clients) })
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Step 1: Create ASR with READ on consumer-1.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer1), "capability": "READ"},
	})

	// Step 2: Wait for the controller to add the current finalizer.
	waitForClusterRole(t, consumer1, expectedCR, resourcePropagationTimeout)

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: controllerNamespace, Name: asrName}
		if getErr := mgmtClient.Get(ctx, key, &asr); getErr != nil {
			return false, getErr
		}
		for _, f := range asr.Finalizers {
			if f == "permission-manager.cloudoperators.dev/kubernetes" {
				return true, nil
			}
		}
		return false, fmt.Errorf("current finalizer not yet added (finalizers: %v)", asr.Finalizers)
	})
	if err != nil {
		t.Fatalf("controller did not add current finalizer: %v", err)
	}
	t.Log("Controller added current finalizer 'permission-manager.cloudoperators.dev/kubernetes'")

	// Step 3: Manually add the deprecated finalizer via patch.
	mgmtCtx := "kind-" + mgmtClusterName
	patch := `{"metadata":{"finalizers":["permission-manager.cloudoperators.dev/kubernetes","permission-manager.cloudoperators.dev/rbac-cleanup"]}}`
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"patch", "applicationspecificrole", asrName,
		"--type=merge", "-p", patch)
	t.Log("Added deprecated finalizer 'permission-manager.cloudoperators.dev/rbac-cleanup'")

	// Step 4: Force reconcile to trigger finalizer migration.
	forceReconcile(t, asrName)

	// Step 5: Verify the deprecated finalizer is removed and only the current one remains.
	err = pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: controllerNamespace, Name: asrName}
		if getErr := mgmtClient.Get(ctx, key, &asr); getErr != nil {
			return false, getErr
		}
		hasDeprecated := false
		hasCurrent := false
		for _, f := range asr.Finalizers {
			if f == "permission-manager.cloudoperators.dev/rbac-cleanup" {
				hasDeprecated = true
			}
			if f == "permission-manager.cloudoperators.dev/kubernetes" {
				hasCurrent = true
			}
		}
		if hasDeprecated {
			return false, fmt.Errorf("deprecated finalizer still present (finalizers: %v)", asr.Finalizers)
		}
		if !hasCurrent {
			return false, fmt.Errorf("current finalizer missing (finalizers: %v)", asr.Finalizers)
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("deprecated finalizer was not migrated: %v", err)
	}

	t.Log("SUCCESS: Deprecated finalizer removed, only current finalizer remains")
}

// TestEdge_Operational_ClusterKubeconfigRotation tests that after the cluster's
// kubeconfig Secret is updated (simulating cert rotation), the controller
// continues to operate correctly. It creates an ASR, waits for the ClusterRole,
// then updates the kubeconfig Secret with a fresh copy and verifies the
// controller still functions.
func TestEdge_Operational_ClusterKubeconfigRotation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	asrName := "edge-kubeconfig-rotate"
	consumer4 := consumerClusters[3].Name
	expectedCR := resourcePrefix + asrName

	mgmtClient := crClientForCluster(t, mgmtClusterName)
	kubeClient := kubeclientForCluster(t, consumer4)
	clients := map[string]kubernetes.Interface{consumer4: kubeClient}
	t.Cleanup(func() { dumpOnFailure(t, mgmtClient, clients) })
	t.Cleanup(func() { deleteASR(t, asrName) })

	// Step 1: Create ASR with READ on consumer-4.
	createASR(t, asrName, []string{connectorRef}, []map[string]string{
		{"resource": clusterCCRN(consumer4), "capability": "READ"},
	})

	// Step 2: Wait for ClusterRole to appear on consumer-4.
	waitForClusterRole(t, consumer4, expectedCR, resourcePropagationTimeout)
	t.Logf("ClusterRole %q confirmed on %s", expectedCR, consumer4)

	// Step 3: Update the kubeconfig Secret for consumer-4 with a fresh copy
	// (simulating certificate rotation to a new valid cert).
	mgmtCtx := "kind-" + mgmtClusterName
	kubeconfig := getInternalKubeconfig(t, consumer4)

	// Generate the secret YAML via dry-run
	genCmd := exec.Command("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"create", "secret", "generic", consumer4,
		"--from-literal=kubeconfig="+kubeconfig,
		"--dry-run=client", "-o", "yaml")
	var yamlOut bytes.Buffer
	genCmd.Stdout = &yamlOut
	genCmd.Stderr = &yamlOut
	if err := genCmd.Run(); err != nil {
		t.Fatalf("failed to generate rotated kubeconfig secret yaml: %v\n%s", err, yamlOut.String())
	}

	// Apply the rotated secret
	applyCmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(yamlOut.String())
	var applyOut bytes.Buffer
	applyCmd.Stdout = &applyOut
	applyCmd.Stderr = &applyOut
	if err := applyCmd.Run(); err != nil {
		t.Fatalf("failed to apply rotated kubeconfig secret: %v\n%s", err, applyOut.String())
	}
	t.Logf("Updated kubeconfig Secret for %s (simulating cert rotation)", consumer4)

	// Step 4: Force reconcile to ensure the controller picks up the new secret.
	forceReconcile(t, asrName)

	// Step 5: Verify ClusterRole still exists on consumer-4 (controller still functional).
	err := pollUntil(t, ctx, resourcePropagationTimeout, pollInterval, func() (bool, error) {
		if clusterRoleExists(t, consumer4, expectedCR) {
			return true, nil
		}
		return false, fmt.Errorf("ClusterRole %q not found on %s after kubeconfig rotation", expectedCR, consumer4)
	})
	if err != nil {
		t.Fatalf("ClusterRole disappeared after kubeconfig rotation: %v", err)
	}

	// Step 6: Verify the kubernetes provider reports success for consumer-4.
	expectClusterStatus(t, ctx, mgmtClient, controllerNamespace, asrName, consumer4, "Success")

	t.Log("SUCCESS: Controller continues to operate after kubeconfig Secret rotation")
}
