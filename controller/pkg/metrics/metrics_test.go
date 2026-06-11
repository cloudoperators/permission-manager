// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUpdateASRConditions_ReadyTrue(t *testing.T) {
	ASRConditionGauge.Reset()

	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue},
		{Type: "Degraded", Status: metav1.ConditionFalse},
	}

	UpdateASRConditions("test-asr", "default", conditions)

	// Ready=True should be 1
	val := testutil.ToFloat64(ASRConditionGauge.WithLabelValues("test-asr", "default", "Ready", "True"))
	if val != 1 {
		t.Errorf("expected Ready/True=1, got %f", val)
	}

	// Ready=False should be 0
	val = testutil.ToFloat64(ASRConditionGauge.WithLabelValues("test-asr", "default", "Ready", "False"))
	if val != 0 {
		t.Errorf("expected Ready/False=0, got %f", val)
	}

	// Degraded=True should be 0
	val = testutil.ToFloat64(ASRConditionGauge.WithLabelValues("test-asr", "default", "Degraded", "True"))
	if val != 0 {
		t.Errorf("expected Degraded/True=0, got %f", val)
	}

	// Degraded=False should be 1
	val = testutil.ToFloat64(ASRConditionGauge.WithLabelValues("test-asr", "default", "Degraded", "False"))
	if val != 1 {
		t.Errorf("expected Degraded/False=1, got %f", val)
	}
}

func TestUpdateASRConditions_DegradedTrue(t *testing.T) {
	ASRConditionGauge.Reset()

	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionFalse},
		{Type: "Degraded", Status: metav1.ConditionTrue},
	}

	UpdateASRConditions("degraded-asr", "ns1", conditions)

	val := testutil.ToFloat64(ASRConditionGauge.WithLabelValues("degraded-asr", "ns1", "Ready", "False"))
	if val != 1 {
		t.Errorf("expected Ready/False=1, got %f", val)
	}

	val = testutil.ToFloat64(ASRConditionGauge.WithLabelValues("degraded-asr", "ns1", "Degraded", "True"))
	if val != 1 {
		t.Errorf("expected Degraded/True=1, got %f", val)
	}
}

func TestDeleteASRConditionMetrics(t *testing.T) {
	ASRConditionGauge.Reset()

	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue},
	}
	UpdateASRConditions("to-delete", "ns1", conditions)

	// Verify metric exists
	val := testutil.ToFloat64(ASRConditionGauge.WithLabelValues("to-delete", "ns1", "Ready", "True"))
	if val != 1 {
		t.Fatalf("expected metric to exist with value 1, got %f", val)
	}

	// Delete
	DeleteASRConditionMetrics("to-delete", "ns1")

	// After delete, no metrics should remain for this ASR
	count := testutil.CollectAndCount(ASRConditionGauge)
	if count != 0 {
		t.Errorf("expected 0 metrics after delete, got %d", count)
	}
}

func TestUpdateASRConditions_UnknownState(t *testing.T) {
	ASRConditionGauge.Reset()

	conditions := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionUnknown},
	}

	UpdateASRConditions("unknown-asr", "default", conditions)

	val := testutil.ToFloat64(ASRConditionGauge.WithLabelValues("unknown-asr", "default", "Ready", "Unknown"))
	if val != 1 {
		t.Errorf("expected Ready/Unknown=1, got %f", val)
	}

	val = testutil.ToFloat64(ASRConditionGauge.WithLabelValues("unknown-asr", "default", "Ready", "True"))
	if val != 0 {
		t.Errorf("expected Ready/True=0, got %f", val)
	}
}
