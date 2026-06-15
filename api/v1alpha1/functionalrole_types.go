// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// FunctionalRoleSpec defines the desired state of FunctionalRole.
type FunctionalRoleSpec struct {
	// Name is the unique slugified identifier used as metadata.name.
	Name string `json:"name"`

	// DisplayName is the human-readable profile name displayed in the CAM UI.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Description is a human-readable description.
	// +optional
	Description string `json:"description,omitempty"`

	// Type is the profile type classification.
	// +kubebuilder:validation:Enum=access;elevated;passport
	// +optional
	Type string `json:"type,omitempty"`

	// Tags for categorization and filtering.
	// +optional
	Tags []string `json:"tags,omitempty"`

	// Roles are access level references included in this profile.
	// +optional
	Roles []RoleReference `json:"roles,omitempty"`

	// Approvers is the list of approvers for this profile.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Approvers *runtime.RawExtension `json:"approvers,omitempty"`

	// Passports are required passport profile names (user must hold these to request this profile).
	// +optional
	Passports []string `json:"passports,omitempty"`

	// Validity defines validity durations in ISO 8601 format.
	// +optional
	Validity *Validity `json:"validity,omitempty"`

	// LongDescription is an extended description for the profile.
	// +optional
	LongDescription string `json:"longDescription,omitempty"`

	// ExternalRoles are environment-nested references to roles from external connectors.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	ExternalRoles *runtime.RawExtension `json:"externalRoles,omitempty"`

	// AdvancedConfig is implementation-specific configuration passed through to the connector.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	AdvancedConfig *runtime.RawExtension `json:"advancedConfig,omitempty"`
}

// RoleReference is a reference to an ApplicationSpecificRole (access level).
type RoleReference struct {
	// Name is the name of the ApplicationSpecificRole.
	Name string `json:"name"`
}

// Validity defines validity durations.
type Validity struct {
	// Default is the default validity duration (e.g., P6M, PT8H, P1D).
	// +optional
	Default string `json:"default,omitempty"`

	// Maximum is the maximum validity duration.
	// +optional
	Maximum string `json:"maximum,omitempty"`
}

// FunctionalRoleStatus defines the observed state of FunctionalRole.
type FunctionalRoleStatus struct {
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=fr
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// FunctionalRole is the Schema for the functionalroles API
type FunctionalRole struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of FunctionalRole
	// +required
	Spec FunctionalRoleSpec `json:"spec"`

	// status defines the observed state of FunctionalRole
	// +optional
	Status FunctionalRoleStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// FunctionalRoleList contains a list of FunctionalRole
type FunctionalRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []FunctionalRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FunctionalRole{}, &FunctionalRoleList{})
}
