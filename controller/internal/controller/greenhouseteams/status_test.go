package greenhouseteams

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	greenhouse "github.com/cloudoperators/permission-manager/controller/pkg/greenhouse"
)

func TestComputeTRBStatus_ReadyTrue(t *testing.T) {
	trb := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "pm-test-trb"},
		Status: greenhouse.TeamRoleBindingStatus{
			StatusConditions: greenhouse.StatusConditions{
				Conditions: []greenhouse.Condition{
					{Type: greenhouse.Ready, Status: metav1.ConditionTrue},
				},
			},
		},
	}

	result := ComputeTRBStatus(trb)

	if result.Status != StatusSuccess {
		t.Errorf("expected status %q, got %q", StatusSuccess, result.Status)
	}
	if result.Name != "pm-test-trb" {
		t.Errorf("expected name %q, got %q", "pm-test-trb", result.Name)
	}
}

func TestComputeTRBStatus_ReadyFalse(t *testing.T) {
	trb := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "pm-test-trb"},
		Status: greenhouse.TeamRoleBindingStatus{
			StatusConditions: greenhouse.StatusConditions{
				Conditions: []greenhouse.Condition{
					{Type: greenhouse.Ready, Status: metav1.ConditionFalse},
				},
			},
		},
	}

	result := ComputeTRBStatus(trb)

	if result.Status != StatusError {
		t.Errorf("expected status %q, got %q", StatusError, result.Status)
	}
}

func TestComputeTRBStatus_NoCondition(t *testing.T) {
	trb := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "pm-test-trb"},
		Status:     greenhouse.TeamRoleBindingStatus{},
	}

	result := ComputeTRBStatus(trb)

	if result.Status != StatusPending {
		t.Errorf("expected status %q, got %q", StatusPending, result.Status)
	}
}

func TestComputeTRBStatus_OtherConditionOnly(t *testing.T) {
	trb := &greenhouse.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "pm-test-trb"},
		Status: greenhouse.TeamRoleBindingStatus{
			StatusConditions: greenhouse.StatusConditions{
				Conditions: []greenhouse.Condition{
					{Type: greenhouse.KubeConfigValid, Status: metav1.ConditionTrue},
				},
			},
		},
	}

	result := ComputeTRBStatus(trb)

	if result.Status != StatusPending {
		t.Errorf("expected status %q when only non-Ready condition exists, got %q", StatusPending, result.Status)
	}
}

func TestComputeProviderStatus_AllReady(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := greenhouse.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	if err := pmv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	trbs := []greenhouse.TeamRoleBinding{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pm-test-trb-1",
				Namespace: "org-ns",
				Labels: map[string]string{
					LabelManagedBy:       LabelManagedByValue,
					LabelSourceASR:       "my-asr",
					LabelSourceNamespace: "pm-system",
				},
			},
			Status: greenhouse.TeamRoleBindingStatus{
				StatusConditions: greenhouse.StatusConditions{
					Conditions: []greenhouse.Condition{
						{Type: greenhouse.Ready, Status: metav1.ConditionTrue},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pm-test-trb-2",
				Namespace: "org-ns",
				Labels: map[string]string{
					LabelManagedBy:       LabelManagedByValue,
					LabelSourceASR:       "my-asr",
					LabelSourceNamespace: "pm-system",
				},
			},
			Status: greenhouse.TeamRoleBindingStatus{
				StatusConditions: greenhouse.StatusConditions{
					Conditions: []greenhouse.Condition{
						{Type: greenhouse.Ready, Status: metav1.ConditionTrue},
					},
				},
			},
		},
	}

	objs := make([]runtime.Object, len(trbs))
	for i := range trbs {
		objs[i] = &trbs[i]
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		Build()

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-asr",
			Namespace: "pm-system",
		},
	}

	status := ComputeProviderStatus(context.Background(), c, asr)

	if !status.Ready {
		t.Errorf("expected Ready=true, got Ready=false")
	}
	if len(status.Targets) != 2 {
		t.Errorf("expected 2 targets, got %d", len(status.Targets))
	}
	if status.Message != "All 2 TeamRoleBindings ready" {
		t.Errorf("unexpected message: %q", status.Message)
	}
}

func TestComputeProviderStatus_MixedStatuses(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := greenhouse.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	if err := pmv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	trbs := []greenhouse.TeamRoleBinding{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pm-test-trb-ok",
				Namespace: "org-ns",
				Labels: map[string]string{
					LabelManagedBy:       LabelManagedByValue,
					LabelSourceASR:       "my-asr",
					LabelSourceNamespace: "pm-system",
				},
			},
			Status: greenhouse.TeamRoleBindingStatus{
				StatusConditions: greenhouse.StatusConditions{
					Conditions: []greenhouse.Condition{
						{Type: greenhouse.Ready, Status: metav1.ConditionTrue},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pm-test-trb-fail",
				Namespace: "org-ns",
				Labels: map[string]string{
					LabelManagedBy:       LabelManagedByValue,
					LabelSourceASR:       "my-asr",
					LabelSourceNamespace: "pm-system",
				},
			},
			Status: greenhouse.TeamRoleBindingStatus{
				StatusConditions: greenhouse.StatusConditions{
					Conditions: []greenhouse.Condition{
						{Type: greenhouse.Ready, Status: metav1.ConditionFalse},
					},
				},
			},
		},
	}

	objs := make([]runtime.Object, len(trbs))
	for i := range trbs {
		objs[i] = &trbs[i]
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		Build()

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-asr",
			Namespace: "pm-system",
		},
	}

	status := ComputeProviderStatus(context.Background(), c, asr)

	if status.Ready {
		t.Errorf("expected Ready=false for mixed statuses, got Ready=true")
	}
	if len(status.Targets) != 2 {
		t.Errorf("expected 2 targets, got %d", len(status.Targets))
	}
	if status.Message != "1 of 2 TeamRoleBindings have errors" {
		t.Errorf("unexpected message: %q", status.Message)
	}
}

func TestComputeProviderStatus_NoTRBs(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := greenhouse.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	if err := pmv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-asr",
			Namespace: "pm-system",
		},
	}

	status := ComputeProviderStatus(context.Background(), c, asr)

	if !status.Ready {
		t.Errorf("expected Ready=true when no TRBs exist")
	}
	if status.Message != "No TeamRoleBindings managed" {
		t.Errorf("unexpected message: %q", status.Message)
	}
}

func TestComputeTeamOnlyStatus(t *testing.T) {
	status := ComputeTeamOnlyStatus()

	if !status.Ready {
		t.Errorf("expected Ready=true for team-only mode")
	}
	if status.Message == "" {
		t.Error("expected non-empty message for team-only mode")
	}
	// Message should indicate conflict suppression
	if status.Message != "Team-only mode: TeamRoleBinding creation suppressed to avoid conflicts" {
		t.Errorf("unexpected message: %q", status.Message)
	}
}

func TestComputeUnmappedCapabilityStatus(t *testing.T) {
	status := ComputeUnmappedCapabilityStatus([]string{"DEPLOY", "BACKUP"})

	if status.Ready {
		t.Errorf("expected Ready=false for unmapped capabilities")
	}
	if status.Message == "" {
		t.Error("expected non-empty message for unmapped capabilities")
	}
}
