// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package greenhouse

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the v1alpha1 GroupVersion for Cluster and Team types.
	GroupVersion = schema.GroupVersion{Group: "greenhouse.sap", Version: "v1alpha1"}
	// SchemeBuilder registers v1alpha1 types.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// GroupVersionV1Alpha2 is the v1alpha2 GroupVersion for TeamRoleBinding.
	GroupVersionV1Alpha2 = schema.GroupVersion{Group: "greenhouse.sap", Version: "v1alpha2"}
	// SchemeBuilderV1Alpha2 registers v1alpha2 types.
	SchemeBuilderV1Alpha2 = &scheme.Builder{GroupVersion: GroupVersionV1Alpha2}

	// AddToScheme registers all greenhouse types (v1alpha1 + v1alpha2) with a scheme.
	AddToScheme = func(s *runtime.Scheme) error {
		if err := SchemeBuilder.AddToScheme(s); err != nil {
			return err
		}
		return SchemeBuilderV1Alpha2.AddToScheme(s)
	}
)

// --- Condition types ---

const (
	// KubeConfigValid is the condition type for cluster kubeconfig validity.
	KubeConfigValid ConditionType = "KubeConfigValid"
	// Ready is the condition type for general readiness (used by TeamRoleBinding).
	Ready ConditionType = "Ready"
)

// ConditionType is a string type for condition names.
type ConditionType string

// Condition represents a status condition on a resource.
type Condition struct {
	Type   ConditionType          `json:"type"`
	Status metav1.ConditionStatus `json:"status"`
}

// StatusConditions holds a list of conditions.
type StatusConditions struct {
	Conditions []Condition `json:"conditions,omitempty"`
}

// GetCondition returns the condition with the given type, or nil if not found.
func (sc *StatusConditions) GetCondition(t ConditionType) *Condition {
	for i := range sc.Conditions {
		if sc.Conditions[i].Type == t {
			return &sc.Conditions[i]
		}
	}
	return nil
}

// --- Cluster (v1alpha1) ---

// ClusterSpec defines the desired state of a Cluster.
type ClusterSpec struct {
}

// ClusterStatus defines the observed state of a Cluster.
type ClusterStatus struct {
	StatusConditions `json:"statusConditions,omitempty"`
}

// +kubebuilder:object:root=true
// Cluster is the Schema for the clusters API.
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ClusterSpec   `json:"spec,omitempty"`
	Status            ClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// ClusterList contains a list of Cluster.
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cluster `json:"items"`
}

// --- Team (v1alpha1) ---

// TeamSpec defines the desired state of a Team.
type TeamSpec struct {
	MappedIDPGroup string `json:"mappedIdPGroup"`
}

// +kubebuilder:object:root=true
// Team is the Schema for the teams API.
type Team struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TeamSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
// TeamList contains a list of Team.
type TeamList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Team `json:"items"`
}

// --- TeamRoleBinding (v1alpha2) ---

// TeamRef references a team by name and namespace.
type TeamRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// ClusterSelector selects a cluster by name.
type ClusterSelector struct {
	Name string `json:"clusterName"`
}

// TeamRoleBindingSpec defines the desired state of a TeamRoleBinding.
type TeamRoleBindingSpec struct {
	TeamRoleRef     string          `json:"teamRoleRef"`
	TeamRefs        []TeamRef       `json:"teamRef"`
	ClusterSelector ClusterSelector `json:"clusterSelector"`
	Namespaces      []string        `json:"namespaces,omitempty"`
}

// TeamRoleBindingStatus defines the observed state of a TeamRoleBinding.
type TeamRoleBindingStatus struct {
	StatusConditions `json:"statusConditions,omitempty"`
}

// +kubebuilder:object:root=true
// TeamRoleBinding is the Schema for the teamrolebindings API.
type TeamRoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TeamRoleBindingSpec   `json:"spec,omitempty"`
	Status            TeamRoleBindingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// TeamRoleBindingList contains a list of TeamRoleBinding.
type TeamRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TeamRoleBinding `json:"items"`
}

func init() {
	// Register v1alpha1 types
	SchemeBuilder.Register(&Cluster{}, &ClusterList{})
	SchemeBuilder.Register(&Team{}, &TeamList{})
	// Register v1alpha2 types
	SchemeBuilderV1Alpha2.Register(&TeamRoleBinding{}, &TeamRoleBindingList{})
}
