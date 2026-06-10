package kubernetes

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

func TestEnqueueMatchingASRs_WildcardMatch(t *testing.T) {
	// ASR with CCRN "nw-a-*" should be enqueued when cluster "nw-a-prod-3" appears
	scheme := newTestScheme()

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "team-reader",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "team-reader",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/nw-a-*", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		Build()

	requests := enqueueMatchingASRs(context.Background(), mgmtClient, logr.Discard(), "nw-a-prod-3")

	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d: %v", len(requests), requests)
	}
	if requests[0].Name != "team-reader" || requests[0].Namespace != "pm-system" {
		t.Errorf("unexpected request: %v", requests[0])
	}
}

func TestEnqueueMatchingASRs_NoMatch(t *testing.T) {
	// ASR with CCRN "prod-*" should NOT be enqueued when cluster "staging-eu" appears
	scheme := newTestScheme()

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-reader",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "prod-reader",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-*", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		Build()

	requests := enqueueMatchingASRs(context.Background(), mgmtClient, logr.Discard(), "staging-eu")

	if len(requests) != 0 {
		t.Errorf("expected 0 requests, got %d: %v", len(requests), requests)
	}
}

func TestEnqueueMatchingASRs_ExactMatch(t *testing.T) {
	// ASR with exact CCRN should also match if cluster name equals
	scheme := newTestScheme()

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "exact-reader",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "exact-reader",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu-1", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		Build()

	requests := enqueueMatchingASRs(context.Background(), mgmtClient, logr.Discard(), "prod-eu-1")

	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d: %v", len(requests), requests)
	}
	if requests[0].Name != "exact-reader" {
		t.Errorf("unexpected request name: %q", requests[0].Name)
	}
}

func TestEnqueueMatchingASRs_NamespaceWildcardAlsoMatches(t *testing.T) {
	// ASR with namespace CCRN using wildcard cluster should also be enqueued
	scheme := newTestScheme()

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ns-reader",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "ns-reader",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/nw-a-*/kube-system", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		Build()

	requests := enqueueMatchingASRs(context.Background(), mgmtClient, logr.Discard(), "nw-a-prod-3")

	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d: %v", len(requests), requests)
	}
	if requests[0].Name != "ns-reader" {
		t.Errorf("unexpected request name: %q", requests[0].Name)
	}
}

func TestEnqueueMatchingASRs_MultipleASRs_OnlyMatchingEnqueued(t *testing.T) {
	// Only matching ASRs should be enqueued, not all
	scheme := newTestScheme()

	asr1 := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nw-reader",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "nw-reader",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/nw-a-*", Capability: "READ"},
			},
		},
	}
	asr2 := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-reader",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "prod-reader",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-*", Capability: "READ"},
			},
		},
	}
	asr3 := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "staging-reader",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "staging-reader",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/staging-*", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr1, asr2, asr3).
		Build()

	requests := enqueueMatchingASRs(context.Background(), mgmtClient, logr.Discard(), "nw-a-prod-1")

	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d: %v", len(requests), requests)
	}
	if requests[0].Name != "nw-reader" {
		t.Errorf("expected nw-reader to be enqueued, got %q", requests[0].Name)
	}
}

func TestEnqueueMatchingASRs_NoDuplicates(t *testing.T) {
	// ASR with multiple matching permissions should only be enqueued once
	scheme := newTestScheme()

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-perm",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "multi-perm",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/nw-a-*", Capability: "READ"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/nw-a-*/default", Capability: "WRITE"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		Build()

	requests := enqueueMatchingASRs(context.Background(), mgmtClient, logr.Discard(), "nw-a-prod-1")

	if len(requests) != 1 {
		t.Fatalf("expected exactly 1 request (no duplicates), got %d: %v", len(requests), requests)
	}
}

func TestEnqueueMatchingASRs_NonCCRNResourcesSkipped(t *testing.T) {
	// ASR with non-CCRN resources should not cause any match
	scheme := newTestScheme()

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "plain-asr",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "plain-asr",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "some/plain/resource", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(asr).
		Build()

	requests := enqueueMatchingASRs(context.Background(), mgmtClient, logr.Discard(), "nw-a-prod-1")

	if len(requests) != 0 {
		t.Errorf("expected 0 requests for non-CCRN ASR, got %d: %v", len(requests), requests)
	}
}

func TestNotifyClusterChange_SendsEvent(t *testing.T) {
	ch := make(chan GenericEvent, 1)
	NotifyClusterChange(ch, "new-cluster-1")

	select {
	case evt := <-ch:
		if evt.Object.GetName() != "new-cluster-1" {
			t.Errorf("expected event with cluster name 'new-cluster-1', got %q", evt.Object.GetName())
		}
	default:
		t.Fatal("expected event in channel, got none")
	}
}

func TestNotifyClusterChange_DoesNotBlockOnFullChannel(t *testing.T) {
	// Channel with buffer 0 - should not block
	ch := make(chan GenericEvent)

	// This should not panic or block
	done := make(chan struct{})
	go func() {
		NotifyClusterChange(ch, "overflow-cluster")
		close(done)
	}()

	<-done // If this blocks, the test will timeout
}

func TestConnectorBindingIndexer_StartupSync_ViaWatch(t *testing.T) {
	// Simulates the startup behavior where the ConnectorBinding watch handler
	// calls idx.Update() for each existing CB during informer cache sync.
	// This verifies that after processing a set of CB events, the indexer
	// correctly reports IsKubernetesConnector for k8s bindings.
	idx := NewConnectorBindingIndexer(logr.Discard())

	// Simulate informer Add events at startup for existing ConnectorBindings
	existingBindings := []*pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s-prod"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s-prod", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s-staging"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s-staging", Provider: "kubernetes"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-azure"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-azure", Provider: "azure"},
		},
	}

	// This is exactly what the watch handler does for each Add event
	for _, cb := range existingBindings {
		idx.Update(cb)
	}

	// Verify the index is correctly populated
	if !idx.IsKubernetesConnector("sci-k8s-prod") {
		t.Error("expected sci-k8s-prod to be a kubernetes connector after startup sync")
	}
	if !idx.IsKubernetesConnector("sci-k8s-staging") {
		t.Error("expected sci-k8s-staging to be a kubernetes connector after startup sync")
	}
	if idx.IsKubernetesConnector("sci-azure") {
		t.Error("expected sci-azure NOT to be a kubernetes connector")
	}
	if idx.IsKubernetesConnector("unknown") {
		t.Error("expected unknown NOT to be a kubernetes connector")
	}

	// Verify HasAnyKubernetesRef works correctly after startup sync
	if !idx.HasAnyKubernetesRef([]string{"sci-k8s-prod"}) {
		t.Error("HasAnyKubernetesRef should return true for k8s connector ref")
	}
	if idx.HasAnyKubernetesRef([]string{"sci-azure"}) {
		t.Error("HasAnyKubernetesRef should return false for non-k8s connector ref")
	}
}
