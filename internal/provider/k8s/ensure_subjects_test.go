// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"errors"
	"testing"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

const rbacGroup = "rbac.authorization.k8s.io"

// localScheme builds a scheme with the built-in and permission-manager types, avoiding mutation of
// the global client-go scheme.
func localScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientscheme.AddToScheme(s); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("v1alpha1 scheme: %v", err)
	}
	return s
}

func subjectIdentity(name string, connectorRefs, roles []string) *v1alpha1.ServiceIdentity {
	return &v1alpha1.ServiceIdentity{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       v1alpha1.ServiceIdentitySpec{Name: name, ConnectorRefs: connectorRefs, Roles: roles},
	}
}

// TestEnsureSubjects derives User subjects only from ServiceIdentities that reference the ASR and
// share one of its kubernetes connectors; deduped and sorted by username.
func TestEnsureSubjects(t *testing.T) {
	s := localScheme(t)

	grants := subjectIdentity("ci1", []string{"sci-k8s"}, []string{"my-asr"})          // references + shares connector
	otherConnector := subjectIdentity("ci2", []string{"other-cb"}, []string{"my-asr"}) // references but no shared connector
	otherRole := subjectIdentity("ci3", []string{"sci-k8s"}, []string{"different"})    // shares connector but different role

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(grants, otherConnector, otherRole).
		WithIndex(&v1alpha1.ServiceIdentity{}, pmapis.ServiceIdentityRolesField, func(obj client.Object) []string {
			return obj.(*v1alpha1.ServiceIdentity).Spec.Roles
		}).Build()

	p := &pmk8s.Phase{Client: c}
	p.SetCBs([]*v1alpha1.ConnectorBinding{{ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s", Namespace: "default"}}})

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr", Namespace: "default"},
		Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: "my-asr", ConnectorRefs: []string{"sci-k8s"}},
	}
	if _, err := p.EnsureSubjects(asr)(context.Background()); err != nil {
		t.Fatalf("EnsureSubjects: %v", err)
	}

	users := p.ExtraUsers()
	if len(users) != 1 || users[0].Kind != "User" || users[0].Name != "ci1" {
		t.Fatalf("extra users = %+v, want exactly [User ci1]", users)
	}
}

// TestEnsureSubjectsReconciled narrows subjects on an already-applied binding even when the ASR is
// InvalidConfig: a deleted ServiceIdentity's User is dropped, the Group is kept, and a user still
// granted is left in place. It never adds a user to a binding that lacks it.
func TestEnsureSubjectsReconciled(t *testing.T) {
	s := localScheme(t)

	labels := map[string]string{
		pmapis.LabelASR:             pmk8s.ASRLabelValue("my-asr"),
		pmapis.LabelSourceNamespace: "default",
	}
	// Previously applied: Group + two users. ci2's ServiceIdentity is now gone.
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "pm-a-my-asr", Labels: labels},
		Subjects: []rbacv1.Subject{
			{Kind: "Group", Name: "my-asr", APIGroup: rbacGroup},
			{Kind: "User", Name: "ci1", APIGroup: rbacGroup},
			{Kind: "User", Name: "ci2", APIGroup: rbacGroup},
		},
	}
	// A binding with only the Group — must not gain ci1 (never widen).
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "pm-a-my-asr", Namespace: "team-ns", Labels: labels},
		Subjects:   []rbacv1.Subject{{Kind: "Group", Name: "my-asr", APIGroup: rbacGroup}},
	}
	remote := fake.NewClientBuilder().WithScheme(s).WithObjects(crb, rb).Build()

	p := &pmk8s.Phase{
		Client:       fake.NewClientBuilder().WithScheme(s).Build(),
		RemoteClient: func(context.Context, client.Client, string, string) (client.Client, error) { return remote, nil },
		AuditLog:     audit.Disabled(),
	}
	p.SetExtraUsers([]rbacv1.Subject{{Kind: "User", Name: "ci1", APIGroup: rbacGroup}})

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr", Namespace: "default"},
		Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: "my-asr"},
	}
	asr.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderKubernetes: {Targets: []pmmeta.TargetStatus{{TargetID: "cluster-a"}}},
	}
	asr.SetCondition(pmmeta.TrueCondition(pmmeta.InvalidConfigCondition, pmmeta.InvalidCCRNReason, "bad ccrn"))

	if _, err := p.EnsureSubjectsReconciled(asr)(context.Background()); err != nil {
		t.Fatalf("EnsureSubjectsReconciled: %v", err)
	}

	var gotCRB rbacv1.ClusterRoleBinding
	if err := remote.Get(context.Background(), types.NamespacedName{Name: "pm-a-my-asr"}, &gotCRB); err != nil {
		t.Fatalf("get crb: %v", err)
	}
	if len(gotCRB.Subjects) != 2 || gotCRB.Subjects[0].Kind != "Group" || gotCRB.Subjects[1].Name != "ci1" {
		t.Errorf("crb subjects = %+v, want [Group my-asr, User ci1] (ci2 revoked)", gotCRB.Subjects)
	}

	var gotRB rbacv1.RoleBinding
	if err := remote.Get(context.Background(), types.NamespacedName{Name: "pm-a-my-asr", Namespace: "team-ns"}, &gotRB); err != nil {
		t.Fatalf("get rb: %v", err)
	}
	if len(gotRB.Subjects) != 1 || gotRB.Subjects[0].Kind != "Group" {
		t.Errorf("rb subjects = %+v, want [Group] (must not add ci1)", gotRB.Subjects)
	}
}

// TestChain_RevokeAfterInvalidConfig walks the ADR-019 §10 sequence at the phase level: an ASR that
// has gone InvalidCCRN (SI since deleted, so no live users) still (a) narrows the deleted user off
// its already-applied binding and (b) keeps its provider Targets so the cluster stays known.
func TestChain_RevokeAfterInvalidConfig(t *testing.T) {
	s := localScheme(t)
	labels := map[string]string{
		pmapis.LabelASR:             pmk8s.ASRLabelValue("my-asr"),
		pmapis.LabelSourceNamespace: "default",
	}
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "pm-a-my-asr", Labels: labels},
		Subjects: []rbacv1.Subject{
			{Kind: "Group", Name: "my-asr", APIGroup: rbacGroup},
			{Kind: "User", Name: "ci1", APIGroup: rbacGroup},
		},
	}
	remote := fake.NewClientBuilder().WithScheme(s).WithObjects(crb).Build()

	p := &pmk8s.Phase{
		Client:       fake.NewClientBuilder().WithScheme(s).Build(),
		RemoteClient: func(context.Context, client.Client, string, string) (client.Client, error) { return remote, nil },
		AuditLog:     audit.Disabled(),
	}
	// SI deleted -> no live users; ASR is InvalidCCRN this reconcile.
	p.SetExtraUsers(nil)
	p.SetInvalidCCRNs([]string{"urn:ccrn:...bad"})

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr", Namespace: "default"},
		Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: "my-asr"},
	}
	asr.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderKubernetes: {Targets: []pmmeta.TargetStatus{{TargetID: "cluster-a", Status: pmmeta.TargetStatusReady}}},
	}
	asr.SetCondition(pmmeta.TrueCondition(pmmeta.InvalidConfigCondition, pmmeta.InvalidCCRNReason, "bad ccrn"))

	if _, err := p.EnsureSubjectsReconciled(asr)(context.Background()); err != nil {
		t.Fatalf("EnsureSubjectsReconciled: %v", err)
	}
	if _, err := p.EnsureProviderStatus(asr)(context.Background()); err != nil {
		t.Fatalf("EnsureProviderStatus: %v", err)
	}

	// (a) the deleted identity's User is gone; the Group remains.
	var got rbacv1.ClusterRoleBinding
	if err := remote.Get(context.Background(), types.NamespacedName{Name: "pm-a-my-asr"}, &got); err != nil {
		t.Fatalf("get crb: %v", err)
	}
	if len(got.Subjects) != 1 || got.Subjects[0].Kind != "Group" {
		t.Errorf("crb subjects = %+v, want [Group] (ci1 revoked)", got.Subjects)
	}

	// (b) the provider Target is preserved despite the degraded (InvalidConfig) status.
	ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
	if ps == nil || ps.Ready {
		t.Fatalf("expected a degraded kubernetes provider status, got %+v", ps)
	}
	if pmmeta.GetTargetStatus(ps.Targets, "cluster-a") == nil {
		t.Errorf("target cluster-a must be preserved while InvalidConfig, got %+v", ps.Targets)
	}
}

// TestEnsureSubjectsReconciled_SkipsNotReadyCluster: a previously-targeted cluster that is now
// not Ready is skipped without a remote call, and status is still written.
func TestEnsureSubjectsReconciled_SkipsNotReadyCluster(t *testing.T) {
	s := localScheme(t)
	called := false
	p := &pmk8s.Phase{
		Client: fake.NewClientBuilder().WithScheme(s).Build(),
		RemoteClient: func(context.Context, client.Client, string, string) (client.Client, error) {
			called = true
			return nil, errors.New("should not be called")
		},
		AuditLog: audit.Disabled(),
	}
	p.SetNotReadyClusters([]string{"cluster-a"})

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr", Namespace: "default"},
		Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: "my-asr"},
	}
	asr.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderKubernetes: {Targets: []pmmeta.TargetStatus{{TargetID: "cluster-a", Status: pmmeta.TargetStatusReady}}},
	}

	if _, err := p.EnsureSubjectsReconciled(asr)(context.Background()); err != nil {
		t.Fatalf("EnsureSubjectsReconciled: %v", err)
	}
	if called {
		t.Error("must not call RemoteClient for a not-ready cluster")
	}
	if _, err := p.EnsureProviderStatus(asr)(context.Background()); err != nil {
		t.Fatalf("EnsureProviderStatus: %v", err)
	}
	ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
	if ps == nil || pmmeta.GetTargetStatus(ps.Targets, "cluster-a") == nil {
		t.Errorf("status should still be written for cluster-a, got %+v", ps)
	}
}

// TestEnsureSubjectsReconciled_PatchFailureSurfacesViaStatus: a failed subject patch does not abort
// the chain (EnsureSubjectsReconciled returns no error); the error surfaces from EnsureProviderStatus
// while status is still written.
func TestEnsureSubjectsReconciled_PatchFailureSurfacesViaStatus(t *testing.T) {
	s := localScheme(t)
	labels := map[string]string{pmapis.LabelASR: pmk8s.ASRLabelValue("my-asr"), pmapis.LabelSourceNamespace: "default"}
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "pm-a-my-asr", Labels: labels},
		Subjects: []rbacv1.Subject{
			{Kind: "Group", Name: "my-asr", APIGroup: rbacGroup},
			{Kind: "User", Name: "ci1", APIGroup: rbacGroup},
		},
	}
	remote := fake.NewClientBuilder().WithScheme(s).WithObjects(crb).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
				return errors.New("patch boom")
			},
		}).Build()

	p := &pmk8s.Phase{
		Client:       fake.NewClientBuilder().WithScheme(s).Build(),
		RemoteClient: func(context.Context, client.Client, string, string) (client.Client, error) { return remote, nil },
		AuditLog:     audit.Disabled(),
	}
	p.SetExtraUsers(nil) // all users revoked -> narrow wants to patch ci1 off

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr", Namespace: "default"},
		Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: "my-asr"},
	}
	asr.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderKubernetes: {Targets: []pmmeta.TargetStatus{{TargetID: "cluster-a", Status: pmmeta.TargetStatusReady}}},
	}

	if _, err := p.EnsureSubjectsReconciled(asr)(context.Background()); err != nil {
		t.Fatalf("EnsureSubjectsReconciled must not return an error (it aggregates), got %v", err)
	}
	if _, err := p.EnsureProviderStatus(asr)(context.Background()); err == nil {
		t.Error("expected EnsureProviderStatus to surface the patch failure")
	}
	ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
	if ps == nil || pmmeta.GetTargetStatus(ps.Targets, "cluster-a") == nil {
		t.Errorf("status should still be written, got %+v", ps)
	}
}

// TestEnsureSubjectsReconciled_SkipsNotReadyInStore: a previously-targeted cluster present in the
// store but not Ready is skipped without a remote call (same readiness check as compile).
func TestEnsureSubjectsReconciled_SkipsNotReadyInStore(t *testing.T) {
	s := localScheme(t)
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	// No Ready condition => not Ready.
	_ = store.Add(&greenhousev1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "default"}})

	called := false
	p := &pmk8s.Phase{
		Client:       fake.NewClientBuilder().WithScheme(s).Build(),
		ClusterStore: store,
		RemoteClient: func(context.Context, client.Client, string, string) (client.Client, error) {
			called = true
			return nil, errors.New("should not be called")
		},
		AuditLog: audit.Disabled(),
	}
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr", Namespace: "default"},
		Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: "my-asr"},
	}
	asr.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderKubernetes: {Targets: []pmmeta.TargetStatus{{TargetID: "cluster-a"}}},
	}
	if _, err := p.EnsureSubjectsReconciled(asr)(context.Background()); err != nil {
		t.Fatalf("EnsureSubjectsReconciled: %v", err)
	}
	if called {
		t.Error("must not call RemoteClient for a not-Ready cluster in the store")
	}
}

// TestEnsureRbacRemoved_DeletesRBACForDegradedASR: deleting an InvalidCCRN ASR whose degraded status
// kept its Targets still removes its RBAC on those clusters.
func TestEnsureRbacRemoved_DeletesRBACForDegradedASR(t *testing.T) {
	s := localScheme(t)
	labels := map[string]string{pmapis.LabelASR: pmk8s.ASRLabelValue("my-asr"), pmapis.LabelSourceNamespace: "default"}
	crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "pm-a-my-asr", Labels: labels}}
	remote := fake.NewClientBuilder().WithScheme(s).WithObjects(crb).Build()

	p := &pmk8s.Phase{
		Client:       fake.NewClientBuilder().WithScheme(s).Build(),
		RemoteClient: func(context.Context, client.Client, string, string) (client.Client, error) { return remote, nil },
		AuditLog:     audit.Disabled(),
	}
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "my-asr", Namespace: "default"},
		Spec:       v1alpha1.ApplicationSpecificRoleSpec{Name: "my-asr"},
	}
	asr.Status.Providers = map[pmmeta.ProviderType]pmmeta.ProviderStatus{
		pmmeta.ProviderKubernetes: {Targets: []pmmeta.TargetStatus{{TargetID: "cluster-a"}}},
	}
	asr.SetCondition(pmmeta.TrueCondition(pmmeta.InvalidConfigCondition, pmmeta.InvalidCCRNReason, "bad ccrn"))

	if _, err := p.EnsureRbacRemoved(asr)(context.Background()); err != nil {
		t.Fatalf("EnsureRbacRemoved: %v", err)
	}
	var got rbacv1.ClusterRoleBinding
	if err := remote.Get(context.Background(), types.NamespacedName{Name: "pm-a-my-asr"}, &got); err == nil {
		t.Error("RBAC should have been deleted for the degraded ASR's kept target")
	}
}
