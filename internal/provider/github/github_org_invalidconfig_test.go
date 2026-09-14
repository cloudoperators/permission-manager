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
// error) and SetOrgConfigValidity turns it into InvalidConfig (mirrored on Ready).
func TestReconcileOrganizations_MissingOrgReturnedNoRequeue(t *testing.T) {
	p, _ := newPhase() // org "missing" not seeded
	asr := newASR("asr", "team-x", []v1alpha1.Permission{perm(orgCCRN("missing"), "ADMIN")})

	missing, err := p.reconcileOrganizations(context.Background(), asr, connectorDef(map[string]string{"ADMIN": "admin"}), "test-ns")
	if err != nil {
		t.Fatalf("expected no requeue error for a missing org, got: %v", err)
	}
	if len(missing) != 1 || missing[0] != "test-ns/missing" {
		t.Fatalf("missing orgs = %v, want [test-ns/missing]", missing)
	}

	SetOrgConfigValidity(asr, missing)

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

// TestSetOrgConfigValidity_ClearsOnceNoOrgsMissing: the missing-org conditions clear when the
// aggregate is empty.
func TestSetOrgConfigValidity_ClearsOnceNoOrgsMissing(t *testing.T) {
	asr := newASR("asr", "team-x", nil)

	SetOrgConfigValidity(asr, []string{"test-ns/my-org"})
	if !asr.HasInvalidConfig() {
		t.Fatal("expected InvalidConfig after first call")
	}

	SetOrgConfigValidity(asr, nil)
	if asr.HasInvalidConfig() {
		t.Errorf("InvalidConfig should be cleared when nothing is missing: %+v", asr.Status.Conditions)
	}
	if r := asr.GetReadyCondition(); r != nil && r.Reason == string(pmmeta.MissingGithubOrganizationReason) {
		t.Errorf("stale Ready reason should be cleared, got %+v", r)
	}
}

// TestSetOrgConfigValidity_AggregateKeepsMissingSignal: a non-empty aggregate keeps the condition
// even when some bindings contributed no missing orgs (guards cross-binding clobbering, #71/#5).
func TestSetOrgConfigValidity_AggregateKeepsMissingSignal(t *testing.T) {
	asr := newASR("asr", "team-x", nil)

	// Binding A found "test-ns/a" missing, binding B found none: the aggregate still lists A.
	SetOrgConfigValidity(asr, []string{"test-ns/a"})
	if !asr.HasInvalidConfig() {
		t.Fatalf("expected InvalidConfig to stay set for the aggregate, got %+v", asr.Status.Conditions)
	}
}
