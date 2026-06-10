package kubernetes

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// fakeClientBuilder is a helper that records calls and returns a fake client.
func fakeClientBuilder(fakeClient client.Client) ClientBuilderFunc {
	return func(cfg *rest.Config, opts client.Options) (client.Client, error) {
		return fakeClient, nil
	}
}

// failingClientBuilder returns a builder that always fails.
func failingClientBuilder() ClientBuilderFunc {
	return func(cfg *rest.Config, opts client.Options) (client.Client, error) {
		return nil, errors.New("connection refused")
	}
}

func TestGreenhouseClusterClientFactory_ImplementsInterface(t *testing.T) {
	var _ ClusterClientFactory = &GreenhouseClusterClientFactory{}
}

func TestGreenhouseClusterClientFactory_PrefersGreenhouseKubeconfig(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	// A valid minimal kubeconfig
	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://preferred.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: my-token
`)

	fallbackData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://fallback.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: fallback-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Type: corev1.SecretType("greenhouse.sap/kubeconfig"),
		Data: map[string][]byte{
			"greenhousekubeconfig": kubeconfigData,
			"kubeconfig":           fallbackData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()
	remoteClient := fake.NewClientBuilder().Build()

	var receivedConfig *rest.Config
	builder := func(cfg *rest.Config, opts client.Options) (client.Client, error) {
		receivedConfig = cfg
		return remoteClient, nil
	}

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    builder,
	}

	result, err := factory.ClientForCluster(ctx, "my-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil client")
	}
	if receivedConfig.Host != "https://preferred.example.com" {
		t.Errorf("expected host from greenhousekubeconfig, got %q", receivedConfig.Host)
	}
}

func TestGreenhouseClusterClientFactory_FallsBackToKubeconfig(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://fallback.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: my-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Type: corev1.SecretType("greenhouse.sap/kubeconfig"),
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()
	remoteClient := fake.NewClientBuilder().Build()

	var receivedConfig *rest.Config
	builder := func(cfg *rest.Config, opts client.Options) (client.Client, error) {
		receivedConfig = cfg
		return remoteClient, nil
	}

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    builder,
	}

	result, err := factory.ClientForCluster(ctx, "my-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil client")
	}
	if receivedConfig.Host != "https://fallback.example.com" {
		t.Errorf("expected host from kubeconfig fallback, got %q", receivedConfig.Host)
	}
}

func TestGreenhouseClusterClientFactory_SecretNotFound(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	mgmtClient := fake.NewClientBuilder().Build()

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    fakeClientBuilder(nil),
	}

	_, err := factory.ClientForCluster(ctx, "missing-cluster")
	if err == nil {
		t.Fatal("expected error for missing secret")
	}

	var unreachable *ClusterUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected ClusterUnreachableError, got %T: %v", err, err)
	}
	if unreachable.ClusterName != "missing-cluster" {
		t.Errorf("ClusterName = %q, want %q", unreachable.ClusterName, "missing-cluster")
	}
	if unreachable.Reason == "" {
		t.Error("expected non-empty Reason for missing secret")
	}
}

func TestGreenhouseClusterClientFactory_MalformedKubeconfig(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "bad-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Type: corev1.SecretType("greenhouse.sap/kubeconfig"),
		Data: map[string][]byte{
			"kubeconfig": []byte("this is not valid kubeconfig yaml {{{{"),
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    fakeClientBuilder(nil),
	}

	_, err := factory.ClientForCluster(ctx, "bad-cluster")
	if err == nil {
		t.Fatal("expected error for malformed kubeconfig")
	}

	var unreachable *ClusterUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected ClusterUnreachableError, got %T: %v", err, err)
	}
	if unreachable.ClusterName != "bad-cluster" {
		t.Errorf("ClusterName = %q, want %q", unreachable.ClusterName, "bad-cluster")
	}
	if unreachable.Reason == "" {
		t.Error("expected non-empty Reason for malformed kubeconfig")
	}
}

func TestGreenhouseClusterClientFactory_NoKubeconfigKeys(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "empty-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Type: corev1.SecretType("greenhouse.sap/kubeconfig"),
		Data: map[string][]byte{
			"some-other-key": []byte("irrelevant"),
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    fakeClientBuilder(nil),
	}

	_, err := factory.ClientForCluster(ctx, "empty-cluster")
	if err == nil {
		t.Fatal("expected error when no kubeconfig keys present")
	}

	var unreachable *ClusterUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected ClusterUnreachableError, got %T: %v", err, err)
	}
}

func TestGreenhouseClusterClientFactory_CachesClient(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://cached.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: my-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "cached-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Type: corev1.SecretType("greenhouse.sap/kubeconfig"),
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()
	remoteClient := fake.NewClientBuilder().Build()

	callCount := 0
	builder := func(cfg *rest.Config, opts client.Options) (client.Client, error) {
		callCount++
		return remoteClient, nil
	}

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    builder,
	}

	// First call should build
	c1, err := factory.ClientForCluster(ctx, "cached-cluster")
	if err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}

	// Second call should use cache
	c2, err := factory.ClientForCluster(ctx, "cached-cluster")
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected builder called once (cached), got %d", callCount)
	}
	if c1 != c2 {
		t.Error("expected same client instance from cache")
	}
}

func TestGreenhouseClusterClientFactory_InvalidateCache(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://invalidated.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: my-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "rotated-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Type: corev1.SecretType("greenhouse.sap/kubeconfig"),
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()
	remoteClient := fake.NewClientBuilder().Build()

	callCount := 0
	builder := func(cfg *rest.Config, opts client.Options) (client.Client, error) {
		callCount++
		return remoteClient, nil
	}

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    builder,
	}

	// First call builds
	_, err := factory.ClientForCluster(ctx, "rotated-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 builder call, got %d", callCount)
	}

	// Invalidate and call again - should rebuild
	factory.InvalidateCache("rotated-cluster")

	_, err = factory.ClientForCluster(ctx, "rotated-cluster")
	if err != nil {
		t.Fatalf("unexpected error after invalidation: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 builder calls after invalidation, got %d", callCount)
	}
}

func TestGreenhouseClusterClientFactory_ClientBuilderError(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://unreachable.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: my-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "failing-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Type: corev1.SecretType("greenhouse.sap/kubeconfig"),
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    failingClientBuilder(),
	}

	_, err := factory.ClientForCluster(ctx, "failing-cluster")
	if err == nil {
		t.Fatal("expected error when client builder fails")
	}

	var unreachable *ClusterUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected ClusterUnreachableError, got %T: %v", err, err)
	}
	if unreachable.ClusterName != "failing-cluster" {
		t.Errorf("ClusterName = %q, want %q", unreachable.ClusterName, "failing-cluster")
	}
}

func TestGreenhouseClusterClientFactory_CacheInvalidatedByResourceVersion(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://versioned.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: my-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "versioned-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Type: corev1.SecretType("greenhouse.sap/kubeconfig"),
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()
	remoteClient := fake.NewClientBuilder().Build()

	callCount := 0
	builder := func(cfg *rest.Config, opts client.Options) (client.Client, error) {
		callCount++
		return remoteClient, nil
	}

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    builder,
	}

	// First call at resourceVersion "1"
	_, err := factory.ClientForCluster(ctx, "versioned-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	// Re-fetch the secret from the fake store to get current resourceVersion
	fetched := &corev1.Secret{}
	if err := mgmtClient.Get(ctx, types.NamespacedName{Name: "versioned-cluster", Namespace: namespace}, fetched); err != nil {
		t.Fatalf("failed to get secret: %v", err)
	}
	// Update data (this will bump resourceVersion in the fake client)
	fetched.Data["kubeconfig"] = kubeconfigData
	if err := mgmtClient.Update(ctx, fetched); err != nil {
		t.Fatalf("failed to update secret: %v", err)
	}

	// Second call should detect resource version change and rebuild
	_, err = factory.ClientForCluster(ctx, "versioned-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls after resource version change, got %d", callCount)
	}
}

func TestGreenhouseClusterClientFactory_ImplementsNamespaceLister(t *testing.T) {
	var _ NamespaceLister = &GreenhouseClusterClientFactory{}
}

// newNsTestScheme creates a scheme with core types for namespace list tests.
func newNsTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	return s
}

func TestGreenhouseClusterClientFactory_NamespacesForCluster_CacheMiss(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"
	scheme := newNsTestScheme()

	// Create namespaces on the remote cluster
	ns1 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}}
	ns2 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}}
	ns3 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "monitoring"}}

	remoteClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns1, ns2, ns3).Build()

	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://ns-test.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: my-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "ns-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder: func(cfg *rest.Config, opts client.Options) (client.Client, error) {
			return remoteClient, nil
		},
	}

	names, err := factory.NamespacesForCluster(ctx, "ns-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(names) != 3 {
		t.Fatalf("expected 3 namespaces, got %d: %v", len(names), names)
	}

	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	for _, want := range []string{"default", "kube-system", "monitoring"} {
		if !nameSet[want] {
			t.Errorf("expected namespace %q in result", want)
		}
	}
}

func TestGreenhouseClusterClientFactory_NamespacesForCluster_CacheHit(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"
	scheme := newNsTestScheme()

	ns1 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}}
	remoteClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns1).Build()

	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://cache-hit.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: my-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "cache-hit-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()

	clientBuilderCalls := 0
	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder: func(cfg *rest.Config, opts client.Options) (client.Client, error) {
			clientBuilderCalls++
			return remoteClient, nil
		},
	}

	// First call: cache miss
	names1, err := factory.NamespacesForCluster(ctx, "cache-hit-cluster")
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// Second call: cache hit (no additional client build)
	names2, err := factory.NamespacesForCluster(ctx, "cache-hit-cluster")
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	if clientBuilderCalls != 1 {
		t.Errorf("expected client builder called once (cached), got %d", clientBuilderCalls)
	}
	if len(names1) != len(names2) {
		t.Errorf("cache hit returned different result: %v vs %v", names1, names2)
	}
}

func TestGreenhouseClusterClientFactory_NamespacesForCluster_TTLExpiry(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"
	scheme := newNsTestScheme()

	ns1 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}}
	remoteClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns1).Build()

	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://ttl-test.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: my-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "ttl-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()

	now := time.Now()
	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder: func(cfg *rest.Config, opts client.Options) (client.Client, error) {
			return remoteClient, nil
		},
		Now: func() time.Time { return now },
	}

	// First call at t=0 -> cache miss, fetches namespaces
	names1, err := factory.NamespacesForCluster(ctx, "ttl-cluster")
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if len(names1) != 1 || names1[0] != "default" {
		t.Fatalf("expected [default], got %v", names1)
	}

	// Add a new namespace to the remote cluster
	ns2 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "new-ns"}}
	if err := remoteClient.Create(ctx, ns2); err != nil {
		t.Fatalf("failed to create namespace: %v", err)
	}

	// Second call at t=30s (within TTL): should still return cached data (only "default")
	now = now.Add(30 * time.Second)
	names2, err := factory.NamespacesForCluster(ctx, "ttl-cluster")
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if len(names2) != 1 {
		t.Errorf("expected 1 namespace from cache (within TTL), got %d: %v", len(names2), names2)
	}

	// Third call at t=61s (TTL expired): should refetch and see the new namespace
	now = now.Add(31 * time.Second) // total 61s
	names3, err := factory.NamespacesForCluster(ctx, "ttl-cluster")
	if err != nil {
		t.Fatalf("third call failed: %v", err)
	}
	if len(names3) != 2 {
		t.Errorf("expected 2 namespaces after TTL expiry (refetched), got %d: %v", len(names3), names3)
	}
}

func TestGreenhouseClusterClientFactory_NamespacesForCluster_Invalidation(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"
	scheme := newNsTestScheme()

	ns1 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}}
	remoteClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns1).Build()

	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://invalidate-ns.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: my-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "inv-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()

	clientBuilderCalls := 0
	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder: func(cfg *rest.Config, opts client.Options) (client.Client, error) {
			clientBuilderCalls++
			return remoteClient, nil
		},
	}

	// First call caches namespaces
	_, err := factory.NamespacesForCluster(ctx, "inv-cluster")
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if clientBuilderCalls != 1 {
		t.Fatalf("expected 1 client builder call, got %d", clientBuilderCalls)
	}

	// Invalidate the cache
	factory.InvalidateCache("inv-cluster")

	// Next call should rebuild the client and refetch namespaces
	_, err = factory.NamespacesForCluster(ctx, "inv-cluster")
	if err != nil {
		t.Fatalf("post-invalidation call failed: %v", err)
	}
	if clientBuilderCalls != 2 {
		t.Errorf("expected 2 client builder calls after invalidation, got %d", clientBuilderCalls)
	}
}

func TestGreenhouseClusterClientFactory_NamespacesForCluster_Error(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	// No secret exists for this cluster -> ClientForCluster will fail
	mgmtClient := fake.NewClientBuilder().Build()

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    fakeClientBuilder(nil),
	}

	_, err := factory.NamespacesForCluster(ctx, "missing-cluster")
	if err == nil {
		t.Fatal("expected error for missing cluster")
	}

	var unreachable *ClusterUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected ClusterUnreachableError, got %T: %v", err, err)
	}
}

func TestStaticNamespaceLister_ReturnsNamespaces(t *testing.T) {
	lister := &StaticNamespaceLister{
		Namespaces: map[string][]string{
			"cluster-a": {"ns1", "ns2"},
			"cluster-b": {"ns3"},
		},
	}

	names, err := lister.NamespacesForCluster(context.Background(), "cluster-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("expected 2 namespaces, got %d", len(names))
	}

	// Unknown cluster returns nil
	names, err = lister.NamespacesForCluster(context.Background(), "unknown")
	if err != nil {
		t.Fatalf("unexpected error for unknown cluster: %v", err)
	}
	if names != nil {
		t.Errorf("expected nil for unknown cluster, got %v", names)
	}
}

func TestStaticNamespaceLister_ReturnsError(t *testing.T) {
	testErr := errors.New("cluster down")
	lister := &StaticNamespaceLister{
		Namespaces: map[string][]string{
			"cluster-a": {"ns1"},
		},
		Err: map[string]error{
			"cluster-a": testErr,
		},
	}

	_, err := lister.NamespacesForCluster(context.Background(), "cluster-a")
	if err != testErr {
		t.Errorf("expected %v, got %v", testErr, err)
	}
}

func TestGreenhouseClusterClientFactory_RejectsExecProvider(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	// A kubeconfig with an exec credential plugin
	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://exec-cluster.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: /malicious/binary
      args:
        - "--steal-secrets"
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "exec-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    fakeClientBuilder(nil),
	}

	_, err := factory.ClientForCluster(ctx, "exec-cluster")
	if err == nil {
		t.Fatal("expected error for kubeconfig with exec credential plugin")
	}

	var unreachable *ClusterUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected ClusterUnreachableError, got %T: %v", err, err)
	}
	if unreachable.ClusterName != "exec-cluster" {
		t.Errorf("ClusterName = %q, want %q", unreachable.ClusterName, "exec-cluster")
	}
	if unreachable.Reason != "kubeconfig uses exec credential plugin (forbidden)" {
		t.Errorf("Reason = %q, want %q", unreachable.Reason, "kubeconfig uses exec credential plugin (forbidden)")
	}
}

func TestGreenhouseClusterClientFactory_RejectsAuthProvider(t *testing.T) {
	ctx := context.Background()
	namespace := "greenhouse"

	// A kubeconfig with an auth provider plugin
	kubeconfigData := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://auth-cluster.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    auth-provider:
      name: gcp
      config:
        access-token: fake-token
`)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "auth-cluster",
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfigData,
		},
	}

	mgmtClient := fake.NewClientBuilder().WithObjects(secret).Build()

	factory := &GreenhouseClusterClientFactory{
		ManagementClient: mgmtClient,
		Namespace:        namespace,
		ClientBuilder:    fakeClientBuilder(nil),
	}

	_, err := factory.ClientForCluster(ctx, "auth-cluster")
	if err == nil {
		t.Fatal("expected error for kubeconfig with auth provider")
	}

	var unreachable *ClusterUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected ClusterUnreachableError, got %T: %v", err, err)
	}
	if unreachable.ClusterName != "auth-cluster" {
		t.Errorf("ClusterName = %q, want %q", unreachable.ClusterName, "auth-cluster")
	}
	if unreachable.Reason != "kubeconfig uses auth provider plugin (forbidden)" {
		t.Errorf("Reason = %q, want %q", unreachable.Reason, "kubeconfig uses auth provider plugin (forbidden)")
	}
}
