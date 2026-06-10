// Package aggregator implements a lightweight status aggregator controller that
// watches all ApplicationSpecificRoles, reads per-provider status, and computes
// holistic conditions (Ready, Degraded). It has zero provider-specific logic.
package aggregator

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	pmmetrics "github.com/cloudoperators/permission-manager/controller/pkg/metrics"
)

// FieldManager is the server-side apply field manager identity for this controller.
const FieldManager = "pm-status-aggregator"

// Reconciler aggregates per-provider status into holistic ASR conditions.
type Reconciler struct {
	Client client.Client
	Log    logr.Logger
}

// SetupWithManager registers the aggregator controller with the manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("asr-status-aggregator").
		For(&pmv1.ApplicationSpecificRole{}).
		Complete(r)
}

// Reconcile reads the Providers map of the ASR and sets Ready/Degraded conditions.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("asr", req.NamespacedName)

	var asr pmv1.ApplicationSpecificRole
	if err := r.Client.Get(ctx, req.NamespacedName, &asr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// ASR deleted - clean up metrics
			pmmetrics.DeleteASRConditionMetrics(req.Name, req.Namespace)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Compute desired conditions from provider statuses
	desired := computeConditions(asr.Status.Providers)

	// Check if conditions actually changed to avoid reconcile loops
	if conditionsMatch(asr.Status.Conditions, desired) {
		// Still emit metrics even if conditions unchanged (idempotent)
		pmmetrics.UpdateASRConditions(asr.Name, asr.Namespace, asr.Status.Conditions)
		return ctrl.Result{}, nil
	}

	// Apply conditions using meta.SetStatusCondition to merge correctly
	for i := range desired {
		meta.SetStatusCondition(&asr.Status.Conditions, desired[i])
	}

	if err := r.Client.Status().Update(ctx, &asr); err != nil {
		log.Error(err, "failed to update status conditions")
		return ctrl.Result{}, err
	}

	// Emit condition gauge metrics
	pmmetrics.UpdateASRConditions(asr.Name, asr.Namespace, asr.Status.Conditions)

	log.V(1).Info("aggregated status", "conditions", len(desired))
	return ctrl.Result{}, nil
}

// conditionsMatch checks if the existing conditions match the desired ones
// (by type, status, and reason — ignoring timestamps).
func conditionsMatch(existing []metav1.Condition, desired []metav1.Condition) bool {
	for _, d := range desired {
		found := false
		for _, e := range existing {
			if e.Type == d.Type {
				found = true
				if e.Status != d.Status || e.Reason != d.Reason {
					return false
				}
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// computeConditions derives Ready and Degraded conditions from the providers map.
func computeConditions(providers map[string]pmv1.ProviderStatus) []metav1.Condition {
	now := metav1.Now()

	if len(providers) == 0 {
		return []metav1.Condition{
			{
				Type:               pmv1.ConditionReady,
				Status:             metav1.ConditionUnknown,
				Reason:             "NoProviders",
				Message:            "Waiting for providers",
				LastTransitionTime: now,
			},
		}
	}

	var readyCount, totalCount int
	for _, ps := range providers {
		totalCount++
		if ps.Ready {
			readyCount++
		}
	}

	allReady := readyCount == totalCount
	anyReady := readyCount > 0

	var conditions []metav1.Condition

	switch {
	case allReady:
		conditions = append(conditions,
			metav1.Condition{
				Type:               pmv1.ConditionReady,
				Status:             metav1.ConditionTrue,
				Reason:             "AllProvidersReady",
				Message:            "All providers have reconciled successfully",
				LastTransitionTime: now,
			},
			metav1.Condition{
				Type:               pmv1.ConditionDegraded,
				Status:             metav1.ConditionFalse,
				Reason:             "AllProvidersReady",
				Message:            "No degradation detected",
				LastTransitionTime: now,
			},
		)
	case anyReady:
		// Partial failure: some ready, some not
		conditions = append(conditions,
			metav1.Condition{
				Type:               pmv1.ConditionReady,
				Status:             metav1.ConditionFalse,
				Reason:             "PartialFailure",
				Message:            "Some providers are not ready",
				LastTransitionTime: now,
			},
			metav1.Condition{
				Type:               pmv1.ConditionDegraded,
				Status:             metav1.ConditionTrue,
				Reason:             "PartialFailure",
				Message:            "Some providers are not ready",
				LastTransitionTime: now,
			},
		)
	default:
		// Total failure: none ready
		conditions = append(conditions,
			metav1.Condition{
				Type:               pmv1.ConditionReady,
				Status:             metav1.ConditionFalse,
				Reason:             "AllProvidersFailed",
				Message:            "No providers are ready",
				LastTransitionTime: now,
			},
			metav1.Condition{
				Type:               pmv1.ConditionDegraded,
				Status:             metav1.ConditionFalse,
				Reason:             "TotalFailure",
				Message:            "All providers have failed",
				LastTransitionTime: now,
			},
		)
	}

	return conditions
}
