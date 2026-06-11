// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

func TestHasAbandonAnnotation(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name:        "no annotations",
			annotations: nil,
			expected:    false,
		},
		{
			name:        "annotation not present",
			annotations: map[string]string{"other": "value"},
			expected:    false,
		},
		{
			name:        "annotation present with true",
			annotations: map[string]string{AbandonCleanupAnnotation: "true"},
			expected:    true,
		},
		{
			name:        "annotation present with false",
			annotations: map[string]string{AbandonCleanupAnnotation: "false"},
			expected:    false,
		},
		{
			name:        "annotation present with empty string",
			annotations: map[string]string{AbandonCleanupAnnotation: ""},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asr := &pmv1.ApplicationSpecificRole{
				ObjectMeta: metav1.ObjectMeta{Annotations: tt.annotations},
			}
			if got := hasAbandonAnnotation(asr); got != tt.expected {
				t.Errorf("hasAbandonAnnotation() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFilterOrphanedClusters(t *testing.T) {
	inv := NewClusterInventory(logr.Discard())
	inv.Update("cluster-a")
	inv.Update("cluster-b")
	// cluster-c is NOT in inventory (gone from Greenhouse)

	skipped := []SkippedCluster{
		{ClusterName: "cluster-a", Reason: "timeout"},
		{ClusterName: "cluster-c", Reason: "not found"},
	}

	orphaned := filterOrphanedClusters(skipped, inv)

	if len(orphaned) != 1 {
		t.Fatalf("expected 1 orphaned cluster, got %d", len(orphaned))
	}
	if orphaned[0].ClusterName != "cluster-a" {
		t.Errorf("expected orphaned cluster to be cluster-a, got %s", orphaned[0].ClusterName)
	}
}

func TestFilterOrphanedClusters_AllGone(t *testing.T) {
	inv := NewClusterInventory(logr.Discard())
	// Neither cluster is in inventory

	skipped := []SkippedCluster{
		{ClusterName: "cluster-x", Reason: "timeout"},
		{ClusterName: "cluster-y", Reason: "not found"},
	}

	orphaned := filterOrphanedClusters(skipped, inv)

	if len(orphaned) != 0 {
		t.Fatalf("expected 0 orphaned clusters when all gone from inventory, got %d", len(orphaned))
	}
}

func TestOrphanSetChanged(t *testing.T) {
	tests := []struct {
		name     string
		skipped  []SkippedCluster
		prev     []ClusterProvisioningStatus
		expected bool
	}{
		{
			name:     "new orphan, no previous",
			skipped:  []SkippedCluster{{ClusterName: "cluster-a"}},
			prev:     nil,
			expected: true,
		},
		{
			name:    "same orphan set",
			skipped: []SkippedCluster{{ClusterName: "cluster-a"}},
			prev: []ClusterProvisioningStatus{
				{ClusterName: "cluster-a", Status: "Orphaned"},
			},
			expected: false,
		},
		{
			name:    "different orphan set",
			skipped: []SkippedCluster{{ClusterName: "cluster-b"}},
			prev: []ClusterProvisioningStatus{
				{ClusterName: "cluster-a", Status: "Orphaned"},
			},
			expected: true,
		},
		{
			name:    "previous has Success entries (ignored)",
			skipped: []SkippedCluster{{ClusterName: "cluster-a"}},
			prev: []ClusterProvisioningStatus{
				{ClusterName: "cluster-a", Status: "Orphaned"},
				{ClusterName: "cluster-b", Status: "Success"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := orphanSetChanged(tt.skipped, tt.prev); got != tt.expected {
				t.Errorf("orphanSetChanged() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestComputeRequeueDelay(t *testing.T) {
	tests := []struct {
		name     string
		prev     []ClusterProvisioningStatus
		expected time.Duration
	}{
		{
			name:     "no previous targets — default delay",
			prev:     nil,
			expected: DefaultRequeueDelay,
		},
		{
			name: "recent orphan — default delay",
			prev: []ClusterProvisioningStatus{
				{ClusterName: "cluster-a", Status: "Orphaned", LastUpdated: metav1.Now()},
			},
			expected: DefaultRequeueDelay,
		},
		{
			name: "old orphan — escalated delay",
			prev: []ClusterProvisioningStatus{
				{ClusterName: "cluster-a", Status: "Orphaned", LastUpdated: metav1.Time{Time: time.Now().Add(-2 * time.Minute)}},
			},
			expected: OrphanRequeueDelay,
		},
		{
			name: "success entries with old timestamps do not escalate",
			prev: []ClusterProvisioningStatus{
				{ClusterName: "cluster-a", Status: "Success", LastUpdated: metav1.Time{Time: time.Now().Add(-10 * time.Minute)}},
			},
			expected: DefaultRequeueDelay,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := computeRequeueDelay(tt.prev); got != tt.expected {
				t.Errorf("computeRequeueDelay() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestBuildOrphanStatuses(t *testing.T) {
	orphaned := []SkippedCluster{
		{ClusterName: "cluster-a", Reason: "connection refused"},
		{ClusterName: "cluster-b", Reason: "timeout"},
	}

	statuses := buildOrphanStatuses(orphaned)

	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	for i, s := range statuses {
		if s.ClusterName != orphaned[i].ClusterName {
			t.Errorf("status[%d].ClusterName = %q, want %q", i, s.ClusterName, orphaned[i].ClusterName)
		}
		if s.Status != "Orphaned" {
			t.Errorf("status[%d].Status = %q, want \"Orphaned\"", i, s.Status)
		}
		if s.Message != orphaned[i].Reason {
			t.Errorf("status[%d].Message = %q, want %q", i, s.Message, orphaned[i].Reason)
		}
		if s.LastUpdated.IsZero() {
			t.Errorf("status[%d].LastUpdated should not be zero", i)
		}
	}
}
