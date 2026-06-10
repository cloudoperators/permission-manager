// Package v1 contains API Schema definitions for the permission-manager v1 API group.
// +kubebuilder:object:generate=true
// +groupName=permission-manager.cloudoperators.dev
package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ApplicationSpecificRole defines an access level with permissions and connector references.
type ApplicationSpecificRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApplicationSpecificRoleSpec   `json:"spec"`
	Status ApplicationSpecificRoleStatus `json:"status,omitempty"`
}

// Condition type constants for ApplicationSpecificRole.
const (
	// ConditionReady indicates the ASR has been successfully reconciled across all providers.
	ConditionReady = "Ready"
	// ConditionDegraded indicates the ASR has partial failures across providers.
	ConditionDegraded = "Degraded"
)

// ApplicationSpecificRoleStatus defines the observed state of ApplicationSpecificRole.
type ApplicationSpecificRoleStatus struct {
	// Providers maps provider name to its reconciliation status.
	// +optional
	Providers map[string]ProviderStatus `json:"providers,omitempty"`

	// Conditions represent the latest available observations of the ASR's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
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

// TargetStatus tracks the provisioning state for a single target within a provider.
type TargetStatus struct {
	// TargetID is the identifier of the target (e.g., cluster name).
	TargetID string `json:"targetID"`

	// Status is the provisioning result (e.g., Success, Error, Unreachable).
	Status string `json:"status"`

	// Message is a human-readable description of the result.
	// +optional
	Message string `json:"message,omitempty"`

	// LastUpdated is the timestamp of the last provisioning attempt for this target.
	LastUpdated metav1.Time `json:"lastUpdated"`
}

// ApplicationSpecificRoleSpec defines the desired state of ApplicationSpecificRole.
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

// +kubebuilder:object:root=true

// ApplicationSpecificRoleList contains a list of ApplicationSpecificRole.
type ApplicationSpecificRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApplicationSpecificRole `json:"items"`
}

// +kubebuilder:object:root=true

// ConnectorBinding defines a connection to an external identity provider.
type ConnectorBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ConnectorBindingSpec `json:"spec"`
}

// ConnectorBindingSpec defines the desired state of ConnectorBinding.
type ConnectorBindingSpec struct {
	// Name is the unique connector name referenced by ApplicationSpecificRole connectorRefs.
	Name string `json:"name"`

	// Provider is the provider identifier.
	Provider string `json:"provider"`

	// Definition is the provider-specific connection configuration.
	// +kubebuilder:pruning:PreserveUnknownFields
	Definition *runtime.RawExtension `json:"definition"`

	// SupportsServiceIdentities indicates whether this connector supports ServiceIdentity resources.
	// +optional
	SupportsServiceIdentities bool `json:"supportsServiceIdentities,omitempty"`

	// ServiceIdentityDefaults are default values applied to ServiceIdentity resources using this connector.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	ServiceIdentityDefaults *runtime.RawExtension `json:"serviceIdentityDefaults,omitempty"`
}

// +kubebuilder:object:root=true

// ConnectorBindingList contains a list of ConnectorBinding.
type ConnectorBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConnectorBinding `json:"items"`
}

// +kubebuilder:object:root=true

// FunctionalRole defines a profile that groups access levels (ApplicationSpecificRoles)
// with approvers, validity, and access prerequisites.
type FunctionalRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec FunctionalRoleSpec `json:"spec"`
}

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

// +kubebuilder:object:root=true

// FunctionalRoleList contains a list of FunctionalRole.
type FunctionalRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FunctionalRole `json:"items"`
}

// +kubebuilder:object:root=true

// ServiceIdentity defines a technical user or service account template provisioned through a connector.
type ServiceIdentity struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ServiceIdentitySpec `json:"spec"`
}

// ServiceIdentitySpec defines the desired state of ServiceIdentity.
type ServiceIdentitySpec struct {
	// Name is the unique slugified identifier used as metadata.name.
	Name string `json:"name"`

	// DisplayName is the human-readable service identity name displayed in the CAM UI.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Description is a human-readable description.
	// +optional
	Description string `json:"description,omitempty"`

	// ConnectorRefs are references to ConnectorBinding names that provide tool configuration.
	// +kubebuilder:validation:MinItems=1
	ConnectorRefs []string `json:"connectorRefs"`

	// Roles are ApplicationSpecificRoles associated with this service identity.
	// +optional
	Roles []RoleReference `json:"roles,omitempty"`

	// Tags for environment filtering (e.g., qa, prod).
	// +optional
	Tags []string `json:"tags,omitempty"`

	// Approvers for this service identity.
	// +optional
	Approvers []ServiceIdentityApprover `json:"approvers,omitempty"`

	// AdvancedConfig is implementation-specific configuration passed through to the connector.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	AdvancedConfig *runtime.RawExtension `json:"advancedConfig,omitempty"`
}

// ServiceIdentityApprover defines an approver for a service identity.
type ServiceIdentityApprover struct {
	// UserID is the ID of the approving user.
	// +optional
	UserID string `json:"userID,omitempty"`

	// Step is the approval step number.
	// +optional
	Step int `json:"step,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceIdentityList contains a list of ServiceIdentity.
type ServiceIdentityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceIdentity `json:"items"`
}
