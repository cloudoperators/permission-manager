// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package greenhouse

import (
	"testing"
)

func TestCapabilityMapping_Resolve_Success(t *testing.T) {
	cm := CapabilityMapping{
		"list-clusters":   "ClusterList",
		"get-permissions": "TeamRoleBindingList",
	}

	result, err := cm.Resolve("list-clusters")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ClusterList" {
		t.Errorf("Resolve(\"list-clusters\") = %q, want %q", result, "ClusterList")
	}

	result, err = cm.Resolve("get-permissions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "TeamRoleBindingList" {
		t.Errorf("Resolve(\"get-permissions\") = %q, want %q", result, "TeamRoleBindingList")
	}
}

func TestCapabilityMapping_Resolve_Unmapped(t *testing.T) {
	cm := CapabilityMapping{
		"list-clusters": "ClusterList",
	}

	_, err := cm.Resolve("nonexistent-capability")
	if err == nil {
		t.Fatal("expected error for unmapped capability, got nil")
	}
}

func TestCapabilityMapping_Validate_NilMap(t *testing.T) {
	var cm CapabilityMapping
	err := cm.Validate()
	if err == nil {
		t.Fatal("expected error for nil capability mapping, got nil")
	}
}

func TestCapabilityMapping_Validate_EmptyMap(t *testing.T) {
	cm := CapabilityMapping{}
	err := cm.Validate()
	if err == nil {
		t.Fatal("expected error for empty capability mapping, got nil")
	}
}

func TestCapabilityMapping_Validate_ValidMap(t *testing.T) {
	cm := CapabilityMapping{
		"list-clusters": "ClusterList",
	}
	err := cm.Validate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCapabilityMapping_Validate_EmptyValue(t *testing.T) {
	cm := CapabilityMapping{
		"list-clusters": "",
	}
	err := cm.Validate()
	if err == nil {
		t.Fatal("expected error for empty value in capability mapping, got nil")
	}
}

func TestCapabilityMapping_Validate_EmptyKey(t *testing.T) {
	cm := CapabilityMapping{
		"": "ClusterList",
	}
	err := cm.Validate()
	if err == nil {
		t.Fatal("expected error for empty key in capability mapping, got nil")
	}
}
