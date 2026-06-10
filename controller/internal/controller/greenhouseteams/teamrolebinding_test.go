package greenhouseteams

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
	greenhouse "github.com/cloudoperators/permission-manager/controller/pkg/greenhouse"
)

func TestReconcileTRB_Create(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	state := &DesiredState{
		TeamName:       "pm-my-team",
		MappedIDPGroup: "SG-COMPUTE",
	}

	trb := &DesiredTRB{
		Name:        "pm-sg-compute-cluster-viewer-eu-de-1",
		TeamRole:    "cluster-viewer",
		ClusterName: "eu-de-1",
	}

	err := ReconcileTRB(ctx, c, state, trb, "org-ns", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTRB failed: %v", err)
	}

	// Verify the TRB was created
	var trbObj greenhouse.TeamRoleBinding
	if err := c.Get(ctx, client.ObjectKey{Name: trb.Name, Namespace: "org-ns"}, &trbObj); err != nil {
		t.Fatalf("failed to get created TRB: %v", err)
	}

	if trbObj.Spec.TeamRoleRef != "cluster-viewer" {
		t.Errorf("TRB.Spec.TeamRoleRef = %q, want %q", trbObj.Spec.TeamRoleRef, "cluster-viewer")
	}
	if len(trbObj.Spec.TeamRefs) != 1 {
		t.Fatalf("expected 1 TeamRef, got %d", len(trbObj.Spec.TeamRefs))
	}
	if trbObj.Spec.TeamRefs[0].Name != "pm-my-team" {
		t.Errorf("TeamRef.Name = %q, want %q", trbObj.Spec.TeamRefs[0].Name, "pm-my-team")
	}
	if trbObj.Spec.TeamRefs[0].Namespace != "org-ns" {
		t.Errorf("TeamRef.Namespace = %q, want %q", trbObj.Spec.TeamRefs[0].Namespace, "org-ns")
	}
	if trbObj.Spec.ClusterSelector.Name != "eu-de-1" {
		t.Errorf("ClusterSelector.Name = %q, want %q", trbObj.Spec.ClusterSelector.Name, "eu-de-1")
	}
	if len(trbObj.Spec.Namespaces) != 0 {
		t.Errorf("Namespaces should be empty, got %v", trbObj.Spec.Namespaces)
	}

	// Check labels
	if trbObj.Labels[LabelManagedBy] != LabelManagedByValue {
		t.Errorf("managed-by label = %q, want %q", trbObj.Labels[LabelManagedBy], LabelManagedByValue)
	}
}

func TestReconcileTRB_WithNamespaces(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	state := &DesiredState{
		TeamName:       "pm-my-team",
		MappedIDPGroup: "SG-NETWORK",
	}

	trb := &DesiredTRB{
		Name:        "pm-sg-network-ns-admin-eu-de-1",
		TeamRole:    "namespace-admin",
		ClusterName: "eu-de-1",
		Namespaces:  []string{"kube-system", "monitoring"},
	}

	err := ReconcileTRB(ctx, c, state, trb, "org-ns", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTRB failed: %v", err)
	}

	var trbObj greenhouse.TeamRoleBinding
	if err := c.Get(ctx, client.ObjectKey{Name: trb.Name, Namespace: "org-ns"}, &trbObj); err != nil {
		t.Fatalf("failed to get created TRB: %v", err)
	}

	if len(trbObj.Spec.Namespaces) != 2 {
		t.Fatalf("expected 2 namespaces, got %d", len(trbObj.Spec.Namespaces))
	}
	if trbObj.Spec.Namespaces[0] != "kube-system" || trbObj.Spec.Namespaces[1] != "monitoring" {
		t.Errorf("Namespaces = %v, want [kube-system, monitoring]", trbObj.Spec.Namespaces)
	}
	// ClusterSelector should still be set for namespace-scoped TRBs
	if trbObj.Spec.ClusterSelector.Name != "eu-de-1" {
		t.Errorf("ClusterSelector.Name = %q, want %q", trbObj.Spec.ClusterSelector.Name, "eu-de-1")
	}
}

func TestReconcileTRB_Update(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	existing := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-trb-1",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamRoleBindingSpec{
			TeamRoleRef: "old-role",
			TeamRefs:    []greenhouse.TeamRef{{Name: "pm-old-team", Namespace: "org-ns"}},
			ClusterSelector: greenhouse.ClusterSelector{
				Name: "old-cluster",
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	state := &DesiredState{
		TeamName:       "pm-new-team",
		MappedIDPGroup: "NEW-GROUP",
	}
	trb := &DesiredTRB{
		Name:        "pm-trb-1",
		TeamRole:    "new-role",
		ClusterName: "new-cluster",
	}

	err := ReconcileTRB(ctx, c, state, trb, "org-ns", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTRB update failed: %v", err)
	}

	var trbObj greenhouse.TeamRoleBinding
	if err := c.Get(ctx, client.ObjectKey{Name: "pm-trb-1", Namespace: "org-ns"}, &trbObj); err != nil {
		t.Fatalf("failed to get updated TRB: %v", err)
	}

	if trbObj.Spec.TeamRoleRef != "new-role" {
		t.Errorf("TeamRoleRef = %q, want %q", trbObj.Spec.TeamRoleRef, "new-role")
	}
	if trbObj.Spec.TeamRefs[0].Name != "pm-new-team" {
		t.Errorf("TeamRef.Name = %q, want %q", trbObj.Spec.TeamRefs[0].Name, "pm-new-team")
	}
	if trbObj.Spec.ClusterSelector.Name != "new-cluster" {
		t.Errorf("ClusterSelector.Name = %q, want %q", trbObj.Spec.ClusterSelector.Name, "new-cluster")
	}
}

func TestDeleteStaleTRBs(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Create existing TRBs: one desired, one stale
	desired := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-trb-desired",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamRoleBindingSpec{
			TeamRoleRef:     "viewer",
			ClusterSelector: greenhouse.ClusterSelector{Name: "eu-de-1"},
		},
	}
	stale := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-trb-stale",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamRoleBindingSpec{
			TeamRoleRef:     "admin",
			ClusterSelector: greenhouse.ClusterSelector{Name: "old-cluster"},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(desired, stale).Build()

	state := &DesiredState{
		TeamName: "pm-my-team",
		TRBs: []DesiredTRB{
			{Name: "pm-trb-desired", TeamRole: "viewer", ClusterName: "eu-de-1"},
		},
	}

	err := DeleteStaleTRBs(ctx, c, state, "test-asr", "default", "org-ns", audit.Disabled())
	if err != nil {
		t.Fatalf("DeleteStaleTRBs failed: %v", err)
	}

	// Desired should still exist
	var trbObj greenhouse.TeamRoleBinding
	err = c.Get(ctx, client.ObjectKey{Name: "pm-trb-desired", Namespace: "org-ns"}, &trbObj)
	if err != nil {
		t.Errorf("desired TRB should still exist: %v", err)
	}

	// Stale should be deleted
	err = c.Get(ctx, client.ObjectKey{Name: "pm-trb-stale", Namespace: "org-ns"}, &trbObj)
	if err == nil {
		t.Error("stale TRB should have been deleted")
	}
}

func TestReconcileAllTRBs(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-existing stale TRB
	stale := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-trb-old",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamRoleBindingSpec{
			TeamRoleRef:     "old-role",
			ClusterSelector: greenhouse.ClusterSelector{Name: "old-cluster"},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build()

	state := &DesiredState{
		TeamName:       "pm-my-team",
		MappedIDPGroup: "SG-COMPUTE",
		TRBs: []DesiredTRB{
			{Name: "pm-trb-new", TeamRole: "viewer", ClusterName: "eu-de-1"},
		},
	}

	err := ReconcileAllTRBs(ctx, c, state, "org-ns", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileAllTRBs failed: %v", err)
	}

	// New TRB should exist
	var trbObj greenhouse.TeamRoleBinding
	err = c.Get(ctx, client.ObjectKey{Name: "pm-trb-new", Namespace: "org-ns"}, &trbObj)
	if err != nil {
		t.Errorf("new TRB should exist: %v", err)
	}

	// Old stale TRB should be deleted
	err = c.Get(ctx, client.ObjectKey{Name: "pm-trb-old", Namespace: "org-ns"}, &trbObj)
	if err == nil {
		t.Error("stale TRB should have been deleted")
	}
}

func TestDeleteAllManagedTRBs(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	trb1 := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-trb-1",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamRoleBindingSpec{
			TeamRoleRef: "viewer",
		},
	}
	trb2 := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-trb-2",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamRoleBindingSpec{
			TeamRoleRef: "admin",
		},
	}
	// TRB from different ASR should not be deleted
	trbOther := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-trb-other",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "other-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamRoleBindingSpec{
			TeamRoleRef: "viewer",
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(trb1, trb2, trbOther).Build()

	err := DeleteAllManagedTRBs(ctx, c, "test-asr", "default", "org-ns", audit.Disabled())
	if err != nil {
		t.Fatalf("DeleteAllManagedTRBs failed: %v", err)
	}

	// trb1 and trb2 should be deleted
	var trbObj greenhouse.TeamRoleBinding
	if err := c.Get(ctx, client.ObjectKey{Name: "pm-trb-1", Namespace: "org-ns"}, &trbObj); err == nil {
		t.Error("pm-trb-1 should have been deleted")
	}
	if err := c.Get(ctx, client.ObjectKey{Name: "pm-trb-2", Namespace: "org-ns"}, &trbObj); err == nil {
		t.Error("pm-trb-2 should have been deleted")
	}

	// trbOther should still exist
	if err := c.Get(ctx, client.ObjectKey{Name: "pm-trb-other", Namespace: "org-ns"}, &trbObj); err != nil {
		t.Errorf("pm-trb-other should still exist: %v", err)
	}
}

func TestReconcileTRB_AuditCreate(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	var buf bytes.Buffer
	al := audit.New(&buf, "greenhouse-teams", "test")

	state := &DesiredState{
		TeamName:       "pm-my-team",
		MappedIDPGroup: "SG-COMPUTE",
	}
	trb := &DesiredTRB{
		Name:        "pm-sg-compute-viewer-eu-de-1",
		TeamRole:    "cluster-viewer",
		ClusterName: "eu-de-1",
	}

	err := ReconcileTRB(ctx, c, state, trb, "org-ns", "test-asr", "default", al)
	if err != nil {
		t.Fatalf("ReconcileTRB failed: %v", err)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to parse audit record: %v", err)
	}

	attrs := record["Attributes"].(map[string]interface{})
	if attrs["audit.action"] != "create" {
		t.Errorf("audit.action = %q, want %q", attrs["audit.action"], "create")
	}
	if attrs["audit.outcome"] != "success" {
		t.Errorf("audit.outcome = %q, want %q", attrs["audit.outcome"], "success")
	}
	if attrs["resource.kind"] != "TeamRoleBinding" {
		t.Errorf("resource.kind = %q, want %q", attrs["resource.kind"], "TeamRoleBinding")
	}
	if attrs["resource.name"] != "pm-sg-compute-viewer-eu-de-1" {
		t.Errorf("resource.name = %q, want %q", attrs["resource.name"], "pm-sg-compute-viewer-eu-de-1")
	}
	if attrs["resource.namespace"] != "org-ns" {
		t.Errorf("resource.namespace = %q, want %q", attrs["resource.namespace"], "org-ns")
	}
	if attrs["resource.cluster"] != "" {
		t.Errorf("resource.cluster = %q, want empty string", attrs["resource.cluster"])
	}
}

func TestDeleteStaleTRBs_AuditDelete(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	stale := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-trb-stale",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamRoleBindingSpec{
			TeamRoleRef:     "admin",
			ClusterSelector: greenhouse.ClusterSelector{Name: "old-cluster"},
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build()

	var buf bytes.Buffer
	al := audit.New(&buf, "greenhouse-teams", "test")

	state := &DesiredState{
		TeamName: "pm-my-team",
		TRBs:    []DesiredTRB{}, // no desired TRBs — stale will be deleted
	}

	err := DeleteStaleTRBs(ctx, c, state, "test-asr", "default", "org-ns", al)
	if err != nil {
		t.Fatalf("DeleteStaleTRBs failed: %v", err)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to parse audit record: %v", err)
	}

	attrs := record["Attributes"].(map[string]interface{})
	if attrs["audit.action"] != "delete" {
		t.Errorf("audit.action = %q, want %q", attrs["audit.action"], "delete")
	}
	if attrs["audit.outcome"] != "success" {
		t.Errorf("audit.outcome = %q, want %q", attrs["audit.outcome"], "success")
	}
	if attrs["resource.kind"] != "TeamRoleBinding" {
		t.Errorf("resource.kind = %q, want %q", attrs["resource.kind"], "TeamRoleBinding")
	}
	if attrs["resource.name"] != "pm-trb-stale" {
		t.Errorf("resource.name = %q, want %q", attrs["resource.name"], "pm-trb-stale")
	}
}

func TestDeleteAllManagedTRBs_AuditMultiple(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	trb1 := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-trb-1",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamRoleBindingSpec{TeamRoleRef: "viewer"},
	}
	trb2 := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-trb-2",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamRoleBindingSpec{TeamRoleRef: "admin"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(trb1, trb2).Build()

	var buf bytes.Buffer
	al := audit.New(&buf, "greenhouse-teams", "test")

	err := DeleteAllManagedTRBs(ctx, c, "test-asr", "default", "org-ns", al)
	if err != nil {
		t.Fatalf("DeleteAllManagedTRBs failed: %v", err)
	}

	// Should have emitted 2 audit records (one per TRB)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit records, got %d: %s", len(lines), buf.String())
	}

	for _, line := range lines {
		var record map[string]interface{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("failed to parse audit line: %v", err)
		}
		attrs := record["Attributes"].(map[string]interface{})
		if attrs["audit.action"] != "delete" {
			t.Errorf("expected delete action, got %q", attrs["audit.action"])
		}
		if attrs["resource.kind"] != "TeamRoleBinding" {
			t.Errorf("expected TeamRoleBinding kind, got %q", attrs["resource.kind"])
		}
	}
}
