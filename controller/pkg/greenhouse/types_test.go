// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package greenhouse

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestSchemeRegistration_Cluster(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme failed: %v", err)
	}

	gvk := schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha1", Kind: "Cluster"}
	obj, err := s.New(gvk)
	if err != nil {
		t.Fatalf("failed to create Cluster from scheme: %v", err)
	}
	if _, ok := obj.(*Cluster); !ok {
		t.Errorf("expected *Cluster, got %T", obj)
	}

	gvk = schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha1", Kind: "ClusterList"}
	obj, err = s.New(gvk)
	if err != nil {
		t.Fatalf("failed to create ClusterList from scheme: %v", err)
	}
	if _, ok := obj.(*ClusterList); !ok {
		t.Errorf("expected *ClusterList, got %T", obj)
	}
}

func TestSchemeRegistration_Team(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme failed: %v", err)
	}

	gvk := schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha1", Kind: "Team"}
	obj, err := s.New(gvk)
	if err != nil {
		t.Fatalf("failed to create Team from scheme: %v", err)
	}
	if _, ok := obj.(*Team); !ok {
		t.Errorf("expected *Team, got %T", obj)
	}

	gvk = schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha1", Kind: "TeamList"}
	obj, err = s.New(gvk)
	if err != nil {
		t.Fatalf("failed to create TeamList from scheme: %v", err)
	}
	if _, ok := obj.(*TeamList); !ok {
		t.Errorf("expected *TeamList, got %T", obj)
	}
}

func TestSchemeRegistration_TeamRoleBinding(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme failed: %v", err)
	}

	gvk := schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha2", Kind: "TeamRoleBinding"}
	obj, err := s.New(gvk)
	if err != nil {
		t.Fatalf("failed to create TeamRoleBinding from scheme: %v", err)
	}
	if _, ok := obj.(*TeamRoleBinding); !ok {
		t.Errorf("expected *TeamRoleBinding, got %T", obj)
	}

	gvk = schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha2", Kind: "TeamRoleBindingList"}
	obj, err = s.New(gvk)
	if err != nil {
		t.Fatalf("failed to create TeamRoleBindingList from scheme: %v", err)
	}
	if _, ok := obj.(*TeamRoleBindingList); !ok {
		t.Errorf("expected *TeamRoleBindingList, got %T", obj)
	}
}

func TestSchemeRegistration_AllThreeTypes(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme failed: %v", err)
	}

	// Verify all three are registered simultaneously
	tests := []struct {
		gvk      schema.GroupVersionKind
		wantType string
	}{
		{schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha1", Kind: "Cluster"}, "*greenhouse.Cluster"},
		{schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha1", Kind: "Team"}, "*greenhouse.Team"},
		{schema.GroupVersionKind{Group: "greenhouse.sap", Version: "v1alpha2", Kind: "TeamRoleBinding"}, "*greenhouse.TeamRoleBinding"},
	}

	for _, tt := range tests {
		obj, err := s.New(tt.gvk)
		if err != nil {
			t.Errorf("failed to create %s from scheme: %v", tt.gvk, err)
			continue
		}
		if got := typeString(obj); got != tt.wantType {
			t.Errorf("for %s: got type %s, want %s", tt.gvk, got, tt.wantType)
		}
	}
}

func TestTeamRoleBindingSpec_Fields(t *testing.T) {
	trb := TeamRoleBinding{
		Spec: TeamRoleBindingSpec{
			TeamRoleRef: "admin-role",
			TeamRefs: []TeamRef{
				{Name: "team-a", Namespace: "org-a"},
			},
			ClusterSelector: ClusterSelector{
				Name: "cluster-1",
			},
			Namespaces: []string{"ns-a", "ns-b"},
		},
	}

	if trb.Spec.TeamRoleRef != "admin-role" {
		t.Errorf("TeamRoleRef = %q, want %q", trb.Spec.TeamRoleRef, "admin-role")
	}
	if len(trb.Spec.TeamRefs) != 1 {
		t.Fatalf("TeamRefs len = %d, want 1", len(trb.Spec.TeamRefs))
	}
	if trb.Spec.TeamRefs[0].Name != "team-a" {
		t.Errorf("TeamRefs[0].Name = %q, want %q", trb.Spec.TeamRefs[0].Name, "team-a")
	}
	if trb.Spec.TeamRefs[0].Namespace != "org-a" {
		t.Errorf("TeamRefs[0].Namespace = %q, want %q", trb.Spec.TeamRefs[0].Namespace, "org-a")
	}
	if trb.Spec.ClusterSelector.Name != "cluster-1" {
		t.Errorf("ClusterSelector.Name = %q, want %q", trb.Spec.ClusterSelector.Name, "cluster-1")
	}
	if len(trb.Spec.Namespaces) != 2 {
		t.Fatalf("Namespaces len = %d, want 2", len(trb.Spec.Namespaces))
	}
}

func TestTeamRoleBindingStatus_ReadyCondition(t *testing.T) {
	trb := TeamRoleBinding{
		Status: TeamRoleBindingStatus{
			StatusConditions: StatusConditions{
				Conditions: []Condition{
					{Type: Ready, Status: "True"},
				},
			},
		},
	}

	cond := trb.Status.GetCondition(Ready)
	if cond == nil {
		t.Fatal("expected Ready condition to be present")
		return // staticcheck: unreachable but convinces SA5011 that cond != nil below
	}
	if cond.Status != "True" {
		t.Errorf("Ready condition status = %q, want %q", cond.Status, "True")
	}
}

func TestClusterSelector_Fields(t *testing.T) {
	cs := ClusterSelector{
		Name: "my-cluster",
	}
	if cs.Name != "my-cluster" {
		t.Errorf("ClusterSelector.Name = %q, want %q", cs.Name, "my-cluster")
	}
}

func typeString(obj interface{}) string {
	return fmt.Sprintf("%T", obj)
}
