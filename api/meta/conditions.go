// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package meta

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConditionType is a valid condition of a resource.
type ConditionType string

// ConditionReason is a valid reason for a condition of a resource.
type ConditionReason string

// Common condition types.
const (
	ReadyCondition    ConditionType = "Ready"
	DeleteCondition   ConditionType = "Delete"
	DegradedCondition ConditionType = "Degraded"
)

// TrueCondition returns a condition with status True.
func TrueCondition(t ConditionType, reason ConditionReason, msg string) metav1.Condition {
	return metav1.Condition{
		Type:               string(t),
		Status:             metav1.ConditionTrue,
		Reason:             string(reason),
		Message:            msg,
		LastTransitionTime: metav1.Now(),
	}
}

// FalseCondition returns a condition with status False.
func FalseCondition(t ConditionType, reason ConditionReason, msg string) metav1.Condition {
	return metav1.Condition{
		Type:               string(t),
		Status:             metav1.ConditionFalse,
		Reason:             string(reason),
		Message:            msg,
		LastTransitionTime: metav1.Now(),
	}
}

// UnknownCondition returns a condition with status Unknown.
func UnknownCondition(t ConditionType, reason ConditionReason, msg string) metav1.Condition {
	return metav1.Condition{
		Type:               string(t),
		Status:             metav1.ConditionUnknown,
		Reason:             string(reason),
		Message:            msg,
		LastTransitionTime: metav1.Now(),
	}
}

// SetCondition upserts a condition into the slice.
// If a condition of the same type already exists, it is updated.
// LastTransitionTime is only updated when Status changes.
func SetCondition(conditions []metav1.Condition, incoming metav1.Condition) []metav1.Condition {
	for i, existing := range conditions {
		if existing.Type != incoming.Type {
			continue
		}
		if existing.Status == incoming.Status {
			// preserve the original transition time when status hasn't changed
			incoming.LastTransitionTime = existing.LastTransitionTime
		}
		conditions[i] = incoming
		return conditions
	}
	return append(conditions, incoming)
}

// RemoveCondition removes the condition with the given type from the slice.
func RemoveCondition(conditions []metav1.Condition, t ConditionType) []metav1.Condition {
	result := make([]metav1.Condition, 0, len(conditions))
	for _, c := range conditions {
		if c.Type != string(t) {
			result = append(result, c)
		}
	}
	return result
}

// GetConditionByType returns a pointer to the condition with the given type, or nil.
func GetConditionByType(conditions []metav1.Condition, t ConditionType) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == string(t) {
			return &conditions[i]
		}
	}
	return nil
}

// IsReady returns true if the Ready condition is True.
func IsReady(conditions []metav1.Condition) bool {
	c := GetConditionByType(conditions, ReadyCondition)
	return c != nil && c.Status == metav1.ConditionTrue
}
