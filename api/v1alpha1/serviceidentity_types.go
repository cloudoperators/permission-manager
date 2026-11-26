// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
)

// ServiceIdentitySpec defines the desired state of ServiceIdentity.
type ServiceIdentitySpec struct {
	// Name is the literal Kubernetes username the identity authenticates as
	// (e.g. "ci1:core:openstack-neutron"). It is attached as a User subject on the
	// bindings of every referenced ApplicationSpecificRole.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('system:')",message="spec.name must not start with the reserved 'system:' prefix"
	Name string `json:"name"`

	// Description is free-form documentation.
	// +optional
	Description string `json:"description,omitempty"`

	// ConnectorRefs selects which provider operators act on this ServiceIdentity,
	// by ConnectorBinding name. The kubernetes operator processes it only when a
	// referenced binding has provider: kubernetes.
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	ConnectorRefs []string `json:"connectorRefs"`

	// Roles are ApplicationSpecificRole references, by bare name, resolved in this
	// ServiceIdentity's own namespace. The identity receives each role's access.
	// +optional
	// +listType=set
	Roles []string `json:"roles,omitempty"`
}

// ServiceIdentityStatus defines the observed state of ServiceIdentity.
type ServiceIdentityStatus struct {
	// Conditions represent the latest observations of reference validity.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=si
// +kubebuilder:printcolumn:name="Username",type="string",JSONPath=".spec.name"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ServiceIdentity grants a named non-group identity the access of the ASRs it references.
type ServiceIdentity struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ServiceIdentity
	// +required
	Spec ServiceIdentitySpec `json:"spec"`

	// status defines the observed state of ServiceIdentity
	// +optional
	Status ServiceIdentityStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ServiceIdentityList contains a list of ServiceIdentity.
type ServiceIdentityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ServiceIdentity `json:"items"`
}

func (si *ServiceIdentity) GetConditions() []metav1.Condition {
	return si.Status.Conditions
}

func (si *ServiceIdentity) SetCondition(c metav1.Condition) {
	si.Status.Conditions = pmmeta.SetCondition(si.Status.Conditions, c)
}

func (si *ServiceIdentity) RemoveCondition(t pmmeta.ConditionType) {
	si.Status.Conditions = pmmeta.RemoveCondition(si.Status.Conditions, t)
}

// GetReadyCondition returns the Ready condition, or nil.
func (si *ServiceIdentity) GetReadyCondition() *metav1.Condition {
	return pmmeta.GetConditionByType(si.Status.Conditions, pmmeta.ReadyCondition)
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &ServiceIdentity{}, &ServiceIdentityList{})
		return nil
	})
}
