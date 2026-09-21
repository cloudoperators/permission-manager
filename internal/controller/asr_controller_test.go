// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"testing"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/provider/github"
)

// repo-guard's github-instance label; hard-coded here because it is unexported in the github package.
const testGithubInstanceLabel = "repo-guard.cloudoperators.dev/github-instance"

func ctrlTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = repoguardv1.AddToScheme(s)
	return s
}

func newTestOrg(name, hostname, org string) *repoguardv1.GithubOrganization {
	return &repoguardv1.GithubOrganization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{testGithubInstanceLabel: hostname},
		},
		Spec: repoguardv1.GithubOrganizationSpec{Organization: org},
	}
}

func TestGithubOrgTriggersReconcile(t *testing.T) {
	base := newTestOrg("gh--o", "github.example.com", "o")

	// Status-only churn: identical labels + spec.organization must not re-trigger.
	if githubOrgTriggersReconcile(event.UpdateEvent{ObjectOld: base, ObjectNew: base.DeepCopy()}) {
		t.Error("status-only churn should not trigger reconcile")
	}

	// Label change (e.g. default-ldap-provider) -> trigger.
	labelChanged := base.DeepCopy()
	labelChanged.Labels["repo-guard.cloudoperators.dev/default-ldap-provider"] = "corp-ldap"
	if !githubOrgTriggersReconcile(event.UpdateEvent{ObjectOld: base, ObjectNew: labelChanged}) {
		t.Error("label change should trigger reconcile")
	}

	// spec.organization change -> trigger.
	orgChanged := base.DeepCopy()
	orgChanged.Spec.Organization = "other"
	if !githubOrgTriggersReconcile(event.UpdateEvent{ObjectOld: base, ObjectNew: orgChanged}) {
		t.Error("spec.organization change should trigger reconcile")
	}

	// Non-org objects -> false.
	if githubOrgTriggersReconcile(event.UpdateEvent{
		ObjectOld: &v1alpha1.ApplicationSpecificRole{},
		ObjectNew: &v1alpha1.ApplicationSpecificRole{},
	}) {
		t.Error("non-org objects should not trigger reconcile")
	}
}

func TestEnqueueASRsForGithubOrganization(t *testing.T) {
	org := newTestOrg("gh--o", "github.example.com", "o")
	targetID := github.OrgTargetID(org)

	// Healthy ASR that targets the org -> enqueued so its team CRs pick up org label edits.
	targeting := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "targeting", Namespace: "default"},
	}
	targeting.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderGithub: {Targets: []pmmeta.TargetStatus{{TargetID: targetID}}},
	}

	// ASR blocked on this org being missing -> enqueued for recovery.
	missing := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "default"},
	}
	missing.SetCondition(pmmeta.TrueCondition(
		pmmeta.InvalidConfigCondition, pmmeta.MissingGithubOrganizationReason, "missing org"))

	// Unrelated ASR (different target, not blocked) -> skipped.
	unrelated := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "default"},
	}
	unrelated.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderGithub: {Targets: []pmmeta.TargetStatus{{TargetID: "other-host/x"}}},
	}

	c := fake.NewClientBuilder().WithScheme(ctrlTestScheme()).
		WithObjects(targeting, missing, unrelated).Build()
	r := &ASRReconciler{Client: c}

	reqs := r.enqueueASRsForGithubOrganization(context.Background(), org)
	got := make(map[string]bool, len(reqs))
	for _, req := range reqs {
		got[req.Name] = true
	}
	if !got["targeting"] {
		t.Error("ASR targeting the changed org should be enqueued")
	}
	if !got["missing"] {
		t.Error("ASR blocked on missing org should be enqueued")
	}
	if got["unrelated"] {
		t.Error("unrelated ASR should not be enqueued")
	}
	if len(reqs) != 2 {
		t.Errorf("expected 2 requests, got %d", len(reqs))
	}
}
