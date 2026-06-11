// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package greenhouseteams

import (
	"sort"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	greenhouse "github.com/cloudoperators/permission-manager/pkg/greenhouse"
)

func TestComputeDesiredState_BasicCluster(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SG-COMPUTE",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/eu-de-1",
					Capability: "READ",
				},
			},
		},
	}

	capMapping := greenhouse.CapabilityMapping{
		"READ": "cluster-viewer",
	}

	state, warnings := ComputeDesiredState(asr, "SG-COMPUTE", capMapping)

	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}

	if state.TeamName != "pm-SG-COMPUTE" {
		t.Errorf("TeamName = %q, want %q", state.TeamName, "pm-SG-COMPUTE")
	}

	if state.MappedIDPGroup != "SG-COMPUTE" {
		t.Errorf("MappedIDPGroup = %q, want %q", state.MappedIDPGroup, "SG-COMPUTE")
	}

	if len(state.TRBs) != 1 {
		t.Fatalf("expected 1 TRB, got %d", len(state.TRBs))
	}

	trb := state.TRBs[0]
	if trb.TeamRole != "cluster-viewer" {
		t.Errorf("TRB.TeamRole = %q, want %q", trb.TeamRole, "cluster-viewer")
	}
	if trb.ClusterName != "eu-de-1" {
		t.Errorf("TRB.ClusterName = %q, want %q", trb.ClusterName, "eu-de-1")
	}
	if len(trb.Namespaces) != 0 {
		t.Errorf("TRB.Namespaces should be empty for cluster CCRN, got %v", trb.Namespaces)
	}
	expectedName := TRBName("SG-COMPUTE", "cluster-viewer", "eu-de-1")
	if trb.Name != expectedName {
		t.Errorf("TRB.Name = %q, want %q", trb.Name, expectedName)
	}
}

func TestComputeDesiredState_NamespaceCCRN(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SG-NETWORK",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/eu-de-1/kube-system",
					Capability: "WRITE",
				},
				{
					Resource:   "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/eu-de-1/monitoring",
					Capability: "WRITE",
				},
			},
		},
	}

	capMapping := greenhouse.CapabilityMapping{
		"WRITE": "namespace-admin",
	}

	state, warnings := ComputeDesiredState(asr, "SG-NETWORK", capMapping)

	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}

	if len(state.TRBs) != 1 {
		t.Fatalf("expected 1 TRB (same cluster+role merged), got %d", len(state.TRBs))
	}

	trb := state.TRBs[0]
	if trb.TeamRole != "namespace-admin" {
		t.Errorf("TRB.TeamRole = %q, want %q", trb.TeamRole, "namespace-admin")
	}
	if trb.ClusterName != "eu-de-1" {
		t.Errorf("TRB.ClusterName = %q, want %q", trb.ClusterName, "eu-de-1")
	}

	sort.Strings(trb.Namespaces)
	if len(trb.Namespaces) != 2 {
		t.Fatalf("expected 2 namespaces, got %d", len(trb.Namespaces))
	}
	if trb.Namespaces[0] != "kube-system" || trb.Namespaces[1] != "monitoring" {
		t.Errorf("TRB.Namespaces = %v, want [kube-system, monitoring]", trb.Namespaces)
	}
}

func TestComputeDesiredState_MultipleClusters(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SG-FOUNDATION",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/eu-de-1",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/eu-nl-1",
					Capability: "READ",
				},
			},
		},
	}

	capMapping := greenhouse.CapabilityMapping{
		"READ": "cluster-viewer",
	}

	state, warnings := ComputeDesiredState(asr, "SG-FOUNDATION", capMapping)

	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}

	if len(state.TRBs) != 2 {
		t.Fatalf("expected 2 TRBs (different clusters), got %d", len(state.TRBs))
	}

	// Check both clusters are present
	clusters := map[string]bool{}
	for _, trb := range state.TRBs {
		clusters[trb.ClusterName] = true
	}
	if !clusters["eu-de-1"] || !clusters["eu-nl-1"] {
		t.Errorf("expected clusters eu-de-1 and eu-nl-1, got %v", clusters)
	}
}

func TestComputeDesiredState_DifferentCapabilities(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SG-OPS",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/eu-de-1",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/eu-de-1",
					Capability: "WRITE",
				},
			},
		},
	}

	capMapping := greenhouse.CapabilityMapping{
		"READ":  "cluster-viewer",
		"WRITE": "cluster-admin",
	}

	state, warnings := ComputeDesiredState(asr, "SG-OPS", capMapping)

	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}

	if len(state.TRBs) != 2 {
		t.Fatalf("expected 2 TRBs (same cluster, different roles), got %d", len(state.TRBs))
	}

	roles := map[string]bool{}
	for _, trb := range state.TRBs {
		roles[trb.TeamRole] = true
		if trb.ClusterName != "eu-de-1" {
			t.Errorf("expected cluster eu-de-1, got %q", trb.ClusterName)
		}
	}
	if !roles["cluster-viewer"] || !roles["cluster-admin"] {
		t.Errorf("expected roles cluster-viewer and cluster-admin, got %v", roles)
	}
}

func TestComputeDesiredState_UnmappedCapability(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SG-TEST",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/eu-de-1",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/eu-de-1",
					Capability: "UNKNOWN_CAP",
				},
			},
		},
	}

	capMapping := greenhouse.CapabilityMapping{
		"READ": "cluster-viewer",
	}

	state, warnings := ComputeDesiredState(asr, "SG-TEST", capMapping)

	// Should have a warning about the unmapped capability
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}

	// Should still produce the valid TRB
	if len(state.TRBs) != 1 {
		t.Fatalf("expected 1 TRB (unmapped capability skipped), got %d", len(state.TRBs))
	}
	if state.TRBs[0].TeamRole != "cluster-viewer" {
		t.Errorf("TRB.TeamRole = %q, want %q", state.TRBs[0].TeamRole, "cluster-viewer")
	}
}

func TestComputeDesiredState_NonK8sCCRN_Skipped(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SG-GIT",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:repo.git.ccrn.cloudoperators.dev/v1/my-org/my-repo",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:cluster.greenhouse.ccrn.cloudoperators.dev/v1/my-cluster",
					Capability: "READ",
				},
			},
		},
	}

	capMapping := greenhouse.CapabilityMapping{
		"READ": "viewer",
	}

	state, warnings := ComputeDesiredState(asr, "SG-GIT", capMapping)

	// Non-k8s/non-github CCRNs (git, greenhouse) produce parse warnings since they're unsupported types
	if len(warnings) != 2 {
		t.Errorf("expected 2 warnings for unsupported CCRN types, got %d: %v", len(warnings), warnings)
	}

	if len(state.TRBs) != 0 {
		t.Errorf("expected 0 TRBs (only non-k8s CCRNs), got %d", len(state.TRBs))
	}
}

func TestComputeDesiredState_InvalidCCRN(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SG-MIXED",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "not-a-valid-ccrn",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/eu-de-1",
					Capability: "READ",
				},
			},
		},
	}

	capMapping := greenhouse.CapabilityMapping{
		"READ": "viewer",
	}

	state, warnings := ComputeDesiredState(asr, "SG-MIXED", capMapping)

	// Invalid CCRN generates a warning
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for invalid CCRN, got %d: %v", len(warnings), warnings)
	}

	// Valid CCRN still produces a TRB
	if len(state.TRBs) != 1 {
		t.Fatalf("expected 1 TRB, got %d", len(state.TRBs))
	}
	if state.TRBs[0].ClusterName != "eu-de-1" {
		t.Errorf("TRB.ClusterName = %q, want %q", state.TRBs[0].ClusterName, "eu-de-1")
	}
}

func TestComputeDesiredState_DeduplicatesNamespaces(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name: "SG-DEDUP",
			Permissions: []v1alpha1.Permission{
				{
					Resource:   "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/eu-de-1/monitoring",
					Capability: "READ",
				},
				{
					Resource:   "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/eu-de-1/monitoring",
					Capability: "READ",
				},
			},
		},
	}

	capMapping := greenhouse.CapabilityMapping{
		"READ": "viewer",
	}

	state, _ := ComputeDesiredState(asr, "SG-DEDUP", capMapping)

	if len(state.TRBs) != 1 {
		t.Fatalf("expected 1 TRB, got %d", len(state.TRBs))
	}
	if len(state.TRBs[0].Namespaces) != 1 {
		t.Errorf("expected 1 namespace (deduped), got %d: %v", len(state.TRBs[0].Namespaces), state.TRBs[0].Namespaces)
	}
}

func TestComputeDesiredState_NoPermissions(t *testing.T) {
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "default",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:        "EMPTY",
			Permissions: nil,
		},
	}

	capMapping := greenhouse.CapabilityMapping{
		"READ": "viewer",
	}

	state, warnings := ComputeDesiredState(asr, "EMPTY", capMapping)

	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if len(state.TRBs) != 0 {
		t.Errorf("expected 0 TRBs, got %d", len(state.TRBs))
	}
	if state.TeamName != "pm-EMPTY" {
		t.Errorf("TeamName = %q, want %q", state.TeamName, "pm-EMPTY")
	}
	if state.MappedIDPGroup != "EMPTY" {
		t.Errorf("MappedIDPGroup = %q, want %q", state.MappedIDPGroup, "EMPTY")
	}
}
