// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
)

// --- Test helpers ---

// newDeletionScheme returns a scheme with all types needed for deletion tests.
func newDeletionScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = repoguardv1.AddToScheme(scheme)
	return scheme
}

// newDeletingASR creates an ASR with the given spec for use in delete-path tests.
// The lifecycle shell owns the DeletionTimestamp/finalizer; the Phase only performs
// cleanup, so these fields are not required here.
func newDeletingASR(name, specName, namespace string, permissions []v1alpha1.Permission, connectorRefs []string) *v1alpha1.ApplicationSpecificRole {
	asr := &v1alpha1.ApplicationSpecificRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ApplicationSpecificRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          specName,
			ConnectorRefs: connectorRefs,
			Permissions:   permissions,
		},
	}
	return asr
}

// newOwnedGithubTeam creates a GithubTeam CR owned by the given ASR.
func newOwnedGithubTeam(crName, namespace, asrName, asrNamespace string) *repoguardv1.GithubTeam {
	team := &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName,
			Namespace: namespace,
			Labels:    BuildLabels(asrName, asrNamespace),
		},
		Spec: repoguardv1.GithubTeamSpec{Team: "my-team"},
	}
	team.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeam"))
	return team
}

// newOwnedGithubTeamRepo creates a GithubTeamRepository CR owned by the given ASR.
func newOwnedGithubTeamRepo(crName, namespace, asrName, asrNamespace string, repos []string, permission string) *repoguardv1.GithubTeamRepository {
	teamRepo := &repoguardv1.GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName,
			Namespace: namespace,
			Labels:    BuildLabels(asrName, asrNamespace),
		},
		Spec: repoguardv1.GithubTeamRepositorySpec{
			Repository: repos,
			Permission: repoguardv1.GithubTeamPermission(permission),
		},
	}
	teamRepo.SetGroupVersionKind(repoguardv1.GroupVersion.WithKind("GithubTeamRepository"))
	return teamRepo
}

// newGithubOrgWithContributions creates a GithubOrganization with contributions from an ASR.
func newGithubOrgWithContributions(namespace, orgName, asrName string, contrib *orgContribution) *repoguardv1.GithubOrganization {
	org := &repoguardv1.GithubOrganization{
		TypeMeta: metav1.TypeMeta{
			APIVersion: repoguardv1.GroupVersion.String(),
			Kind:       "GithubOrganization",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        orgName,
			Namespace:   namespace,
			Annotations: make(map[string]string),
		},
		Spec: repoguardv1.GithubOrganizationSpec{},
	}

	if contrib != nil {
		contribJSON, _ := json.Marshal(contrib)
		org.Annotations[annotationASRDefaultTeamsPrefix+asrName] = string(contribJSON)
		org.Annotations[AnnotationASRManagedConfig] = asrName

		org.Spec.OrganizationOwnerTeams = append(org.Spec.OrganizationOwnerTeams, contrib.OwnerTeams...)
		org.Spec.DefaultPublicRepositoryTeams = append(org.Spec.DefaultPublicRepositoryTeams, contrib.PublicTeams...)
		org.Spec.DefaultPrivateRepositoryTeams = append(org.Spec.DefaultPrivateRepositoryTeams, contrib.PrivateTeams...)
	}

	return org
}

// phaseForDeletion creates a Phase for deletion tests with the given objects.
func phaseForDeletion(scheme *runtime.Scheme, objects ...client.Object) (*Phase, client.Client) {
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objects) > 0 {
		builder = builder.WithObjects(objects...)
	}
	fakeClient := builder.Build()

	return &Phase{
		Client:   fakeClient,
		Log:      logr.Discard(),
		AuditLog: audit.Disabled(),
	}, fakeClient
}

// --- GithubTeam cleanup tests ---

func TestReconcileDelete_DeletesAllOwnedTeams(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-teams", "team-alpha", "default", nil, []string{"github-cb"})

	// Create multiple teams owned by this ASR
	team1 := newOwnedGithubTeam("asr-teams-github-example-com-org1", "target-ns", "asr-teams", "default")
	team2 := newOwnedGithubTeam("asr-teams-github-example-com-org2", "target-ns", "asr-teams", "default")

	p, fakeClient := phaseForDeletion(scheme, team1, team2)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	// Verify all owned teams were deleted
	var teamList repoguardv1.GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList, client.InNamespace("target-ns")); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams after deletion, got %d", len(teamList.Items))
	}
}

func TestReconcileDelete_DoesNotDeleteTeamsFromOtherASRs(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-del", "team-del", "default", nil, []string{"github-cb"})

	// Team owned by deleting ASR
	ownedTeam := newOwnedGithubTeam("asr-del-team1", "target-ns", "asr-del", "default")
	// Team owned by a DIFFERENT ASR
	otherTeam := newOwnedGithubTeam("asr-other-team1", "target-ns", "asr-other", "default")

	p, fakeClient := phaseForDeletion(scheme, ownedTeam, otherTeam)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	// Verify other ASR's team was NOT deleted
	var team repoguardv1.GithubTeam
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "asr-other-team1"}, &team); err != nil {
		t.Errorf("other ASR's team should not be deleted: %v", err)
	}

	// Verify our team WAS deleted
	err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "asr-del-team1"}, &team)
	if err == nil {
		t.Error("owned team should have been deleted")
	}
}

// --- GithubTeamRepository cleanup tests ---

func TestReconcileDelete_DeletesAllOwnedTeamRepos(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-repos", "team-repos", "default", nil, []string{"github-cb"})

	// Create multiple team repos owned by this ASR
	repo1 := newOwnedGithubTeamRepo("asr-repos-push", "target-ns", "asr-repos", "default",
		[]string{"repo-a", "repo-b"}, "push")
	repo2 := newOwnedGithubTeamRepo("asr-repos-pull", "target-ns", "asr-repos", "default",
		[]string{"repo-c"}, "pull")

	p, fakeClient := phaseForDeletion(scheme, repo1, repo2)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	// Verify all owned team repos were deleted
	var repoList repoguardv1.GithubTeamRepositoryList
	if err := fakeClient.List(context.Background(), &repoList, client.InNamespace("target-ns")); err != nil {
		t.Fatalf("failed to list team repos: %v", err)
	}
	if len(repoList.Items) != 0 {
		t.Errorf("expected 0 team repos after deletion, got %d", len(repoList.Items))
	}
}

func TestReconcileDelete_DoesNotDeleteTeamReposFromOtherASRs(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-del", "team-del", "default", nil, []string{"github-cb"})

	// Repo owned by deleting ASR
	ownedRepo := newOwnedGithubTeamRepo("asr-del-repo1", "target-ns", "asr-del", "default",
		[]string{"repo-a"}, "push")
	// Repo owned by a DIFFERENT ASR
	otherRepo := newOwnedGithubTeamRepo("asr-other-repo1", "target-ns", "asr-other", "default",
		[]string{"repo-b"}, "push")

	p, fakeClient := phaseForDeletion(scheme, ownedRepo, otherRepo)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	// Verify other ASR's repo was NOT deleted
	var repo repoguardv1.GithubTeamRepository
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "asr-other-repo1"}, &repo); err != nil {
		t.Errorf("other ASR's team repo should not be deleted: %v", err)
	}

	// Verify our repo WAS deleted
	err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "asr-del-repo1"}, &repo)
	if err == nil {
		t.Error("owned team repo should have been deleted")
	}
}

// --- GithubOrganization entry cleanup tests ---

func TestReconcileDelete_RemovesOrgEntriesContributedByASR(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-org", "team-org", "default", nil, []string{"github-cb"})

	// Create org with contributions from this ASR
	contrib := &orgContribution{
		OwnerTeams:   []string{"team-org"},
		PublicTeams:  []repoguardv1.GithubTeamWithPermission{{Team: "team-org", Permission: repoguardv1.GithubTeamPermission("push")}},
		PrivateTeams: []repoguardv1.GithubTeamWithPermission{{Team: "team-org", Permission: repoguardv1.GithubTeamPermission("push")}},
	}
	org := newGithubOrgWithContributions("target-ns", "my-org", "team-org", contrib)

	p, fakeClient := phaseForDeletion(scheme, org)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	// Verify org entries were cleaned up
	var updatedOrg repoguardv1.GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("expected 0 owner teams after deletion, got %v", updatedOrg.Spec.OrganizationOwnerTeams)
	}
	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 0 {
		t.Errorf("expected 0 public teams after deletion, got %v", updatedOrg.Spec.DefaultPublicRepositoryTeams)
	}
	if len(updatedOrg.Spec.DefaultPrivateRepositoryTeams) != 0 {
		t.Errorf("expected 0 private teams after deletion, got %v", updatedOrg.Spec.DefaultPrivateRepositoryTeams)
	}

	// Verify annotations were cleaned up
	if _, ok := updatedOrg.Annotations[annotationASRDefaultTeamsPrefix+"team-org"]; ok {
		t.Error("expected per-ASR annotation to be removed")
	}
	if _, ok := updatedOrg.Annotations[AnnotationASRManagedConfig]; ok {
		t.Error("expected managed-config annotation to be removed (was last ASR)")
	}
}

func TestReconcileDelete_PreservesOtherASROrgEntries(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-del", "team-del", "default", nil, []string{"github-cb"})

	// Create org with contributions from BOTH the deleting ASR and another ASR
	delContrib := &orgContribution{
		OwnerTeams: []string{"team-del"},
	}
	otherContrib := &orgContribution{
		PublicTeams: []repoguardv1.GithubTeamWithPermission{{Team: "team-other", Permission: repoguardv1.GithubTeamPermission("push")}},
	}
	delContribJSON, _ := json.Marshal(delContrib)
	otherContribJSON, _ := json.Marshal(otherContrib)

	org := &repoguardv1.GithubOrganization{
		TypeMeta: metav1.TypeMeta{
			APIVersion: repoguardv1.GroupVersion.String(),
			Kind:       "GithubOrganization",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-org",
			Namespace: "target-ns",
			Annotations: map[string]string{
				AnnotationASRManagedConfig:                     "team-del,team-other",
				annotationASRDefaultTeamsPrefix + "team-del":   string(delContribJSON),
				annotationASRDefaultTeamsPrefix + "team-other": string(otherContribJSON),
			},
		},
		Spec: repoguardv1.GithubOrganizationSpec{
			OrganizationOwnerTeams:       []string{"team-del"},
			DefaultPublicRepositoryTeams: []repoguardv1.GithubTeamWithPermission{{Team: "team-other", Permission: repoguardv1.GithubTeamPermission("push")}},
		},
	}

	p, fakeClient := phaseForDeletion(scheme, org)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	// Verify ONLY the deleting ASR's entries were removed
	var updatedOrg repoguardv1.GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	// Owner teams should be empty (only team-del was there)
	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("expected 0 owner teams, got %v", updatedOrg.Spec.OrganizationOwnerTeams)
	}

	// Public teams should still have team-other
	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 1 {
		t.Fatalf("expected 1 public team, got %d", len(updatedOrg.Spec.DefaultPublicRepositoryTeams))
	}
	if updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Team != "team-other" {
		t.Errorf("expected 'team-other' to remain, got %q", updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Team)
	}

	// Annotations: team-del should be removed, team-other should remain
	if _, ok := updatedOrg.Annotations[annotationASRDefaultTeamsPrefix+"team-del"]; ok {
		t.Error("expected per-ASR annotation for team-del to be removed")
	}
	if _, ok := updatedOrg.Annotations[annotationASRDefaultTeamsPrefix+"team-other"]; !ok {
		t.Error("expected per-ASR annotation for team-other to remain")
	}
	if updatedOrg.Annotations[AnnotationASRManagedConfig] != "team-other" {
		t.Errorf("expected managed config 'team-other', got %q", updatedOrg.Annotations[AnnotationASRManagedConfig])
	}
}

func TestReconcileDelete_CleansUpMultipleOrgs(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-multi", "team-multi", "default", nil, []string{"github-cb"})

	// Contributions to org1
	contrib1 := &orgContribution{OwnerTeams: []string{"team-multi"}}
	org1 := newGithubOrgWithContributions("target-ns", "org1", "team-multi", contrib1)

	// Contributions to org2 (different namespace)
	contrib2 := &orgContribution{PublicTeams: []repoguardv1.GithubTeamWithPermission{{Team: "team-multi", Permission: repoguardv1.GithubTeamPermission("push")}}}
	org2 := newGithubOrgWithContributions("other-ns", "org2", "team-multi", contrib2)

	p, fakeClient := phaseForDeletion(scheme, org1, org2)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	// Verify org1 was cleaned
	var updatedOrg1 repoguardv1.GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "org1"}, &updatedOrg1); err != nil {
		t.Fatalf("failed to get org1: %v", err)
	}
	if len(updatedOrg1.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("org1: expected 0 owner teams, got %v", updatedOrg1.Spec.OrganizationOwnerTeams)
	}

	// Verify org2 was cleaned (in a different namespace)
	var updatedOrg2 repoguardv1.GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "other-ns", Name: "org2"}, &updatedOrg2); err != nil {
		t.Fatalf("failed to get org2: %v", err)
	}
	if len(updatedOrg2.Spec.DefaultPublicRepositoryTeams) != 0 {
		t.Errorf("org2: expected 0 public teams, got %v", updatedOrg2.Spec.DefaultPublicRepositoryTeams)
	}
}

// --- Status cleanup tests ---

func TestReconcileDelete_RemovesProviderStatus(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-status", "team-status", "default", nil, []string{"github-cb"})
	// Seed an existing github provider status entry.
	asr.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderGithub: {Ready: true},
	}

	p, _ := phaseForDeletion(scheme)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	if _, ok := asr.Status.Providers[pmmeta.ProviderGithub]; ok {
		t.Error("expected github provider status entry to be removed after successful cleanup")
	}
}

// --- Idempotency tests ---

func TestReconcileDelete_IdempotentWithNoResources(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-idemp", "team-idemp", "default", nil, []string{"github-cb"})

	p, _ := phaseForDeletion(scheme)

	// First run - nothing to clean up
	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("first ReconcileDelete failed: %v", err)
	}

	// Second run - still a no-op, should succeed
	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("second ReconcileDelete failed: %v", err)
	}
}

func TestReconcileDelete_IdempotentWithAlreadyDeletedResources(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-idemp2", "team-idemp2", "default", nil, []string{"github-cb"})

	// Create a team, then delete it manually before reconcile to simulate partial cleanup
	team := newOwnedGithubTeam("asr-idemp2-team1", "target-ns", "asr-idemp2", "default")

	p, fakeClient := phaseForDeletion(scheme, team)

	// Manually delete the team first
	if err := fakeClient.Delete(context.Background(), team); err != nil {
		t.Fatalf("failed to pre-delete team: %v", err)
	}

	// ReconcileDelete should still succeed (idempotent)
	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed on already-deleted resources: %v", err)
	}
}

// --- Cross-namespace cleanup tests ---

func TestReconcileDelete_CrossNamespaceOrgCleanup(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-cross", "team-cross", "default", nil, []string{"github-cb"})

	// Create orgs in different namespaces with contributions from this ASR
	contrib := &orgContribution{OwnerTeams: []string{"team-cross"}}
	org1 := newGithubOrgWithContributions("ns-a", "org1", "team-cross", contrib)
	org2 := newGithubOrgWithContributions("ns-b", "org2", "team-cross", contrib)
	org3 := newGithubOrgWithContributions("ns-c", "org3", "team-cross", contrib)

	p, fakeClient := phaseForDeletion(scheme, org1, org2, org3)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	// Verify all orgs across all namespaces were cleaned up
	namespaces := []string{"ns-a", "ns-b", "ns-c"}
	orgNames := []string{"org1", "org2", "org3"}
	for i, ns := range namespaces {
		var updatedOrg repoguardv1.GithubOrganization
		if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: orgNames[i]}, &updatedOrg); err != nil {
			t.Fatalf("failed to get org %s/%s: %v", ns, orgNames[i], err)
		}
		if len(updatedOrg.Spec.OrganizationOwnerTeams) != 0 {
			t.Errorf("org %s/%s: expected 0 owner teams, got %v", ns, orgNames[i], updatedOrg.Spec.OrganizationOwnerTeams)
		}
		if _, ok := updatedOrg.Annotations[annotationASRDefaultTeamsPrefix+"team-cross"]; ok {
			t.Errorf("org %s/%s: expected per-ASR annotation to be removed", ns, orgNames[i])
		}
	}
}

// --- cleanupOwnedResources unit tests ---

func TestCleanupOwnedResources_DeletesTeamsAndReposAcrossNamespaces(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-cleanup", "team-cleanup", "default", nil, []string{"github-cb"})

	// Owned resources are deleted by label across all namespaces.
	team1 := newOwnedGithubTeam("asr-cleanup-t1", "target-ns", "asr-cleanup", "default")
	team2 := newOwnedGithubTeam("asr-cleanup-t2", "other-ns", "asr-cleanup", "default")
	repo1 := newOwnedGithubTeamRepo("asr-cleanup-r1", "target-ns", "asr-cleanup", "default",
		[]string{"repo-a"}, "push")
	repo2 := newOwnedGithubTeamRepo("asr-cleanup-r2", "other-ns", "asr-cleanup", "default",
		[]string{"repo-b"}, "pull")

	p, fakeClient := phaseForDeletion(scheme, team1, team2, repo1, repo2)

	if err := p.cleanupOwnedResources(context.Background(), asr); err != nil {
		t.Fatalf("cleanupOwnedResources failed: %v", err)
	}

	// Verify all teams deleted (cluster-wide)
	var teamList repoguardv1.GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams, got %d", len(teamList.Items))
	}

	// Verify all repos deleted (cluster-wide)
	var repoList repoguardv1.GithubTeamRepositoryList
	if err := fakeClient.List(context.Background(), &repoList); err != nil {
		t.Fatalf("failed to list repos: %v", err)
	}
	if len(repoList.Items) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repoList.Items))
	}
}

func TestCleanupOwnedResources_DoesNotDeleteOtherASRResources(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-cleanup", "team-cleanup", "default", nil, []string{"github-cb"})

	owned := newOwnedGithubTeam("asr-cleanup-t1", "target-ns", "asr-cleanup", "default")
	other := newOwnedGithubTeam("asr-other-t1", "target-ns", "asr-other", "default")

	p, fakeClient := phaseForDeletion(scheme, owned, other)

	if err := p.cleanupOwnedResources(context.Background(), asr); err != nil {
		t.Fatalf("cleanupOwnedResources failed: %v", err)
	}

	var team repoguardv1.GithubTeam
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "asr-other-t1"}, &team); err != nil {
		t.Errorf("other ASR's team should not be deleted: %v", err)
	}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "asr-cleanup-t1"}, &team); err == nil {
		t.Error("owned team should have been deleted")
	}
}

// --- cleanupOrgEntries unit tests ---

func TestCleanupOrgEntries_RemovesFromAllOrgs(t *testing.T) {
	scheme := newDeletionScheme()

	asr := newDeletingASR("asr-orgclean", "team-orgclean", "default", nil, []string{"github-cb"})

	contrib := &orgContribution{OwnerTeams: []string{"team-orgclean"}}
	org1 := newGithubOrgWithContributions("ns-1", "org-a", "team-orgclean", contrib)
	org2 := newGithubOrgWithContributions("ns-2", "org-b", "team-orgclean", contrib)

	// Org without contribution from this ASR (should be untouched)
	otherContrib := &orgContribution{OwnerTeams: []string{"team-other"}}
	org3 := newGithubOrgWithContributions("ns-3", "org-c", "team-other", otherContrib)

	p, fakeClient := phaseForDeletion(scheme, org1, org2, org3)

	if err := p.cleanupOrgEntries(context.Background(), asr); err != nil {
		t.Fatalf("cleanupOrgEntries failed: %v", err)
	}

	// Verify org1 was cleaned
	var updatedOrg1 repoguardv1.GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "ns-1", Name: "org-a"}, &updatedOrg1); err != nil {
		t.Fatalf("failed to get org1: %v", err)
	}
	if len(updatedOrg1.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("org1: expected 0 owner teams, got %v", updatedOrg1.Spec.OrganizationOwnerTeams)
	}

	// Verify org2 was cleaned
	var updatedOrg2 repoguardv1.GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "ns-2", Name: "org-b"}, &updatedOrg2); err != nil {
		t.Fatalf("failed to get org2: %v", err)
	}
	if len(updatedOrg2.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("org2: expected 0 owner teams, got %v", updatedOrg2.Spec.OrganizationOwnerTeams)
	}

	// Verify org3 was NOT touched (different ASR)
	var updatedOrg3 repoguardv1.GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "ns-3", Name: "org-c"}, &updatedOrg3); err != nil {
		t.Fatalf("failed to get org3: %v", err)
	}
	if len(updatedOrg3.Spec.OrganizationOwnerTeams) != 1 || updatedOrg3.Spec.OrganizationOwnerTeams[0] != "team-other" {
		t.Errorf("org3: expected owner teams ['team-other'], got %v", updatedOrg3.Spec.OrganizationOwnerTeams)
	}
}

func TestCleanupOrgEntries_NoOrgsExist(t *testing.T) {
	scheme := newDeletionScheme()

	asr := newDeletingASR("asr-noorg", "team-noorg", "org", nil, []string{"github-cb"})

	p, _ := phaseForDeletion(scheme)

	// Should succeed gracefully with no orgs
	if err := p.cleanupOrgEntries(context.Background(), asr); err != nil {
		t.Fatalf("cleanupOrgEntries should not fail with no orgs: %v", err)
	}
}

// --- Full deletion flow integration tests ---

func TestReconcileDelete_FullFlow_TeamsReposAndOrgs(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-full", "team-full", "default", []v1alpha1.Permission{{Resource: "urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/github.com/my-org", Capability: "ADMIN"}}, []string{"github-cb"})

	// Create all resource types owned by this ASR
	team := newOwnedGithubTeam("asr-full-team", "target-ns", "asr-full", "default")
	repo := newOwnedGithubTeamRepo("asr-full-repo", "target-ns", "asr-full", "default",
		[]string{"repo-a"}, "push")
	contrib := &orgContribution{
		OwnerTeams:  []string{"team-full"},
		PublicTeams: []repoguardv1.GithubTeamWithPermission{{Team: "team-full", Permission: repoguardv1.GithubTeamPermission("push")}},
	}
	org := newGithubOrgWithContributions("target-ns", "my-org", "team-full", contrib)

	p, fakeClient := phaseForDeletion(scheme, team, repo, org)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	// Verify teams deleted
	var teamList repoguardv1.GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList, client.InNamespace("target-ns")); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams, got %d", len(teamList.Items))
	}

	// Verify repos deleted
	var repoList repoguardv1.GithubTeamRepositoryList
	if err := fakeClient.List(context.Background(), &repoList, client.InNamespace("target-ns")); err != nil {
		t.Fatalf("failed to list repos: %v", err)
	}
	if len(repoList.Items) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repoList.Items))
	}

	// Verify org entries cleaned
	var updatedOrg repoguardv1.GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}
	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("expected 0 owner teams, got %v", updatedOrg.Spec.OrganizationOwnerTeams)
	}
	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 0 {
		t.Errorf("expected 0 public teams, got %v", updatedOrg.Spec.DefaultPublicRepositoryTeams)
	}
}

// --- Edge case tests ---

func TestReconcileDelete_OrgAlreadyDeletedDuringCleanup(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-orgdel", "team-orgdel", "default", nil, []string{"github-cb"})

	// No orgs exist; cleanupOrgEntries should list 0 orgs and succeed.
	p, _ := phaseForDeletion(scheme)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete should succeed when orgs don't exist: %v", err)
	}
}

func TestReconcileDelete_ManyTeamsAndRepos(t *testing.T) {
	scheme := newDeletionScheme()
	asr := newDeletingASR("asr-many", "team-many", "default", nil, []string{"github-cb"})

	// Create many teams and repos
	var objects []client.Object
	for i := range 10 {
		team := newOwnedGithubTeam(
			fmt.Sprintf("asr-many-team-%d", i), "target-ns", "asr-many", "default")
		objects = append(objects, team)
	}
	for i := range 10 {
		repo := newOwnedGithubTeamRepo(
			fmt.Sprintf("asr-many-repo-%d", i), "target-ns", "asr-many", "default",
			[]string{fmt.Sprintf("repo-%d", i)}, "push")
		objects = append(objects, repo)
	}

	p, fakeClient := phaseForDeletion(scheme, objects...)

	if err := p.ReconcileDelete(context.Background(), asr); err != nil {
		t.Fatalf("ReconcileDelete failed: %v", err)
	}

	// Verify all teams deleted
	var teamList repoguardv1.GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList,
		client.MatchingLabels{pmapis.LabelSourceASR: "asr-many"},
	); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams, got %d", len(teamList.Items))
	}

	// Verify all repos deleted
	var repoList repoguardv1.GithubTeamRepositoryList
	if err := fakeClient.List(context.Background(), &repoList,
		client.MatchingLabels{pmapis.LabelSourceASR: "asr-many"},
	); err != nil {
		t.Fatalf("failed to list repos: %v", err)
	}
	if len(repoList.Items) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repoList.Items))
	}
}

// --- deleteStaleTeams / deleteStaleTeamRepos with nil desiredNames tests ---

func TestDeleteStaleTeams_NilDesiredDeletesAll(t *testing.T) {
	scheme := newDeletionScheme()

	team1 := newOwnedGithubTeam("team-1", "ns", "my-asr", "default")
	team2 := newOwnedGithubTeam("team-2", "ns", "my-asr", "org")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team1, team2).Build()

	// nil desired set means delete ALL matching teams
	err := deleteStaleTeams(context.Background(), c, "ns", "my-asr", nil, audit.Disabled())
	if err != nil {
		t.Fatalf("deleteStaleTeams failed: %v", err)
	}

	var teamList repoguardv1.GithubTeamList
	if err := c.List(context.Background(), &teamList, client.InNamespace("ns")); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams with nil desired, got %d", len(teamList.Items))
	}
}

func TestDeleteStaleTeamRepos_NilDesiredDeletesAll(t *testing.T) {
	scheme := newDeletionScheme()

	repo1 := newOwnedGithubTeamRepo("repo-1", "ns", "my-asr", "default", []string{"a"}, "push")
	repo2 := newOwnedGithubTeamRepo("repo-2", "ns", "my-asr", "org", []string{"b"}, "pull")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo1, repo2).Build()

	// nil desired set means delete ALL matching repos
	err := deleteStaleTeamRepos(context.Background(), c, "ns", "my-asr", nil, audit.Disabled())
	if err != nil {
		t.Fatalf("deleteStaleTeamRepos failed: %v", err)
	}

	var repoList repoguardv1.GithubTeamRepositoryList
	if err := c.List(context.Background(), &repoList, client.InNamespace("ns")); err != nil {
		t.Fatalf("failed to list repos: %v", err)
	}
	if len(repoList.Items) != 0 {
		t.Errorf("expected 0 repos with nil desired, got %d", len(repoList.Items))
	}
}

// --- removeASRFromOrg unit tests ---

func TestRemoveASRFromOrg_OrgNotFound_Succeeds(t *testing.T) {
	scheme := newDeletionScheme()

	p, _ := phaseForDeletion(scheme)

	// Call removeASRFromOrg with a non-existent org
	org := &repoguardv1.GithubOrganization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nonexistent-org",
			Namespace: "target-ns",
		},
	}

	if err := p.removeASRFromOrg(context.Background(), org, "team-x"); err != nil {
		t.Fatalf("removeASRFromOrg should succeed for non-existent org: %v", err)
	}
}

func TestRemoveASRFromOrg_RemovesContributionsAndAnnotations(t *testing.T) {
	scheme := newDeletionScheme()

	contrib := &orgContribution{
		OwnerTeams:   []string{"team-rem"},
		PublicTeams:  []repoguardv1.GithubTeamWithPermission{{Team: "team-rem", Permission: repoguardv1.GithubTeamPermission("push")}},
		PrivateTeams: []repoguardv1.GithubTeamWithPermission{{Team: "team-rem", Permission: repoguardv1.GithubTeamPermission("pull")}},
	}
	org := newGithubOrgWithContributions("target-ns", "my-org", "team-rem", contrib)

	p, fakeClient := phaseForDeletion(scheme, org)

	if err := p.removeASRFromOrg(context.Background(), org, "team-rem"); err != nil {
		t.Fatalf("removeASRFromOrg failed: %v", err)
	}

	var updatedOrg repoguardv1.GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("expected 0 owner teams, got %v", updatedOrg.Spec.OrganizationOwnerTeams)
	}
	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 0 {
		t.Errorf("expected 0 public teams, got %v", updatedOrg.Spec.DefaultPublicRepositoryTeams)
	}
	if len(updatedOrg.Spec.DefaultPrivateRepositoryTeams) != 0 {
		t.Errorf("expected 0 private teams, got %v", updatedOrg.Spec.DefaultPrivateRepositoryTeams)
	}
	if _, ok := updatedOrg.Annotations[annotationASRDefaultTeamsPrefix+"team-rem"]; ok {
		t.Error("expected per-ASR annotation to be removed")
	}
}
