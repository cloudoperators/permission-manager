// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func init() {
	// Suppress the "log.SetLogger(...) was never called" warning from
	// controller-runtime when creating clients in test helpers.
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))
}

// TestMain manages the full e2e lifecycle:
// 1. Create kind clusters in parallel
// 2. Install CRDs on mgmt cluster
// 3. Build and load controller image
// 4. Deploy controller
// 5. Set up consumer namespaces
// 6. Create kubeconfig Secrets and Greenhouse simulation
// 7. Run tests
// 8. Tear down (unless E2E_KEEP_CLUSTERS=1)
func TestMain(m *testing.M) {
	// Create a wrapper testing.T for setup/teardown logging.
	// Since TestMain doesn't have a *testing.T, we use a setup test.
	code := runE2E(m)
	os.Exit(code)
}

func runE2E(m *testing.M) int {
	// Check prerequisites
	if _, err := runCmdNoFail("kind", "--version"); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: 'kind' CLI not found in PATH. Install from https://kind.sigs.k8s.io/\n")
		return 1
	}
	if _, err := runCmdNoFail("kubectl", "version", "--client"); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: 'kubectl' CLI not found in PATH\n")
		return 1
	}
	if _, err := runCmdNoFail("docker", "info"); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Docker is not running or not accessible\n")
		return 1
	}

	// Run tests (setup happens in TestSetup which must run first)
	code := m.Run()

	// Cleanup
	if os.Getenv("E2E_KEEP_CLUSTERS") == "1" {
		fmt.Println("E2E_KEEP_CLUSTERS=1: keeping clusters alive for debugging")
	} else {
		fmt.Println("Cleaning up kind clusters...")
		cleanup()
	}

	return code
}

// cleanup deletes all e2e kind clusters. Safe to call even if clusters don't exist.
func cleanup() {
	clusters := allClusterNames()
	for _, name := range clusters {
		fmt.Printf("  Deleting cluster %s...\n", name)
		_, _ = runCmdNoFail("kind", "delete", "cluster", "--name", name)
	}
}

// Test000_Setup creates the infrastructure. It must run before any other test.
// The "000" prefix ensures alphabetical ordering places it first.
// This function is idempotent: re-running it with existing clusters is safe.
func Test000_Setup(t *testing.T) {
	t.Log("=== E2E SETUP: Creating infrastructure ===")

	t.Log("Step 1/15: Creating kind clusters...")
	createKindClusters(t)

	t.Log("Step 2/15: Installing CRDs on management cluster...")
	installCRDs(t)

	t.Log("Step 3/15: Building and loading controller image...")
	buildAndLoadControllerImage(t)

	t.Log("Step 4/15: Building and loading GitHub provider image...")
	buildAndLoadGithubProviderImage(t)

	t.Log("Step 5/15: Building and loading greenhouse-teams provider image...")
	buildAndLoadGreenhouseTeamsProviderImage(t)

	t.Log("Step 6/15: Building and loading aggregator image...")
	buildAndLoadAggregatorImage(t)

	t.Log("Step 7/15: Creating controller namespace...")
	ensureControllerNamespace(t)

	t.Log("Step 8/15: Creating consumer namespaces...")
	createConsumerNamespaces(t)

	t.Log("Step 9/15: Verifying Docker network connectivity...")
	connectClustersToDocker(t)

	t.Log("Step 10/15: Setting up Greenhouse simulation...")
	createKubeconfigSecrets(t)
	createGreenhouseClusterCRs(t)
	createConnectorBinding(t)

	t.Log("Step 11/15: Creating provider ConnectorBindings...")
	createGithubConnectorBinding(t)
	createGreenhouseTeamsConnectorBinding(t)

	t.Log("Step 12/15: Pre-seeding provider resources...")
	createGithubOrganization(t, controllerNamespace, "e2e-org")
	createTeamRole(t, controllerNamespace, "cluster-viewer")
	createTeamRole(t, controllerNamespace, "cluster-developer")
	createTeamRole(t, controllerNamespace, "cluster-admin")

	t.Log("Step 13/15: Cleaning up stale ASRs from previous runs...")
	cleanupStaleASRs(t)

	t.Log("Step 14/15: Deploying controllers...")
	deployController(t)
	deployGithubProvider(t)
	deployGreenhouseTeamsProvider(t)
	deployAggregator(t)

	t.Log("Step 15/15: Waiting for all controllers to become ready...")
	waitForControllerReady(t)
	waitForGithubProviderReady(t)
	waitForGreenhouseTeamsProviderReady(t)

	t.Log("=== E2E SETUP COMPLETE ===")
}

// TestSmoke_CreateASR_ClusterRoleAppears validates the core pipeline:
// Create an ASR targeting consumer-1 via a cluster-scoped CCRN resource,
// then verify that the expected ClusterRole (pm-<asr-name>) appears on consumer-1.
func TestSmoke_CreateASR_ClusterRoleAppears(t *testing.T) {
	asrName := "smoke-reader"
	consumer1 := consumerClusters[0].Name // pm-e2e-consumer-1
	expectedCR := "pm-" + asrName

	// The CCRN resource targeting consumer-1 cluster-wide:
	// urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/<cluster-name>
	resource := fmt.Sprintf("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/%s", consumer1)

	t.Logf("Creating ASR %q with resource %q", asrName, resource)
	createASR(t, asrName, []string{"e2e-k8s"}, []map[string]string{
		{"resource": resource, "capability": "READ"},
	})

	// Cleanup on test completion
	t.Cleanup(func() {
		deleteASR(t, asrName)
	})

	// Wait for ClusterRole to appear on consumer-1
	waitForClusterRole(t, consumer1, expectedCR, resourcePropagationTimeout)

	// Verify the ClusterRole has expected rules
	rules := getClusterRoleRules(t, consumer1, expectedCR)
	if len(rules) == 0 {
		t.Fatalf("ClusterRole %q has no rules", expectedCR)
	}

	// The READ capability maps to: get, list, watch
	// Verify the rule contains these verbs
	rule := rules[0]
	verbs, ok := rule["verbs"].([]interface{})
	if !ok {
		t.Fatalf("ClusterRole rule has no verbs: %+v", rule)
	}

	expectedVerbs := map[string]bool{"get": false, "list": false, "watch": false}
	for _, v := range verbs {
		vs, _ := v.(string)
		if _, exists := expectedVerbs[vs]; exists {
			expectedVerbs[vs] = true
		}
	}
	for verb, found := range expectedVerbs {
		if !found {
			t.Errorf("ClusterRole %q missing expected verb %q. Got verbs: %v", expectedCR, verb, verbs)
		}
	}

	t.Logf("SUCCESS: ClusterRole %q with correct verbs found on %s", expectedCR, consumer1)
}
