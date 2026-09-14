// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package meta

import (
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConditionType is a valid condition of a resource.
type ConditionType string

// ConditionReason is a valid reason for a condition of a resource.
type ConditionReason string

// Condition types form the shared vocabulary across providers.
//
//   - Ready          rolls up the terminal sub-type.
//   - Provisioned    records the last apply attempt against the provider's targets.
//   - InvalidConfig  is present only while the spec cannot be reconciled.
const (
	ReadyCondition         ConditionType = "Ready"
	ProvisionedCondition   ConditionType = "Provisioned"
	InvalidConfigCondition ConditionType = "InvalidConfig"
)

// Condition reasons are reused across types and providers.
const (
	InvalidCCRNReason               ConditionReason = "InvalidCCRN"
	UnknownCapabilityReason         ConditionReason = "UnknownCapability"
	NoTargetsResolvedReason         ConditionReason = "NoTargetsResolved"
	MissingConnectorBindingReason   ConditionReason = "MissingConnectorBinding"
	MissingGithubOrganizationReason ConditionReason = "MissingGithubOrganization"
	DiscoveryUnavailableReason      ConditionReason = "DiscoveryUnavailable"

	ProvisioningSucceededReason ConditionReason = "ProvisioningSucceeded"
	ProvisioningFailedReason    ConditionReason = "ProvisioningFailed"
	PartialProvisioningReason   ConditionReason = "PartialProvisioning"
	NoTargetsReason             ConditionReason = "NoTargets"
)

// TrueCondition returns a condition with status True and zero LastTransitionTime.
// SetCondition stamps the time on insert or transition.
func TrueCondition(t ConditionType, reason ConditionReason, msg string) metav1.Condition {
	return metav1.Condition{
		Type:    string(t),
		Status:  metav1.ConditionTrue,
		Reason:  string(reason),
		Message: msg,
	}
}

// FalseCondition returns a condition with status False.
func FalseCondition(t ConditionType, reason ConditionReason, msg string) metav1.Condition {
	return metav1.Condition{
		Type:    string(t),
		Status:  metav1.ConditionFalse,
		Reason:  string(reason),
		Message: msg,
	}
}

// UnknownCondition returns a condition with status Unknown.
func UnknownCondition(t ConditionType, reason ConditionReason, msg string) metav1.Condition {
	return metav1.Condition{
		Type:    string(t),
		Status:  metav1.ConditionUnknown,
		Reason:  string(reason),
		Message: msg,
	}
}

// SetCondition upserts a condition by type. LastTransitionTime is preserved
// when the status is unchanged and stamped with metav1.Now otherwise.
func SetCondition(conditions []metav1.Condition, incoming metav1.Condition) []metav1.Condition {
	for i, existing := range conditions {
		if existing.Type != incoming.Type {
			continue
		}
		if existing.Status == incoming.Status {
			incoming.LastTransitionTime = existing.LastTransitionTime
		} else if incoming.LastTransitionTime.IsZero() {
			incoming.LastTransitionTime = metav1.Now()
		}
		conditions[i] = incoming
		return conditions
	}
	if incoming.LastTransitionTime.IsZero() {
		incoming.LastTransitionTime = metav1.Now()
	}
	return append(conditions, incoming)
}

// RemoveCondition removes the condition of the given type.
func RemoveCondition(conditions []metav1.Condition, t ConditionType) []metav1.Condition {
	return slices.DeleteFunc(conditions, func(c metav1.Condition) bool {
		return c.Type == string(t)
	})
}

// GetConditionByType returns a pointer to the condition of the given type, or nil.
func GetConditionByType(conditions []metav1.Condition, t ConditionType) *metav1.Condition {
	if i := slices.IndexFunc(conditions, func(c metav1.Condition) bool {
		return c.Type == string(t)
	}); i >= 0 {
		return &conditions[i]
	}
	return nil
}
