package kubernetes

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

// AbandonCleanupAnnotation is an annotation that operators can set on an ASR to
// abandon cleanup of unreachable clusters during deletion. When set, the finalizer
// is removed even if some clusters are still unreachable.
const AbandonCleanupAnnotation = "permission-manager.cloudoperators.dev/abandon-cleanup"

// OrphanEscalationThreshold is the duration after which orphaned clusters trigger
// a longer requeue delay.
const OrphanEscalationThreshold = 90 * time.Second

// OrphanRequeueDelay is the escalated requeue delay used when orphaned clusters
// have been unreachable for longer than OrphanEscalationThreshold.
const OrphanRequeueDelay = 5 * time.Minute

// hasAbandonAnnotation returns true if the ASR has the abandon-cleanup annotation
// set to "true".
func hasAbandonAnnotation(asr *pmv1.ApplicationSpecificRole) bool {
	if asr.Annotations == nil {
		return false
	}
	return asr.Annotations[AbandonCleanupAnnotation] == "true"
}

// filterOrphanedClusters removes skipped clusters that are no longer in the
// inventory. Clusters gone from the inventory are "gone", not "orphaned" — they
// don't need cleanup and should not hold the finalizer.
func filterOrphanedClusters(skipped []SkippedCluster, inventory *ClusterInventory) []SkippedCluster {
	orphaned := make([]SkippedCluster, 0, len(skipped))
	for _, s := range skipped {
		if inventory.Has(s.ClusterName) {
			orphaned = append(orphaned, s)
		}
	}
	return orphaned
}

// orphanSetChanged returns true if the set of orphaned cluster names differs
// from the previously-recorded orphaned targets (those with Status != "Success").
func orphanSetChanged(skipped []SkippedCluster, prevTargets []ClusterProvisioningStatus) bool {
	// Build current orphan set
	currentSet := make(map[string]struct{}, len(skipped))
	for _, s := range skipped {
		currentSet[s.ClusterName] = struct{}{}
	}

	// Build previous orphan set (targets with non-Success status)
	prevSet := make(map[string]struct{})
	for _, t := range prevTargets {
		if t.Status != "Success" {
			prevSet[t.ClusterName] = struct{}{}
		}
	}

	if len(currentSet) != len(prevSet) {
		return true
	}
	for name := range currentSet {
		if _, ok := prevSet[name]; !ok {
			return true
		}
	}
	return false
}

// computeRequeueDelay returns the requeue delay based on how long clusters have
// been orphaned. If any non-Success target has LastUpdated older than
// OrphanEscalationThreshold, returns OrphanRequeueDelay; otherwise DefaultRequeueDelay.
func computeRequeueDelay(prevTargets []ClusterProvisioningStatus) time.Duration {
	now := time.Now()
	for _, t := range prevTargets {
		if t.Status != "Success" && !t.LastUpdated.IsZero() {
			if now.Sub(t.LastUpdated.Time) > OrphanEscalationThreshold {
				return OrphanRequeueDelay
			}
		}
	}
	return DefaultRequeueDelay
}

// buildOrphanStatuses converts a slice of SkippedCluster into
// ClusterProvisioningStatus entries with Status "Orphaned".
func buildOrphanStatuses(orphaned []SkippedCluster) []ClusterProvisioningStatus {
	statuses := make([]ClusterProvisioningStatus, 0, len(orphaned))
	for _, o := range orphaned {
		statuses = append(statuses, ClusterProvisioningStatus{
			ClusterName: o.ClusterName,
			Status:      "Orphaned",
			Message:     o.Reason,
			LastUpdated: metav1.Now(),
		})
	}
	return statuses
}
