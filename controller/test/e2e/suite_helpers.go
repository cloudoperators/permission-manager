//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	greenhouse "github.com/cloudoperators/permission-manager/controller/pkg/greenhouse"
)

// testScheme registers all types needed by the E2E test clients (core k8s + PM CRDs + Greenhouse).
var testScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(pmv1.AddToScheme(s))
	utilruntime.Must(greenhouse.AddToScheme(s))
	return s
}()

const (
	// clusterPrefix is the shared prefix for all e2e kind clusters.
	clusterPrefix = "pm-e2e-"

	// mgmtClusterName is the management cluster name.
	mgmtClusterName = clusterPrefix + "mgmt"

	// controllerNamespace is the namespace where the controller is deployed.
	controllerNamespace = "permission-manager"

	// controllerImage is the docker image used for the controller in e2e.
	controllerImage = "pm-controller:e2e"

	// githubProviderImage is the docker image used for the GitHub provider in E2E.
	githubProviderImage = "pm-controller-github:e2e"

	// greenhouseTeamsProviderImage is the docker image used for the greenhouse-teams provider in E2E.
	greenhouseTeamsProviderImage = "pm-controller-greenhouse-teams:e2e"

	// aggregatorImage is the docker image used for the status aggregator in E2E.
	aggregatorImage = "pm-controller-aggregator:e2e"

	// pollInterval is the default poll interval for waiting operations.
	pollInterval = 2 * time.Second

	// controllerReadyTimeout is how long to wait for the controller pod to become ready.
	controllerReadyTimeout = 60 * time.Second

	// resourcePropagationTimeout is how long to wait for RBAC resources to appear on consumer clusters.
	resourcePropagationTimeout = 60 * time.Second
)

// consumerCluster defines a consumer cluster and its pre-populated namespaces.
type consumerCluster struct {
	Name       string
	Namespaces []string
}

// consumerClusters is the allocation table from the PRD.
var consumerClusters = []consumerCluster{
	{
		Name:       clusterPrefix + "consumer-1",
		Namespaces: []string{"apps-prod", "apps-staging", "monitoring", "logging"},
	},
	{
		Name:       clusterPrefix + "consumer-2",
		Namespaces: []string{"apps-prod", "apps-dev", "monitoring"},
	},
	{
		Name:       clusterPrefix + "consumer-3",
		Namespaces: []string{"shoot--wireapi--prod", "shoot--wireapi--staging", "monitoring"},
	},
	{
		Name:       clusterPrefix + "consumer-4",
		Namespaces: []string{"apps-prod"},
	},
}

// allClusterNames returns the names of all clusters (mgmt + consumers).
func allClusterNames() []string {
	names := []string{mgmtClusterName}
	for _, c := range consumerClusters {
		names = append(names, c.Name)
	}
	return names
}

// testdataDir returns the absolute path to the testdata directory.
func testdataDir(t *testing.T) string {
	t.Helper()
	// Go test runs with CWD set to the package directory.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	return filepath.Join(dir, "testdata")
}

// repoRoot returns the absolute path to the repository root (parent of controller).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	// We are in controller/test/e2e, repo root is 3 levels up.
	return filepath.Join(dir, "..", "..", "..")
}

// runCmd executes a command and returns its combined output. Fails the test on error.
func runCmd(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("command %q %v failed: %v\nOutput:\n%s", name, args, err, out.String())
	}
	return out.String()
}

// runCmdNoFail executes a command and returns output + error without failing.
func runCmdNoFail(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// kindClusterExists checks if a kind cluster already exists.
func kindClusterExists(name string) bool {
	out, err := runCmdNoFail("kind", "get", "clusters")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// createKindClusters creates all kind clusters in parallel.
// If a cluster already exists, it is skipped (idempotent).
func createKindClusters(t *testing.T) {
	t.Helper()
	kindConfig := filepath.Join(testdataDir(t), "kind-config.yaml")

	clusters := allClusterNames()
	var wg sync.WaitGroup
	errors := make([]error, len(clusters))

	for i, name := range clusters {
		wg.Add(1)
		go func(idx int, clusterName string) {
			defer wg.Done()
			if kindClusterExists(clusterName) {
				t.Logf("Cluster %s already exists, skipping creation", clusterName)
				return
			}
			t.Logf("Creating kind cluster %s...", clusterName)
			out, err := runCmdNoFail("kind", "create", "cluster",
				"--name", clusterName,
				"--config", kindConfig,
				"--wait", "60s",
			)
			if err != nil {
				errors[idx] = fmt.Errorf("failed to create cluster %s, error %s: %s", clusterName, err, out)
			} else {
				t.Logf("Cluster %s created", clusterName)
			}
		}(i, name)
	}
	wg.Wait()

	for _, err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// deleteKindClusters deletes all e2e kind clusters.
func deleteKindClusters(t *testing.T) {
	t.Helper()
	clusters := allClusterNames()
	var wg sync.WaitGroup

	for _, name := range clusters {
		wg.Add(1)
		go func(clusterName string) {
			defer wg.Done()
			t.Logf("Deleting kind cluster %s...", clusterName)
			_, _ = runCmdNoFail("kind", "delete", "cluster", "--name", clusterName)
		}(name)
	}
	wg.Wait()
}

// getKubeconfig returns the kubeconfig for a kind cluster.
func getKubeconfig(t *testing.T, clusterName string) string {
	t.Helper()
	return runCmd(t, "kind", "get", "kubeconfig", "--name", clusterName)
}

// getInternalKubeconfig returns the internal kubeconfig for a kind cluster.
// This rewrites the server address to the control-plane container name for
// in-Docker connectivity.
func getInternalKubeconfig(t *testing.T, clusterName string) string {
	t.Helper()
	return runCmd(t, "kind", "get", "kubeconfig", "--internal", "--name", clusterName)
}

// kubeclientForCluster builds a kubernetes.Interface for the named kind cluster.
func kubeclientForCluster(t *testing.T, clusterName string) kubernetes.Interface {
	t.Helper()
	kubeconfig := getKubeconfig(t, clusterName)
	cfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		t.Fatalf("failed to build rest config for %s: %v", clusterName, err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("failed to build clientset for %s: %v", clusterName, err)
	}
	return cs
}

// crClientForCluster builds a controller-runtime client for the named kind cluster.
func crClientForCluster(t *testing.T, clusterName string) client.Client {
	t.Helper()
	kubeconfig := getKubeconfig(t, clusterName)
	cfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		t.Fatalf("failed to build rest config for %s: %v", clusterName, err)
	}
	c, err := client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		t.Fatalf("failed to build controller-runtime client for %s: %v", clusterName, err)
	}
	return c
}

// ensureControllerNamespace creates the controller namespace on the management cluster
// if it doesn't already exist. This must run before any resource creation that targets
// the permission-manager namespace (secrets, CRDs, Cluster CRs, etc.).
func ensureControllerNamespace(t *testing.T) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	nsYAML := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s`, controllerNamespace)
	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(nsYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create controller namespace: %v\n%s", err, out.String())
	}
}

// installCRDs installs CRD yamls on the management cluster.
func installCRDs(t *testing.T) {
	t.Helper()
	td := testdataDir(t)
	crdFiles := []string{
		filepath.Join(td, "crds", "pm-crds.yaml"),
		filepath.Join(td, "crds", "greenhouse-cluster.yaml"),
		filepath.Join(td, "crds", "github-provider.yaml"),
		filepath.Join(td, "crds", "greenhouse-teams-provider.yaml"),
	}

	for _, f := range crdFiles {
		t.Logf("Installing CRDs from %s", f)
		runCmd(t, "kubectl", "--context", "kind-"+mgmtClusterName, "apply", "-f", f)
	}

	// Wait for CRDs to be established
	crds := []string{
		"applicationspecificroles.permission-manager.cloudoperators.dev",
		"connectorbindings.permission-manager.cloudoperators.dev",
		"functionalroles.permission-manager.cloudoperators.dev",
		"serviceidentities.permission-manager.cloudoperators.dev",
		"clusters.greenhouse.sap",
		// GitHub provider CRDs
		"githubteams.repo-guard.cloudoperators.dev",
		"githubteamrepositories.repo-guard.cloudoperators.dev",
		"githuborganizations.repo-guard.cloudoperators.dev",
		// Greenhouse teams provider CRDs
		"teams.greenhouse.sap",
		"teamrolebindings.greenhouse.sap",
		"teamroles.greenhouse.sap",
	}
	for _, crd := range crds {
		runCmd(t, "kubectl", "--context", "kind-"+mgmtClusterName,
			"wait", "--for=condition=Established", "crd/"+crd, "--timeout=30s")
	}
}

// buildAndLoadControllerImage builds the controller docker image and loads it into the mgmt cluster.
// Skipped if E2E_SKIP_BUILD=1 (useful when images are already loaded from a prior run).
func buildAndLoadControllerImage(t *testing.T) {
	t.Helper()
	if os.Getenv("E2E_SKIP_BUILD") == "1" {
		t.Log("E2E_SKIP_BUILD=1: skipping controller image build")
		return
	}
	root := repoRoot(t)

	t.Logf("Building controller image from %s...", root)
	runCmd(t, "docker", "build",
		"-t", controllerImage,
		"-f", filepath.Join(root, "controller", "Dockerfile.e2e"),
		root,
	)

	t.Logf("Loading image into kind cluster %s...", mgmtClusterName)
	runCmd(t, "kind", "load", "docker-image", controllerImage, "--name", mgmtClusterName)
}

// deployController deploys the controller on the management cluster.
// If the deployment already exists, it performs a rollout restart to ensure
// the controller starts fresh (picking up ConnectorBindings and clusters).
func deployController(t *testing.T) {
	t.Helper()
	td := testdataDir(t)
	mgmtCtx := "kind-" + mgmtClusterName
	deployFiles := []string{
		filepath.Join(td, "deploy", "namespace.yaml"),
		filepath.Join(td, "deploy", "serviceaccount.yaml"),
		filepath.Join(td, "deploy", "clusterrole.yaml"),
		filepath.Join(td, "deploy", "clusterrolebinding.yaml"),
		filepath.Join(td, "deploy", "deployment.yaml"),
	}

	for _, f := range deployFiles {
		runCmd(t, "kubectl", "--context", mgmtCtx, "apply", "-f", f)
	}

	// Always rollout restart to ensure fresh controller state.
	// This is critical: the controller must start AFTER ConnectorBindings and
	// Cluster CRs exist so its startup sync picks them up correctly.
	t.Log("Restarting controller to ensure fresh state...")
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"rollout", "restart", "deployment", "pm-controller")
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"rollout", "status", "deployment", "pm-controller", "--timeout=60s")
}

// waitForControllerReady waits until the controller pod is Ready.
func waitForControllerReady(t *testing.T) {
	t.Helper()
	t.Logf("Waiting for controller pod to be ready (timeout: %s)...", controllerReadyTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), controllerReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Dump pod status for debugging
			out, _ := runCmdNoFail("kubectl", "--context", "kind-"+mgmtClusterName,
				"-n", controllerNamespace, "get", "pods", "-o", "wide")
			logs, _ := runCmdNoFail("kubectl", "--context", "kind-"+mgmtClusterName,
				"-n", controllerNamespace, "logs", "-l", "app=pm-controller", "--tail=50")
			t.Fatalf("controller pod not ready within %s\nPods:\n%s\nLogs:\n%s",
				controllerReadyTimeout, out, logs)
		case <-ticker.C:
			out, err := runCmdNoFail("kubectl", "--context", "kind-"+mgmtClusterName,
				"-n", controllerNamespace,
				"get", "deployment", "pm-controller",
				"-o", "jsonpath={.status.readyReplicas}")
			if err == nil && strings.TrimSpace(out) == "1" {
				t.Log("Controller pod is ready")
				return
			}
		}
	}
}

// createConsumerNamespaces creates the pre-populated namespaces on each consumer cluster.
func createConsumerNamespaces(t *testing.T) {
	t.Helper()
	for _, cc := range consumerClusters {
		ctx := "kind-" + cc.Name
		for _, ns := range cc.Namespaces {
			// Use apply to be idempotent
			nsYAML := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s`, ns)
			cmd := exec.Command("kubectl", "--context", ctx, "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nsYAML)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			if err := cmd.Run(); err != nil {
				t.Fatalf("failed to create namespace %s on %s: %v\n%s", ns, cc.Name, err, out.String())
			}
		}
		t.Logf("Created namespaces on %s: %v", cc.Name, cc.Namespaces)
	}
}

// connectClustersToDocker connects all kind cluster networks so they can reach each other.
// kind clusters share the "kind" docker network by default, so control-plane nodes
// can reach each other by container name (e.g., pm-e2e-consumer-1-control-plane:6443).
func connectClustersToDocker(t *testing.T) {
	t.Helper()
	// Verify connectivity by checking that the mgmt cluster control-plane can
	// resolve consumer cluster control-plane hostnames.
	// Since kind clusters are all on the same Docker network, this should work out of the box.
	t.Log("Verifying Docker network connectivity between kind clusters...")
	for _, cc := range consumerClusters {
		containerName := cc.Name + "-control-plane"
		out, err := runCmdNoFail("docker", "inspect", containerName,
			"--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}")
		if err != nil {
			t.Fatalf("failed to inspect container %s: %v\n%s", containerName, err, out)
		}
		t.Logf("  %s IP: %s", containerName, strings.TrimSpace(out))
	}
}

// createKubeconfigSecrets creates kubeconfig Secrets on the mgmt cluster for each consumer.
// These secrets use the internal kubeconfig (control-plane container address) so the
// controller pod can reach consumer clusters from within the Docker network.
func createKubeconfigSecrets(t *testing.T) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	for _, cc := range consumerClusters {
		// Get internal kubeconfig for the consumer cluster
		internalKubeconfig := getInternalKubeconfig(t, cc.Name)

		// The secret name is the cluster name (without prefix for simplicity in greenhouse naming).
		// The controller looks up secrets by the Cluster CR name.
		secretName := cc.Name

		// Create the secret in the controller's watch namespace
		cmd := exec.Command("kubectl", "--context", mgmtCtx,
			"-n", controllerNamespace,
			"create", "secret", "generic", secretName,
			"--from-literal=kubeconfig="+internalKubeconfig,
			"--dry-run=client", "-o", "yaml")
		var yamlOut bytes.Buffer
		cmd.Stdout = &yamlOut
		cmd.Stderr = &yamlOut
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to generate secret yaml for %s: %v\n%s", cc.Name, err, yamlOut.String())
		}

		// Apply the secret
		applyCmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
		applyCmd.Stdin = strings.NewReader(yamlOut.String())
		var applyOut bytes.Buffer
		applyCmd.Stdout = &applyOut
		applyCmd.Stderr = &applyOut
		if err := applyCmd.Run(); err != nil {
			t.Fatalf("failed to apply kubeconfig secret for %s: %v\n%s", cc.Name, err, applyOut.String())
		}
		t.Logf("Created kubeconfig Secret %q in namespace %q", secretName, controllerNamespace)
	}
}

// createGreenhouseClusterCRs creates Greenhouse Cluster CRs on the mgmt cluster with
// status.statusConditions[].type=KubeConfigValid, status=True.
func createGreenhouseClusterCRs(t *testing.T) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	for _, cc := range consumerClusters {
		clusterName := cc.Name

		// Create the Cluster CR
		clusterYAML := fmt.Sprintf(`apiVersion: greenhouse.sap/v1alpha1
kind: Cluster
metadata:
  name: %s
  namespace: %s
spec:
  accessMode: direct`, clusterName, controllerNamespace)

		cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(clusterYAML)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to create Cluster CR %s: %v\n%s", clusterName, err, out.String())
		}

		// Patch the status to set KubeConfigValid=True
		// We use a JSON merge-patch on the status subresource.
		statusPatch := `{"status":{"statusConditions":{"conditions":[{"type":"KubeConfigValid","status":"True"}]}}}`
		runCmd(t, "kubectl", "--context", mgmtCtx,
			"-n", controllerNamespace,
			"patch", "cluster.greenhouse.sap", clusterName,
			"--type=merge", "--subresource=status",
			"-p", statusPatch)

		t.Logf("Created Cluster CR %q with KubeConfigValid=True", clusterName)
	}
}

// createConnectorBinding creates the e2e-k8s ConnectorBinding on the mgmt cluster.
func createConnectorBinding(t *testing.T) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	cbYAML := fmt.Sprintf(`apiVersion: permission-manager.cloudoperators.dev/v1
kind: ConnectorBinding
metadata:
  name: e2e-k8s
  namespace: %s
spec:
  name: e2e-k8s
  provider: kubernetes
  definition: {}`, controllerNamespace)

	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(cbYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create ConnectorBinding: %v\n%s", err, out.String())
	}
	t.Log("Created ConnectorBinding e2e-k8s")
}

// waitForClusterRole waits for a ClusterRole with the given name to appear on the specified cluster.
func waitForClusterRole(t *testing.T, clusterName, clusterRoleName string, timeout time.Duration) {
	t.Helper()
	t.Logf("Waiting for ClusterRole %q on %s (timeout: %s)...", clusterRoleName, clusterName, timeout)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	clusterCtx := "kind-" + clusterName
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("ClusterRole %q did not appear on %s within %s", clusterRoleName, clusterName, timeout)
		case <-ticker.C:
			_, err := runCmdNoFail("kubectl", "--context", clusterCtx,
				"get", "clusterrole", clusterRoleName)
			if err == nil {
				t.Logf("ClusterRole %q found on %s", clusterRoleName, clusterName)
				return
			}
		}
	}
}

// getClusterRoleRules fetches the rules of a ClusterRole on a given cluster.
func getClusterRoleRules(t *testing.T, clusterName, clusterRoleName string) []map[string]interface{} {
	t.Helper()
	clusterCtx := "kind-" + clusterName
	out := runCmd(t, "kubectl", "--context", clusterCtx,
		"get", "clusterrole", clusterRoleName, "-o", "json")

	var cr map[string]interface{}
	if err := json.Unmarshal([]byte(out), &cr); err != nil {
		t.Fatalf("failed to parse ClusterRole JSON: %v", err)
	}

	rules, ok := cr["rules"].([]interface{})
	if !ok {
		t.Fatalf("ClusterRole %q has no rules field", clusterRoleName)
	}

	result := make([]map[string]interface{}, 0, len(rules))
	for _, r := range rules {
		rule, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		result = append(result, rule)
	}
	return result
}

// createASR creates an ApplicationSpecificRole on the management cluster.
func createASR(t *testing.T, name string, connectorRefs []string, permissions []map[string]string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	permsJSON := "["
	for i, p := range permissions {
		if i > 0 {
			permsJSON += ","
		}
		permsJSON += fmt.Sprintf(`{"resource":%q,"capability":%q}`, p["resource"], p["capability"])
	}
	permsJSON += "]"

	refsJSON := "["
	for i, r := range connectorRefs {
		if i > 0 {
			refsJSON += ","
		}
		refsJSON += fmt.Sprintf("%q", r)
	}
	refsJSON += "]"

	asrYAML := fmt.Sprintf(`apiVersion: permission-manager.cloudoperators.dev/v1
kind: ApplicationSpecificRole
metadata:
  name: %s
  namespace: %s
spec:
  name: %s
  connectorRefs: %s
  permissions: %s`, name, controllerNamespace, name, refsJSON, permsJSON)

	cmd := exec.Command("kubectl", "--context", mgmtCtx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(asrYAML)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create ASR %s: %v\n%s", name, err, out.String())
	}
	t.Logf("Created ASR %q", name)
}

// deleteASR deletes an ApplicationSpecificRole from the management cluster.
// It strips the finalizer via JSON patch (not merge patch, to avoid conflicts)
// and deletes with a timeout to prevent hanging.
func deleteASR(t *testing.T, name string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	// Strip finalizer using JSON patch (atomic — avoids resourceVersion conflicts)
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"patch", "applicationspecificrole", name,
		"--type=json", "-p", `[{"op":"replace","path":"/metadata/finalizers","value":[]}]`)
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"delete", "applicationspecificrole", name, "--ignore-not-found", "--timeout=15s")
}

// deleteASRGraceful deletes an ASR without stripping finalizers, allowing the
// controller's finalizer-based cleanup logic to run (cleanup of owned resources).
// Use this when the test needs to verify that the controller properly cleans up
// resources on ASR deletion.
func deleteASRGraceful(t *testing.T, name string) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"delete", "applicationspecificrole", name, "--ignore-not-found", "--timeout=30s")
}

// cleanupStaleASRs removes any ASRs left over from a previous test run.
// This ensures the controller starts with a clean slate and tests don't
// interfere with each other across runs.
// It strips finalizers first to avoid hanging on delete (controller may not be running).
func cleanupStaleASRs(t *testing.T) {
	t.Helper()
	mgmtCtx := "kind-" + mgmtClusterName

	// List existing ASRs
	out, err := runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"get", "applicationspecificrole", "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}

	// Strip finalizers from each ASR and then delete
	for _, name := range strings.Fields(out) {
		// Remove finalizers via patch
		_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
			"-n", controllerNamespace,
			"patch", "applicationspecificrole", name,
			"--type=merge", "-p", `{"metadata":{"finalizers":[]}}`)
		// Delete with a short timeout
		_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
			"-n", controllerNamespace,
			"delete", "applicationspecificrole", name, "--ignore-not-found", "--timeout=10s")
		t.Logf("Cleaned up stale ASR: %s", name)
	}

	// Also clean up any stale ConnectorBindings that aren't the expected set
	out, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"get", "connectorbinding", "-o", "jsonpath={.items[*].metadata.name}")
	expectedCBs := map[string]bool{
		"e2e-k8s":               true,
		"test-github":           true,
		"test-greenhouse-teams": true,
	}
	for _, name := range strings.Fields(out) {
		if !expectedCBs[name] {
			_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
				"-n", controllerNamespace,
				"delete", "connectorbinding", name, "--ignore-not-found")
			t.Logf("Cleaned up stale ConnectorBinding: %s", name)
		}
	}

	// Clean up stale GithubTeam and GithubTeamRepository CRs left from previous runs.
	// These persist when deleteASR strips finalizers (bypassing controller cleanup).
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"delete", "githubteam", "--all", "--ignore-not-found")
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"delete", "githubteamrepository", "--all", "--ignore-not-found")
	t.Log("Cleaned up stale GithubTeam and GithubTeamRepository CRs")

	// Clean up stale Team and TeamRoleBinding CRs left from previous runs.
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"delete", "team.greenhouse.sap", "--all", "--ignore-not-found")
	_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"delete", "teamrolebinding.greenhouse.sap", "--all", "--ignore-not-found")
	t.Log("Cleaned up stale Team and TeamRoleBinding CRs")

	// Clean up stale Cluster CRs that aren't part of the e2e consumer set.
	// These may be left over from status/reconcile tests (e.g., pm-e2e-broken-*).
	validClusters := make(map[string]bool)
	for _, c := range consumerClusters {
		validClusters[c.Name] = true
	}
	out, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
		"-n", controllerNamespace,
		"get", "cluster.greenhouse.sap", "-o", "jsonpath={.items[*].metadata.name}")
	for _, name := range strings.Fields(out) {
		if !validClusters[name] {
			_, _ = runCmdNoFail("kubectl", "--context", mgmtCtx,
				"-n", controllerNamespace,
				"delete", "cluster.greenhouse.sap", name, "--ignore-not-found")
			t.Logf("Cleaned up stale Cluster CR: %s", name)
		}
	}
}

// clusterRoleExists checks if a ClusterRole exists on a cluster.
func clusterRoleExists(t *testing.T, clusterName, clusterRoleName string) bool {
	t.Helper()
	clusterCtx := "kind-" + clusterName
	_, err := runCmdNoFail("kubectl", "--context", clusterCtx,
		"get", "clusterrole", clusterRoleName)
	return err == nil
}

// waitForClusterRoleAbsent waits for a ClusterRole to be removed from the specified cluster.
func waitForClusterRoleAbsent(t *testing.T, clusterName, clusterRoleName string, timeout time.Duration) {
	t.Helper()
	t.Logf("Waiting for ClusterRole %q to be removed from %s (timeout: %s)...", clusterRoleName, clusterName, timeout)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("ClusterRole %q still exists on %s after %s", clusterRoleName, clusterName, timeout)
		case <-ticker.C:
			if !clusterRoleExists(t, clusterName, clusterRoleName) {
				t.Logf("ClusterRole %q removed from %s", clusterRoleName, clusterName)
				return
			}
		}
	}
}

// controllerLogs fetches recent controller logs for debugging.
func controllerLogs(t *testing.T) string {
	t.Helper()
	out, _ := runCmdNoFail("kubectl", "--context", "kind-"+mgmtClusterName,
		"-n", controllerNamespace, "logs", "-l", "app=pm-controller", "--tail=100")
	return out
}

// buildAndLoadGithubProviderImage builds the GitHub provider docker image and loads it into the mgmt cluster.
func buildAndLoadGithubProviderImage(t *testing.T) {
	t.Helper()
	if os.Getenv("E2E_SKIP_BUILD") == "1" {
		t.Log("E2E_SKIP_BUILD=1: skipping GitHub provider image build")
		return
	}
	root := repoRoot(t)

	t.Logf("Building GitHub provider image from %s...", root)
	runCmd(t, "docker", "build",
		"-t", githubProviderImage,
		"-f", filepath.Join(root, "controller", "Dockerfile.github.e2e"),
		root,
	)

	t.Logf("Loading GitHub provider image into kind cluster %s...", mgmtClusterName)
	runCmd(t, "kind", "load", "docker-image", githubProviderImage, "--name", mgmtClusterName)
}

// buildAndLoadGreenhouseTeamsProviderImage builds the greenhouse-teams provider docker image
// and loads it into the mgmt cluster.
func buildAndLoadGreenhouseTeamsProviderImage(t *testing.T) {
	t.Helper()
	if os.Getenv("E2E_SKIP_BUILD") == "1" {
		t.Log("E2E_SKIP_BUILD=1: skipping greenhouse-teams provider image build")
		return
	}
	root := repoRoot(t)

	t.Logf("Building greenhouse-teams provider image from %s...", root)
	runCmd(t, "docker", "build",
		"-t", greenhouseTeamsProviderImage,
		"-f", filepath.Join(root, "controller", "Dockerfile.greenhouse-teams.e2e"),
		root,
	)

	t.Logf("Loading greenhouse-teams provider image into kind cluster %s...", mgmtClusterName)
	runCmd(t, "kind", "load", "docker-image", greenhouseTeamsProviderImage, "--name", mgmtClusterName)
}

// deployGithubProvider deploys the GitHub provider on the management cluster.
func deployGithubProvider(t *testing.T) {
	t.Helper()
	td := testdataDir(t)
	mgmtCtx := "kind-" + mgmtClusterName

	deployFile := filepath.Join(td, "deploy", "deployment-github.yaml")
	runCmd(t, "kubectl", "--context", mgmtCtx, "apply", "-f", deployFile)

	// Rollout restart to pick up fresh state
	t.Log("Restarting GitHub provider to ensure fresh state...")
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"rollout", "restart", "deployment", "pm-controller-github")
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"rollout", "status", "deployment", "pm-controller-github", "--timeout=60s")
}

// deployGreenhouseTeamsProvider deploys the greenhouse-teams provider on the management cluster.
func deployGreenhouseTeamsProvider(t *testing.T) {
	t.Helper()
	td := testdataDir(t)
	mgmtCtx := "kind-" + mgmtClusterName

	deployFile := filepath.Join(td, "deploy", "deployment-greenhouse-teams.yaml")
	runCmd(t, "kubectl", "--context", mgmtCtx, "apply", "-f", deployFile)

	// Rollout restart to pick up fresh state
	t.Log("Restarting greenhouse-teams provider to ensure fresh state...")
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"rollout", "restart", "deployment", "pm-controller-greenhouse-teams")
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"rollout", "status", "deployment", "pm-controller-greenhouse-teams", "--timeout=60s")
}

// buildAndLoadAggregatorImage builds the aggregator docker image and loads it into the mgmt cluster.
func buildAndLoadAggregatorImage(t *testing.T) {
	t.Helper()
	if os.Getenv("E2E_SKIP_BUILD") == "1" {
		t.Log("E2E_SKIP_BUILD=1: skipping aggregator image build")
		return
	}
	root := repoRoot(t)

	t.Logf("Building aggregator image from %s...", root)
	runCmd(t, "docker", "build",
		"-t", aggregatorImage,
		"-f", filepath.Join(root, "controller", "Dockerfile.aggregator.e2e"),
		root,
	)

	t.Logf("Loading aggregator image into kind cluster %s...", mgmtClusterName)
	runCmd(t, "kind", "load", "docker-image", aggregatorImage, "--name", mgmtClusterName)
}

// deployAggregator deploys the status aggregator on the management cluster.
func deployAggregator(t *testing.T) {
	t.Helper()
	td := testdataDir(t)
	mgmtCtx := "kind-" + mgmtClusterName

	deployFile := filepath.Join(td, "deploy", "deployment-aggregator.yaml")
	runCmd(t, "kubectl", "--context", mgmtCtx, "apply", "-f", deployFile)

	// Rollout restart to pick up fresh state
	t.Log("Restarting aggregator to ensure fresh state...")
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"rollout", "restart", "deployment", "pm-controller-aggregator")
	runCmd(t, "kubectl", "--context", mgmtCtx, "-n", controllerNamespace,
		"rollout", "status", "deployment", "pm-controller-aggregator", "--timeout=60s")
}
