// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package v1_test

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	v1 "github.com/cloudoperators/permission-manager/api/v1"
)

func TestSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	err := v1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed to add v1 types to scheme: %v", err)
	}

	gvk := schema.GroupVersionKind{
		Group:   "permission-manager.cloudoperators.dev",
		Version: "v1",
		Kind:    "ApplicationSpecificRole",
	}

	// Verify ApplicationSpecificRole is registered
	obj, err := scheme.New(gvk)
	if err != nil {
		t.Fatalf("expected ApplicationSpecificRole to be registered, got error: %v", err)
	}
	if obj == nil {
		t.Fatal("expected non-nil object for ApplicationSpecificRole")
	}

	// Verify ConnectorBinding is registered
	gvk.Kind = "ConnectorBinding"
	obj, err = scheme.New(gvk)
	if err != nil {
		t.Fatalf("expected ConnectorBinding to be registered, got error: %v", err)
	}
	if obj == nil {
		t.Fatal("expected non-nil object for ConnectorBinding")
	}

	// Verify FunctionalRole is registered
	gvk.Kind = "FunctionalRole"
	obj, err = scheme.New(gvk)
	if err != nil {
		t.Fatalf("expected FunctionalRole to be registered, got error: %v", err)
	}
	if obj == nil {
		t.Fatal("expected non-nil object for FunctionalRole")
	}

	// Verify ServiceIdentity is registered
	gvk.Kind = "ServiceIdentity"
	obj, err = scheme.New(gvk)
	if err != nil {
		t.Fatalf("expected ServiceIdentity to be registered, got error: %v", err)
	}
	if obj == nil {
		t.Fatal("expected non-nil object for ServiceIdentity")
	}
}

func TestListTypesRegistered(t *testing.T) {
	scheme := runtime.NewScheme()
	err := v1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed to add v1 types to scheme: %v", err)
	}

	listKinds := []string{
		"ApplicationSpecificRoleList",
		"ConnectorBindingList",
		"FunctionalRoleList",
		"ServiceIdentityList",
	}

	for _, kind := range listKinds {
		gvk := schema.GroupVersionKind{
			Group:   "permission-manager.cloudoperators.dev",
			Version: "v1",
			Kind:    kind,
		}
		obj, err := scheme.New(gvk)
		if err != nil {
			t.Errorf("expected %s to be registered, got error: %v", kind, err)
		}
		if obj == nil {
			t.Errorf("expected non-nil object for %s", kind)
		}
	}
}

func TestGroupVersion(t *testing.T) {
	if v1.GroupVersion.Group != "permission-manager.cloudoperators.dev" {
		t.Errorf("expected group 'permission-manager.cloudoperators.dev', got '%s'", v1.GroupVersion.Group)
	}
	if v1.GroupVersion.Version != "v1" {
		t.Errorf("expected version 'v1', got '%s'", v1.GroupVersion.Version)
	}
}

func TestApplicationSpecificRoleFields(t *testing.T) {
	asr := &v1.ApplicationSpecificRole{
		Spec: v1.ApplicationSpecificRoleSpec{
			Name:           "test-role",
			DisplayName:    "Test Role",
			Description:    "A test role",
			Classification: "SU2",
			ConnectorRefs:  []string{"connector-1", "connector-2"},
			Permissions: []v1.Permission{
				{Resource: "namespace", Capability: "read"},
				{Resource: "pod", Capability: "write"},
			},
			Deny: []v1.Permission{
				{Resource: "secret", Capability: "read"},
			},
			Tags:               []string{"qa", "prod"},
			FunctionalRoleName: "profile-1",
			Approvers: []v1.Approver{
				{UserID: "user1", Step: 1},
				{ApplicationSpecificRole: "admin-role", Step: 2},
			},
		},
	}

	if asr.Spec.Name != "test-role" {
		t.Errorf("expected name 'test-role', got '%s'", asr.Spec.Name)
	}
	if len(asr.Spec.ConnectorRefs) != 2 {
		t.Errorf("expected 2 connector refs, got %d", len(asr.Spec.ConnectorRefs))
	}
	if len(asr.Spec.Permissions) != 2 {
		t.Errorf("expected 2 permissions, got %d", len(asr.Spec.Permissions))
	}
	if len(asr.Spec.Deny) != 1 {
		t.Errorf("expected 1 deny entry, got %d", len(asr.Spec.Deny))
	}
	if asr.Spec.Approvers[0].UserID != "user1" {
		t.Errorf("expected approver userID 'user1', got '%s'", asr.Spec.Approvers[0].UserID)
	}
}

func TestConnectorBindingFields(t *testing.T) {
	defRaw, _ := json.Marshal(map[string]interface{}{
		"region":  "eu-central-1",
		"account": "123456789",
	})

	cb := &v1.ConnectorBinding{
		Spec: v1.ConnectorBindingSpec{
			Name:     "my-connector",
			Provider: "aws-iam",
			Definition: &runtime.RawExtension{
				Raw: defRaw,
			},
			SupportsServiceIdentities: true,
			ServiceIdentityDefaults: &runtime.RawExtension{
				Raw: []byte(`{"ttl":"8h"}`),
			},
		},
	}

	if cb.Spec.Name != "my-connector" {
		t.Errorf("expected name 'my-connector', got '%s'", cb.Spec.Name)
	}
	if cb.Spec.Provider != "aws-iam" {
		t.Errorf("expected provider 'aws-iam', got '%s'", cb.Spec.Provider)
	}
	if cb.Spec.Definition == nil {
		t.Error("expected definition to be non-nil")
	}
	if !cb.Spec.SupportsServiceIdentities {
		t.Error("expected SupportsServiceIdentities to be true")
	}
}

func TestFunctionalRoleFields(t *testing.T) {
	fr := &v1.FunctionalRole{
		Spec: v1.FunctionalRoleSpec{
			Name:        "dev-profile",
			DisplayName: "Developer Profile",
			Description: "Access for developers",
			Type:        "access",
			Tags:        []string{"dev"},
			Roles: []v1.RoleReference{
				{Name: "role-1"},
				{Name: "role-2"},
			},
			Passports: []string{"basic-passport"},
			Validity: &v1.Validity{
				Default: "P6M",
				Maximum: "P1Y",
			},
			LongDescription: "Extended description",
		},
	}

	if fr.Spec.Name != "dev-profile" {
		t.Errorf("expected name 'dev-profile', got '%s'", fr.Spec.Name)
	}
	if fr.Spec.Type != "access" {
		t.Errorf("expected type 'access', got '%s'", fr.Spec.Type)
	}
	if len(fr.Spec.Roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(fr.Spec.Roles))
	}
	if fr.Spec.Validity.Default != "P6M" {
		t.Errorf("expected validity default 'P6M', got '%s'", fr.Spec.Validity.Default)
	}
}

func TestServiceIdentityFields(t *testing.T) {
	si := &v1.ServiceIdentity{
		Spec: v1.ServiceIdentitySpec{
			Name:          "svc-account",
			DisplayName:   "Service Account",
			Description:   "A service account",
			ConnectorRefs: []string{"connector-1"},
			Roles: []v1.RoleReference{
				{Name: "role-1"},
			},
			Tags: []string{"prod"},
			Approvers: []v1.ServiceIdentityApprover{
				{UserID: "admin1", Step: 1},
			},
		},
	}

	if si.Spec.Name != "svc-account" {
		t.Errorf("expected name 'svc-account', got '%s'", si.Spec.Name)
	}
	if len(si.Spec.ConnectorRefs) != 1 {
		t.Errorf("expected 1 connector ref, got %d", len(si.Spec.ConnectorRefs))
	}
	if si.Spec.Approvers[0].UserID != "admin1" {
		t.Errorf("expected approver userID 'admin1', got '%s'", si.Spec.Approvers[0].UserID)
	}
}

func TestDeepCopy(t *testing.T) {
	asr := &v1.ApplicationSpecificRole{
		Spec: v1.ApplicationSpecificRoleSpec{
			Name:          "test",
			ConnectorRefs: []string{"conn-1"},
			Permissions: []v1.Permission{
				{Resource: "ns", Capability: "read"},
			},
		},
	}

	copied := asr.DeepCopy()
	if copied.Spec.Name != "test" {
		t.Error("deep copy should preserve Name")
	}

	// Mutate the copy and ensure original is unchanged
	copied.Spec.Name = "modified"
	if asr.Spec.Name != "test" {
		t.Error("mutating deep copy should not affect original")
	}

	copied.Spec.ConnectorRefs[0] = "modified"
	if asr.Spec.ConnectorRefs[0] != "conn-1" {
		t.Error("mutating deep copy slices should not affect original")
	}
}
