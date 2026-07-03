// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package meta

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderType identifies a permission-manager provider.
type ProviderType string

const (
	// ProviderKubernetes is the provider ID for the Kubernetes RBAC provider.
	ProviderKubernetes ProviderType = "kubernetes"

	// ProviderGithub is the provider ID for the GitHub provider.
	ProviderGithub ProviderType = "github"
)

// TargetStatusValue is the type for target provisioning status values.
type TargetStatusValue string

const (
	TargetStatusReady    TargetStatusValue = "Ready"
	TargetStatusPending  TargetStatusValue = "Pending"
	TargetStatusDegraded TargetStatusValue = "Degraded"
	TargetStatusError    TargetStatusValue = "Error"
)

// +kubebuilder:object:generate=true

// AppliedResourceRef identifies a single RBAC resource that was applied to a target.
type AppliedResourceRef struct {
	// Kind is the Kubernetes resource kind (e.g., Role, ClusterRole).
	Kind string `json:"kind"`

	// Namespace is the namespace of the resource. Empty for cluster-scoped resources.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Name is the name of the resource.
	Name string `json:"name"`
}

// +kubebuilder:object:generate=true

// TargetStatus tracks the provisioning state for a single target within a provider.
type TargetStatus struct {
	// TargetID is the identifier of the target (e.g., cluster name).
	TargetID string `json:"targetID"`

	// Status is the provisioning result.
	Status TargetStatusValue `json:"status"`

	// Message is a human-readable description of the result.
	// +optional
	Message string `json:"message,omitempty"`

	// LastUpdated is the timestamp of the last provisioning attempt for this target.
	LastUpdated metav1.Time `json:"lastUpdated"`

	// AppliedResources is the list of RBAC resources applied to this target on the last successful reconcile.
	// +optional
	AppliedResources []AppliedResourceRef `json:"appliedResources,omitempty"`
}

// +kubebuilder:object:generate=true

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

// ProviderStatusFromTargets builds a ProviderStatus from per-target results.
// Ready is true only when all targets are TargetStatusReady.
func ProviderStatusFromTargets(targets []TargetStatus) ProviderStatus {
	allReady := true
	for _, t := range targets {
		if t.Status != TargetStatusReady {
			allReady = false
			break
		}
	}
	return ProviderStatus{
		Ready:          allReady,
		Targets:        targets,
		LastReconciled: metav1.Now(),
	}
}

// DegradedProviderStatus builds a ProviderStatus with Ready=false and a message.
func DegradedProviderStatus(msg string) ProviderStatus {
	return ProviderStatus{
		Ready:          false,
		Message:        msg,
		LastReconciled: metav1.Now(),
	}
}

// SetTargetStatus upserts a TargetStatus into the slice by TargetID.
// LastUpdated is only updated when Status or Message changes.
func SetTargetStatus(targets []TargetStatus, incoming TargetStatus) []TargetStatus {
	for i, existing := range targets {
		if existing.TargetID != incoming.TargetID {
			continue
		}
		if existing.Status == incoming.Status && existing.Message == incoming.Message {
			incoming.LastUpdated = existing.LastUpdated
		} else {
			incoming.LastUpdated = metav1.Now()
		}
		targets[i] = incoming
		return targets
	}
	if incoming.LastUpdated.IsZero() {
		incoming.LastUpdated = metav1.Now()
	}
	return append(targets, incoming)
}

// RemoveTargetStatus removes the target with the given TargetID from the slice.
func RemoveTargetStatus(targets []TargetStatus, targetID string) []TargetStatus {
	result := make([]TargetStatus, 0, len(targets))
	for _, t := range targets {
		if t.TargetID != targetID {
			result = append(result, t)
		}
	}
	return result
}

// GetTargetStatus returns a pointer to the target with the given TargetID, or nil.
func GetTargetStatus(targets []TargetStatus, targetID string) *TargetStatus {
	for i := range targets {
		if targets[i].TargetID == targetID {
			return &targets[i]
		}
	}
	return nil
}

// SetProviderStatus upserts a ProviderStatus into the providers map by providerKey.
// LastReconciled is only updated when Ready, Message, or Targets change.
// Allocates the map if nil.
func SetProviderStatus(providers map[ProviderType]ProviderStatus, providerKey ProviderType, incoming ProviderStatus) map[ProviderType]ProviderStatus {
	if providers == nil {
		providers = make(map[ProviderType]ProviderStatus)
	}
	if existing, ok := providers[providerKey]; ok {
		if existing.Ready == incoming.Ready && existing.Message == incoming.Message && targetsSemanticEqual(existing.Targets, incoming.Targets) {
			incoming.LastReconciled = existing.LastReconciled
		}
	}
	providers[providerKey] = incoming
	return providers
}

// RemoveProviderStatus removes the provider entry with the given key from the map.
func RemoveProviderStatus(providers map[ProviderType]ProviderStatus, providerKey ProviderType) map[ProviderType]ProviderStatus {
	delete(providers, providerKey)
	return providers
}

// GetProviderStatus returns a pointer to the ProviderStatus for the given key, or nil.
func GetProviderStatus(providers map[ProviderType]ProviderStatus, providerKey ProviderType) *ProviderStatus {
	if providers == nil {
		return nil
	}
	ps, ok := providers[providerKey]
	if !ok {
		return nil
	}
	return &ps
}

func targetsSemanticEqual(a, b []TargetStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].TargetID != b[i].TargetID || a[i].Status != b[i].Status || a[i].Message != b[i].Message {
			return false
		}
		if !appliedResourcesEqual(a[i].AppliedResources, b[i].AppliedResources) {
			return false
		}
	}
	return true
}

func appliedResourcesEqual(a, b []AppliedResourceRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
