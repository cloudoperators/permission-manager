// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package controller contains the single controller that reconciles ApplicationSpecificRole.
package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// ASRReconciler is the single controller for ApplicationSpecificRole.
// It will dispatch to the per-provider logic (for now it is a bootstrap skeleton).
type ASRReconciler struct {
	client.Client
}

// Reconcile loads the ASR and routes to EnsureCreated or EnsureDeleted.
func (r *ASRReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var asr v1alpha1.ApplicationSpecificRole
	if err := r.Get(ctx, req.NamespacedName, &asr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !asr.DeletionTimestamp.IsZero() {
		return r.EnsureDeleted(ctx, &asr)
	}
	return r.EnsureCreated(ctx, &asr)
}

// EnsureCreated reconciles the ASR towards its desired state.
// TODO: dispatch to the providers.
func (r *ASRReconciler) EnsureCreated(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// EnsureDeleted cleans up resources owned by the ASR.
// TODO: per-provider cleanup.
func (r *ASRReconciler) EnsureDeleted(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// SetupWithManager registers the controller with the manager.
func (r *ASRReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ApplicationSpecificRole{}).
		Complete(r)
}
