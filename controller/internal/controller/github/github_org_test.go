// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
)

// helper to build a ConnectorBinding definition JSON with capability mapping.
func buildConnectorDef(mapping map[string]string) *runtime.RawExtension {
	def := GithubConnectorDefinition{
		CapabilityMapping:     mapping,
		DefaultTargetNamespace: "test-ns",
	}
	raw, _ := json.Marshal(def)
	return &runtime.RawExtension{Raw: raw}
}

// helper to create a test scheme with all required types.
func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = pmv1.AddToScheme(scheme)
	_ = AddToScheme(scheme)
	return scheme
}

// helper to create a GithubOrganization CR.
func newGithubOrg(namespace, name string) *GithubOrganization {
	return &GithubOrganization{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "GithubOrganization",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: GithubOrganizationSpec{},
	}
}

// helper to create an ASR with github connector ref.
func newASR(name, specName string, permissions []pmv1.Permission) *pmv1.ApplicationSpecificRole {
	return &pmv1.ApplicationSpecificRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "permission-manager.cloudoperators.dev/v1",
			Kind:       "ApplicationSpecificRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          specName,
			ConnectorRefs: []string{"github-cb"},
		},
	}
}

func TestReconcileOrganizations_OrgCCRN_AdminAddsOwnerTeam(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"ADMIN": "admin",
		"WRITE": "push",
		"READ":  "pull",
	}

	org := newGithubOrg("test-ns", "my-org")
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	asr := newASR("asr-1", "team-alpha", []pmv1.Permission{
		{
			Resource:   "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org",
			Capability: "ADMIN",
		},
	})
	asr.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org",
			Capability: "ADMIN",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(org, cb, asr).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	cbDef, _ := ParseConnectorDefinition(cb.Spec.Definition)
	result, err := r.reconcileOrganizations(context.Background(), asr, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("unexpected requeue: %v", result)
	}

	// Verify the org was patched
	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 1 {
		t.Fatalf("expected 1 owner team, got %d", len(updatedOrg.Spec.OrganizationOwnerTeams))
	}
	if updatedOrg.Spec.OrganizationOwnerTeams[0] != "team-alpha" {
		t.Errorf("expected owner team 'team-alpha', got %q", updatedOrg.Spec.OrganizationOwnerTeams[0])
	}

	// Verify annotations
	if updatedOrg.Annotations == nil {
		t.Fatal("expected annotations to be set")
	}
	if updatedOrg.Annotations[AnnotationASRManagedConfig] != "team-alpha" {
		t.Errorf("expected managed config annotation 'team-alpha', got %q", updatedOrg.Annotations[AnnotationASRManagedConfig])
	}
}

func TestReconcileOrganizations_OrgCCRN_NonAdminDoesNotAddOwnerTeam(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"ADMIN": "admin",
		"WRITE": "push",
		"READ":  "pull",
	}

	org := newGithubOrg("test-ns", "my-org")
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	asr := newASR("asr-1", "team-beta", nil)
	asr.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org",
			Capability: "WRITE",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(org, cb, asr).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	cbDef, _ := ParseConnectorDefinition(cb.Spec.Definition)
	result, err := r.reconcileOrganizations(context.Background(), asr, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("unexpected requeue: %v", result)
	}

	// Verify the org was NOT patched (no org-level changes for non-admin)
	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("expected 0 owner teams for non-admin, got %d", len(updatedOrg.Spec.OrganizationOwnerTeams))
	}
}

func TestReconcileOrganizations_WildcardPublicPatchesPublicTeams(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"WRITE": "push",
		"READ":  "pull",
	}

	org := newGithubOrg("test-ns", "my-org")
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	asr := newASR("asr-1", "team-gamma", nil)
	asr.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/public",
			Capability: "WRITE",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(org, cb, asr).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	cbDef, _ := ParseConnectorDefinition(cb.Spec.Definition)
	result, err := r.reconcileOrganizations(context.Background(), asr, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("unexpected requeue: %v", result)
	}

	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 1 {
		t.Fatalf("expected 1 public team, got %d", len(updatedOrg.Spec.DefaultPublicRepositoryTeams))
	}
	if updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Name != "team-gamma" {
		t.Errorf("expected public team 'team-gamma', got %q", updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Name)
	}
	if updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Permission != "push" {
		t.Errorf("expected permission 'push', got %q", updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Permission)
	}
	if len(updatedOrg.Spec.DefaultPrivateRepositoryTeams) != 0 {
		t.Errorf("expected 0 private teams, got %d", len(updatedOrg.Spec.DefaultPrivateRepositoryTeams))
	}
}

func TestReconcileOrganizations_WildcardPrivatePatchesPrivateTeams(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"READ": "pull",
	}

	org := newGithubOrg("test-ns", "my-org")
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	asr := newASR("asr-1", "team-delta", nil)
	asr.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/private",
			Capability: "READ",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(org, cb, asr).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	cbDef, _ := ParseConnectorDefinition(cb.Spec.Definition)
	result, err := r.reconcileOrganizations(context.Background(), asr, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("unexpected requeue: %v", result)
	}

	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	if len(updatedOrg.Spec.DefaultPrivateRepositoryTeams) != 1 {
		t.Fatalf("expected 1 private team, got %d", len(updatedOrg.Spec.DefaultPrivateRepositoryTeams))
	}
	if updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Name != "team-delta" {
		t.Errorf("expected private team 'team-delta', got %q", updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Name)
	}
	if updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Permission != "pull" {
		t.Errorf("expected permission 'pull', got %q", updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Permission)
	}
	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 0 {
		t.Errorf("expected 0 public teams, got %d", len(updatedOrg.Spec.DefaultPublicRepositoryTeams))
	}
}

func TestReconcileOrganizations_WildcardStarPatchesBoth(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"WRITE": "push",
	}

	org := newGithubOrg("test-ns", "my-org")
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	asr := newASR("asr-1", "team-epsilon", nil)
	asr.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/*",
			Capability: "WRITE",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(org, cb, asr).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	cbDef, _ := ParseConnectorDefinition(cb.Spec.Definition)
	result, err := r.reconcileOrganizations(context.Background(), asr, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("unexpected requeue: %v", result)
	}

	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 1 {
		t.Fatalf("expected 1 public team, got %d", len(updatedOrg.Spec.DefaultPublicRepositoryTeams))
	}
	if updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Name != "team-epsilon" {
		t.Errorf("expected public team 'team-epsilon', got %q", updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Name)
	}

	if len(updatedOrg.Spec.DefaultPrivateRepositoryTeams) != 1 {
		t.Fatalf("expected 1 private team, got %d", len(updatedOrg.Spec.DefaultPrivateRepositoryTeams))
	}
	if updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Name != "team-epsilon" {
		t.Errorf("expected private team 'team-epsilon', got %q", updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Name)
	}
}

func TestReconcileOrganizations_AnnotationTracking(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"ADMIN": "admin",
		"WRITE": "push",
	}

	org := newGithubOrg("test-ns", "my-org")
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	asr := newASR("asr-1", "team-zeta", nil)
	asr.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org",
			Capability: "ADMIN",
		},
		{
			Resource:   "repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/public",
			Capability: "WRITE",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(org, cb, asr).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	cbDef, _ := ParseConnectorDefinition(cb.Spec.Definition)
	_, err := r.reconcileOrganizations(context.Background(), asr, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations failed: %v", err)
	}

	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	// Check managed config annotation
	managedConfig := updatedOrg.Annotations[AnnotationASRManagedConfig]
	if managedConfig != "team-zeta" {
		t.Errorf("expected managed config 'team-zeta', got %q", managedConfig)
	}

	// Check per-ASR annotation exists and is valid JSON
	perASRAnnotation := updatedOrg.Annotations[annotationASRDefaultTeamsPrefix+"team-zeta"]
	if perASRAnnotation == "" {
		t.Fatal("expected per-ASR annotation to be set")
	}

	var contrib orgContribution
	if err := json.Unmarshal([]byte(perASRAnnotation), &contrib); err != nil {
		t.Fatalf("failed to unmarshal per-ASR annotation: %v", err)
	}

	if len(contrib.OwnerTeams) != 1 || contrib.OwnerTeams[0] != "team-zeta" {
		t.Errorf("expected owner teams ['team-zeta'], got %v", contrib.OwnerTeams)
	}
	if len(contrib.PublicTeams) != 1 || contrib.PublicTeams[0].Name != "team-zeta" {
		t.Errorf("expected public teams with 'team-zeta', got %v", contrib.PublicTeams)
	}
}

func TestReconcileOrganizations_StaleEntryRemoval(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"ADMIN": "admin",
		"WRITE": "push",
		"READ":  "pull",
	}

	// Pre-populate the org with a previous contribution from the same ASR
	prevContrib := orgContribution{
		OwnerTeams:  []string{"team-eta"},
		PublicTeams: []GithubTeamWithPermission{{Name: "team-eta", Permission: "push"}},
	}
	prevContribJSON, _ := json.Marshal(prevContrib)

	org := newGithubOrg("test-ns", "my-org")
	org.Annotations = map[string]string{
		AnnotationASRManagedConfig:                "team-eta",
		annotationASRDefaultTeamsPrefix + "team-eta": string(prevContribJSON),
	}
	org.Spec.OrganizationOwnerTeams = []string{"team-eta"}
	org.Spec.DefaultPublicRepositoryTeams = []GithubTeamWithPermission{{Name: "team-eta", Permission: "push"}}

	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	// New permissions: only READ on private repos (no more admin/push on public)
	asr := newASR("asr-1", "team-eta", nil)
	asr.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/private",
			Capability: "READ",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(org, cb, asr).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	cbDef, _ := ParseConnectorDefinition(cb.Spec.Definition)
	_, err := r.reconcileOrganizations(context.Background(), asr, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations failed: %v", err)
	}

	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	// Old owner teams should be removed
	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("expected 0 owner teams after permission change, got %d: %v", len(updatedOrg.Spec.OrganizationOwnerTeams), updatedOrg.Spec.OrganizationOwnerTeams)
	}

	// Old public teams should be removed
	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 0 {
		t.Errorf("expected 0 public teams after permission change, got %d", len(updatedOrg.Spec.DefaultPublicRepositoryTeams))
	}

	// New private teams should be added
	if len(updatedOrg.Spec.DefaultPrivateRepositoryTeams) != 1 {
		t.Fatalf("expected 1 private team, got %d", len(updatedOrg.Spec.DefaultPrivateRepositoryTeams))
	}
	if updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Name != "team-eta" {
		t.Errorf("expected private team 'team-eta', got %q", updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Name)
	}
	if updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Permission != "pull" {
		t.Errorf("expected permission 'pull', got %q", updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Permission)
	}
}

func TestReconcileOrganizations_MultipleASRsNoConflict(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"ADMIN": "admin",
		"WRITE": "push",
	}

	org := newGithubOrg("test-ns", "my-org")
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	asr1 := newASR("asr-1", "team-one", nil)
	asr1.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org",
			Capability: "ADMIN",
		},
	}

	asr2 := newASR("asr-2", "team-two", nil)
	asr2.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/public",
			Capability: "WRITE",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(org, cb, asr1, asr2).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	cbDef, _ := ParseConnectorDefinition(cb.Spec.Definition)

	// Reconcile ASR 1
	_, err := r.reconcileOrganizations(context.Background(), asr1, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations for asr-1 failed: %v", err)
	}

	// Reconcile ASR 2
	_, err = r.reconcileOrganizations(context.Background(), asr2, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations for asr-2 failed: %v", err)
	}

	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	// team-one should be in owner teams
	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 1 || updatedOrg.Spec.OrganizationOwnerTeams[0] != "team-one" {
		t.Errorf("expected owner teams ['team-one'], got %v", updatedOrg.Spec.OrganizationOwnerTeams)
	}

	// team-two should be in public teams
	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 1 || updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Name != "team-two" {
		t.Errorf("expected public teams ['team-two'], got %v", updatedOrg.Spec.DefaultPublicRepositoryTeams)
	}

	// Check managed config annotation has both
	managedConfig := updatedOrg.Annotations[AnnotationASRManagedConfig]
	if managedConfig != "team-one,team-two" {
		t.Errorf("expected managed config 'team-one,team-two', got %q", managedConfig)
	}
}

func TestReconcileOrganizations_MissingOrgRequeues(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"ADMIN": "admin",
	}

	// No GithubOrganization created - it's missing
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	asr := newASR("asr-1", "team-missing", nil)
	asr.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/missing-org",
			Capability: "ADMIN",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cb, asr).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	cbDef, _ := ParseConnectorDefinition(cb.Spec.Definition)
	result, err := r.reconcileOrganizations(context.Background(), asr, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("expected no error for missing org, got: %v", err)
	}
	if result.RequeueAfter != DefaultOrgRequeueDelay {
		t.Errorf("expected requeue after %v, got %v", DefaultOrgRequeueDelay, result.RequeueAfter)
	}
}

func TestReconcileOrganizations_NonGithubCCRNSkipped(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"ADMIN": "admin",
	}

	org := newGithubOrg("test-ns", "my-org")
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	asr := newASR("asr-1", "team-k8s", nil)
	asr.Spec.Permissions = []pmv1.Permission{
		{
			// This is a k8s CCRN, not a github one
			Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/my-cluster",
			Capability: "ADMIN",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(org, cb, asr).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	cbDef, _ := ParseConnectorDefinition(cb.Spec.Definition)
	result, err := r.reconcileOrganizations(context.Background(), asr, cbDef, "test-ns")
	if err != nil {
		t.Fatalf("reconcileOrganizations failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("unexpected requeue: %v", result)
	}

	// Org should remain unchanged
	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}
	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("expected 0 owner teams, got %d", len(updatedOrg.Spec.OrganizationOwnerTeams))
	}
}

func TestFullReconcile_EndToEnd(t *testing.T) {
	scheme := testScheme()
	capMapping := map[string]string{
		"ADMIN": "admin",
		"WRITE": "push",
	}

	org := newGithubOrg("test-ns", "my-org")
	cb := &pmv1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: pmv1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   ProviderGithub,
			Definition: buildConnectorDef(capMapping),
		},
	}

	asr := newASR("asr-e2e", "team-full", nil)
	asr.Spec.Permissions = []pmv1.Permission{
		{
			Resource:   "organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org",
			Capability: "ADMIN",
		},
		{
			Resource:   "repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/*",
			Capability: "WRITE",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(org, cb, asr).
		WithStatusSubresource(&pmv1.ApplicationSpecificRole{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if patch.Type() == types.ApplyPatchType {
					// SSA not supported in fake client; accept silently for testing.
					return nil
				}
				return c.Status().Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	indexer := NewGithubIndexer(logr.Discard())
	indexer.Sync([]pmv1.ConnectorBinding{*cb})

	r := &GithubReconciler{
		Client:   fakeClient,
		Log:      logr.Discard(),
		CBIndex:  indexer,
		AuditLog: audit.Disabled(),
	}

	// Run a full reconcile
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "asr-e2e"},
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("unexpected requeue: %v", result)
	}

	// Verify org was patched
	var updatedOrg GithubOrganization
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &updatedOrg); err != nil {
		t.Fatalf("failed to get org: %v", err)
	}

	if len(updatedOrg.Spec.OrganizationOwnerTeams) != 1 || updatedOrg.Spec.OrganizationOwnerTeams[0] != "team-full" {
		t.Errorf("expected owner teams ['team-full'], got %v", updatedOrg.Spec.OrganizationOwnerTeams)
	}
	if len(updatedOrg.Spec.DefaultPublicRepositoryTeams) != 1 {
		t.Fatalf("expected 1 public team, got %d", len(updatedOrg.Spec.DefaultPublicRepositoryTeams))
	}
	if updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Name != "team-full" || updatedOrg.Spec.DefaultPublicRepositoryTeams[0].Permission != "push" {
		t.Errorf("unexpected public team: %+v", updatedOrg.Spec.DefaultPublicRepositoryTeams[0])
	}
	if len(updatedOrg.Spec.DefaultPrivateRepositoryTeams) != 1 {
		t.Fatalf("expected 1 private team, got %d", len(updatedOrg.Spec.DefaultPrivateRepositoryTeams))
	}
	if updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Name != "team-full" || updatedOrg.Spec.DefaultPrivateRepositoryTeams[0].Permission != "push" {
		t.Errorf("unexpected private team: %+v", updatedOrg.Spec.DefaultPrivateRepositoryTeams[0])
	}

	// Verify finalizer was added
	var updatedASR pmv1.ApplicationSpecificRole
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "asr-e2e"}, &updatedASR); err != nil {
		t.Fatalf("failed to get ASR: %v", err)
	}
	hasFinalizer := false
	for _, f := range updatedASR.Finalizers {
		if f == FinalizerGithub {
			hasFinalizer = true
			break
		}
	}
	if !hasFinalizer {
		t.Error("expected github finalizer on ASR")
	}
}

func TestRemoveASRContributions_CleanupOnDeletion(t *testing.T) {
	// Test that removeASRContributions correctly removes entries
	prevContrib := orgContribution{
		OwnerTeams:   []string{"team-cleanup"},
		PublicTeams:  []GithubTeamWithPermission{{Name: "team-cleanup", Permission: "push"}},
		PrivateTeams: []GithubTeamWithPermission{{Name: "team-cleanup", Permission: "pull"}},
	}
	prevContribJSON, _ := json.Marshal(prevContrib)

	org := &GithubOrganization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-org",
			Namespace: "test-ns",
			Annotations: map[string]string{
				AnnotationASRManagedConfig:                       "other-team,team-cleanup",
				annotationASRDefaultTeamsPrefix + "team-cleanup": string(prevContribJSON),
				annotationASRDefaultTeamsPrefix + "other-team":   `{"publicTeams":[{"name":"other-team","permission":"admin"}]}`,
			},
		},
		Spec: GithubOrganizationSpec{
			OrganizationOwnerTeams: []string{"team-cleanup"},
			DefaultPublicRepositoryTeams: []GithubTeamWithPermission{
				{Name: "team-cleanup", Permission: "push"},
				{Name: "other-team", Permission: "admin"},
			},
			DefaultPrivateRepositoryTeams: []GithubTeamWithPermission{
				{Name: "team-cleanup", Permission: "pull"},
			},
		},
	}

	removeASRContributions(org, "team-cleanup")
	removeASRFromAnnotations(org, "team-cleanup")

	// Owner teams should be empty
	if len(org.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("expected 0 owner teams, got %v", org.Spec.OrganizationOwnerTeams)
	}

	// Public teams should only have "other-team"
	if len(org.Spec.DefaultPublicRepositoryTeams) != 1 {
		t.Fatalf("expected 1 public team remaining, got %d", len(org.Spec.DefaultPublicRepositoryTeams))
	}
	if org.Spec.DefaultPublicRepositoryTeams[0].Name != "other-team" {
		t.Errorf("expected 'other-team' remaining, got %q", org.Spec.DefaultPublicRepositoryTeams[0].Name)
	}

	// Private teams should be empty
	if len(org.Spec.DefaultPrivateRepositoryTeams) != 0 {
		t.Errorf("expected 0 private teams, got %v", org.Spec.DefaultPrivateRepositoryTeams)
	}

	// Annotation should only have other-team
	managedConfig := org.Annotations[AnnotationASRManagedConfig]
	if managedConfig != "other-team" {
		t.Errorf("expected managed config 'other-team', got %q", managedConfig)
	}

	// Per-ASR annotation for team-cleanup should be removed
	if _, ok := org.Annotations[annotationASRDefaultTeamsPrefix+"team-cleanup"]; ok {
		t.Error("expected per-ASR annotation to be removed")
	}

	// Per-ASR annotation for other-team should still be there
	if _, ok := org.Annotations[annotationASRDefaultTeamsPrefix+"other-team"]; !ok {
		t.Error("expected per-ASR annotation for other-team to remain")
	}
}

func TestHelpers_AppendUniqueString(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		item     string
		expected int
	}{
		{"empty slice", nil, "a", 1},
		{"new item", []string{"a"}, "b", 2},
		{"duplicate item", []string{"a", "b"}, "a", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := appendUniqueString(tt.slice, tt.item)
			if len(result) != tt.expected {
				t.Errorf("expected length %d, got %d", tt.expected, len(result))
			}
		})
	}
}

func TestHelpers_AppendUniqueTeam(t *testing.T) {
	existing := []GithubTeamWithPermission{
		{Name: "team-a", Permission: "push"},
	}

	// Different team name
	result := appendUniqueTeam(existing, GithubTeamWithPermission{Name: "team-b", Permission: "push"})
	if len(result) != 2 {
		t.Errorf("expected 2 teams, got %d", len(result))
	}

	// Same team, same permission (duplicate)
	result = appendUniqueTeam(existing, GithubTeamWithPermission{Name: "team-a", Permission: "push"})
	if len(result) != 1 {
		t.Errorf("expected 1 team (no duplicate), got %d", len(result))
	}

	// Same team, different permission
	result = appendUniqueTeam(existing, GithubTeamWithPermission{Name: "team-a", Permission: "pull"})
	if len(result) != 2 {
		t.Errorf("expected 2 teams (different permission), got %d", len(result))
	}
}

func TestHelpers_RemoveString(t *testing.T) {
	result := removeString([]string{"a", "b", "c"}, "b")
	if len(result) != 2 {
		t.Errorf("expected 2 items, got %d", len(result))
	}
	for _, s := range result {
		if s == "b" {
			t.Error("'b' should have been removed")
		}
	}

	// Remove non-existent
	result = removeString([]string{"a", "b"}, "z")
	if len(result) != 2 {
		t.Errorf("expected 2 items (nothing removed), got %d", len(result))
	}
}

func TestHelpers_RemoveTeam(t *testing.T) {
	teams := []GithubTeamWithPermission{
		{Name: "team-a", Permission: "push"},
		{Name: "team-b", Permission: "pull"},
		{Name: "team-a", Permission: "admin"},
	}

	result := removeTeam(teams, GithubTeamWithPermission{Name: "team-a", Permission: "push"})
	if len(result) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(result))
	}
	// Should keep team-a with admin and team-b
	for _, team := range result {
		if team.Name == "team-a" && team.Permission == "push" {
			t.Error("team-a with push should have been removed")
		}
	}
}

// Suppress unused import warning - these are used in the test.
var _ = client.ObjectKey{}
