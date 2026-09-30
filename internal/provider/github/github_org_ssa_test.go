// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
)

// unstructuredFromApply recovers the *unstructured.Unstructured that
// ApplyConfigurationFromUnstructured wrapped, so tests can inspect the SSA payload.
func unstructuredFromApply(obj runtime.ApplyConfiguration) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	if c, ok := obj.(interface{ UnstructuredContent() map[string]any }); ok {
		u.Object = c.UnstructuredContent()
	}
	return u
}

// orgWithIdentity returns an org with the Helm-owned identity fields set.
func orgWithIdentity() *repoguardv1.GithubOrganization {
	org := newGithubOrg("test-ns", "my-org")
	org.Spec.Github = "github.example.com"
	org.Spec.Organization = "my-org"
	org.Spec.InstallationID = 42
	org.Spec.ProtectedMembers = []string{"bot-account"}
	return org
}

// TestPatchOrganization_UsesServerSideApplyWithoutIdentityFields asserts the write is an
// SSA under the ASR's per-ASR field manager carrying only the team-list fields.
func TestPatchOrganization_UsesServerSideApplyWithoutIdentityFields(t *testing.T) {
	var (
		gotOpts client.ApplyOptions
		gotObj  *unstructured.Unstructured
	)

	base := fake.NewClientBuilder().WithScheme(newScheme()).WithReturnManagedFields().
		WithObjects(orgWithIdentity()).Build()
	c := interceptor.NewClient(base, interceptor.Funcs{
		Apply: func(ctx context.Context, cl client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			gotOpts = client.ApplyOptions{}
			gotOpts.ApplyOptions(opts)
			gotObj = unstructuredFromApply(obj).DeepCopy()
			return cl.Apply(ctx, obj, opts...)
		},
	})
	p := New(c, audit.Disabled(), logr.Discard())

	contrib := &orgContribution{OwnerTeams: []string{"team-x"}}
	if err := p.patchOrganization(context.Background(), "default", "asr-x", "my-org", "test-ns", contrib); err != nil {
		t.Fatalf("patchOrganization: %v", err)
	}

	// SSA is used (typed Apply), under this ASR's per-ASR field manager, with force ownership.
	if gotOpts.FieldManager != orgFieldManager("default", "asr-x") {
		t.Errorf("field manager = %q, want %q", gotOpts.FieldManager, orgFieldManager("default", "asr-x"))
	}
	if gotOpts.Force == nil || !*gotOpts.Force {
		t.Errorf("force ownership = %v, want true", gotOpts.Force)
	}

	// The applied object carries the team-list fields but no identity fields or status.
	u := gotObj
	spec, found, err := unstructured.NestedMap(u.Object, "spec")
	if err != nil || !found {
		t.Fatalf("apply payload has no spec (found=%v err=%v)", found, err)
	}
	for _, want := range []string{
		"organizationOwnerTeams",
		"defaultPublicRepositoryTeams",
		"defaultPrivateRepositoryTeams",
	} {
		if _, ok := spec[want]; !ok {
			t.Errorf("apply payload spec is missing team-list field %q", want)
		}
	}
	for _, forbidden := range []string{"github", "organization", "installationID", "protectedMembers"} {
		if _, ok := spec[forbidden]; ok {
			t.Errorf("apply payload must not include identity field spec.%s", forbidden)
		}
	}
	if _, ok := u.Object["status"]; ok {
		t.Error("apply payload must not include status")
	}
	if len(u.GetAnnotations()) != 0 {
		t.Errorf("apply payload must not carry annotations, got %v", u.GetAnnotations())
	}

	// The identity fields survive on the stored object (Helm keeps ownership).
	var stored repoguardv1.GithubOrganization
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &stored); err != nil {
		t.Fatalf("get org: %v", err)
	}
	if stored.Spec.Github != "github.example.com" || stored.Spec.InstallationID != 42 ||
		len(stored.Spec.ProtectedMembers) != 1 || stored.Spec.ProtectedMembers[0] != "bot-account" {
		t.Errorf("identity fields were clobbered: %+v", stored.Spec)
	}
	if len(stored.Spec.OrganizationOwnerTeams) != 1 || stored.Spec.OrganizationOwnerTeams[0] != "team-x" {
		t.Errorf("owner team not applied: %v", stored.Spec.OrganizationOwnerTeams)
	}
}

// capturedApply records the field manager and spec of one intercepted SSA apply.
type capturedApply struct {
	fieldManager string
	spec         map[string]any
}

// TestPatchOrganization_AppliesOnlyOwnEntriesPerASRManager asserts each ASR applies under
// its own field manager, sending only its own entries (cross-manager merge covered by e2e).
func TestPatchOrganization_AppliesOnlyOwnEntriesPerASRManager(t *testing.T) {
	var applies []capturedApply
	base := fake.NewClientBuilder().WithScheme(newScheme()).WithReturnManagedFields().
		WithObjects(orgWithIdentity()).Build()
	c := interceptor.NewClient(base, interceptor.Funcs{
		Apply: func(ctx context.Context, cl client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			po := client.ApplyOptions{}
			po.ApplyOptions(opts)
			u := unstructuredFromApply(obj)
			spec, _, _ := unstructured.NestedMap(u.Object, "spec")
			applies = append(applies, capturedApply{fieldManager: po.FieldManager, spec: spec})
			return cl.Apply(ctx, obj, opts...)
		},
	})
	p := New(c, audit.Disabled(), logr.Discard())

	if err := p.patchOrganization(context.Background(), "default", "asr-one", "my-org", "test-ns",
		&orgContribution{OwnerTeams: []string{"team-one"}}); err != nil {
		t.Fatalf("apply team-one: %v", err)
	}
	if err := p.patchOrganization(context.Background(), "default", "asr-two", "my-org", "test-ns",
		&orgContribution{PublicTeams: []repoguardv1.GithubTeamWithPermission{{Team: "team-two", Permission: "push"}}}); err != nil {
		t.Fatalf("apply team-two: %v", err)
	}

	if len(applies) != 2 {
		t.Fatalf("expected 2 applies, got %d", len(applies))
	}

	// team-one: owns the org-owner entry, contributes no public teams.
	one := applies[0]
	if one.fieldManager != orgFieldManager("default", "asr-one") {
		t.Errorf("first apply field manager = %q, want %q", one.fieldManager, orgFieldManager("default", "asr-one"))
	}
	if owners, _, _ := unstructured.NestedSlice(one.spec, "organizationOwnerTeams"); len(owners) != 1 || owners[0] != "team-one" {
		t.Errorf("team-one apply owner teams = %v, want [team-one]", owners)
	}
	if pub, _, _ := unstructured.NestedSlice(one.spec, "defaultPublicRepositoryTeams"); len(pub) != 0 {
		t.Errorf("team-one apply must not carry public teams, got %v", pub)
	}

	// team-two: contributes a public team only, claims no owner entry.
	two := applies[1]
	if two.fieldManager != orgFieldManager("default", "asr-two") {
		t.Errorf("second apply field manager = %q, want %q", two.fieldManager, orgFieldManager("default", "asr-two"))
	}
	if owners, _, _ := unstructured.NestedSlice(two.spec, "organizationOwnerTeams"); len(owners) != 0 {
		t.Errorf("team-two apply must not carry owner teams, got %v", owners)
	}
	pub, _, _ := unstructured.NestedSlice(two.spec, "defaultPublicRepositoryTeams")
	if len(pub) != 1 {
		t.Fatalf("team-two apply public teams = %v, want one entry", pub)
	}
	if entry, ok := pub[0].(map[string]any); !ok || entry["team"] != "team-two" || entry["permission"] != "push" {
		t.Errorf("team-two apply public entry = %v, want {team-two push}", pub[0])
	}
}

// TestReconcileOrganizations_FieldManagerKeyedByASRObject asserts ASRs sharing spec.Name
// still apply under distinct field managers (keyed by namespace/name).
func TestReconcileOrganizations_FieldManagerKeyedByASRObject(t *testing.T) {
	mapping := map[string]string{"ADMIN": "admin"}
	var managers []string
	base := fake.NewClientBuilder().WithScheme(newScheme()).WithReturnManagedFields().
		WithObjects(orgWithIdentity()).Build()
	c := interceptor.NewClient(base, interceptor.Funcs{
		Apply: func(ctx context.Context, cl client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			po := client.ApplyOptions{}
			po.ApplyOptions(opts)
			managers = append(managers, po.FieldManager)
			return cl.Apply(ctx, obj, opts...)
		},
	})
	p := New(c, audit.Disabled(), logr.Discard())

	// Two distinct ASR objects (asr-a, asr-b) sharing spec.Name "shared".
	orgAdmin := []v1alpha1.Permission{perm(orgCCRN("my-org"), "ADMIN")}
	for _, asr := range []*v1alpha1.ApplicationSpecificRole{
		newASR("asr-a", "shared", orgAdmin),
		newASR("asr-b", "shared", orgAdmin),
	} {
		if _, err := p.reconcileOrganizations(context.Background(), asr, connectorDef(mapping), "test-ns"); err != nil {
			t.Fatalf("reconcileOrganizations %s: %v", asr.Name, err)
		}
	}

	if len(managers) != 2 {
		t.Fatalf("expected 2 applies, got %d", len(managers))
	}
	if managers[0] == managers[1] {
		t.Fatalf("ASRs sharing spec.Name must apply under distinct field managers, both = %q", managers[0])
	}
	if managers[0] != orgFieldManager("default", "asr-a") || managers[1] != orgFieldManager("default", "asr-b") {
		t.Errorf("field managers = %v, want [%s %s]", managers,
			orgFieldManager("default", "asr-a"), orgFieldManager("default", "asr-b"))
	}
}

// TestOrgFieldManager_BoundedStableCollisionResistant asserts the manager stays <=128 bytes,
// is stable per ASR, and differs across ASRs.
func TestOrgFieldManager_BoundedStableCollisionResistant(t *testing.T) {
	longNS, longName := strings.Repeat("n", 63), strings.Repeat("a", 253)
	got := orgFieldManager(longNS, longName)
	if len(got) > fieldManagerMaxLen {
		t.Errorf("field manager is %d bytes, want <= %d", len(got), fieldManagerMaxLen)
	}
	if got != orgFieldManager(longNS, longName) {
		t.Error("field manager must be stable for the same ASR")
	}
	if got == orgFieldManager(longNS, longName+"x") {
		t.Error("distinct ASRs must not collide")
	}
	if orgFieldManager("default", "asr-x") != "permission-manager/default/asr-x" {
		t.Error("short ns/name should keep the readable form")
	}
}

// TestReconcileOrganizations_ReleasesOrgsDroppedFromDesiredState asserts an ASR that no
// longer contributes to a managed org releases its entries via an empty SSA apply.
func TestReconcileOrganizations_ReleasesOrgsDroppedFromDesiredState(t *testing.T) {
	base := newClient()
	if err := base.Create(context.Background(), orgWithIdentity()); err != nil {
		t.Fatalf("create org: %v", err)
	}
	seed := New(base, audit.Disabled(), logr.Discard())
	if err := seed.patchOrganization(context.Background(), "default", "asr-x", "my-org", "test-ns",
		&orgContribution{OwnerTeams: []string{"team-x"}}); err != nil {
		t.Fatalf("seed owner entry: %v", err)
	}

	var applies []capturedApply
	c := interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
		Apply: func(ctx context.Context, cl client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			po := client.ApplyOptions{}
			po.ApplyOptions(opts)
			u := unstructuredFromApply(obj)
			spec, _, _ := unstructured.NestedMap(u.Object, "spec")
			applies = append(applies, capturedApply{fieldManager: po.FieldManager, spec: spec})
			return cl.Apply(ctx, obj, opts...)
		},
	})
	p := New(c, audit.Disabled(), logr.Discard())

	// asr-x now has no org-level permissions.
	asr := newASR("asr-x", "shared", nil)
	if _, err := p.reconcileOrganizations(context.Background(), asr, connectorDef(map[string]string{"ADMIN": "admin"}), "test-ns"); err != nil {
		t.Fatalf("reconcileOrganizations: %v", err)
	}

	if len(applies) != 1 {
		t.Fatalf("expected one release apply, got %d: %+v", len(applies), applies)
	}
	if applies[0].fieldManager != orgFieldManager("default", "asr-x") {
		t.Errorf("release manager = %q, want %q", applies[0].fieldManager, orgFieldManager("default", "asr-x"))
	}
	if owners, _, _ := unstructured.NestedSlice(applies[0].spec, "organizationOwnerTeams"); len(owners) != 0 {
		t.Errorf("release apply must empty owner teams, got %v", owners)
	}
	var stored repoguardv1.GithubOrganization
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &stored); err != nil {
		t.Fatalf("get org: %v", err)
	}
	if len(stored.Spec.OrganizationOwnerTeams) != 0 {
		t.Errorf("owner teams not released: %v", stored.Spec.OrganizationOwnerTeams)
	}
}
