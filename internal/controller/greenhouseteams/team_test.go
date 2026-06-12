// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package greenhouseteams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	greenhouse "github.com/cloudoperators/greenhouse/api/v1alpha1"
	greenhousev1alpha2 "github.com/cloudoperators/greenhouse/api/v1alpha2"

	"github.com/cloudoperators/permission-manager/pkg/audit"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = greenhouse.AddToScheme(s)
	_ = greenhousev1alpha2.AddToScheme(s)
	return s
}

// deleteTeam is a test helper that deletes a Team CR if it exists.
func deleteTeam(ctx context.Context, c client.Client, name, namespace string) error {
	team := &greenhouse.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if err := c.Delete(ctx, team); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil
		}
		return fmt.Errorf("failed to delete Team %s/%s: %w", namespace, name, err)
	}
	return nil
}

func TestReconcileTeam_Create(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	state := &DesiredState{
		TeamName:       "pm-my-team",
		MappedIDPGroup: "SG-COMPUTE",
	}

	err := ReconcileTeam(ctx, c, state, "org-ns", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeam failed: %v", err)
	}

	// Verify the Team was created
	var team greenhouse.Team
	if err := c.Get(ctx, client.ObjectKey{Name: "pm-my-team", Namespace: "org-ns"}, &team); err != nil {
		t.Fatalf("failed to get created Team: %v", err)
	}

	if team.Spec.MappedIDPGroup != "SG-COMPUTE" {
		t.Errorf("Team.Spec.MappedIDPGroup = %q, want %q", team.Spec.MappedIDPGroup, "SG-COMPUTE")
	}

	// Check labels
	if team.Labels[LabelManagedBy] != LabelManagedByValue {
		t.Errorf("label %s = %q, want %q", LabelManagedBy, team.Labels[LabelManagedBy], LabelManagedByValue)
	}
	if team.Labels[LabelSourceASR] != "test-asr" {
		t.Errorf("label %s = %q, want %q", LabelSourceASR, team.Labels[LabelSourceASR], "test-asr")
	}
	if team.Labels[LabelSourceNamespace] != "default" {
		t.Errorf("label %s = %q, want %q", LabelSourceNamespace, team.Labels[LabelSourceNamespace], "default")
	}
}

func TestReconcileTeam_Update(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Pre-create a Team with old values
	existingTeam := &greenhouse.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-my-team",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamSpec{
			MappedIDPGroup: "OLD-GROUP",
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingTeam).Build()

	state := &DesiredState{
		TeamName:       "pm-my-team",
		MappedIDPGroup: "NEW-GROUP",
	}

	err := ReconcileTeam(ctx, c, state, "org-ns", "test-asr", "default", audit.Disabled())
	if err != nil {
		t.Fatalf("ReconcileTeam update failed: %v", err)
	}

	// Verify the Team was updated
	var team greenhouse.Team
	if err := c.Get(ctx, client.ObjectKey{Name: "pm-my-team", Namespace: "org-ns"}, &team); err != nil {
		t.Fatalf("failed to get updated Team: %v", err)
	}

	if team.Spec.MappedIDPGroup != "NEW-GROUP" {
		t.Errorf("Team.Spec.MappedIDPGroup = %q, want %q", team.Spec.MappedIDPGroup, "NEW-GROUP")
	}
}

func TestDeleteTeam(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	existingTeam := &greenhouse.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-my-team",
			Namespace: "org-ns",
		},
		Spec: greenhouse.TeamSpec{
			MappedIDPGroup: "SG-COMPUTE",
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingTeam).Build()

	err := deleteTeam(ctx, c, "pm-my-team", "org-ns")
	if err != nil {
		t.Fatalf("deleteTeam failed: %v", err)
	}

	// Verify it's gone
	var team greenhouse.Team
	err = c.Get(ctx, client.ObjectKey{Name: "pm-my-team", Namespace: "org-ns"}, &team)
	if err == nil {
		t.Error("expected Team to be deleted, but it still exists")
	}
}

func TestDeleteTeam_NotFound(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Deleting a non-existent Team should not error
	err := deleteTeam(ctx, c, "nonexistent", "org-ns")
	if err != nil {
		t.Fatalf("deleteTeam for non-existent Team should not error: %v", err)
	}
}

func TestListManagedTeams(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	team1 := &greenhouse.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-team-1",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "my-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamSpec{MappedIDPGroup: "group-1"},
	}
	team2 := &greenhouse.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-team-2",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "other-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamSpec{MappedIDPGroup: "group-2"},
	}
	team3 := &greenhouse.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unmanaged-team",
			Namespace: "org-ns",
		},
		Spec: greenhouse.TeamSpec{MappedIDPGroup: "group-3"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team1, team2, team3).Build()

	teams, err := ListManagedTeams(ctx, c, "my-asr", "default", "org-ns")
	if err != nil {
		t.Fatalf("ListManagedTeams failed: %v", err)
	}

	if len(teams) != 1 {
		t.Fatalf("expected 1 managed team, got %d", len(teams))
	}
	if teams[0].Name != "pm-team-1" {
		t.Errorf("expected team pm-team-1, got %q", teams[0].Name)
	}
}

func TestManagedLabels(t *testing.T) {
	labels := managedLabels("my-asr", "my-namespace")

	if labels[LabelManagedBy] != LabelManagedByValue {
		t.Errorf("managed-by label = %q, want %q", labels[LabelManagedBy], LabelManagedByValue)
	}
	if labels[LabelSourceASR] != "my-asr" {
		t.Errorf("source-asr label = %q, want %q", labels[LabelSourceASR], "my-asr")
	}
	if labels[LabelSourceNamespace] != "my-namespace" {
		t.Errorf("source-namespace label = %q, want %q", labels[LabelSourceNamespace], "my-namespace")
	}
}

func TestReconcileTeam_AuditCreate(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	var buf bytes.Buffer
	al := audit.New(&buf, "greenhouse-teams", "test")

	state := &DesiredState{
		TeamName:       "pm-my-team",
		MappedIDPGroup: "SG-COMPUTE",
	}

	err := ReconcileTeam(ctx, c, state, "org-ns", "test-asr", "default", al)
	if err != nil {
		t.Fatalf("ReconcileTeam failed: %v", err)
	}

	// Parse the audit record
	var record map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to parse audit record: %v (raw: %s)", err, buf.String())
	}

	attrs, ok := record["Attributes"].(map[string]interface{})
	if !ok {
		t.Fatal("audit record missing Attributes")
	}

	if attrs["audit.action"] != "create" {
		t.Errorf("audit.action = %q, want %q", attrs["audit.action"], "create")
	}
	if attrs["audit.outcome"] != "success" {
		t.Errorf("audit.outcome = %q, want %q", attrs["audit.outcome"], "success")
	}
	if attrs["resource.kind"] != "Team" {
		t.Errorf("resource.kind = %q, want %q", attrs["resource.kind"], "Team")
	}
	if attrs["resource.name"] != "pm-my-team" {
		t.Errorf("resource.name = %q, want %q", attrs["resource.name"], "pm-my-team")
	}
	if attrs["resource.namespace"] != "org-ns" {
		t.Errorf("resource.namespace = %q, want %q", attrs["resource.namespace"], "org-ns")
	}
	if attrs["resource.cluster"] != "" {
		t.Errorf("resource.cluster = %q, want empty string", attrs["resource.cluster"])
	}
	if attrs["asr.name"] != "test-asr" {
		t.Errorf("asr.name = %q, want %q", attrs["asr.name"], "test-asr")
	}
}

func TestReconcileTeam_AuditUpdate(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	existingTeam := &greenhouse.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-my-team",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamSpec{
			MappedIDPGroup: "OLD-GROUP",
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingTeam).Build()

	var buf bytes.Buffer
	al := audit.New(&buf, "greenhouse-teams", "test")

	state := &DesiredState{
		TeamName:       "pm-my-team",
		MappedIDPGroup: "NEW-GROUP",
	}

	err := ReconcileTeam(ctx, c, state, "org-ns", "test-asr", "default", al)
	if err != nil {
		t.Fatalf("ReconcileTeam failed: %v", err)
	}

	var record map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("failed to parse audit record: %v", err)
	}

	attrs := record["Attributes"].(map[string]interface{})
	if attrs["audit.action"] != "update" {
		t.Errorf("audit.action = %q, want %q", attrs["audit.action"], "update")
	}
	if attrs["audit.outcome"] != "success" {
		t.Errorf("audit.outcome = %q, want %q", attrs["audit.outcome"], "success")
	}
}

func TestReconcileTeam_NoAuditWhenUnchanged(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Team already matches desired state
	existingTeam := &greenhouse.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pm-my-team",
			Namespace: "org-ns",
			Labels: map[string]string{
				LabelManagedBy:       LabelManagedByValue,
				LabelSourceASR:       "test-asr",
				LabelSourceNamespace: "default",
			},
		},
		Spec: greenhouse.TeamSpec{
			MappedIDPGroup: "SG-COMPUTE",
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingTeam).Build()

	var buf bytes.Buffer
	al := audit.New(&buf, "greenhouse-teams", "test")

	state := &DesiredState{
		TeamName:       "pm-my-team",
		MappedIDPGroup: "SG-COMPUTE",
	}

	err := ReconcileTeam(ctx, c, state, "org-ns", "test-asr", "default", al)
	if err != nil {
		t.Fatalf("ReconcileTeam failed: %v", err)
	}

	// No audit record should be emitted when resource is unchanged
	if buf.Len() != 0 {
		t.Errorf("expected no audit record when unchanged, got: %s", buf.String())
	}
}
