package utils

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// TargetStatusValue is the type for target provisioning status values.
type TargetStatusValue string

// Target status values.
const (
	TargetStatusReady    TargetStatusValue = "Ready"
	TargetStatusDegraded TargetStatusValue = "Degraded"
	TargetStatusError    TargetStatusValue = "Error"
)

// ProviderStatusFromTargets builds a ProviderStatus from per-target results.
// Ready is true only when all targets are TargetStatusReady.
func ProviderStatusFromTargets(targets []v1alpha1.TargetStatus) v1alpha1.ProviderStatus {
	allReady := true
	for _, t := range targets {
		if t.Status != string(TargetStatusReady) {
			allReady = false
			break
		}
	}
	return v1alpha1.ProviderStatus{
		Ready:          allReady,
		Targets:        targets,
		LastReconciled: metav1.Now(),
	}
}

// DegradedProviderStatus builds a ProviderStatus with Ready=false and a message.
// Used when capabilities cannot be mapped or the provider is in a degraded state.
func DegradedProviderStatus(msg string) v1alpha1.ProviderStatus {
	return v1alpha1.ProviderStatus{
		Ready:          false,
		Message:        msg,
		LastReconciled: metav1.Now(),
	}
}

// SetTargetStatus upserts a TargetStatus into the slice by TargetID.
// If a target with the same TargetID already exists, it is updated.
// LastUpdated is only updated when Status or Message changes.
func SetTargetStatus(targets []v1alpha1.TargetStatus, incoming v1alpha1.TargetStatus) []v1alpha1.TargetStatus {
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
func RemoveTargetStatus(targets []v1alpha1.TargetStatus, targetID string) []v1alpha1.TargetStatus {
	result := make([]v1alpha1.TargetStatus, 0, len(targets))
	for _, t := range targets {
		if t.TargetID != targetID {
			result = append(result, t)
		}
	}
	return result
}

// GetTargetStatus returns a pointer to the target with the given TargetID, or nil.
func GetTargetStatus(targets []v1alpha1.TargetStatus, targetID string) *v1alpha1.TargetStatus {
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
func SetProviderStatus(providers map[string]v1alpha1.ProviderStatus, providerKey v1alpha1.ProviderType, incoming v1alpha1.ProviderStatus) map[string]v1alpha1.ProviderStatus {
	if providers == nil {
		providers = make(map[string]v1alpha1.ProviderStatus)
	}
	key := string(providerKey)
	if existing, ok := providers[key]; ok {
		if existing.Ready == incoming.Ready && existing.Message == incoming.Message && targetsSemanticEqual(existing.Targets, incoming.Targets) {
			incoming.LastReconciled = existing.LastReconciled
		}
	}
	providers[key] = incoming
	return providers
}

// RemoveProviderStatus removes the provider entry with the given key from the map.
func RemoveProviderStatus(providers map[string]v1alpha1.ProviderStatus, providerKey v1alpha1.ProviderType) map[string]v1alpha1.ProviderStatus {
	delete(providers, string(providerKey))
	return providers
}

// GetProviderStatus returns a pointer to the ProviderStatus for the given key, or nil.
func GetProviderStatus(providers map[string]v1alpha1.ProviderStatus, providerKey v1alpha1.ProviderType) *v1alpha1.ProviderStatus {
	if providers == nil {
		return nil
	}
	ps, ok := providers[string(providerKey)]
	if !ok {
		return nil
	}
	return &ps
}

// targetsSemanticEqual returns true if two TargetStatus slices are semantically
// equal, ignoring LastUpdated timestamps.
func targetsSemanticEqual(a, b []v1alpha1.TargetStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].TargetID != b[i].TargetID || a[i].Status != b[i].Status || a[i].Message != b[i].Message {
			return false
		}
	}
	return true
}
