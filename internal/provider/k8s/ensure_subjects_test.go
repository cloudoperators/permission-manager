// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
)

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
