// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// Contexts and names for the shared dev environment (hack/setup.mk),
// overridable via env vars to run against other clusters.
var (
	adminContext        = envOr("E2E_ADMIN_CONTEXT", "kind-greenhouse-admin")
	remoteContext       = envOr("E2E_REMOTE_CONTEXT", "kind-greenhouse-remote")
	orgNamespace        = envOr("E2E_ORG", "demo")
	remoteClusterName   = envOr("E2E_REMOTE_CLUSTER", "kind-greenhouse-remote")
	controllerNamespace = envOr("E2E_CONTROLLER_NAMESPACE", "greenhouse")
	controllerDeploy    = envOr("E2E_CONTROLLER_DEPLOYMENT", "permission-manager-controller-manager")
	providerConfigMap   = envOr("E2E_PROVIDER_CONFIGMAP", "permission-manager-provider-cm")
)

const (
	pollInterval               = 2 * time.Second
	resourcePropagationTimeout = 60 * time.Second
	defaultAssertTimeout       = 30 * time.Second
	assertPollInterval         = 2 * time.Second
	fieldOwner                 = client.FieldOwner("pm-e2e")
	remoteTestNamespace        = "apps-prod"

	managedByLabel      = "app.kubernetes.io/managed-by"
	managedByController = "pm-controller"
	managedByGithub     = "github"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var testScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(pmv1.AddToScheme(s))
	return s
}()

// Initialized once per ginkgo process in the suite setup.
var (
	adminClient     client.Client
	remoteClient    client.Client
	adminRestConfig *rest.Config
)

func initClients() {
	if adminClient != nil {
		return
	}
	adminRestConfig = restConfigForContext(adminContext)
	adminClient = newClient(adminRestConfig, adminContext)
	remoteClient = newClient(restConfigForContext(remoteContext), remoteContext)
}

func restConfigForContext(kubeCtx string) *rest.Config {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: kubeCtx},
	).ClientConfig()
	Expect(err).NotTo(HaveOccurred(), "build rest config for context %s", kubeCtx)
	return cfg
}

func newClient(cfg *rest.Config, kubeCtx string) client.Client {
	c, err := client.New(cfg, client.Options{Scheme: testScheme})
	Expect(err).NotTo(HaveOccurred(), "build client for context %s", kubeCtx)
	return c
}

func greenhouseCluster() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha1", Kind: "Cluster"})
	return obj
}

func verifyEnvironment() {
	GinkgoHelper()
	ctx := context.Background()

	reachCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	Expect(adminClient.List(reachCtx, &corev1.NamespaceList{})).
		To(Succeed(), "admin cluster %q unreachable; run 'make setup' first", adminContext)
	Expect(remoteClient.List(reachCtx, &corev1.NamespaceList{})).
		To(Succeed(), "remote cluster %q unreachable; run 'make setup' first", remoteContext)

	// The Ready condition can flap on kubeconfig rotation, so poll instead of
	// sampling it once.
	Eventually(func(g Gomega) {
		cluster := greenhouseCluster()
		g.Expect(adminClient.Get(ctx, types.NamespacedName{Namespace: orgNamespace, Name: remoteClusterName}, cluster)).
			To(Succeed(), "Cluster CR %q not found in namespace %q; run 'make demo' first", remoteClusterName, orgNamespace)
		g.Expect(clusterReadyStatus(cluster)).To(Equal("True"),
			"Cluster CR %q is not Ready; re-run 'make demo'", remoteClusterName)
	}).WithTimeout(defaultAssertTimeout).WithPolling(pollInterval).Should(Succeed())

	Expect(adminClient.List(ctx, &pmv1.ApplicationSpecificRoleList{}, client.InNamespace(orgNamespace))).
		To(Succeed(), "permission-manager CRDs missing; run 'make docker-build kind-load deploy' first")

	var cm corev1.ConfigMap
	Expect(adminClient.Get(ctx, types.NamespacedName{Namespace: controllerNamespace, Name: providerConfigMap}, &cm)).
		To(Succeed(), "provider ConfigMap %q not found in namespace %q; run 'make setup-e2e' first", providerConfigMap, controllerNamespace)
	for _, provider := range []string{"kubernetes", "github"} {
		Expect(cm.Data[provider]).To(Equal("true"),
			"%s provider disabled in ConfigMap %q; deploy via 'make setup-e2e'", provider, providerConfigMap)
	}

	Eventually(func(g Gomega) {
		var d appsv1.Deployment
		g.Expect(adminClient.Get(ctx, types.NamespacedName{Namespace: controllerNamespace, Name: controllerDeploy}, &d)).
			To(Succeed(), "controller deployment missing; run 'make docker-build kind-load deploy' first")
		g.Expect(d.Status.ReadyReplicas).To(BeNumerically(">=", 1), "controller not ready")
	}).WithTimeout(resourcePropagationTimeout).WithPolling(pollInterval).Should(Succeed())
}

func clusterReadyStatus(cluster *unstructured.Unstructured) string {
	conditions, _, _ := unstructured.NestedSlice(cluster.Object, "status", "statusConditions", "conditions")
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] == "Ready" {
			status, _ := cond["status"].(string)
			return status
		}
	}
	return ""
}

func applyFixtures() {
	GinkgoHelper()
	for _, f := range []string{
		"setup/connectorbinding-k8s.yaml",
		"setup/connectorbinding-github.yaml",
		"setup/github-organization.yaml",
	} {
		raw, err := os.ReadFile(filepath.Join(testdataDir(), f))
		Expect(err).NotTo(HaveOccurred(), "read %s", f)
		obj := &unstructured.Unstructured{}
		Expect(kyaml.Unmarshal(raw, &obj.Object)).To(Succeed(), "decode %s", f)
		obj.SetNamespace(orgNamespace)
		Expect(adminClient.Patch(context.Background(), obj, client.Apply, fieldOwner, client.ForceOwnership)).
			To(Succeed(), "apply %s", f)
	}
}

func ensureRemoteTestNamespace() {
	GinkgoHelper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: remoteTestNamespace}}
	Expect(client.IgnoreAlreadyExists(remoteClient.Create(context.Background(), ns))).
		To(Succeed(), "create namespace %q on remote", remoteTestNamespace)
}

func testdataDir() string {
	GinkgoHelper()
	// Go test runs with CWD set to the package directory.
	dir, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred(), "get working directory")
	return filepath.Join(dir, "testdata")
}

// ASRs go first so the controller finalizer removes what it owns; the sweeps
// catch orphans left by a stripped finalizer, which would otherwise
// false-green the existence asserts.
func cleanupStaleResources() {
	ctx := context.Background()

	var asrs pmv1.ApplicationSpecificRoleList
	if err := adminClient.List(ctx, &asrs, client.InNamespace(orgNamespace)); err != nil {
		GinkgoWriter.Printf("list stale ASRs: %v\n", err)
	}
	for _, asr := range asrs.Items {
		deleteASR(asr.Name)
	}

	for _, kind := range []string{"GithubTeam", "GithubTeamRepository"} {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{Group: "repo-guard.cloudoperators.dev", Version: "v1", Kind: kind + "List"})
		if err := adminClient.List(ctx, list, client.InNamespace(orgNamespace), client.MatchingLabels{managedByLabel: managedByGithub}); err != nil {
			GinkgoWriter.Printf("list stale %s: %v\n", kind, err)
			continue
		}
		for i := range list.Items {
			obj := &list.Items[i]
			if err := client.IgnoreNotFound(adminClient.Delete(ctx, obj)); err != nil {
				GinkgoWriter.Printf("delete stale %s %s: %v\n", kind, obj.GetName(), err)
			}
			if len(obj.GetFinalizers()) == 0 {
				continue
			}
			if err := stripFinalizers(ctx, adminClient, obj); err != nil {
				GinkgoWriter.Printf("strip finalizers on stale %s %s: %v\n", kind, obj.GetName(), err)
			}
		}
	}

	sweep := func(obj client.Object, opts ...client.DeleteAllOfOption) {
		opts = append(opts, client.MatchingLabels{managedByLabel: managedByController})
		if err := remoteClient.DeleteAllOf(ctx, obj, opts...); err != nil {
			GinkgoWriter.Printf("sweep stale %T on remote: %v\n", obj, err)
		}
	}
	sweep(&rbacv1.ClusterRoleBinding{})
	sweep(&rbacv1.ClusterRole{})
	sweep(&rbacv1.RoleBinding{}, client.InNamespace(remoteTestNamespace))
	sweep(&rbacv1.Role{}, client.InNamespace(remoteTestNamespace))
}

func createASR(name string, connectorRefs []string, permissions []map[string]string) {
	GinkgoHelper()
	perms := make([]pmv1.Permission, len(permissions))
	for i, p := range permissions {
		perms[i] = pmv1.Permission{Resource: p["resource"], Capability: p["capability"]}
	}
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: orgNamespace},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          name,
			ConnectorRefs: connectorRefs,
			Permissions:   perms,
		},
	}
	Expect(adminClient.Create(context.Background(), asr)).To(Succeed(), "create ASR %s", name)
}

// The clusters outlive the test run, so let the finalizer clean up remote
// RBAC. Strip it only when the delete hangs.
func deleteASR(name string) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: orgNamespace, Name: name}
	asr := &pmv1.ApplicationSpecificRole{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: orgNamespace}}
	if err := adminClient.Delete(ctx, asr); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		GinkgoWriter.Printf("delete ASR %s: %v\n", name, err)
	}

	gone := func() bool {
		return apierrors.IsNotFound(adminClient.Get(ctx, key, &pmv1.ApplicationSpecificRole{}))
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if gone() {
			return
		}
		time.Sleep(pollInterval)
	}

	GinkgoWriter.Printf("ASR %s delete timed out, stripping finalizers\n", name)
	if err := stripFinalizers(ctx, adminClient, asr); err != nil {
		GinkgoWriter.Printf("strip finalizers on ASR %s: %v\n", name, err)
	}
}

func stripFinalizers(ctx context.Context, c client.Client, obj client.Object) error {
	patch := client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`))
	return client.IgnoreNotFound(c.Patch(ctx, obj, patch))
}

func dumpControllerLogs() {
	ctx := context.Background()
	var d appsv1.Deployment
	if err := adminClient.Get(ctx, types.NamespacedName{Namespace: controllerNamespace, Name: controllerDeploy}, &d); err != nil {
		GinkgoWriter.Printf("get controller deployment: %v\n", err)
		return
	}
	cs, err := kubernetes.NewForConfig(adminRestConfig)
	if err != nil {
		GinkgoWriter.Printf("build admin clientset: %v\n", err)
		return
	}
	pods, err := cs.CoreV1().Pods(controllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set(d.Spec.Selector.MatchLabels).String(),
	})
	if err != nil {
		GinkgoWriter.Printf("list controller pods: %v\n", err)
		return
	}
	tail := int64(200)
	for _, pod := range pods.Items {
		raw, err := cs.CoreV1().Pods(controllerNamespace).
			GetLogs(pod.Name, &corev1.PodLogOptions{TailLines: &tail}).DoRaw(ctx)
		if err != nil {
			GinkgoWriter.Printf("logs for pod %s: %v\n", pod.Name, err)
			continue
		}
		GinkgoWriter.Printf("---- controller logs: %s ----\n%s\n", pod.Name, raw)
	}
}
