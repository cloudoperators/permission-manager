// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/pkg/audit"
)

// ReconcilePlan is a value type produced by the plan phase. It captures which
// clusters need RBAC provisioning (with their scopes), which clusters are
// orphans (previously provisioned but no longer targeted), and the ASR metadata
// needed for resource naming.
type ReconcilePlan struct {
	// ASRName is the metadata.name of the ASR object (used in resource naming).
	ASRName string
	// SpecName is the spec.name field (used as the RBAC subject group name).
	SpecName string
	// ClustersToProvision maps cluster name to its parsed scope.
	ClustersToProvision map[string]*clusterScope
	// OrphanClusters are cluster names that were previously provisioned
	// (per status) but are no longer in the current scopes.
	OrphanClusters []string
}

// planReconcile computes a ReconcilePlan from the ASR, the set of matched
// clusters (scopes), and the previously reported cluster statuses. This is a
// pure function — no I/O, no clients.
func planReconcile(asr *v1alpha1.ApplicationSpecificRole, scopes map[string]*clusterScope, prevStatuses []ClusterProvisioningStatus) ReconcilePlan {
	plan := ReconcilePlan{
		ASRName:             asr.Name,
		SpecName:            asr.Spec.Name,
		ClustersToProvision: scopes,
	}

	// Compute orphan clusters: clusters in prevStatuses that are NOT in current scopes.
	for _, ps := range prevStatuses {
		if _, stillTargeted := scopes[ps.ClusterName]; !stillTargeted {
			plan.OrphanClusters = append(plan.OrphanClusters, ps.ClusterName)
		}
	}

	return plan
}

// executeReconcilePlan carries out the plan: for each cluster to provision, it
// acquires a client, expands namespace wildcards, builds the RBAC plan, and
// applies it. Returns provisioning statuses and collected errors (partial
// failure semantics — same as the original inline loop).
//
// The emitEvent callback is invoked for event recording; pass nil to skip events.
func executeReconcilePlan(
	ctx context.Context,
	plan ReconcilePlan,
	factory ClusterClientFactory,
	nsLister NamespaceLister,
	emitEvent func(eventType, reason, message string),
	al *audit.Logger,
) ([]ClusterProvisioningStatus, []error) {
	logger := log.FromContext(ctx)
	var statuses []ClusterProvisioningStatus
	var errs []error

	for clusterName, scope := range plan.ClustersToProvision {
		status := ClusterProvisioningStatus{
			ClusterName: clusterName,
			LastUpdated: metav1.Now(),
		}

		// Get client for cluster
		clusterClient, err := factory.ClientForCluster(ctx, clusterName)
		if err != nil {
			if _, ok := err.(*ClusterUnreachableError); ok {
				logger.Error(err, "cluster unreachable, skipping", "cluster", clusterName)
				status.Status = "Unreachable"
				status.Message = err.Error()
				if emitEvent != nil {
					emitEvent(corev1.EventTypeWarning, "ClusterUnreachable",
						fmt.Sprintf("Cluster %s unreachable: %v", clusterName, err))
				}
			} else {
				logger.Error(err, "failed to get client for cluster", "cluster", clusterName)
				status.Status = "Error"
				status.Message = err.Error()
				if emitEvent != nil {
					emitEvent(corev1.EventTypeWarning, "RBACProvisioningFailed",
						fmt.Sprintf("Failed to get client for cluster %s: %v", clusterName, err))
				}
			}
			errs = append(errs, err)
			statuses = append(statuses, status)
			continue
		}

		// Expand namespace wildcards and validate exact namespaces
		if nsLister != nil {
			// Invalidate stale namespace cache so dynamic namespace changes
			// (create/delete) are picked up immediately on force-reconcile.
			if inv, ok := nsLister.(NamespaceCacheInvalidator); ok {
				inv.InvalidateNamespaceCache(clusterName)
			}
			nsListerFn := func(ctx context.Context) ([]string, error) {
				return nsLister.NamespacesForCluster(ctx, clusterName)
			}
			if err := expandNamespaceWildcards(ctx, nsListerFn, scope, logger); err != nil {
				logger.Error(err, "namespace list failed, skipping cluster", "cluster", clusterName)
				status.Status = "Error"
				status.Message = fmt.Sprintf("namespace list failed: %v", err)
				errs = append(errs, fmt.Errorf("cluster %q namespace list: %w", clusterName, err))
				statuses = append(statuses, status)
				continue
			}
		}

		// Build and apply RBAC plan
		rbacPlan := buildRBACPlan(plan.ASRName, plan.SpecName, scope)
		if err := applyRBACPlan(ctx, clusterClient, rbacPlan, plan.ASRName, clusterName, al); err != nil {
			logger.Error(err, "failed to apply RBAC on cluster", "cluster", clusterName)
			status.Status = "Error"
			status.Message = err.Error()
			errs = append(errs, fmt.Errorf("cluster %q RBAC apply: %w", clusterName, err))
			statuses = append(statuses, status)
			if emitEvent != nil {
				emitEvent(corev1.EventTypeWarning, "RBACProvisioningFailed",
					fmt.Sprintf("Failed to provision RBAC on cluster %s: %v", clusterName, err))
			}
			continue
		}

		logger.Info("successfully provisioned RBAC on cluster",
			"cluster", clusterName,
			"clusterRole", ResourceNamePrefix+plan.ASRName,
		)
		status.Status = "Success"
		status.Message = "RBAC resources provisioned successfully"
		statuses = append(statuses, status)
		if emitEvent != nil {
			emitEvent(corev1.EventTypeNormal, "RBACProvisioned",
				fmt.Sprintf("Successfully provisioned RBAC on cluster %s", clusterName))
		}
	}

	return statuses, errs
}

// newReconcileEmitFunc creates an emitEvent callback that records events on an ASR.
func (r *Reconciler) newReconcileEmitFunc(asr *v1alpha1.ApplicationSpecificRole) func(string, string, string) {
	return func(eventType, reason, message string) {
		r.emitEvent(asr, eventType, reason, message)
	}
}

// reconcileWithPlan uses the Plan/Execute split to provision RBAC.
// It replaces the inline provisioning loop in Reconcile(), preserving all
// existing behavior: namespace expansion, event recording, orphan cleanup,
// status patching, and rate-limited requeue.
func (r *Reconciler) reconcileWithPlan(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole, scopes map[string]*clusterScope) ([]ClusterProvisioningStatus, []error) {
	// Plan phase (pure — no I/O)
	prevStatuses := extractPrevStatuses(asr)
	plan := planReconcile(asr, scopes, prevStatuses)

	// Execute phase
	emitFn := r.newReconcileEmitFunc(asr)
	statuses, errs := executeReconcilePlan(ctx, plan, r.ClusterClientFactory, r.NamespaceLister, emitFn, r.AuditLog)

	// Orphan cleanup (uses plan's orphan list)
	orphanStatuses, orphanErr := r.Cleaner.CleanupOrphans(ctx, asr.Name, plan.OrphanClusters)
	statuses = append(statuses, orphanStatuses...)
	if orphanErr != nil {
		errs = append(errs, orphanErr)
	}

	return statuses, errs
}

// extractPrevStatuses reads the previous ClusterProvisioningStatus entries from
// the ASR status. It reads from status.providers["kubernetes"].targets.
// Returns nil if the ASR has no previous provider status.
func extractPrevStatuses(asr *v1alpha1.ApplicationSpecificRole) []ClusterProvisioningStatus {
	if asr.Status.Providers == nil {
		return nil
	}
	ps, ok := asr.Status.Providers["kubernetes"]
	if !ok || len(ps.Targets) == 0 {
		return nil
	}
	statuses := make([]ClusterProvisioningStatus, 0, len(ps.Targets))
	for _, t := range ps.Targets {
		statuses = append(statuses, ClusterProvisioningStatus{
			ClusterName: t.TargetID,
			Status:      t.Status,
			Message:     t.Message,
			LastUpdated: t.LastUpdated,
		})
	}
	return statuses
}

// extractTargetClusterNames returns the cluster names to iterate during full
// cleanup. When provider status exists it uses only the targets listed there
// (since those are the clusters that have RBAC deployed). When provider status
// does not exist (ASR was never reconciled), it falls back to the full
// ClusterInventory.
func extractTargetClusterNames(asr *v1alpha1.ApplicationSpecificRole, inventory *ClusterInventory) []string {
	if asr.Status.Providers == nil {
		return inventory.Names() // never reconciled, fall back
	}
	ps, ok := asr.Status.Providers["kubernetes"]
	if !ok {
		return inventory.Names() // never reconciled, fall back
	}
	// Provider status key exists — use only the targets listed there
	names := make([]string, 0, len(ps.Targets))
	for _, t := range ps.Targets {
		names = append(names, t.TargetID)
	}
	return names
}
