// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

func teamWithStatus(name, asrName string, state repoguardv1.GithubTeamState) *repoguardv1.GithubTeam {
	return &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				pmapis.LabelManagedByKey: pmapis.ManagedByGithub,
				pmapis.LabelSourceASR:    asrName,
			},
		},
		Status: repoguardv1.GithubTeamStatus{TeamStatus: state},
	}
}

func TestMapTeamStatus(t *testing.T) {
	tests := []struct {
		state   repoguardv1.GithubTeamState
		want    pmmeta.TargetStatusValue
		wantMsg bool
	}{
		{repoguardv1.GithubTeamStateComplete, pmmeta.TargetStatusReady, false},
		{repoguardv1.GithubTeamStateFailed, pmmeta.TargetStatusError, true},
		{repoguardv1.GithubTeamStatePendingOperations, pmmeta.TargetStatusPending, true},
		{repoguardv1.GithubTeamStateRateLimited, pmmeta.TargetStatusPending, true},
		{"", pmmeta.TargetStatusPending, true},
		{"something-unknown", pmmeta.TargetStatusPending, true},
	}
	for _, tt := range tests {
		name := string(tt.state)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			got, msg := mapTeamStatus(tt.state)
			if got != tt.want {
				t.Errorf("status = %q, want %q", got, tt.want)
			}
			if (msg != "") != tt.wantMsg {
				t.Errorf("message = %q, wantMsg %v", msg, tt.wantMsg)
			}
		})
	}
}

func TestComputeProviderStatus_NoTargetsIsReady(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{ObjectMeta: metav1.ObjectMeta{Name: "asr", Namespace: "default"}}
	status := computeProviderStatus(context.Background(), newClient(), asr, &DesiredState{}, nil)
	if !status.Ready || len(status.Targets) != 0 {
		t.Errorf("expected Ready with 0 targets, got Ready=%v targets=%d", status.Ready, len(status.Targets))
	}
}

func TestComputeProviderStatus_UnmappedCapsDegraded(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{ObjectMeta: metav1.ObjectMeta{Name: "asr", Namespace: "default"}}
	status := computeProviderStatus(context.Background(), newClient(), asr, &DesiredState{}, []string{"FOO"})
	if status.Ready {
		t.Error("expected not Ready when capabilities are unmapped")
	}
	if !strings.Contains(status.Message, "FOO") {
		t.Errorf("expected message to mention the unmapped capability, got %q", status.Message)
	}
}

func TestComputeProviderStatus_AggregatesTeamStates(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{ObjectMeta: metav1.ObjectMeta{Name: "asr", Namespace: "default"}}
	desired := &DesiredState{Teams: []DesiredTeam{
		{Instance: "github.example.com", Org: "ready", CRName: TeamCRName("ghe", "ready", "team")},
		{Instance: "github.example.com", Org: "broken", CRName: TeamCRName("ghe", "broken", "team")},
		{Instance: "github.example.com", Org: "absent", CRName: TeamCRName("ghe", "absent", "team")},
	}}
	// The "absent" team CR is intentionally not created.
	c := newClient(
		teamWithStatus(TeamCRName("ghe", "ready", "team"), "asr", repoguardv1.GithubTeamStateComplete),
		teamWithStatus(TeamCRName("ghe", "broken", "team"), "asr", repoguardv1.GithubTeamStateFailed),
	)

	status := computeProviderStatus(context.Background(), c, asr, desired, nil)

	if status.Ready {
		t.Error("expected not Ready when a target is not complete")
	}
	want := map[string]pmmeta.TargetStatusValue{
		"github-example-com/ready":  pmmeta.TargetStatusReady,
		"github-example-com/broken": pmmeta.TargetStatusError,
		"github-example-com/absent": pmmeta.TargetStatusPending,
	}
	if len(status.Targets) != len(want) {
		t.Fatalf("expected %d targets, got %d", len(want), len(status.Targets))
	}
	for _, target := range status.Targets {
		if want[target.TargetID] != target.Status {
			t.Errorf("target %q: status %q, want %q", target.TargetID, target.Status, want[target.TargetID])
		}
	}
}

func TestComputeProviderStatus_AllCompleteIsReady(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{ObjectMeta: metav1.ObjectMeta{Name: "asr", Namespace: "default"}}
	desired := &DesiredState{Teams: []DesiredTeam{
		{Instance: "github.example.com", Org: "my-org", CRName: TeamCRName("ghe", "my-org", "team")},
	}}
	c := newClient(teamWithStatus(TeamCRName("ghe", "my-org", "team"), "asr", repoguardv1.GithubTeamStateComplete))

	status := computeProviderStatus(context.Background(), c, asr, desired, nil)
	if !status.Ready {
		t.Errorf("expected Ready, got false (message=%q)", status.Message)
	}
}
