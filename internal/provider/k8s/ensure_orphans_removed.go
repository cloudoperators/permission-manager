// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
// Continue-only: errors are stashed on p.orphanErrs / p.orphanStatuses;
// fully-cleaned clusters are recorded on p.orphanCleaned for status pruning.
func (p *Phase) EnsureOrphansCleanedUp(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(ctx context.Context) (lifecycle.Result, error) {
		if asr.HasInvalidConfig() {
			return lifecycle.Continue(), nil
		}
		current := make(map[string]struct{}, len(p.compiled)+len(p.notReadyClusters)+len(p.unmatchedClusterPatterns))
		for _, t := range p.compiled {
			current[t.clusterName] = struct{}{}
		}
		// Not-ready clusters keep their existing RBAC: a transient blip must not
		// tear down access, and they are unreachable for cleanup anyway.
		for _, name := range p.notReadyClusters {
			current[name] = struct{}{}
		}
		// Missing clusters/patterns are intentional targets, not orphans.
		for _, name := range p.unmatchedClusterPatterns {
			current[name] = struct{}{}
		}

		ps := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
		if ps == nil {
			return lifecycle.Continue(), nil
		}

		for _, t := range ps.Targets {
			if _, stillTargeted := current[t.TargetID]; stillTargeted {
				continue
			}
			// Cluster gone from inventory: no remote to reach, prune instead of retrying forever.
			if !p.clusterKnown(t.TargetID, asr.Namespace) {
				p.Log.Info("orphan cluster no longer exists, pruning stale target", "namespace", asr.Namespace, "name", asr.Name, "cluster", t.TargetID)
				p.orphanCleaned = append(p.orphanCleaned, t.TargetID)
				continue
			}
			remoteClient, err := p.RemoteClient(ctx, p.Client, t.TargetID, asr.Namespace)
			if err != nil {
				p.Log.Error(err, "orphan cluster unreachable, will retry", "namespace", asr.Namespace, "name", asr.Name, "cluster", t.TargetID)
				p.orphanStatuses = append(p.orphanStatuses, clusterStatus{name: t.TargetID, err: err})
				p.orphanErrs = append(p.orphanErrs, err)
				continue
			}
			if err := p.deleteRBACByLabel(ctx, remoteClient, asr.Name, asr.Namespace, t.TargetID); err != nil {
				p.Log.Error(err, "failed to clean up orphan RBAC", "namespace", asr.Namespace, "name", asr.Name, "cluster", t.TargetID)
				p.orphanStatuses = append(p.orphanStatuses, clusterStatus{name: t.TargetID, err: err})
				p.orphanErrs = append(p.orphanErrs, err)
				continue
			}
			p.Log.Info("orphan RBAC cleaned up", "namespace", asr.Namespace, "name", asr.Name, "cluster", t.TargetID)
			p.orphanCleaned = append(p.orphanCleaned, t.TargetID)
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
			// Cluster gone from inventory: nothing to delete, skip so deletion can finish.
			if !p.clusterKnown(clusterName, asr.Namespace) {
				p.Log.Info("target cluster no longer exists, skipping cleanup", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
				continue
			}
			remoteClient, err := p.RemoteClient(ctx, p.Client, clusterName, asr.Namespace)
			if err != nil {
				p.Log.Error(err, "cluster unreachable during cleanup, will retry", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
				errs = append(errs, err)
				continue
			}
			if err := p.deleteRBACByLabel(ctx, remoteClient, asr.Name, asr.Namespace, clusterName); err != nil {
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
// ASR on a remote cluster. Selection is namespace-precise: two ASRs that share a
// name in different namespaces own disjoint objects, so the LabelSourceNamespace
// selector keeps one ASR's cleanup from revoking the other's RBAC.
func (p *Phase) deleteRBACByLabel(ctx context.Context, remoteClient client.Client, asrName, asrNamespace, clusterName string) error {
	labelSel := client.MatchingLabels{
		pmapis.LabelASR:             asrLabelValue(asrName),
		pmapis.LabelSourceNamespace: asrNamespace,
	}
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

// clusterKnown reports whether a cluster still exists in the informer store.
// A nil store (unit tests) is treated as known so cleanup proceeds.
func (p *Phase) clusterKnown(name, namespace string) bool {
	if p.ClusterStore == nil {
		return true
	}
	_, ok := getClusterFromStore(p.ClusterStore, namespace, name)
	return ok
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
	if !strings.HasPrefix(ref.Name, resourceNamePrefix+asrName) {
		p.Log.Info("skipping orphan cleanup for unexpected resource name", "cluster", clusterName, "expectedPrefix", resourceNamePrefix+asrName, "kind", ref.Kind, "resourceNamespace", ref.Namespace, "resourceName", ref.Name)
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

// EnsureSharedClusterRoleOrphansRemoved deletes shared ClusterRoles (labelled by
// CB) on each target cluster whose capability no longer exists in that CB's
// current capabilityMapping. This reclaims roles when a CB author drops a
// capability. Runs after EnsureRbacApplied. Continue-only and per-cluster
// non-fatal: a delete failure is logged and retried next reconcile.
func (p *Phase) EnsureSharedClusterRoleOrphansRemoved(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(ctx context.Context) (lifecycle.Result, error) {
		if asr.HasInvalidConfig() {
			return lifecycle.Continue(), nil
		}
		// desired[cb][sharedRoleName] holds every shared-role name each CB
		// currently defines (both root and baseline child), so any labelled
		// ClusterRole not in the set is an orphan.
		desired := make(map[string]map[string]struct{})
		for _, cb := range p.cbs {
			def, err := ParseConnectorDefinition(cb.Spec.Definition)
			if err != nil {
				continue
			}
			names := make(map[string]struct{})
			add := func(scope, capability string, entry CapabilityEntry) {
				if entry.ClusterRoleName != "" {
					return // direct binding: no shared role
				}
				names[sharedRoleName(cb.Spec.Name, scope, capability)] = struct{}{}
				names[baselineChildName(cb.Spec.Name, scope, capability)] = struct{}{}
			}
			for capn, e := range def.CapabilityMapping.Cluster {
				add(scopeCluster, strings.ToUpper(capn), e)
			}
			for capn, e := range def.CapabilityMapping.Namespace {
				add(scopeNamespace, strings.ToUpper(capn), e)
			}
			desired[cb.Spec.Name] = names
		}

		for _, clusterName := range p.targetClusterNames() {
			remoteClient, err := p.RemoteClient(ctx, p.Client, clusterName, asr.Namespace)
			if err != nil {
				p.Log.Error(err, "cluster unreachable for shared ClusterRole orphan cleanup, will retry",
					"namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
				continue
			}
			for cbName, keep := range desired {
				var list rbacv1.ClusterRoleList
				if err := remoteClient.List(ctx, &list, client.MatchingLabels{pmapis.LabelCB: cbName}); err != nil {
					p.Log.Error(err, "failed to list shared ClusterRoles", "cluster", clusterName, "cb", cbName)
					continue
				}
				for i := range list.Items {
					name := list.Items[i].Name
					if _, ok := keep[name]; ok {
						continue
					}
					if err := p.deleteIfExists(ctx, remoteClient, &list.Items[i], asr.Name, clusterName, kindClusterRole); err != nil {
						p.Log.Error(err, "failed to delete orphan shared ClusterRole", "cluster", clusterName, "role", name)
					}
				}
			}
		}
		return lifecycle.Continue(), nil
	}
}

// targetClusterNames returns the clusters this reconcile ensured shared roles
// on, from p.compiled. A cluster the ASR no longer compiles to has all its
// asr-labelled objects revoked by the cluster-level orphan path; shared roles
// lingering there are reclaimed when another ASR referencing the same CB
// reconciles against that cluster.
func (p *Phase) targetClusterNames() []string {
	set := make(map[string]struct{}, len(p.compiled))
	for _, t := range p.compiled {
		set[t.clusterName] = struct{}{}
	}
	return sortedKeys(set)
}
