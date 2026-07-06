// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
)

// EnsureRbacApplied applies each cluster's compiled RBAC via the remote client.
// Per-cluster failures are recorded on p.provisionStatuses / p.provisionErrs
// and surfaced by EnsureProviderStatus; other clusters are not affected.
//
// Skipped when InvalidConfig=True: the stale Provisioned condition is removed
// so the audit trail doesn't report success for an unreconcilable spec.
//
// Writes the Provisioned condition:
//   - True / ProvisioningSucceeded — all targets applied cleanly
//   - True / NoTargets             — p.compiled was empty
//   - False / ProvisioningFailed   — no targets succeeded
//   - False / PartialProvisioning  — some succeeded, some failed
func (p *Phase) EnsureRbacApplied(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(ctx context.Context) (lifecycle.Result, error) {
		if asr.HasInvalidConfig() {
			asr.RemoveCondition(pmmeta.ProvisionedCondition)
			return lifecycle.Continue(), nil
		}
		for _, target := range p.compiled {
			remoteClient, err := p.RemoteClient(ctx, p.Client, target.clusterName, asr.Namespace)
			if err != nil {
				p.Log.Error(err, "cluster unreachable, skipping", "namespace", asr.Namespace, "name", asr.Name, "cluster", target.clusterName)
				p.provisionStatuses = append(p.provisionStatuses, clusterStatus{name: target.clusterName, err: err})
				p.provisionErrs = append(p.provisionErrs, err)
				continue
			}

			var clusterErrs []error
			for i := range target.clusterRoles {
				if err := p.applyClusterScopedRBAC(ctx, remoteClient, &target.clusterRoles[i], &target.clusterRoleBindings[i], asr.Name, target.clusterName); err != nil {
					p.Log.Error(err, "failed to apply cluster-scoped RBAC", "namespace", asr.Namespace, "name", asr.Name, "cluster", target.clusterName)
					clusterErrs = append(clusterErrs, err)
				}
			}
			for i := range target.roles {
				if err := p.applyNamespaceScopedRBAC(ctx, remoteClient, &target.roles[i], &target.roleBindings[i], asr.Name, target.clusterName); err != nil {
					p.Log.Error(err, "failed to apply namespace-scoped RBAC", "namespace", asr.Namespace, "name", asr.Name, "cluster", target.clusterName, "targetNamespace", target.roles[i].Namespace)
					clusterErrs = append(clusterErrs, err)
				}
			}

			if len(clusterErrs) > 0 {
				aggregated := utilerrors.NewAggregate(clusterErrs)
				p.provisionStatuses = append(p.provisionStatuses, clusterStatus{name: target.clusterName, err: aggregated})
				p.provisionErrs = append(p.provisionErrs, aggregated)
				continue
			}
			p.provisionStatuses = append(p.provisionStatuses, clusterStatus{
				name:             target.clusterName,
				appliedResources: compiledTargetToRefs(target),
			})
		}

		total := len(p.compiled)
		failed := len(p.provisionErrs)
		switch {
		case total == 0:
			asr.SetCondition(pmmeta.TrueCondition(
				pmmeta.ProvisionedCondition,
				pmmeta.NoTargetsReason,
				"no matched targets to apply RBAC to",
			))
		case failed == 0:
			p.Log.Info("rbac(s) provisioned", "namespace", asr.Namespace, "name", asr.Name)
			asr.SetCondition(pmmeta.TrueCondition(
				pmmeta.ProvisionedCondition,
				pmmeta.ProvisioningSucceededReason,
				fmt.Sprintf("applied RBAC on %d/%d target(s)", total, total),
			))
		case failed == total:
			p.Log.Info("rbac apply failed on all targets", "namespace", asr.Namespace, "name", asr.Name, "errors", failed)
			asr.SetCondition(pmmeta.FalseCondition(
				pmmeta.ProvisionedCondition,
				pmmeta.ProvisioningFailedReason,
				fmt.Sprintf("failed to apply RBAC on all %d target(s): %v", total, utilerrors.NewAggregate(p.provisionErrs)),
			))
		default:
			p.Log.Info("not all rbac(s) were provisioned", "namespace", asr.Namespace, "name", asr.Name, "errors", failed)
			asr.SetCondition(pmmeta.FalseCondition(
				pmmeta.ProvisionedCondition,
				pmmeta.PartialProvisioningReason,
				fmt.Sprintf("applied %d/%d target(s); failures: %v", total-failed, total, utilerrors.NewAggregate(p.provisionErrs)),
			))
		}
		return lifecycle.Continue(), nil
	}
}

// applyClusterScopedRBAC create-or-patches a ClusterRole and its ClusterRoleBinding.
func (p *Phase) applyClusterScopedRBAC(ctx context.Context, remoteClient client.Client, cr *rbacv1.ClusterRole, crb *rbacv1.ClusterRoleBinding, asrName, clusterName string) error {
	existing := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: cr.Name}}
	op, err := controllerutil.CreateOrPatch(ctx, remoteClient, existing, func() error {
		existing.Labels = cr.Labels
		existing.Rules = cr.Rules
		return nil
	})
	ref := audit.ResourceRef{Kind: kindClusterRole, Name: cr.Name, Cluster: clusterName}
	if err != nil {
		p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, ref, err)
		return fmt.Errorf("%s %q: %w", kindClusterRole, cr.Name, err)
	}
	if op != controllerutil.OperationResultNone {
		p.AuditLog.Log(ctx, opToAction(op), audit.OutcomeSuccess, asrName, ref, nil)
	}

	existingCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: crb.Name}}
	op, err = controllerutil.CreateOrPatch(ctx, remoteClient, existingCRB, func() error {
		existingCRB.Labels = crb.Labels
		existingCRB.RoleRef = crb.RoleRef
		existingCRB.Subjects = crb.Subjects
		return nil
	})
	refCRB := audit.ResourceRef{Kind: kindClusterRoleBinding, Name: crb.Name, Cluster: clusterName}
	if err != nil {
		p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, refCRB, err)
		return fmt.Errorf("%s %q: %w", kindClusterRoleBinding, crb.Name, err)
	}
	if op != controllerutil.OperationResultNone {
		p.AuditLog.Log(ctx, opToAction(op), audit.OutcomeSuccess, asrName, refCRB, nil)
	}

	return nil
}

// applyNamespaceScopedRBAC create-or-patches a Role and its RoleBinding.
func (p *Phase) applyNamespaceScopedRBAC(ctx context.Context, remoteClient client.Client, role *rbacv1.Role, rb *rbacv1.RoleBinding, asrName, clusterName string) error {
	existing := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: role.Name, Namespace: role.Namespace}}
	op, err := controllerutil.CreateOrPatch(ctx, remoteClient, existing, func() error {
		existing.Labels = role.Labels
		existing.Rules = role.Rules
		return nil
	})
	ref := audit.ResourceRef{Kind: kindRole, Name: role.Name, Namespace: role.Namespace, Cluster: clusterName}
	if err != nil {
		p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, ref, err)
		return fmt.Errorf("%s %q in %q: %w", kindRole, role.Name, role.Namespace, err)
	}
	if op != controllerutil.OperationResultNone {
		p.AuditLog.Log(ctx, opToAction(op), audit.OutcomeSuccess, asrName, ref, nil)
	}

	return p.applyRoleBinding(ctx, remoteClient, rb, asrName, clusterName)
}

// applyRoleBinding handles RoleBinding create/patch. RoleRef is immutable, so
// a change forces delete+recreate.
func (p *Phase) applyRoleBinding(ctx context.Context, remoteClient client.Client, desired *rbacv1.RoleBinding, asrName, clusterName string) error {
	existing := &rbacv1.RoleBinding{}
	ref := audit.ResourceRef{Kind: kindRoleBinding, Name: desired.Name, Namespace: desired.Namespace, Cluster: clusterName}

	err := remoteClient.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := remoteClient.Create(ctx, desired); err != nil {
			p.AuditLog.Log(ctx, audit.ActionCreate, audit.OutcomeFailure, asrName, ref, err)
			return fmt.Errorf("%s %q in %q: %w", kindRoleBinding, desired.Name, desired.Namespace, err)
		}
		p.AuditLog.Log(ctx, audit.ActionCreate, audit.OutcomeSuccess, asrName, ref, nil)
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s %q in %q: %w", kindRoleBinding, desired.Name, desired.Namespace, err)
	}

	if existing.RoleRef != desired.RoleRef {
		if err := remoteClient.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			p.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, ref, err)
			return fmt.Errorf("%s %q in %q (roleRef migration delete): %w", kindRoleBinding, desired.Name, desired.Namespace, err)
		}
		if err := remoteClient.Create(ctx, desired); err != nil {
			p.AuditLog.Log(ctx, audit.ActionCreate, audit.OutcomeFailure, asrName, ref, err)
			return fmt.Errorf("%s %q in %q (roleRef migration create): %w", kindRoleBinding, desired.Name, desired.Namespace, err)
		}
		p.AuditLog.Log(ctx, audit.ActionCreate, audit.OutcomeSuccess, asrName, ref, nil)
		return nil
	}

	patch := client.MergeFrom(existing.DeepCopy())
	existing.Labels = desired.Labels
	existing.Subjects = desired.Subjects
	if err := remoteClient.Patch(ctx, existing, patch); err != nil {
		p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, ref, err)
		return fmt.Errorf("%s %q in %q: %w", kindRoleBinding, desired.Name, desired.Namespace, err)
	}
	p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeSuccess, asrName, ref, nil)
	return nil
}

// opToAction maps controllerutil.OperationResult to audit.Action.
func opToAction(op controllerutil.OperationResult) audit.Action {
	if op == controllerutil.OperationResultCreated {
		return audit.ActionCreate
	}
	return audit.ActionUpdate
}

// compiledTargetToRefs extracts the set of applied resource refs from a compiled target.
func compiledTargetToRefs(t compiledTarget) []pmmeta.AppliedResourceRef {
	var refs []pmmeta.AppliedResourceRef
	for _, cr := range t.clusterRoles {
		refs = append(refs, pmmeta.AppliedResourceRef{Kind: kindClusterRole, Name: cr.Name})
	}
	for _, crb := range t.clusterRoleBindings {
		refs = append(refs, pmmeta.AppliedResourceRef{Kind: kindClusterRoleBinding, Name: crb.Name})
	}
	for _, r := range t.roles {
		refs = append(refs, pmmeta.AppliedResourceRef{Kind: kindRole, Namespace: r.Namespace, Name: r.Name})
	}
	for _, rb := range t.roleBindings {
		refs = append(refs, pmmeta.AppliedResourceRef{Kind: kindRoleBinding, Namespace: rb.Namespace, Name: rb.Name})
	}
	return refs
}
