// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/internal/audit"
)

func TestReconcileDelete_DeletesOwnedResourcesKeepingOtherASRs(t *testing.T) {
	asr := newASR("asr-del", "team-del", nil)

	// Owned resources span two namespaces; other-ASR resources must survive.
	ownedTeam := newOwnedTeam("asr-del-team", "ns-a", "asr-del")
	ownedRepo := newOwnedTeamRepo("asr-del-repo", "ns-b", "asr-del", []string{"r"}, "push")
	otherTeam := newOwnedTeam("asr-other-team", "ns-a", "asr-other")
	otherRepo := newOwnedTeamRepo("asr-other-repo", "ns-b", "asr-other", []string{"r"}, "push")

	p, c := newPhase(ownedTeam, ownedRepo, otherTeam, otherRepo)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete: %v", err)
	}

	var team repoguardv1.GithubTeam
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns-a", Name: "asr-del-team"}, &team); err == nil {
		t.Error("owned team should be deleted")
	}
	var repo repoguardv1.GithubTeamRepository
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns-b", Name: "asr-del-repo"}, &repo); err == nil {
		t.Error("owned team repository should be deleted")
	}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns-a", Name: "asr-other-team"}, &team); err != nil {
		t.Errorf("other ASR's team should be preserved: %v", err)
	}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns-b", Name: "asr-other-repo"}, &repo); err != nil {
		t.Errorf("other ASR's team repository should be preserved: %v", err)
	}
}

// TestReconcileDelete_RemovesOrgContributionsFromManagedOrgs asserts cleanup only touches
// orgs the ASR manages (via its field manager), releasing them with an empty contribution.
func TestReconcileDelete_RemovesOrgContributionsFromManagedOrgs(t *testing.T) {
	asr := newASR("asr-del", "team-del", nil)

	// Seed org-a (managed by asr-del) and org-c (managed by a different ASR) via SSA,
	// so each carries the seeding ASR's field manager in managedFields.
	base := newClient()
	pSeed := New(base, audit.Disabled(), logr.Discard())
	seedOrgViaSSA(t, pSeed, base, "ns-a", "org-a", map[string]*orgContribution{
		"asr-del": {OwnerTeams: []string{"team-del"}},
	})
	seedOrgViaSSA(t, pSeed, base, "ns-c", "org-c", map[string]*orgContribution{
		"asr-other": {OwnerTeams: []string{"team-other"}},
	})

	// Record the applies made during cleanup only.
	type applied struct {
		fieldManager, namespace, name string
		spec                          map[string]any
	}
	var applies []applied
	ic := interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
		Apply: func(ctx context.Context, cl client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			po := client.ApplyOptions{}
			po.ApplyOptions(opts)
			u := unstructuredFromApply(obj)
			spec, _, _ := unstructured.NestedMap(u.Object, "spec")
			applies = append(applies, applied{po.FieldManager, u.GetNamespace(), u.GetName(), spec})
			return cl.Apply(ctx, obj, opts...)
		},
	})
	pDel := New(ic, audit.Disabled(), logr.Discard())

	if err := pDel.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete: %v", err)
	}

	// Only org-a (managed by asr-del) is touched; org-c is skipped.
	if len(applies) != 1 {
		t.Fatalf("expected exactly one cleanup apply (org-a), got %d: %+v", len(applies), applies)
	}
	got := applies[0]
	if got.fieldManager != orgFieldManager("default", "asr-del") {
		t.Errorf("cleanup field manager = %q, want %q", got.fieldManager, orgFieldManager("default", "asr-del"))
	}
	if got.namespace != "ns-a" || got.name != "org-a" {
		t.Errorf("cleanup targeted %s/%s, want ns-a/org-a", got.namespace, got.name)
	}
	if owners, _, _ := unstructured.NestedSlice(got.spec, "organizationOwnerTeams"); len(owners) != 0 {
		t.Errorf("cleanup apply must release owner teams (empty list), got %v", owners)
	}
}

func TestReconcileDelete_RemovesProviderStatus(t *testing.T) {
	asr := newASR("asr", "team", nil)
	asr.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderGithub: {Ready: true},
	}

	p, _ := newPhase()
	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete: %v", err)
	}
	if _, ok := asr.Status.Providers[pmmeta.ProviderGithub]; ok {
		t.Error("github provider status should be removed after successful cleanup")
	}
}

func TestReconcileDelete_IsIdempotent(t *testing.T) {
	asr := newASR("asr", "team", nil)
	p, _ := newPhase()
	for i := range 2 {
		if err := p.ReconcileDelete(context.Background(), asr); err != nil {
			t.Fatalf("ReconcileDelete run %d: %v", i+1, err)
		}
	}
}
