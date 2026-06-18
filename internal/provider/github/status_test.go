// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

func newStatusTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	_ = repoguardv1.AddToScheme(s)
	return s
}

func TestMapTeamStatus(t *testing.T) {
	tests := []struct {
		name           string
		teamStatus     repoguardv1.GithubTeamState
		expectedStatus pmmeta.TargetStatusValue
		expectMessage  bool
	}{
		{
			name:           "complete maps to Ready",
			teamStatus:     "complete",
			expectedStatus: pmmeta.TargetStatusReady,
			expectMessage:  false,
		},
		{
			name:           "failed maps to Error",
			teamStatus:     "failed",
			expectedStatus: pmmeta.TargetStatusError,
			expectMessage:  true,
		},
		{
			name:           "pending maps to Pending",
			teamStatus:     "pending",
			expectedStatus: pmmeta.TargetStatusPending,
			expectMessage:  true,
		},
		{
			name:           "ratelimited maps to Pending",
			teamStatus:     "ratelimited",
			expectedStatus: pmmeta.TargetStatusPending,
			expectMessage:  true,
		},
		{
			name:           "empty string maps to Pending",
			teamStatus:     "",
			expectedStatus: pmmeta.TargetStatusPending,
			expectMessage:  true,
		},
		{
			name:           "unknown value maps to Pending",
			teamStatus:     "unknown-xyz",
			expectedStatus: pmmeta.TargetStatusPending,
			expectMessage:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, message := mapTeamStatus(tt.teamStatus)
			if status != tt.expectedStatus {
				t.Errorf("expected status %q, got %q", tt.expectedStatus, status)
			}
			if tt.expectMessage && message == "" {
				t.Error("expected non-empty message")
			}
			if !tt.expectMessage && message != "" {
				t.Errorf("expected empty message, got %q", message)
			}
		})
	}
}

func TestComputeProviderStatus_NoTargets(t *testing.T) {
	scheme := newStatusTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr", Namespace: "default"},
	}
	desired := &DesiredState{Teams: nil, TeamRepos: nil}

	status := computeProviderStatus(context.Background(), c, asr, desired, nil)

	if !status.Ready {
		t.Error("expected Ready=true for no targets")
	}
	if len(status.Targets) != 0 {
		t.Errorf("expected 0 targets, got %d", len(status.Targets))
	}
}

func TestComputeProviderStatus_AllComplete(t *testing.T) {
	scheme := newStatusTestScheme()

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr", Namespace: "default"},
	}

	desired := &DesiredState{
		Teams: []DesiredTeam{
			{Instance: "github.example.com", Org: "my-org", CRName: TeamCRName("test-asr", "github.example.com", "my-org")},
			{Instance: "github.com", Org: "other-org", CRName: TeamCRName("test-asr", "github.com", "other-org")},
		},
	}

	// Create GithubTeam CRs with "complete" status
	team1 := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TeamCRName("test-asr", "github.example.com", "my-org"),
			Namespace: "default",
			Labels: map[string]string{
				pmapis.LabelManagedByKey: pmapis.ManagedByGithub,
				pmapis.LabelSourceASR:    "test-asr",
			},
		},
		Spec:   repoguardv1.GithubTeamSpec{Team: "test-asr"},
		Status: repoguardv1.GithubTeamStatus{TeamStatus: repoguardv1.GithubTeamStateComplete},
	}
	team1.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	team2 := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TeamCRName("test-asr", "github.com", "other-org"),
			Namespace: "default",
			Labels: map[string]string{
				pmapis.LabelManagedByKey: pmapis.ManagedByGithub,
				pmapis.LabelSourceASR:    "test-asr",
			},
		},
		Spec:   repoguardv1.GithubTeamSpec{Team: "test-asr"},
		Status: repoguardv1.GithubTeamStatus{TeamStatus: repoguardv1.GithubTeamStateComplete},
	}
	team2.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(team1, team2).
		Build()

	status := computeProviderStatus(context.Background(), c, asr, desired, nil)

	if !status.Ready {
		t.Errorf("expected Ready=true, got false; message=%q", status.Message)
	}
	if len(status.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(status.Targets))
	}
	for _, target := range status.Targets {
		if target.Status != pmmeta.TargetStatusReady {
			t.Errorf("target %q: expected Ready, got %q", target.TargetID, target.Status)
		}
	}
}

func TestComputeProviderStatus_MixedStatuses(t *testing.T) {
	scheme := newStatusTestScheme()

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr", Namespace: "default"},
	}

	desired := &DesiredState{
		Teams: []DesiredTeam{
			{Instance: "github.example.com", Org: "org-a", CRName: TeamCRName("test-asr", "github.example.com", "org-a")},
			{Instance: "github.example.com", Org: "org-b", CRName: TeamCRName("test-asr", "github.example.com", "org-b")},
			{Instance: "github.example.com", Org: "org-c", CRName: TeamCRName("test-asr", "github.example.com", "org-c")},
		},
	}

	teamComplete := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TeamCRName("test-asr", "github.example.com", "org-a"),
			Namespace: "default",
			Labels: map[string]string{
				pmapis.LabelManagedByKey: pmapis.ManagedByGithub,
				pmapis.LabelSourceASR:    "test-asr",
			},
		},
		Spec:   repoguardv1.GithubTeamSpec{Team: "test-asr"},
		Status: repoguardv1.GithubTeamStatus{TeamStatus: repoguardv1.GithubTeamStateComplete},
	}
	teamComplete.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	teamFailed := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TeamCRName("test-asr", "github.example.com", "org-b"),
			Namespace: "default",
			Labels: map[string]string{
				pmapis.LabelManagedByKey: pmapis.ManagedByGithub,
				pmapis.LabelSourceASR:    "test-asr",
			},
		},
		Spec:   repoguardv1.GithubTeamSpec{Team: "test-asr"},
		Status: repoguardv1.GithubTeamStatus{TeamStatus: repoguardv1.GithubTeamStateFailed},
	}
	teamFailed.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	teamRatelimited := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TeamCRName("test-asr", "github.example.com", "org-c"),
			Namespace: "default",
			Labels: map[string]string{
				pmapis.LabelManagedByKey: pmapis.ManagedByGithub,
				pmapis.LabelSourceASR:    "test-asr",
			},
		},
		Spec:   repoguardv1.GithubTeamSpec{Team: "test-asr"},
		Status: repoguardv1.GithubTeamStatus{TeamStatus: repoguardv1.GithubTeamStateRateLimited},
	}
	teamRatelimited.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(teamComplete, teamFailed, teamRatelimited).
		Build()

	status := computeProviderStatus(context.Background(), c, asr, desired, nil)

	if status.Ready {
		t.Error("expected Ready=false for mixed statuses")
	}
	if len(status.Targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(status.Targets))
	}

	// Targets are sorted by instance/org
	expectStatuses := map[string]pmmeta.TargetStatusValue{
		SanitizeInstance("github.example.com") + "/org-a": pmmeta.TargetStatusReady,
		SanitizeInstance("github.example.com") + "/org-b": pmmeta.TargetStatusError,
		SanitizeInstance("github.example.com") + "/org-c": pmmeta.TargetStatusPending,
	}

	for _, target := range status.Targets {
		expected, ok := expectStatuses[target.TargetID]
		if !ok {
			t.Errorf("unexpected target ID %q", target.TargetID)
			continue
		}
		if target.Status != expected {
			t.Errorf("target %q: expected %q, got %q", target.TargetID, expected, target.Status)
		}
	}
}

func TestComputeProviderStatus_MissingTeamCR(t *testing.T) {
	scheme := newStatusTestScheme()

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr", Namespace: "default"},
	}

	desired := &DesiredState{
		Teams: []DesiredTeam{
			{Instance: "github.example.com", Org: "my-org", CRName: TeamCRName("test-asr", "github.example.com", "my-org")},
		},
	}

	// No GithubTeam CRs exist
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	status := computeProviderStatus(context.Background(), c, asr, desired, nil)

	if status.Ready {
		t.Error("expected Ready=false when team CR is missing")
	}
	if len(status.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(status.Targets))
	}
	if status.Targets[0].Status != pmmeta.TargetStatusPending {
		t.Errorf("expected Pending, got %q", status.Targets[0].Status)
	}
}

func TestComputeProviderStatus_TargetIDFormat(t *testing.T) {
	scheme := newStatusTestScheme()

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr", Namespace: "default"},
	}

	desired := &DesiredState{
		Teams: []DesiredTeam{
			{Instance: "github.example.com", Org: "my-org", CRName: TeamCRName("test-asr", "github.example.com", "my-org")},
		},
	}

	team := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TeamCRName("test-asr", "github.example.com", "my-org"),
			Namespace: "default",
			Labels: map[string]string{
				pmapis.LabelManagedByKey: pmapis.ManagedByGithub,
				pmapis.LabelSourceASR:    "test-asr",
			},
		},
		Spec:   repoguardv1.GithubTeamSpec{Team: "test-asr"},
		Status: repoguardv1.GithubTeamStatus{TeamStatus: repoguardv1.GithubTeamStateComplete},
	}
	team.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(team).
		Build()

	status := computeProviderStatus(context.Background(), c, asr, desired, nil)

	expectedTargetID := "github-example-com/my-org"
	if len(status.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(status.Targets))
	}
	if status.Targets[0].TargetID != expectedTargetID {
		t.Errorf("expected targetID %q, got %q", expectedTargetID, status.Targets[0].TargetID)
	}
}

func TestComputeProviderStatus_DeduplicatesInstanceOrg(t *testing.T) {
	scheme := newStatusTestScheme()

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr", Namespace: "default"},
	}

	// Same instance/org in both Teams and TeamRepos
	desired := &DesiredState{
		Teams: []DesiredTeam{
			{Instance: "github.example.com", Org: "my-org", CRName: TeamCRName("test-asr", "github.example.com", "my-org")},
		},
		TeamRepos: []DesiredTeamRepo{
			{Instance: "github.example.com", Org: "my-org", Permission: "push",
				Repositories: []string{"repo1"}, CRName: TeamRepoCRName("test-asr", "github.example.com", "my-org", "push")},
		},
	}

	team := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TeamCRName("test-asr", "github.example.com", "my-org"),
			Namespace: "default",
			Labels: map[string]string{
				pmapis.LabelManagedByKey: pmapis.ManagedByGithub,
				pmapis.LabelSourceASR:    "test-asr",
			},
		},
		Spec:   repoguardv1.GithubTeamSpec{Team: "test-asr"},
		Status: repoguardv1.GithubTeamStatus{TeamStatus: repoguardv1.GithubTeamStateComplete},
	}
	team.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(team).
		Build()

	status := computeProviderStatus(context.Background(), c, asr, desired, nil)

	// Should only have 1 target even though the same instance/org appears in both slices
	if len(status.Targets) != 1 {
		t.Errorf("expected 1 deduplicated target, got %d", len(status.Targets))
	}
}

func TestComputeProviderStatus_PendingStatus(t *testing.T) {
	scheme := newStatusTestScheme()

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr", Namespace: "default"},
	}

	desired := &DesiredState{
		Teams: []DesiredTeam{
			{Instance: "github.example.com", Org: "my-org", CRName: TeamCRName("test-asr", "github.example.com", "my-org")},
		},
	}

	// Team exists but has "pending" status
	team := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TeamCRName("test-asr", "github.example.com", "my-org"),
			Namespace: "default",
			Labels: map[string]string{
				pmapis.LabelManagedByKey: pmapis.ManagedByGithub,
				pmapis.LabelSourceASR:    "test-asr",
			},
		},
		Spec:   repoguardv1.GithubTeamSpec{Team: "test-asr"},
		Status: repoguardv1.GithubTeamStatus{TeamStatus: repoguardv1.GithubTeamStatePendingOperations},
	}
	team.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(team).
		Build()

	status := computeProviderStatus(context.Background(), c, asr, desired, nil)

	if status.Ready {
		t.Error("expected Ready=false for pending status")
	}
	if len(status.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(status.Targets))
	}
	if status.Targets[0].Status != pmmeta.TargetStatusPending {
		t.Errorf("expected Pending, got %q", status.Targets[0].Status)
	}
}
