// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type ReconcileResult string

// conditionReason is the lifecycle package's local condition reason type.
type conditionReason string

const (
	commonCleanupFinalizer = "pm.cloudoperators.dev/cleanup"

	// readyConditionType is the type string the wrapper writes for the rollup Ready.
	readyConditionType = "Ready"

	// deleteCondition tracks the delete phase. Read by isResourceDeleted, written by setupDeleteState.
	deleteCondition = "Delete"

	createdReason         conditionReason = "Created"
	pendingCreationReason conditionReason = "PendingCreation"
	failingCreationReason conditionReason = "FailingCreation"

	pendingDeletionReason conditionReason = "PendingDeletion"
	failingDeletionReason conditionReason = "FailingDeletion"
	deletedReason         conditionReason = "Deleted"

	eventReasonReconciled = "Reconciled"
	eventReasonDeleted    = "Deleted"

	// Success should be returned in case the operator reached its target state.
	Success ReconcileResult = "Success"
	// Failed should be returned in case the operator wasn't able to reach its target state and without external changes it's unlikely that this will succeed in the next try.
	Failed ReconcileResult = "Failed"
	// Pending should be returned in case the operator is still trying to reach the target state.
	Pending ReconcileResult = "Pending"
)

// Conditioner is an optional hook for the caller to set custom status conditions
// after EnsureCreated runs. When nil, default Ready conditions are applied.
type Conditioner func(context.Context, RuntimeObject)

// RuntimeObject generalises the CR being reconciled.
type RuntimeObject interface {
	runtime.Object
	metav1.Object
	GetConditions() []metav1.Condition
	SetCondition(metav1.Condition)
	// GetReadyCondition returns the Ready condition, or nil.
	GetReadyCondition() *metav1.Condition
}

// Reconciler is the interface controllers must implement.
type Reconciler interface {
	EnsureCreated(context.Context, RuntimeObject) (ctrl.Result, ReconcileResult, error)
	EnsureDeleted(context.Context, RuntimeObject) (ctrl.Result, ReconcileResult, error)
	GetEventRecorder() events.EventRecorder
}

// Reconcile is the generic reconciliation entry point.
// It handles finalizer lifecycle, deletion, and creation phases, then patches status.
func Reconcile(ctx context.Context, kubeClient client.Client, namespacedName types.NamespacedName, runtimeObject RuntimeObject, reconciler Reconciler, statusFunc Conditioner) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	if err := kubeClient.Get(ctx, namespacedName, runtimeObject); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ctx = createContextFromRuntimeObject(ctx, runtimeObject, reconciler.GetEventRecorder())

	shouldBeDeleted := runtimeObject.GetDeletionTimestamp() != nil
	hasFinalizer := controllerutil.ContainsFinalizer(runtimeObject, commonCleanupFinalizer)

	if !shouldBeDeleted && !hasFinalizer {
		return ctrl.Result{}, ensureFinalizer(ctx, kubeClient, runtimeObject, commonCleanupFinalizer)
	}

	var (
		result ctrl.Result
		err    error
	)

	if shouldBeDeleted {
		if !hasFinalizer {
			return ctrl.Result{}, nil
		}
		if isResourceDeleted(runtimeObject) {
			return ctrl.Result{}, client.IgnoreNotFound(removeFinalizer(ctx, kubeClient, runtimeObject, commonCleanupFinalizer))
		}
		result, err = ensureDeleted(ctx, logger, reconciler, runtimeObject)
	} else {
		result, err = ensureCreated(ctx, logger, reconciler, runtimeObject, statusFunc)
	}

	return result, patchStatus(ctx, kubeClient, runtimeObject, err)
}

// isResourceDeleted returns true when the Delete condition is True with reason Deleted.
func isResourceDeleted(o RuntimeObject) bool {
	for _, c := range o.GetConditions() {
		if c.Type == deleteCondition {
			return c.Status == metav1.ConditionTrue && c.Reason == string(deletedReason)
		}
	}
	return false
}

// eventTypeFor maps a reconcile outcome to a Kubernetes Event type. Anything
// other than a clean Success surfaces as Warning so kubectl describe and
// dashboards flag the resource instead of burying the failure in a Normal event.
func eventTypeFor(r ReconcileResult, err error) string {
	if err != nil || r == Failed {
		return corev1.EventTypeWarning
	}
	return corev1.EventTypeNormal
}

// ensureCreated invokes EnsureCreated and applies status conditions.
func ensureCreated(ctx context.Context, logger logr.Logger, reconciler Reconciler, o RuntimeObject, statusFunc Conditioner) (ctrl.Result, error) {
	logger.Info("ensure created")
	result, reconcileResult, err := reconciler.EnsureCreated(ctx, o)
	if statusFunc != nil {
		statusFunc(ctx, o)
	} else {
		setupCreateState(o, reconcileResult, err)
	}
	recorder := GetEventRecorderFromContext(ctx)
	recorder.Eventf(o, nil, eventTypeFor(reconcileResult, err), eventReasonReconciled, "EnsureCreated", "reconcile result: %s", string(reconcileResult))
	return result, err
}

// ensureDeleted invokes EnsureDeleted and applies delete conditions.
func ensureDeleted(ctx context.Context, logger logr.Logger, reconciler Reconciler, o RuntimeObject) (ctrl.Result, error) {
	logger.Info("ensure deleted")
	setupDeleteState(o, Pending, nil)
	result, reconcileResult, err := reconciler.EnsureDeleted(ctx, o)
	setupDeleteState(o, reconcileResult, err)
	recorder := GetEventRecorderFromContext(ctx)
	recorder.Eventf(o, nil, eventTypeFor(reconcileResult, err), eventReasonDeleted, "EnsureDeleted", "delete result: %s", string(reconcileResult))
	return result, err
}

// setupCreateState maps ReconcileResult to a Ready condition. Skipped when
// Ready was already written by a subroutine this reconcile (reason other than
// the wrapper's PendingCreation placeholder).
func setupCreateState(o RuntimeObject, r ReconcileResult, err error) {
	if existing := o.GetReadyCondition(); existing != nil &&
		existing.Reason != string(pendingCreationReason) {
		return
	}
	switch r {
	case Success:
		o.SetCondition(metav1.Condition{
			Type:    readyConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  string(createdReason),
			Message: "resource is successfully reconciled",
		})
	case Failed:
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		o.SetCondition(metav1.Condition{
			Type:    readyConditionType,
			Status:  metav1.ConditionFalse,
			Reason:  string(failingCreationReason),
			Message: "reconciliation failed: " + msg,
		})
	default:
		o.SetCondition(metav1.Condition{
			Type:    readyConditionType,
			Status:  metav1.ConditionUnknown,
			Reason:  string(pendingCreationReason),
			Message: "reconciliation is pending",
		})
	}
}

// setupDeleteState maps ReconcileResult to a Delete condition.
func setupDeleteState(o RuntimeObject, r ReconcileResult, err error) {
	switch r {
	case Success:
		o.SetCondition(metav1.Condition{
			Type:    deleteCondition,
			Status:  metav1.ConditionTrue,
			Reason:  string(deletedReason),
			Message: "resource is successfully deleted",
		})
	case Failed:
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		o.SetCondition(metav1.Condition{
			Type:    deleteCondition,
			Status:  metav1.ConditionFalse,
			Reason:  string(failingDeletionReason),
			Message: "deletion failed: " + msg,
		})
	default:
		o.SetCondition(metav1.Condition{
			Type:    deleteCondition,
			Status:  metav1.ConditionFalse,
			Reason:  string(pendingDeletionReason),
			Message: "deletion is pending",
		})
	}
}

// patchStatus patches the object's status subresource.
func patchStatus(ctx context.Context, kubeClient client.Client, newObj RuntimeObject, reconcileError error) error {
	oldObj, err := GetOrigResourceFromContext(ctx)
	if err != nil {
		return err
	}
	if err = kubeClient.Status().Patch(ctx, newObj, client.MergeFrom(oldObj)); err != nil {
		return err
	}
	return reconcileError
}

// ensureFinalizer adds the given finalizer if not already present.
func ensureFinalizer(ctx context.Context, c client.Client, o client.Object, finalizer string) error {
	if controllerutil.AddFinalizer(o, finalizer) {
		return c.Update(ctx, o)
	}
	return nil
}

// removeFinalizer removes the given finalizer if present.
func removeFinalizer(ctx context.Context, c client.Client, o client.Object, finalizer string) error {
	if controllerutil.RemoveFinalizer(o, finalizer) {
		return c.Update(ctx, o)
	}
	return nil
}
