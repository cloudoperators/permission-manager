// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package metrics defines custom Prometheus metrics for the pm-controller.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ASRConditionGauge exposes per-ASR condition state as a multi-label boolean gauge.
// Labels: name, namespace, condition (Ready|Degraded), status (True|False|Unknown).
// Value: 1 if the condition matches this status, 0 otherwise.
var ASRConditionGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "permission_manager_asr_condition",
		Help: "Current condition status of ApplicationSpecificRole (1=matches, 0=does not match)",
	},
	[]string{"name", "namespace", "condition", "status"},
)

// conditionTypes are the condition types we track.
var conditionTypes = []string{"Ready", "Degraded"}

// statuses are all possible metav1.ConditionStatus values.
var statuses = []string{string(metav1.ConditionTrue), string(metav1.ConditionFalse), string(metav1.ConditionUnknown)}

// UpdateASRConditions sets the gauge values for all condition/status combinations
// of the given ASR. For each condition type, the matching status gets value 1,
// all others get 0.
func UpdateASRConditions(name, namespace string, conditions []metav1.Condition) {
	// Reset all combinations to 0
	for _, ct := range conditionTypes {
		for _, s := range statuses {
			ASRConditionGauge.WithLabelValues(name, namespace, ct, s).Set(0)
		}
	}

	// Set matching conditions to 1
	for i := range conditions {
		for _, ct := range conditionTypes {
			if conditions[i].Type == ct {
				ASRConditionGauge.WithLabelValues(name, namespace, ct, string(conditions[i].Status)).Set(1)
			}
		}
	}
}

// DeleteASRConditionMetrics removes all gauge series for the given ASR.
// Call this when an ASR is deleted to prevent stale metrics.
func DeleteASRConditionMetrics(name, namespace string) {
	ASRConditionGauge.DeletePartialMatch(prometheus.Labels{
		"name":      name,
		"namespace": namespace,
	})
}
