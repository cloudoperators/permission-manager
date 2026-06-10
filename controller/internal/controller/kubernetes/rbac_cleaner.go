package kubernetes

import (
	"context"
	"errors"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
)

// SkippedCluster records a cluster that was unreachable during cleanup.
type SkippedCluster struct {
	ClusterName string
	Reason      string
}

// RBACCleaner encapsulates all RBAC deletion logic. It iterates clusters
// using the ClusterInventory and deletes RBAC resources by label selector.
type RBACCleaner struct {
	ClusterInventory     *ClusterInventory
	ClusterClientFactory ClusterClientFactory
	AuditLog             *audit.Logger
}

// CleanupAll deletes all RBAC resources owned by the given ASR across the
// specified clusters. The caller decides which clusters to iterate (typically
// derived from provider status targets or the full inventory as a fallback).
//
// Unreachable clusters are collected into []SkippedCluster — they must not block
// finalizer removal. If the cluster comes back online later, its RBAC resources
// will be orphaned, but this is preferable to an ASR that can never be deleted.
// Errors from reachable clusters (e.g. permission denied on delete) still
// cause a requeue so those are retried.
func (c *RBACCleaner) CleanupAll(ctx context.Context, asrName string, clusters []string) ([]SkippedCluster, error) {
	logger := log.FromContext(ctx).WithValues("asr", asrName)
	labelSelector := client.MatchingLabels{LabelASRName: asrName}
	var errs []error
	var skipped []SkippedCluster

	for _, clusterName := range clusters {
		clusterClient, err := c.ClusterClientFactory.ClientForCluster(ctx, clusterName)
		if err != nil {
			logger.Info("cluster unreachable during cleanup, skipping", "cluster", clusterName, "error", err.Error())
			skipped = append(skipped, SkippedCluster{ClusterName: clusterName, Reason: err.Error()})
			continue
		}

		if err := c.deleteRBACOnCluster(ctx, clusterClient, clusterName, asrName, labelSelector); err != nil {
			errs = append(errs, err)
		}
	}

	return skipped, errors.Join(errs...)
}

// CleanupOrphans deletes RBAC resources on clusters that are no longer targeted
// by the ASR. Returns statuses for orphans that failed cleanup (so they remain
// in status targets for retry) and an aggregated error.
func (c *RBACCleaner) CleanupOrphans(ctx context.Context, asrName string, orphanClusters []string) ([]ClusterProvisioningStatus, error) {
	if len(orphanClusters) == 0 {
		return nil, nil
	}

	logger := log.FromContext(ctx).WithValues("asr", asrName)
	logger.Info("detected orphan clusters for cleanup", "orphans", orphanClusters)

	labelSelector := client.MatchingLabels{LabelASRName: asrName}
	var statuses []ClusterProvisioningStatus
	var errs []error

	for _, clusterName := range orphanClusters {
		clusterClient, err := c.ClusterClientFactory.ClientForCluster(ctx, clusterName)
		if err != nil {
			logger.Error(err, "orphan cluster unreachable, will retry", "cluster", clusterName)
			statuses = append(statuses, ClusterProvisioningStatus{
				ClusterName: clusterName,
				Status:      "Unreachable",
				Message:     err.Error(),
				LastUpdated: metav1.Now(),
			})
			errs = append(errs, fmt.Errorf("orphan cluster %q unreachable: %w", clusterName, err))
			continue
		}

		if err := c.deleteRBACOnCluster(ctx, clusterClient, clusterName, asrName, labelSelector); err != nil {
			statuses = append(statuses, ClusterProvisioningStatus{
				ClusterName: clusterName,
				Status:      "Error",
				Message:     err.Error(),
				LastUpdated: metav1.Now(),
			})
			errs = append(errs, err)
			continue
		}

		logger.Info("successfully cleaned up RBAC on orphan cluster", "cluster", clusterName)
		// Successfully cleaned — do NOT add to statuses (remove from status targets)
	}

	return statuses, errors.Join(errs...)
}

// deleteRBACOnCluster deletes all RBAC resources matching the label selector on a single cluster.
func (c *RBACCleaner) deleteRBACOnCluster(ctx context.Context, clusterClient ClusterClient, clusterName string, asrName string, labelSelector client.MatchingLabels) error {
	logger := log.FromContext(ctx).WithValues("cluster", clusterName)
	var errs []error

	// Delete ClusterRoles with the ASR label
	var crList rbacv1.ClusterRoleList
	if err := clusterClient.List(ctx, &crList, labelSelector); err != nil {
		logger.Error(err, "failed to list ClusterRoles for cleanup")
		return fmt.Errorf("failed to list ClusterRoles on cluster %q: %w", clusterName, err)
	}
	for i := range crList.Items {
		ref := audit.ResourceRef{Kind: "ClusterRole", Name: crList.Items[i].Name, Cluster: clusterName}
		if err := clusterClient.Delete(ctx, &crList.Items[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to delete ClusterRole", "name", crList.Items[i].Name)
				c.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, ref, err)
				errs = append(errs, fmt.Errorf("failed to delete ClusterRole %q on cluster %q: %w", crList.Items[i].Name, clusterName, err))
			}
		} else {
			logger.Info("deleted ClusterRole", "name", crList.Items[i].Name)
			c.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, ref, nil)
		}
	}

	// Delete ClusterRoleBindings with the ASR label
	var crbList rbacv1.ClusterRoleBindingList
	if err := clusterClient.List(ctx, &crbList, labelSelector); err != nil {
		logger.Error(err, "failed to list ClusterRoleBindings for cleanup")
		return fmt.Errorf("failed to list ClusterRoleBindings on cluster %q: %w", clusterName, err)
	}
	for i := range crbList.Items {
		ref := audit.ResourceRef{Kind: "ClusterRoleBinding", Name: crbList.Items[i].Name, Cluster: clusterName}
		if err := clusterClient.Delete(ctx, &crbList.Items[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to delete ClusterRoleBinding", "name", crbList.Items[i].Name)
				c.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, ref, err)
				errs = append(errs, fmt.Errorf("failed to delete ClusterRoleBinding %q on cluster %q: %w", crbList.Items[i].Name, clusterName, err))
			}
		} else {
			logger.Info("deleted ClusterRoleBinding", "name", crbList.Items[i].Name)
			c.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, ref, nil)
		}
	}

	// Delete RoleBindings with the ASR label (across all namespaces)
	var rbList rbacv1.RoleBindingList
	if err := clusterClient.List(ctx, &rbList, labelSelector); err != nil {
		logger.Error(err, "failed to list RoleBindings for cleanup")
		return fmt.Errorf("failed to list RoleBindings on cluster %q: %w", clusterName, err)
	}
	for i := range rbList.Items {
		ref := audit.ResourceRef{Kind: "RoleBinding", Name: rbList.Items[i].Name, Namespace: rbList.Items[i].Namespace, Cluster: clusterName}
		if err := clusterClient.Delete(ctx, &rbList.Items[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to delete RoleBinding", "namespace", rbList.Items[i].Namespace, "name", rbList.Items[i].Name)
				c.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, ref, err)
				errs = append(errs, fmt.Errorf("failed to delete RoleBinding %q in namespace %q on cluster %q: %w", rbList.Items[i].Name, rbList.Items[i].Namespace, clusterName, err))
			}
		} else {
			logger.Info("deleted RoleBinding", "namespace", rbList.Items[i].Namespace, "name", rbList.Items[i].Name)
			c.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, ref, nil)
		}
	}

	return errors.Join(errs...)
}
