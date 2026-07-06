// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
)

// EnsureOrphansCleanedUp removes RBAC from clusters that were previously
// targeted but are no longer in p.compiled. Runs before EnsureRbacApplied so
// scope reductions tighten access before any expansion.
//
// Skipped when InvalidConfig=True: without a reliable view of intentional
// targets we could prematurely revoke valid access.
//
// Continue-only: errors are stashed on p.orphanErrs / p.orphanStatuses.
func (p *Phase) EnsureOrphansCleanedUp(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(ctx context.Context) (lifecycle.Result, error) {
		if asr.HasInvalidConfig() {
			return lifecycle.Continue(), nil
		}
		current := make(map[string]struct{}, len(p.compiled))
		for _, t := range p.compiled {
			current[t.clusterName] = struct{}{}
		}

		ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
		if ps == nil {
			return lifecycle.Continue(), nil
		}

		for _, t := range ps.Targets {
			if _, stillTargeted := current[t.TargetID]; stillTargeted {
				continue
			}
			remoteClient, err := p.RemoteClient(ctx, p.Client, t.TargetID, asr.Namespace)
			if err != nil {
				p.Log.Error(err, "orphan cluster unreachable, will retry", "namespace", asr.Namespace, "name", asr.Name, "cluster", t.TargetID)
				p.orphanStatuses = append(p.orphanStatuses, clusterStatus{name: t.TargetID, err: err})
				p.orphanErrs = append(p.orphanErrs, err)
				continue
			}
			if err := p.deleteRBACByLabel(ctx, remoteClient, asr.Name, t.TargetID); err != nil {
				p.Log.Error(err, "failed to clean up orphan RBAC", "namespace", asr.Namespace, "name", asr.Name, "cluster", t.TargetID)
				p.orphanStatuses = append(p.orphanStatuses, clusterStatus{name: t.TargetID, err: err})
				p.orphanErrs = append(p.orphanErrs, err)
				continue
			}
			p.Log.Info("orphan RBAC cleaned up", "namespace", asr.Namespace, "name", asr.Name, "cluster", t.TargetID)
		}
		for _, target := range p.compiled {
			prior := pmmeta.GetTargetStatus(ps.Targets, target.clusterName)
			if prior == nil || len(prior.AppliedResources) == 0 {
				continue
			}
			desired := compiledTargetToRefSet(target)
			var orphaned []pmmeta.AppliedResourceRef
			for _, ref := range prior.AppliedResources {
				if _, ok := desired[appliedRefKey(ref)]; !ok {
					orphaned = append(orphaned, ref)
				}
			}
			if len(orphaned) == 0 {
				continue
			}
			remoteClient, err := p.RemoteClient(ctx, p.Client, target.clusterName, asr.Namespace)
			if err != nil {
				p.Log.Error(err, "cluster unreachable for sub-resource orphan cleanup, will retry", "namespace", asr.Namespace, "name", asr.Name, "cluster", target.clusterName)
				p.orphanStatuses = append(p.orphanStatuses, clusterStatus{name: target.clusterName, err: err})
				p.orphanErrs = append(p.orphanErrs, err)
				continue
			}
			for _, ref := range orphaned {
				if err := p.deleteSpecificRBAC(ctx, remoteClient, ref, asr.Name, target.clusterName); err != nil {
					p.Log.Error(err, "failed to clean up orphan sub-resource", "namespace", asr.Namespace, "name", asr.Name, "cluster", target.clusterName, "kind", ref.Kind, "resourceNamespace", ref.Namespace, "resourceName", ref.Name)
					p.orphanStatuses = append(p.orphanStatuses, clusterStatus{name: target.clusterName, err: err})
					p.orphanErrs = append(p.orphanErrs, err)
				}
			}
		}
		return lifecycle.Continue(), nil
	}
}

// EnsureRbacRemoved is the delete-path subroutine: it removes RBAC managed by
// this ASR from every cluster in status.providers.kubernetes.targets. On full
// success it clears the kubernetes provider status entry; partial failures
// leave it in place so the next reconcile retries the unfinished clusters.
func (p *Phase) EnsureRbacRemoved(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(ctx context.Context) (lifecycle.Result, error) {
		var errs []error
		for _, clusterName := range previouslyTargetedClusters(asr) {
			remoteClient, err := p.RemoteClient(ctx, p.Client, clusterName, asr.Namespace)
			if err != nil {
				p.Log.Error(err, "cluster unreachable during cleanup, will retry", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
				errs = append(errs, err)
				continue
			}
			if err := p.deleteRBACByLabel(ctx, remoteClient, asr.Name, clusterName); err != nil {
				p.Log.Error(err, "failed to delete RBAC during cleanup", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
				errs = append(errs, err)
				continue
			}
			p.Log.Info("RBAC cleaned up", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
		}

		if len(errs) == 0 {
			asr.Status.Providers = pmmeta.RemoveProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
			return lifecycle.Continue(), nil
		}
		return lifecycle.Break(), utilerrors.NewAggregate(errs)
	}
}

// deleteRBACByLabel deletes every pm-managed RBAC resource owned by the named
// ASR on a remote cluster.
func (p *Phase) deleteRBACByLabel(ctx context.Context, remoteClient client.Client, asrName, clusterName string) error {
	labelSel := client.MatchingLabels{pmapis.LabelASR: asrName}
	var errs []error

	var crList rbacv1.ClusterRoleList
	if err := remoteClient.List(ctx, &crList, labelSel); err != nil {
		return fmt.Errorf("list %s: %w", kindClusterRole, err)
	}
	for i := range crList.Items {
		if err := p.deleteIfExists(ctx, remoteClient, &crList.Items[i], asrName, clusterName, kindClusterRole); err != nil {
			errs = append(errs, err)
		}
	}

	var crbList rbacv1.ClusterRoleBindingList
	if err := remoteClient.List(ctx, &crbList, labelSel); err != nil {
		return fmt.Errorf("list %s: %w", kindClusterRoleBinding, err)
	}
	for i := range crbList.Items {
		if err := p.deleteIfExists(ctx, remoteClient, &crbList.Items[i], asrName, clusterName, kindClusterRoleBinding); err != nil {
			errs = append(errs, err)
		}
	}

	var roleList rbacv1.RoleList
	if err := remoteClient.List(ctx, &roleList, labelSel); err != nil {
		return fmt.Errorf("list %s: %w", kindRole, err)
	}
	for i := range roleList.Items {
		if err := p.deleteIfExists(ctx, remoteClient, &roleList.Items[i], asrName, clusterName, kindRole); err != nil {
			errs = append(errs, err)
		}
	}

	var rbList rbacv1.RoleBindingList
	if err := remoteClient.List(ctx, &rbList, labelSel); err != nil {
		return fmt.Errorf("list %s: %w", kindRoleBinding, err)
	}
	for i := range rbList.Items {
		if err := p.deleteIfExists(ctx, remoteClient, &rbList.Items[i], asrName, clusterName, kindRoleBinding); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// deleteIfExists deletes a resource, ignoring not-found.
func (p *Phase) deleteIfExists(ctx context.Context, remoteClient client.Client, obj client.Object, asrName, clusterName, kind string) error {
	if err := remoteClient.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		ref := audit.ResourceRef{Kind: kind, Name: obj.GetName(), Cluster: clusterName}
		p.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, ref, err)
		return fmt.Errorf("delete %s %q: %w", kind, obj.GetName(), err)
	}
	return nil
}

// previouslyTargetedClusters returns the cluster names from
// status.providers.kubernetes.targets.
func previouslyTargetedClusters(asr *v1alpha1.ApplicationSpecificRole) []string {
	ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
	if ps == nil {
		return nil
	}
	names := make([]string, 0, len(ps.Targets))
	for _, t := range ps.Targets {
		names = append(names, t.TargetID)
	}
	return names
}

// compiledTargetToRefSet returns a set of appliedRefKey strings for all
// resources in a compiled target.
func compiledTargetToRefSet(t compiledTarget) map[string]struct{} {
	refs := compiledTargetToRefs(t)
	set := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		set[appliedRefKey(r)] = struct{}{}
	}
	return set
}

// appliedRefKey returns a unique string key for an AppliedResourceRef.
func appliedRefKey(r pmmeta.AppliedResourceRef) string {
	return r.Kind + "/" + r.Namespace + "/" + r.Name
}

// deleteSpecificRBAC deletes a single RBAC resource identified by ref.
func (p *Phase) deleteSpecificRBAC(ctx context.Context, remoteClient client.Client, ref pmmeta.AppliedResourceRef, asrName, clusterName string) error {
	if ref.Name != resourceNamePrefix+asrName {
		p.Log.Info("skipping orphan cleanup for unexpected resource name", "cluster", clusterName, "expectedName", resourceNamePrefix+asrName, "kind", ref.Kind, "resourceNamespace", ref.Namespace, "resourceName", ref.Name)
		return nil
	}
	switch ref.Kind {
	case kindClusterRole:
		obj := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: ref.Name}}
		return p.deleteIfExists(ctx, remoteClient, obj, asrName, clusterName, kindClusterRole)
	case kindClusterRoleBinding:
		obj := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: ref.Name}}
		return p.deleteIfExists(ctx, remoteClient, obj, asrName, clusterName, kindClusterRoleBinding)
	case kindRole:
		obj := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: ref.Namespace}}
		return p.deleteIfExists(ctx, remoteClient, obj, asrName, clusterName, kindRole)
	case kindRoleBinding:
		obj := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: ref.Namespace}}
		return p.deleteIfExists(ctx, remoteClient, obj, asrName, clusterName, kindRoleBinding)
	default:
		return fmt.Errorf("unknown RBAC kind %q", ref.Kind)
	}
}
