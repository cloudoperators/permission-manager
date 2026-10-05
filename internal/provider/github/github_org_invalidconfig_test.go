// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// TestReconcileOrganizations_MissingOrgReturnedNoRequeue: a missing org CR is returned (not an
// error) and SetGithubConfigValidity turns it into InvalidConfig (mirrored on Ready).
func TestReconcileOrganizations_MissingOrgReturnedNoRequeue(t *testing.T) {
	p, _ := newPhase() // org "missing" not seeded
	asr := newASR("asr", "team-x", []v1alpha1.Permission{perm(orgCCRN("missing"), "ADMIN")})

	_, problems, err := p.reconcileOrganizations(context.Background(), asr, connectorDef(map[string]string{"ADMIN": "admin"}), "test-ns")
	if err != nil {
		t.Fatalf("expected no requeue error for a missing org, got: %v", err)
	}
	if len(problems.MissingOrgs) != 1 || problems.MissingOrgs[0] != "test-ns/missing" {
		t.Fatalf("missing orgs = %v, want [test-ns/missing]", problems.MissingOrgs)
	}

	SetGithubConfigValidity(asr, problems)

	invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
	if invalid == nil || invalid.Status != metav1.ConditionTrue {
		t.Fatalf("expected InvalidConfig=True, got %+v", invalid)
	}
	if invalid.Reason != string(pmmeta.MissingGithubOrganizationReason) {
		t.Errorf("reason = %q, want %q", invalid.Reason, pmmeta.MissingGithubOrganizationReason)
	}
	if !strings.Contains(invalid.Message, "test-ns/missing") ||
		!strings.Contains(invalid.Message, "ensure repo-guard has created it") {
		t.Errorf("unexpected message: %q", invalid.Message)
	}

	ready := asr.GetReadyCondition()
	if ready == nil || ready.Status != metav1.ConditionFalse ||
		ready.Reason != string(pmmeta.MissingGithubOrganizationReason) {
		t.Errorf("expected Ready=False/MissingGithubOrganization, got %+v", ready)
	}
}

// TestReconcileOrganizations_UnmappedRepoTypeCap: an unmapped capability on a repository_type CCRN
// is surfaced in UnmappedCaps rather than silently dropped, and contributes no org entry.
func TestReconcileOrganizations_UnmappedRepoTypeCap(t *testing.T) {
	p, _ := newPhase()
	asr := newASR("asr", "team-x", []v1alpha1.Permission{perm(repoTypeCCRN("public"), "OPERATE")})

	_, problems, err := p.reconcileOrganizations(context.Background(), asr, connectorDef(map[string]string{"ADMIN": "admin"}), "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations: %v", err)
	}
	if len(problems.UnmappedCaps) != 1 || problems.UnmappedCaps[0] != "OPERATE" {
		t.Fatalf("unmapped caps = %v, want [OPERATE]", problems.UnmappedCaps)
	}
	if len(problems.MissingOrgs) != 0 {
		t.Errorf("missing orgs = %v, want none (unmapped cap never reaches org patch)", problems.MissingOrgs)
	}

	SetGithubConfigValidity(asr, problems)
	invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
	if invalid == nil || invalid.Reason != string(pmmeta.UnmappedGithubCapabilityReason) {
		t.Errorf("expected InvalidConfig/UnknownCapability, got %+v", invalid)
	}
}

// TestSetGithubConfigValidity_ClearsOnceNoProblems: the missing-org conditions clear when the
// aggregate is empty.
func TestSetGithubConfigValidity_ClearsOnceNoProblems(t *testing.T) {
	asr := newASR("asr", "team-x", nil)

	SetGithubConfigValidity(asr, GithubConfigProblems{MissingOrgs: []string{"test-ns/my-org"}})
	if !asr.HasInvalidConfig() {
		t.Fatal("expected InvalidConfig after first call")
	}

	SetGithubConfigValidity(asr, GithubConfigProblems{})
	if asr.HasInvalidConfig() {
		t.Errorf("InvalidConfig should be cleared when nothing is missing: %+v", asr.Status.Conditions)
	}
	if r := asr.GetReadyCondition(); r != nil && r.Reason == string(pmmeta.MissingGithubOrganizationReason) {
		t.Errorf("stale Ready reason should be cleared, got %+v", r)
	}
}

// TestSetGithubConfigValidity_AggregateKeepsMissingSignal: a non-empty aggregate keeps the condition
// even when some bindings contributed no missing orgs (guards cross-binding clobbering).
func TestSetGithubConfigValidity_AggregateKeepsMissingSignal(t *testing.T) {
	asr := newASR("asr", "team-x", nil)

	// Binding A found "test-ns/a" missing, binding B found none: the aggregate still lists A.
	SetGithubConfigValidity(asr, GithubConfigProblems{MissingOrgs: []string{"test-ns/a"}})
	if !asr.HasInvalidConfig() {
		t.Fatalf("expected InvalidConfig to stay set for the aggregate, got %+v", asr.Status.Conditions)
	}
}

// TestSetGithubConfigValidity_Precedence: with several problem kinds present at once, missing org
// wins over invalid CCRN wins over unmapped capability; each reason surfaces when it is highest.
func TestSetGithubConfigValidity_Precedence(t *testing.T) {
	cases := []struct {
		name       string
		problems   GithubConfigProblems
		wantReason pmmeta.ConditionReason
		wantSubstr string
	}{
		{
			name:       "missing org wins over all",
			problems:   GithubConfigProblems{MissingOrgs: []string{"test-ns/a"}, InvalidCCRNs: []string{"gh/org/*"}, UnmappedCaps: []string{"OPERATE"}},
			wantReason: pmmeta.MissingGithubOrganizationReason,
			wantSubstr: "test-ns/a",
		},
		{
			name:       "invalid CCRN wins over unmapped capability",
			problems:   GithubConfigProblems{InvalidCCRNs: []string{"github.example.com/cc/*"}, UnmappedCaps: []string{"OPERATE"}},
			wantReason: pmmeta.InvalidGithubCCRNReason,
			wantSubstr: "github.example.com/cc/*",
		},
		{
			name:       "unmapped capability on its own",
			problems:   GithubConfigProblems{UnmappedCaps: []string{"OPERATE"}},
			wantReason: pmmeta.UnmappedGithubCapabilityReason,
			wantSubstr: "OPERATE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := newASR("asr", "team-x", nil)
			SetGithubConfigValidity(asr, tc.problems)

			invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
			if invalid == nil || invalid.Status != metav1.ConditionTrue {
				t.Fatalf("expected InvalidConfig=True, got %+v", invalid)
			}
			if invalid.Reason != string(tc.wantReason) {
				t.Errorf("reason = %q, want %q", invalid.Reason, tc.wantReason)
			}
			if !strings.Contains(invalid.Message, tc.wantSubstr) {
				t.Errorf("message %q should contain %q", invalid.Message, tc.wantSubstr)
			}
			if r := asr.GetReadyCondition(); r == nil || r.Status != metav1.ConditionFalse || r.Reason != string(tc.wantReason) {
				t.Errorf("expected Ready=False/%s, got %+v", tc.wantReason, r)
			}
		})
	}
}

// TestSetGithubConfigValidity_SurvivesK8sClear: github reasons differ from the generic ones the
// k8s phase clears, so a k8s healthy-clear can't wipe github's signal.
func TestSetGithubConfigValidity_SurvivesK8sClear(t *testing.T) {
	cases := []struct {
		name     string
		problems GithubConfigProblems
		want     pmmeta.ConditionReason
	}{
		{"wildcard CCRN", GithubConfigProblems{InvalidCCRNs: []string{"github.example.com/cc/*"}}, pmmeta.InvalidGithubCCRNReason},
		{"unmapped cap", GithubConfigProblems{UnmappedCaps: []string{"OPERATE"}}, pmmeta.UnmappedGithubCapabilityReason},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asr := newASR("asr", "team-x", nil)
			SetGithubConfigValidity(asr, tc.problems)

			// k8s phase, finding no k8s problem, clears its generic reasons.
			asr.ClearInvalidConfigIfReason(pmmeta.InvalidCCRNReason, pmmeta.UnknownCapabilityReason)

			invalid := pmmeta.GetConditionByType(asr.Status.Conditions, pmmeta.InvalidConfigCondition)
			if invalid == nil || invalid.Reason != string(tc.want) {
				t.Errorf("github InvalidConfig should survive the k8s clear, got %+v", invalid)
			}
		})
	}
}
