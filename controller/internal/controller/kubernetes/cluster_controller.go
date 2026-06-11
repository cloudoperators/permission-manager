// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	greenhouse "github.com/cloudoperators/permission-manager/controller/pkg/greenhouse"
)

// ClusterCacheInvalidator is the interface for invalidating cached cluster clients.
// This allows the reconciler to depend on an interface rather than the concrete factory type.
type ClusterCacheInvalidator interface {
	InvalidateCache(clusterName string)
}

// ClusterReconciler reconciles Greenhouse Cluster CRs.
// It watches for changes to Cluster resources and updates the ClusterInventory
// accordingly based on the KubeConfigValid condition.
type ClusterReconciler struct {
	client.Client
	ClusterInventory     *ClusterInventory
	ClusterClientFactory ClusterCacheInvalidator
	// OnClusterChange is an optional callback invoked when a cluster is added to
	// the inventory. It can be used to trigger ASR re-reconciliation.
	OnClusterChange func(clusterName string)
}

// +kubebuilder:rbac:groups=greenhouse.sap,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile handles a single Cluster CR event.
// On create/update: checks the KubeConfigValid condition and adds/removes from inventory.
// On delete (not found): removes from inventory.
func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the Cluster CR
	var cluster greenhouse.Cluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Cluster was deleted - remove from inventory
			log.Info("Cluster CR deleted, removing from inventory")
			r.ClusterInventory.Remove(req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Check KubeConfigValid condition
	if r.isKubeConfigValid(&cluster) {
		// Cluster is usable - add to inventory and invalidate client cache
		r.ClusterInventory.Update(cluster.Name)
		r.ClusterClientFactory.InvalidateCache(cluster.Name)
		log.Info("Cluster is valid, added to inventory", "cluster", cluster.Name)

		// Notify about cluster change (for ASR re-reconciliation)
		if r.OnClusterChange != nil {
			r.OnClusterChange(cluster.Name)
		}
	} else {
		// Cluster is not usable - remove from inventory
		r.ClusterInventory.Remove(cluster.Name)
		log.Info("Cluster is not valid, removed from inventory", "cluster", cluster.Name)
	}

	return ctrl.Result{}, nil
}

// isKubeConfigValid checks whether the cluster has a KubeConfigValid condition
// set to True.
func (r *ClusterReconciler) isKubeConfigValid(cluster *greenhouse.Cluster) bool {
	cond := cluster.Status.GetCondition(greenhouse.KubeConfigValid)
	if cond == nil {
		return false
	}
	return cond.Status == metav1.ConditionTrue
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterReconciler) SetupWithManager(name string, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&greenhouse.Cluster{}).
		Complete(r)
}
