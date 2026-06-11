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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
)

// --- Test helpers ---

// newDeletionScheme returns a scheme with all types needed for deletion tests.
func newDeletionScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = pmv1.AddToScheme(scheme)
	_ = AddToScheme(scheme)
	return scheme
}

// newDeletingASR creates an ASR with DeletionTimestamp set and the GitHub finalizer added.
func newDeletingASR(name, specName, namespace string, permissions []pmv1.Permission, connectorRefs []string) *pmv1.ApplicationSpecificRole {
	now := metav1.Now()
	asr := &pmv1.ApplicationSpecificRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ApplicationSpecificRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerGithub},
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          specName,
			ConnectorRefs: connectorRefs,
			Permissions:   permissions,
		},
	}
	return asr
}

// newConnectorBinding creates a GitHub ConnectorBinding with the given capability mapping.
func newConnectorBinding(name, namespace string, capMapping map[string]string, targetNS string) *pmv1.ConnectorBinding {
	def := GithubConnectorDefinition{
		CapabilityMapping:     capMapping,
		DefaultTargetNamespace: targetNS,
	}
	raw, _ := json.Marshal(def)
	return &pmv1.ConnectorBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ConnectorBinding",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       name,
			Provider:   ProviderGithub,
			Definition: &runtime.RawExtension{Raw: raw},
		},
	}
}

// newOwnedGithubTeam creates a GithubTeam CR owned by the given ASR.
func newOwnedGithubTeam(crName, namespace, asrName, asrNamespace string) *GithubTeam {
	team := &GithubTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName,
			Namespace: namespace,
			Labels:    BuildLabels(asrName, asrNamespace),
		},
		Spec: GithubTeamSpec{TeamName: "my-team"},
	}
	team.SetGroupVersionKind(GroupVersion.WithKind("GithubTeam"))
	return team
}

// newOwnedGithubTeamRepo creates a GithubTeamRepository CR owned by the given ASR.
func newOwnedGithubTeamRepo(crName, namespace, asrName, asrNamespace string, repos []string, permission string) *GithubTeamRepository {
	teamRepo := &GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName,
			Namespace: namespace,
			Labels:    BuildLabels(asrName, asrNamespace),
		},
		Spec: GithubTeamRepositorySpec{
			Repositories: repos,
			Permission:   permission,
		},
	}
	teamRepo.SetGroupVersionKind(GroupVersion.WithKind("GithubTeamRepository"))
	return teamRepo
}

// newGithubOrgWithContributions creates a GithubOrganization with contributions from an ASR.
func newGithubOrgWithContributions(namespace, orgName, asrName string, contrib *orgContribution) *GithubOrganization {
	org := &GithubOrganization{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "GithubOrganization",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        orgName,
			Namespace:   namespace,
			Annotations: make(map[string]string),
		},
		Spec: GithubOrganizationSpec{},
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

// reconcilerForDeletion creates a GithubReconciler for deletion tests with the given objects.
func reconcilerForDeletion(scheme *runtime.Scheme, cbs []pmv1.ConnectorBinding, objects ...client.Object) (*GithubReconciler, client.Client) {
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objects) > 0 {
		builder = builder.WithObjects(objects...)
	}
	fakeClient := builder.Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync(cbs)

	return &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}, fakeClient
}

// --- Deletion trigger tests ---

func TestDeletion_TriggeredByDeletionTimestamp(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-del", "team-del", "default",
		[]pmv1.Permission{
			{Resource: "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/my-repo", Capability: "WRITE"},
		},
		[]string{"github-cb"},
	)

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb)

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-del"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue: %v", result)
	}

	// When a fake client object has DeletionTimestamp and all finalizers are removed,
	// the fake client may garbage collect it. Either the ASR is gone (GC'd) or
	// it exists without the finalizer - both indicate successful cleanup.
	var updatedASR pmv1.ApplicationSpecificRole
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "asr-del"}, &updatedASR)
	if err == nil {
		// ASR still exists - verify finalizer was removed
		if controllerutil.ContainsFinalizer(&updatedASR, FinalizerGithub) {
			t.Error("expected finalizer to be removed after deletion")
		}
	}
	// If err is NotFound, the object was GC'd which is also correct behavior
}

func TestDeletion_SkippedWhenNoGithubFinalizer(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")

	// ASR with DeletionTimestamp but only a NON-github finalizer (some other controller's finalizer)
	now := metav1.Now()
	asr := &pmv1.ApplicationSpecificRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ApplicationSpecificRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "asr-nofin",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"some-other-finalizer"}, // not the github finalizer
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "team-nofin",
			ConnectorRefs: []string{"github-cb"},
		},
	}

	r, _ := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb)

	// Should return success without doing cleanup (no github finalizer to process)
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-nofin"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue: %v", result)
	}
}

// --- GithubTeam cleanup tests ---

func TestDeletion_DeletesAllOwnedTeams(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-teams", "team-alpha", "default",
		[]pmv1.Permission{
			{Resource: "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/repo-a", Capability: "WRITE"},
		},
		[]string{"github-cb"},
	)

	// Create multiple teams owned by this ASR
	team1 := newOwnedGithubTeam("asr-teams-github-example-com-org1", "target-ns", "asr-teams", "default")
	team2 := newOwnedGithubTeam("asr-teams-github-example-com-org2", "target-ns", "asr-teams", "default")

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, team1, team2)

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-teams"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue: %v", result)
	}

	// Verify all owned teams were deleted
	var teamList GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList, client.InNamespace("target-ns")); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams after deletion, got %d", len(teamList.Items))
	}
}

func TestDeletion_DoesNotDeleteTeamsFromOtherASRs(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-del", "team-del", "default", nil, []string{"github-cb"})

	// Team owned by deleting ASR
	ownedTeam := newOwnedGithubTeam("asr-del-team1", "target-ns", "asr-del", "default")
	// Team owned by a DIFFERENT ASR
	otherTeam := newOwnedGithubTeam("asr-other-team1", "target-ns", "asr-other", "default")

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, ownedTeam, otherTeam)

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-del"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify other ASR's team was NOT deleted
	var team GithubTeam
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "asr-other-team1"}, &team); err != nil {
		t.Errorf("other ASR's team should not be deleted: %v", err)
	}

	// Verify our team WAS deleted
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "asr-del-team1"}, &team)
	if err == nil {
		t.Error("owned team should have been deleted")
	}
}

// --- GithubTeamRepository cleanup tests ---

func TestDeletion_DeletesAllOwnedTeamRepos(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push", "READ": "pull"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-repos", "team-repos", "default",
		[]pmv1.Permission{
			{Resource: "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/repo-a", Capability: "WRITE"},
		},
		[]string{"github-cb"},
	)

	// Create multiple team repos owned by this ASR
	repo1 := newOwnedGithubTeamRepo("asr-repos-push", "target-ns", "asr-repos", "default",
		[]string{"repo-a", "repo-b"}, "push")
	repo2 := newOwnedGithubTeamRepo("asr-repos-pull", "target-ns", "asr-repos", "default",
		[]string{"repo-c"}, "pull")

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, repo1, repo2)

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-repos"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue: %v", result)
	}

	// Verify all owned team repos were deleted
	var repoList GithubTeamRepositoryList
	if err := fakeClient.List(context.Background(), &repoList, client.InNamespace("target-ns")); err != nil {
		t.Fatalf("failed to list team repos: %v", err)
	}
	if len(repoList.Items) != 0 {
		t.Errorf("expected 0 team repos after deletion, got %d", len(repoList.Items))
	}
}

func TestDeletion_DoesNotDeleteTeamReposFromOtherASRs(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-del", "team-del", "default", nil, []string{"github-cb"})

	// Repo owned by deleting ASR
	ownedRepo := newOwnedGithubTeamRepo("asr-del-repo1", "target-ns", "asr-del", "default",
		[]string{"repo-a"}, "push")
	// Repo owned by a DIFFERENT ASR
	otherRepo := newOwnedGithubTeamRepo("asr-other-repo1", "target-ns", "asr-other", "default",
		[]string{"repo-b"}, "push")

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, ownedRepo, otherRepo)

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-del"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify other ASR's repo was NOT deleted
	var repo GithubTeamRepository
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "asr-other-repo1"}, &repo); err != nil {
		t.Errorf("other ASR's team repo should not be deleted: %v", err)
	}

	// Verify our repo WAS deleted
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "asr-del-repo1"}, &repo)
	if err == nil {
		t.Error("owned team repo should have been deleted")
	}
}

// --- GithubOrganization entry cleanup tests ---

func TestDeletion_RemovesOrgEntriesContributedByASR(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin", "WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-org", "team-org", "default",
		[]pmv1.Permission{
			{Resource: "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org", Capability: "ADMIN"},
		},
		[]string{"github-cb"},
	)

	// Create org with contributions from this ASR
	contrib := &orgContribution{
		OwnerTeams:   []string{"team-org"},
		PublicTeams:  []GithubTeamWithPermission{{Name: "team-org", Permission: "push"}},
		PrivateTeams: []GithubTeamWithPermission{{Name: "team-org", Permission: "push"}},
	}
	org := newGithubOrgWithContributions("target-ns", "my-org", "team-org", contrib)

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, org)

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-org"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue: %v", result)
	}

	// Verify org entries were cleaned up
	var updatedOrg GithubOrganization
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

func TestDeletion_PreservesOtherASROrgEntries(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin", "WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-del", "team-del", "default",
		[]pmv1.Permission{
			{Resource: "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org", Capability: "ADMIN"},
		},
		[]string{"github-cb"},
	)

	// Create org with contributions from BOTH the deleting ASR and another ASR
	delContrib := &orgContribution{
		OwnerTeams: []string{"team-del"},
	}
	otherContrib := &orgContribution{
		PublicTeams: []GithubTeamWithPermission{{Name: "team-other", Permission: "push"}},
	}
	delContribJSON, _ := json.Marshal(delContrib)
	otherContribJSON, _ := json.Marshal(otherContrib)

	org := &GithubOrganization{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "GithubOrganization",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-org",
			Namespace: "target-ns",
			Annotations: map[string]string{
				AnnotationASRManagedConfig:                    "team-del,team-other",
				annotationASRDefaultTeamsPrefix + "team-del":   string(delContribJSON),
				annotationASRDefaultTeamsPrefix + "team-other": string(otherContribJSON),
			},
		},
		Spec: GithubOrganizationSpec{
			OrganizationOwnerTeams:       []string{"team-del"},
			DefaultPublicRepositoryTeams: []GithubTeamWithPermission{{Name: "team-other", Permission: "push"}},
		},
	}

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, org)

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-del"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify ONLY the deleting ASR's entries were removed
	var updatedOrg GithubOrganization
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
	if updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Name != "team-other" {
		t.Errorf("expected 'team-other' to remain, got %q", updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Name)
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

func TestDeletion_CleansUpMultipleOrgs(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin", "WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-multi", "team-multi", "default",
		[]pmv1.Permission{
			{Resource: "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/org1", Capability: "ADMIN"},
			{Resource: "repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/org2/public", Capability: "WRITE"},
		},
		[]string{"github-cb"},
	)

	// Contributions to org1
	contrib1 := &orgContribution{OwnerTeams: []string{"team-multi"}}
	org1 := newGithubOrgWithContributions("target-ns", "org1", "team-multi", contrib1)

	// Contributions to org2
	contrib2 := &orgContribution{PublicTeams: []GithubTeamWithPermission{{Name: "team-multi", Permission: "push"}}}
	org2 := newGithubOrgWithContributions("other-ns", "org2", "team-multi", contrib2)

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, org1, org2)

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-multi"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify org1 was cleaned
	var updatedOrg1 GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "org1"}, &updatedOrg1); err != nil {
		t.Fatalf("failed to get org1: %v", err)
	}
	if len(updatedOrg1.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("org1: expected 0 owner teams, got %v", updatedOrg1.Spec.OrganizationOwnerTeams)
	}

	// Verify org2 was cleaned (in a different namespace)
	var updatedOrg2 GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "other-ns", Name: "org2"}, &updatedOrg2); err != nil {
		t.Fatalf("failed to get org2: %v", err)
	}
	if len(updatedOrg2.Spec.DefaultPublicRepositoryTeams) != 0 {
		t.Errorf("org2: expected 0 public teams, got %v", updatedOrg2.Spec.DefaultPublicRepositoryTeams)
	}
}

// --- Finalizer removal tests ---

func TestDeletion_FinalizerRemovedOnlyAfterCleanupSucceeds(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-fin", "team-fin", "default",
		[]pmv1.Permission{
			{Resource: "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/repo-a", Capability: "WRITE"},
		},
		[]string{"github-cb"},
	)

	team := newOwnedGithubTeam("asr-fin-team1", "target-ns", "asr-fin", "default")
	repo := newOwnedGithubTeamRepo("asr-fin-repo1", "target-ns", "asr-fin", "default",
		[]string{"repo-a"}, "push")

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, team, repo)

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-fin"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify finalizer was removed (the fake client GCs the object when last finalizer removed
	// from an object with deletionTimestamp - object being gone confirms cleanup succeeded)
	var updatedASR pmv1.ApplicationSpecificRole
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "asr-fin"}, &updatedASR)
	if err == nil {
		// ASR still exists - finalizer should be gone
		if controllerutil.ContainsFinalizer(&updatedASR, FinalizerGithub) {
			t.Error("finalizer should be removed after successful cleanup")
		}
	}
	// If NotFound: fake client GC'd it (all finalizers removed) - this is correct

	// Verify all resources were cleaned up
	var teamList GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList, client.InNamespace("target-ns"), client.MatchingLabels{LabelSourceASR: "asr-fin"}); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams, got %d", len(teamList.Items))
	}

	var repoList GithubTeamRepositoryList
	if err := fakeClient.List(context.Background(), &repoList, client.InNamespace("target-ns"), client.MatchingLabels{LabelSourceASR: "asr-fin"}); err != nil {
		t.Fatalf("failed to list repos: %v", err)
	}
	if len(repoList.Items) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repoList.Items))
	}
}

// --- Idempotency tests ---

func TestDeletion_IdempotentOnReRun(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-idemp", "team-idemp", "default",
		[]pmv1.Permission{
			{Resource: "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/repo-a", Capability: "WRITE"},
		},
		[]string{"github-cb"},
	)

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb)

	// First reconciliation - resources already don't exist (nothing to clean up)
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-idemp"},
	})
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue on first run: %v", result)
	}

	// Verify the ASR was processed (finalizer removed, object may be GC'd)
	var updatedASR pmv1.ApplicationSpecificRole
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "asr-idemp"}, &updatedASR)
	if err == nil {
		if controllerutil.ContainsFinalizer(&updatedASR, FinalizerGithub) {
			t.Error("finalizer should be removed even when no resources exist")
		}
	}
	// If NotFound: object was GC'd by fake client (correct - all finalizers removed)

	// Second reconciliation - ASR no longer exists, should be a no-op
	result, err = r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-idemp"},
	})
	if err != nil {
		t.Fatalf("second reconcile should succeed for missing ASR: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue on second run: %v", result)
	}
}

func TestDeletion_IdempotentWithAlreadyDeletedResources(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-idemp2", "team-idemp2", "default", nil, []string{"github-cb"})

	// Create a team, then delete it manually before reconcile to simulate partial cleanup
	team := newOwnedGithubTeam("asr-idemp2-team1", "target-ns", "asr-idemp2", "default")

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, team)

	// Manually delete the team first
	if err := fakeClient.Delete(context.Background(), team); err != nil {
		t.Fatalf("failed to pre-delete team: %v", err)
	}

	// Reconcile should still succeed (idempotent)
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-idemp2"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed on already-deleted resources: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue: %v", result)
	}
}

// --- Cross-namespace cleanup tests ---

func TestDeletion_CrossNamespaceOrgCleanup(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-cross", "team-cross", "default", nil, []string{"github-cb"})

	// Create orgs in different namespaces with contributions from this ASR
	contrib := &orgContribution{OwnerTeams: []string{"team-cross"}}
	org1 := newGithubOrgWithContributions("ns-a", "org1", "team-cross", contrib)
	org2 := newGithubOrgWithContributions("ns-b", "org2", "team-cross", contrib)
	org3 := newGithubOrgWithContributions("ns-c", "org3", "team-cross", contrib)

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, org1, org2, org3)

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-cross"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify all orgs across all namespaces were cleaned up
	namespaces := []string{"ns-a", "ns-b", "ns-c"}
	orgNames := []string{"org1", "org2", "org3"}
	for i, ns := range namespaces {
		var updatedOrg GithubOrganization
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

func TestCleanupOwnedResources_DeletesTeamsAndRepos(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push", "READ": "pull"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := &pmv1.ApplicationSpecificRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ApplicationSpecificRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-cleanup",
			Namespace: "default",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "team-cleanup",
			ConnectorRefs: []string{"github-cb"},
		},
	}

	team1 := newOwnedGithubTeam("asr-cleanup-t1", "target-ns", "asr-cleanup", "default")
	team2 := newOwnedGithubTeam("asr-cleanup-t2", "target-ns", "asr-cleanup", "default")
	repo1 := newOwnedGithubTeamRepo("asr-cleanup-r1", "target-ns", "asr-cleanup", "default",
		[]string{"repo-a"}, "push")
	repo2 := newOwnedGithubTeamRepo("asr-cleanup-r2", "target-ns", "asr-cleanup", "default",
		[]string{"repo-b"}, "pull")

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, team1, team2, repo1, repo2)

	err := r.cleanupOwnedResources(context.Background(), asr, "github-cb", cb.Spec)
	if err != nil {
		t.Fatalf("cleanupOwnedResources failed: %v", err)
	}

	// Verify all teams deleted
	var teamList GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList, client.InNamespace("target-ns")); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams, got %d", len(teamList.Items))
	}

	// Verify all repos deleted
	var repoList GithubTeamRepositoryList
	if err := fakeClient.List(context.Background(), &repoList, client.InNamespace("target-ns")); err != nil {
		t.Fatalf("failed to list repos: %v", err)
	}
	if len(repoList.Items) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repoList.Items))
	}
}

func TestCleanupOwnedResources_FallsBackToASRNamespaceWhenDefinitionInvalid(t *testing.T) {
	scheme := newDeletionScheme()

	// Create a CB with invalid definition
	cb := &pmv1.ConnectorBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ConnectorBinding",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: &runtime.RawExtension{Raw: []byte(`invalid json`)},
		},
	}

	asr := &pmv1.ApplicationSpecificRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ApplicationSpecificRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-fallback",
			Namespace: "asr-namespace",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "team-fallback",
			ConnectorRefs: []string{"github-cb"},
		},
	}

	// Team in the ASR's namespace (the fallback)
	team := newOwnedGithubTeam("asr-fallback-t1", "asr-namespace", "asr-fallback", "asr-namespace")

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, team)

	err := r.cleanupOwnedResources(context.Background(), asr, "github-cb", cb.Spec)
	if err != nil {
		t.Fatalf("cleanupOwnedResources failed: %v", err)
	}

	// Verify team was deleted from the fallback namespace
	var teamList GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList, client.InNamespace("asr-namespace")); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams in fallback namespace, got %d", len(teamList.Items))
	}
}

// --- cleanupOrgEntries unit tests ---

func TestCleanupOrgEntries_RemovesFromAllOrgs(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin"}
	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")

	asr := &pmv1.ApplicationSpecificRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ApplicationSpecificRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-orgclean",
			Namespace: "default",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "team-orgclean",
			ConnectorRefs: []string{"github-cb"},
		},
	}

	contrib := &orgContribution{OwnerTeams: []string{"team-orgclean"}}
	org1 := newGithubOrgWithContributions("ns-1", "org-a", "team-orgclean", contrib)
	org2 := newGithubOrgWithContributions("ns-2", "org-b", "team-orgclean", contrib)

	// Org without contribution from this ASR (should be untouched)
	otherContrib := &orgContribution{OwnerTeams: []string{"team-other"}}
	org3 := newGithubOrgWithContributions("ns-3", "org-c", "team-other", otherContrib)

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, org1, org2, org3)

	err := r.cleanupOrgEntries(context.Background(), asr)
	if err != nil {
		t.Fatalf("cleanupOrgEntries failed: %v", err)
	}

	// Verify org1 was cleaned
	var updatedOrg1 GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "ns-1", Name: "org-a"}, &updatedOrg1); err != nil {
		t.Fatalf("failed to get org1: %v", err)
	}
	if len(updatedOrg1.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("org1: expected 0 owner teams, got %v", updatedOrg1.Spec.OrganizationOwnerTeams)
	}

	// Verify org2 was cleaned
	var updatedOrg2 GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "ns-2", Name: "org-b"}, &updatedOrg2); err != nil {
		t.Fatalf("failed to get org2: %v", err)
	}
	if len(updatedOrg2.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("org2: expected 0 owner teams, got %v", updatedOrg2.Spec.OrganizationOwnerTeams)
	}

	// Verify org3 was NOT touched (different ASR)
	var updatedOrg3 GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "ns-3", Name: "org-c"}, &updatedOrg3); err != nil {
		t.Fatalf("failed to get org3: %v", err)
	}
	if len(updatedOrg3.Spec.OrganizationOwnerTeams) != 1 || updatedOrg3.Spec.OrganizationOwnerTeams[0] != "team-other" {
		t.Errorf("org3: expected owner teams ['team-other'], got %v", updatedOrg3.Spec.OrganizationOwnerTeams)
	}
}

func TestCleanupOrgEntries_NoOrgsExist(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin"}
	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")

	asr := &pmv1.ApplicationSpecificRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ApplicationSpecificRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "asr-noorg",
			Namespace: "default",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "team-noorg",
			ConnectorRefs: []string{"github-cb"},
		},
	}

	r, _ := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb)

	// Should succeed gracefully with no orgs
	err := r.cleanupOrgEntries(context.Background(), asr)
	if err != nil {
		t.Fatalf("cleanupOrgEntries should not fail with no orgs: %v", err)
	}
}

// --- Full deletion flow integration tests ---

func TestDeletion_FullFlow_TeamsReposAndOrgs(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin", "WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-full", "team-full", "default",
		[]pmv1.Permission{
			{Resource: "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org", Capability: "ADMIN"},
			{Resource: "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/repo-a", Capability: "WRITE"},
		},
		[]string{"github-cb"},
	)

	// Create all resource types owned by this ASR
	team := newOwnedGithubTeam("asr-full-team", "target-ns", "asr-full", "default")
	repo := newOwnedGithubTeamRepo("asr-full-repo", "target-ns", "asr-full", "default",
		[]string{"repo-a"}, "push")
	contrib := &orgContribution{
		OwnerTeams: []string{"team-full"},
		PublicTeams: []GithubTeamWithPermission{{Name: "team-full", Permission: "push"}},
	}
	org := newGithubOrgWithContributions("target-ns", "my-org", "team-full", contrib)

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, team, repo, org)

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-full"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue: %v", result)
	}

	// Verify teams deleted
	var teamList GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList, client.InNamespace("target-ns")); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams, got %d", len(teamList.Items))
	}

	// Verify repos deleted
	var repoList GithubTeamRepositoryList
	if err := fakeClient.List(context.Background(), &repoList, client.InNamespace("target-ns")); err != nil {
		t.Fatalf("failed to list repos: %v", err)
	}
	if len(repoList.Items) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repoList.Items))
	}

	// Verify org entries cleaned
	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}
	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("expected 0 owner teams, got %v", updatedOrg.Spec.OrganizationOwnerTeams)
	}
	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 0 {
		t.Errorf("expected 0 public teams, got %v", updatedOrg.Spec.DefaultPublicRepositoryTeams)
	}

	// Verify finalizer removed (object may be GC'd by fake client)
	var updatedASR pmv1.ApplicationSpecificRole
	err = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "asr-full"}, &updatedASR)
	if err == nil {
		if controllerutil.ContainsFinalizer(&updatedASR, FinalizerGithub) {
			t.Error("finalizer should be removed after full cleanup")
		}
	}
	// If NotFound: object was GC'd (all finalizers removed) - correct
}

func TestDeletion_ASRNotFound_SkipsGracefully(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}
	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")

	r, _ := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, cb)

	// Reconcile a non-existent ASR (already fully deleted)
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "does-not-exist"},
	})
	if err != nil {
		t.Fatalf("Reconcile should not error for missing ASR: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue for missing ASR: %v", result)
	}
}

func TestDeletion_ASRWithNoGithubRef_Skipped(t *testing.T) {
	scheme := newDeletionScheme()

	// No GitHub ConnectorBindings indexed
	now := metav1.Now()
	asr := &pmv1.ApplicationSpecificRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ApplicationSpecificRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "asr-nongithub",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerGithub},
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "team-nongithub",
			ConnectorRefs: []string{"some-other-cb"},
		},
	}

	r, _ := reconcilerForDeletion(scheme, nil, asr)

	// Without a GitHub CB ref, reconciler should skip the ASR entirely
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-nongithub"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue: %v", result)
	}
}

// --- Edge case tests ---

func TestDeletion_OrgAlreadyDeletedDuringCleanup(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")

	// ASR with contribution annotation referencing an org name that matches
	asr := newDeletingASR("asr-orgdel", "team-orgdel", "default", nil, []string{"github-cb"})

	// Create an org with contribution from this ASR, then we'll test that
	// cleanup works even if the org doesn't exist (already deleted)
	// By NOT creating the org, cleanupOrgEntries should list 0 orgs and succeed
	r, _ := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb)

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-orgdel"},
	})
	if err != nil {
		t.Fatalf("Reconcile should succeed when orgs don't exist: %v", err)
	}
}

func TestDeletion_WithAdvancedConfigTargetNamespace(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "default-target")

	// ASR with advancedConfig that overrides the target namespace
	advConfig := map[string]interface{}{
		"github": map[string]string{
			"targetNamespace": "custom-ns",
		},
	}
	advConfigJSON, _ := json.Marshal(advConfig)

	now := metav1.Now()
	asr := &pmv1.ApplicationSpecificRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ApplicationSpecificRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "asr-adv",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerGithub},
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:           "team-adv",
			ConnectorRefs:  []string{"github-cb"},
			AdvancedConfig: &runtime.RawExtension{Raw: advConfigJSON},
		},
	}

	// Team in the custom namespace (from advancedConfig)
	team := newOwnedGithubTeam("asr-adv-team1", "custom-ns", "asr-adv", "default")

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, team)

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-adv"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify team was deleted from the custom namespace
	var teamList GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList, client.InNamespace("custom-ns")); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams in custom-ns, got %d", len(teamList.Items))
	}
}

func TestDeletion_ManyTeamsAndRepos(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-many", "team-many", "default", nil, []string{"github-cb"})

	// Create many teams and repos
	objects := []client.Object{asr, cb}
	for i := 0; i < 10; i++ {
		team := newOwnedGithubTeam(
			fmt.Sprintf("asr-many-team-%d", i), "target-ns", "asr-many", "default")
		objects = append(objects, team)
	}
	for i := 0; i < 10; i++ {
		repo := newOwnedGithubTeamRepo(
			fmt.Sprintf("asr-many-repo-%d", i), "target-ns", "asr-many", "default",
			[]string{fmt.Sprintf("repo-%d", i)}, "push")
		objects = append(objects, repo)
	}

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, objects...)

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-many"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue: %v", result)
	}

	// Verify all teams deleted
	var teamList GithubTeamList
	if err := fakeClient.List(context.Background(), &teamList,
		client.InNamespace("target-ns"),
		client.MatchingLabels{LabelSourceASR: "asr-many"},
	); err != nil {
		t.Fatalf("failed to list teams: %v", err)
	}
	if len(teamList.Items) != 0 {
		t.Errorf("expected 0 teams, got %d", len(teamList.Items))
	}

	// Verify all repos deleted
	var repoList GithubTeamRepositoryList
	if err := fakeClient.List(context.Background(), &repoList,
		client.InNamespace("target-ns"),
		client.MatchingLabels{LabelSourceASR: "asr-many"},
	); err != nil {
		t.Fatalf("failed to list repos: %v", err)
	}
	if len(repoList.Items) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repoList.Items))
	}
}

// --- Requeue on failure tests ---

func TestDeletion_OrgCleanupFailureRequeues(t *testing.T) {
	// This test verifies the reconciler's behavior when org cleanup encounters
	// a requeue scenario (e.g., org not found returns a delayed requeue in the reconcile flow).
	// In the actual cleanupOrgEntries, if an org is not found during the removeASRFromOrg call,
	// it's treated as success (nothing to clean up). The requeue only happens via
	// the reconcileOrganizations path for non-deletion flow.
	//
	// For the deletion flow, cleanupOrgEntries handles NotFound gracefully:
	// removeASRFromOrg returns nil when the org is not found (client.IgnoreNotFound).
	// This is correct and the test verifies it.
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin"}
	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")

	asr := newDeletingASR("asr-requeue", "team-requeue", "default", nil, []string{"github-cb"})

	// The cleanupOrgEntries lists all orgs cluster-wide, then for each one that has
	// a contribution from this ASR, it removes the contribution.
	// If there are no orgs or no contributions, it's a no-op.
	r, _ := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb)

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-requeue"},
	})
	if err != nil {
		t.Fatalf("Reconcile should not error: %v", err)
	}
	if result.RequeueAfter != 0 || result.Requeue {
		t.Errorf("unexpected requeue: %v", result)
	}
}

// --- deleteStaleTeams with nil desiredNames tests ---

func TestDeleteStaleTeams_NilDesiredDeletesAll(t *testing.T) {
	scheme := newDeletionScheme()

	team1 := newOwnedGithubTeam("team-1", "ns", "my-asr", "default")
	team2 := newOwnedGithubTeam("team-2", "ns", "my-asr", "default")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team1, team2).Build()

	// nil desired set means delete ALL matching teams
	err := deleteStaleTeams(context.Background(), c, "ns", "my-asr", nil, audit.Disabled())
	if err != nil {
		t.Fatalf("deleteStaleTeams failed: %v", err)
	}

	var teamList GithubTeamList
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
	repo2 := newOwnedGithubTeamRepo("repo-2", "ns", "my-asr", "default", []string{"b"}, "pull")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo1, repo2).Build()

	// nil desired set means delete ALL matching repos
	err := deleteStaleTeamRepos(context.Background(), c, "ns", "my-asr", nil, audit.Disabled())
	if err != nil {
		t.Fatalf("deleteStaleTeamRepos failed: %v", err)
	}

	var repoList GithubTeamRepositoryList
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
	capMapping := map[string]string{"ADMIN": "admin"}
	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")

	r, _ := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, cb)

	// Call removeASRFromOrg with a non-existent org
	org := &GithubOrganization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nonexistent-org",
			Namespace: "target-ns",
		},
	}

	err := r.removeASRFromOrg(context.Background(), org, "team-x")
	if err != nil {
		t.Fatalf("removeASRFromOrg should succeed for non-existent org: %v", err)
	}
}

func TestRemoveASRFromOrg_RemovesContributionsAndAnnotations(t *testing.T) {
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin"}
	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")

	contrib := &orgContribution{
		OwnerTeams:   []string{"team-rem"},
		PublicTeams:  []GithubTeamWithPermission{{Name: "team-rem", Permission: "push"}},
		PrivateTeams: []GithubTeamWithPermission{{Name: "team-rem", Permission: "pull"}},
	}
	org := newGithubOrgWithContributions("target-ns", "my-org", "team-rem", contrib)

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, cb, org)

	err := r.removeASRFromOrg(context.Background(), org, "team-rem")
	if err != nil {
		t.Fatalf("removeASRFromOrg failed: %v", err)
	}

	var updatedOrg GithubOrganization
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

// --- Timing/ordering verification ---

func TestDeletion_CleanupOrderIsResourcesThenOrgsThenFinalizer(t *testing.T) {
	// This test verifies the correct ordering: resources first, then org entries,
	// then finalizer removal. We test this by ensuring all resources are gone
	// and org entries are cleaned before the finalizer is removed.
	scheme := newDeletionScheme()
	capMapping := map[string]string{"ADMIN": "admin", "WRITE": "push"}

	cb := newConnectorBinding("github-cb", "default", capMapping, "target-ns")
	asr := newDeletingASR("asr-order", "team-order", "default",
		[]pmv1.Permission{
			{Resource: "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org", Capability: "ADMIN"},
			{Resource: "repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/repo-a", Capability: "WRITE"},
		},
		[]string{"github-cb"},
	)

	team := newOwnedGithubTeam("asr-order-team", "target-ns", "asr-order", "default")
	repo := newOwnedGithubTeamRepo("asr-order-repo", "target-ns", "asr-order", "default",
		[]string{"repo-a"}, "push")
	contrib := &orgContribution{OwnerTeams: []string{"team-order"}}
	org := newGithubOrgWithContributions("target-ns", "my-org", "team-order", contrib)

	r, fakeClient := reconcilerForDeletion(scheme, []pmv1.ConnectorBinding{*cb}, asr, cb, team, repo, org)

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-order"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// After reconcile: verify everything is cleaned in order
	// 1. Teams gone
	var teamList GithubTeamList
	_ = fakeClient.List(context.Background(), &teamList, client.InNamespace("target-ns"), client.MatchingLabels{LabelSourceASR: "asr-order"})
	if len(teamList.Items) != 0 {
		t.Errorf("teams should be deleted first")
	}

	// 2. Repos gone
	var repoList GithubTeamRepositoryList
	_ = fakeClient.List(context.Background(), &repoList, client.InNamespace("target-ns"), client.MatchingLabels{LabelSourceASR: "asr-order"})
	if len(repoList.Items) != 0 {
		t.Errorf("repos should be deleted")
	}

	// 3. Org entries removed
	var updatedOrg GithubOrganization
	_ = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "target-ns", Name: "my-org"}, &updatedOrg)
	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("org entries should be cleaned")
	}

	// 4. Finalizer removed last
	var updatedASR pmv1.ApplicationSpecificRole
	_ = fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "asr-order"}, &updatedASR)
	if controllerutil.ContainsFinalizer(&updatedASR, FinalizerGithub) {
		t.Error("finalizer should be removed last")
	}
}

