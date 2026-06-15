// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

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
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ProviderType identifies a permission-manager provider.
type ProviderType string

const (
	// ProviderKubernetes is the provider ID for the Kubernetes RBAC provider.
	ProviderKubernetes ProviderType = "kubernetes"

	// ProviderGithub is the provider ID for the GitHub provider.
	ProviderGithub ProviderType = "github"
)

// ApplicationSpecificRoleSpec defines the desired state of ApplicationSpecificRole
type ApplicationSpecificRoleSpec struct {
	// Name is the unique slugified identifier used as metadata.name.
	Name string `json:"name"`

	// DisplayName is the human-readable access level name displayed in the CAM UI.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Description is a human-readable description of the access level.
	// +optional
	Description string `json:"description,omitempty"`

	// Classification is the security classification level (e.g., SU2, VSNFD).
	// +optional
	Classification string `json:"classification,omitempty"`

	// ConnectorRefs are references to ConnectorBinding names that provide tool configuration.
	// +kubebuilder:validation:MinItems=1
	ConnectorRefs []string `json:"connectorRefs"`

	// Permissions are granted permissions (resource + capability pairs).
	// +optional
	Permissions []Permission `json:"permissions,omitempty"`

	// Deny are denied permissions (exclusions).
	// +optional
	Deny []Permission `json:"deny,omitempty"`

	// Tags for environment filtering (e.g., qa, prod).
	// +optional
	Tags []string `json:"tags,omitempty"`

	// FunctionalRoleName is the name of the auto-generated profile that includes this access level.
	// +optional
	FunctionalRoleName string `json:"functionalRoleName,omitempty"`

	// Approvers are access level-specific approvers.
	// +optional
	Approvers []Approver `json:"approvers,omitempty"`

	// AdvancedConfig is implementation-specific configuration passed through to the connector.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	AdvancedConfig *runtime.RawExtension `json:"advancedConfig,omitempty"`
}

// Permission defines a resource + capability pair.
type Permission struct {
	// Resource is the resource name.
	Resource string `json:"resource"`

	// Capability is the capability name.
	Capability string `json:"capability"`
}

// Approver defines an approver for an access level or profile.
type Approver struct {
	// UserID is the ID of the approving user.
	// +optional
	UserID string `json:"userID,omitempty"`

	// ApplicationSpecificRole is the reference to an AccessLevel metadata.name whose holders may approve.
	// +optional
	ApplicationSpecificRole string `json:"applicationSpecificRole,omitempty"`

	// Step is the approval step number.
	// +optional
	Step int `json:"step,omitempty"`
}

// TargetStatus tracks the provisioning state for a single target within a provider.
type TargetStatus struct {
	// TargetID is the identifier of the target (e.g., cluster name).
	TargetID string `json:"targetID"`

	// Status is the provisioning result (e.g., Success, Error, Unreachable).
	// TODO: change to TargetStatusValue once github provider is updated.
	Status string `json:"status"`

	// Message is a human-readable description of the result.
	// +optional
	Message string `json:"message,omitempty"`

	// LastUpdated is the timestamp of the last provisioning attempt for this target.
	LastUpdated metav1.Time `json:"lastUpdated"`
}

// ProviderStatus tracks the reconciliation state for a single provider.
type ProviderStatus struct {
	// Ready indicates whether the provider has successfully reconciled all targets.
	Ready bool `json:"ready"`

	// Message is a human-readable summary of the provider's reconciliation state.
	// +optional
	Message string `json:"message,omitempty"`

	// Targets contains per-target provisioning status.
	// +optional
	Targets []TargetStatus `json:"targets,omitempty"`

	// LastReconciled is the timestamp of the last reconciliation attempt.
	LastReconciled metav1.Time `json:"lastReconciled"`
}

// ApplicationSpecificRoleStatus defines the observed state of ApplicationSpecificRole.
type ApplicationSpecificRoleStatus struct {
	// Providers maps provider type to its reconciliation status.
	// TODO: change key type to ProviderType once all providers are migrated.
	// +optional
	Providers map[string]ProviderStatus `json:"providers,omitempty"`

	// Conditions represent the latest available observations of the ASR's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=asr
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ApplicationSpecificRole is the Schema for the applicationspecificroles API
type ApplicationSpecificRole struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ApplicationSpecificRole
	// +required
	Spec ApplicationSpecificRoleSpec `json:"spec"`

	// status defines the observed state of ApplicationSpecificRole
	// +optional
	Status ApplicationSpecificRoleStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ApplicationSpecificRoleList contains a list of ApplicationSpecificRole
type ApplicationSpecificRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ApplicationSpecificRole `json:"items"`
}

func (asr *ApplicationSpecificRole) GetConditions() []metav1.Condition {
	return asr.Status.Conditions
}

func (asr *ApplicationSpecificRole) SetCondition(c metav1.Condition) {
	asr.Status.Conditions = pmmeta.SetCondition(asr.Status.Conditions, c)
}

func (asr *ApplicationSpecificRole) RemoveCondition(t pmmeta.ConditionType) {
	asr.Status.Conditions = slices.DeleteFunc(asr.Status.Conditions, func(c metav1.Condition) bool {
		return c.Type == string(t)
	})
}

func init() {
	SchemeBuilder.Register(&ApplicationSpecificRole{}, &ApplicationSpecificRoleList{})
}
