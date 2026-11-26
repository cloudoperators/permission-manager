// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// siEvaluationErrorReason marks a transient failure to evaluate references (Ready=Unknown).
const siEvaluationErrorReason = "EvaluationError"

// ServiceIdentityReconciler validates a ServiceIdentity's references and writes its status.
// It never writes RBAC (the ASR reconciler owns subjects) and uses no finalizer: a ServiceIdentity
// owns no remote objects, so deletion needs no cleanup (ADR-019 §2, §7, §10).
type ServiceIdentityReconciler struct {
	client.Client
	Log logr.Logger
}

// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=serviceidentities,verbs=get;list;watch
// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=serviceidentities/status,verbs=get;update;patch

// SetupWithManager registers the ServiceIdentity controller and its field indexes.
func (r *ServiceIdentityReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.ServiceIdentity{}, pmapis.ServiceIdentityRolesField,
		func(obj client.Object) []string {
			si, ok := obj.(*v1alpha1.ServiceIdentity)
			if !ok {
				return nil
			}
			return si.Spec.Roles
		}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.ServiceIdentity{}, pmapis.ServiceIdentityConnectorRefsField,
		func(obj client.Object) []string {
			si, ok := obj.(*v1alpha1.ServiceIdentity)
			if !ok {
				return nil
			}
			return si.Spec.ConnectorRefs
		}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ServiceIdentity{}).
		Watches(
			&v1alpha1.ApplicationSpecificRole{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueServiceIdentitiesForASR),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Watches(
			&v1alpha1.ConnectorBinding{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueServiceIdentitiesForConnectorBinding),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Complete(r)
}

// Reconcile validates references and patches status. No finalizer, no lifecycle harness: a
// ServiceIdentity owns no remote objects, so a hard delete needs no cleanup.
func (r *ServiceIdentityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	si := &v1alpha1.ServiceIdentity{}
	if err := r.Get(ctx, req.NamespacedName, si); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !si.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	orig := si.DeepCopy()
	err := r.reconcileStatus(ctx, si)
	if patchErr := r.patchStatus(ctx, orig, si); patchErr != nil && err == nil {
		err = patchErr
	}
	return ctrl.Result{}, err
}

// reconcileStatus evaluates reference validity and sets si's Ready condition. Validity is reference
// well-formedness, not referenced-ASR health (ADR-019 §8). On a client error it sets Ready=Unknown
// and returns the error so the object requeues.
func (r *ServiceIdentityReconciler) reconcileStatus(ctx context.Context, si *v1alpha1.ServiceIdentity) error {
	cbCache := map[string]*v1alpha1.ConnectorBinding{}

	siK8s, err := r.k8sConnectors(ctx, si.Namespace, si.Spec.ConnectorRefs, cbCache)
	if err != nil {
		r.setReady(si, metav1.ConditionUnknown, siEvaluationErrorReason, "failed to resolve connectors: "+err.Error())
		return err
	}
	// Not a kubernetes ServiceIdentity: clear any stale Ready we previously wrote and ignore it.
	if len(siK8s) == 0 {
		si.RemoveCondition(pmmeta.ReadyCondition)
		return nil
	}

	// A reserved system: username is invalid regardless of roles (also barred by the CRD).
	if strings.HasPrefix(si.Spec.Name, "system:") {
		r.setReady(si, metav1.ConditionFalse, string(pmmeta.InvalidUsernameReason),
			"spec.name must not start with the reserved 'system:' prefix")
		return nil
	}

	if len(si.Spec.Roles) == 0 {
		r.setReady(si, metav1.ConditionTrue, string(pmmeta.RolesResolvedReason), "no roles referenced")
		return nil
	}

	var dangling, mismatch []string
	for _, role := range si.Spec.Roles {
		asr := &v1alpha1.ApplicationSpecificRole{}
		if err := r.Get(ctx, types.NamespacedName{Name: role, Namespace: si.Namespace}, asr); err != nil {
			if apierrors.IsNotFound(err) {
				dangling = append(dangling, role)
				continue
			}
			r.setReady(si, metav1.ConditionUnknown, siEvaluationErrorReason, "failed to resolve role "+role+": "+err.Error())
			return err
		}
		asrK8s, err := r.k8sConnectors(ctx, asr.Namespace, asr.Spec.ConnectorRefs, cbCache)
		if err != nil {
			r.setReady(si, metav1.ConditionUnknown, siEvaluationErrorReason, "failed to resolve role connectors: "+err.Error())
			return err
		}
		switch {
		case len(asrK8s) == 0:
			dangling = append(dangling, role) // exists but not a kubernetes ASR
		case !sharesAny(siK8s, asrK8s):
			mismatch = append(mismatch, role)
		}
	}

	r.setValidity(si, dangling, mismatch)
	return nil
}

// setValidity writes a single Ready condition. Reason precedence is dangling > mismatch > resolved;
// when both problems exist the message lists both groups.
func (r *ServiceIdentityReconciler) setValidity(si *v1alpha1.ServiceIdentity, dangling, mismatch []string) {
	var parts []string
	if len(dangling) > 0 {
		slices.Sort(dangling)
		parts = append(parts, "roles not found as kubernetes ASRs: "+strings.Join(dangling, ", "))
	}
	if len(mismatch) > 0 {
		slices.Sort(mismatch)
		parts = append(parts, "roles do not share a kubernetes connector with this ServiceIdentity: "+strings.Join(mismatch, ", "))
	}
	switch {
	case len(dangling) > 0:
		r.setReady(si, metav1.ConditionFalse, string(pmmeta.DanglingRoleRefReason), strings.Join(parts, "; "))
	case len(mismatch) > 0:
		r.setReady(si, metav1.ConditionFalse, string(pmmeta.ConnectorMismatchReason), strings.Join(parts, "; "))
	default:
		r.setReady(si, metav1.ConditionTrue, string(pmmeta.RolesResolvedReason),
			"all roles resolve to kubernetes ASRs sharing a connector")
	}
}

// setReady upserts the Ready condition, stamping the observed generation.
func (r *ServiceIdentityReconciler) setReady(si *v1alpha1.ServiceIdentity, status metav1.ConditionStatus, reason, msg string) {
	si.SetCondition(metav1.Condition{
		Type:               string(pmmeta.ReadyCondition),
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: si.Generation,
	})
}

// patchStatus writes the status subresource only when conditions changed.
func (r *ServiceIdentityReconciler) patchStatus(ctx context.Context, orig, si *v1alpha1.ServiceIdentity) error {
	if equality.Semantic.DeepEqual(orig.Status, si.Status) {
		return nil
	}
	return r.Status().Patch(ctx, si, client.MergeFrom(orig))
}

// k8sConnectors returns the subset of refs that name a provider: kubernetes ConnectorBinding
// in ns. Missing bindings are treated as non-kubernetes (skipped), not errors.
func (r *ServiceIdentityReconciler) k8sConnectors(ctx context.Context, ns string, refs []string, cache map[string]*v1alpha1.ConnectorBinding) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	for _, ref := range refs {
		cb, cached := cache[ref]
		if !cached {
			cb = &v1alpha1.ConnectorBinding{}
			if err := r.Get(ctx, types.NamespacedName{Name: ref, Namespace: ns}, cb); err != nil {
				if apierrors.IsNotFound(err) {
					cache[ref] = nil
					continue
				}
				return nil, err
			}
			cache[ref] = cb
		}
		if cb != nil && cb.Spec.Provider == string(pmmeta.ProviderKubernetes) {
			out[ref] = struct{}{}
		}
	}
	return out, nil
}

// sharesAny reports whether a and b intersect.
func sharesAny(a, b map[string]struct{}) bool {
	for k := range a {
		if _, ok := b[k]; ok {
			return true
		}
	}
	return false
}

// enqueueServiceIdentitiesForASR enqueues ServiceIdentities (same namespace) whose roles
// reference the changed ASR, so they re-validate.
func (r *ServiceIdentityReconciler) enqueueServiceIdentitiesForASR(ctx context.Context, obj client.Object) []ctrl.Request {
	return r.requestsForRef(ctx, obj, pmapis.ServiceIdentityRolesField)
}

// enqueueServiceIdentitiesForConnectorBinding enqueues ServiceIdentities (same namespace)
// that reference the changed ConnectorBinding, so they re-validate.
func (r *ServiceIdentityReconciler) enqueueServiceIdentitiesForConnectorBinding(ctx context.Context, obj client.Object) []ctrl.Request {
	return r.requestsForRef(ctx, obj, pmapis.ServiceIdentityConnectorRefsField)
}

func (r *ServiceIdentityReconciler) requestsForRef(ctx context.Context, obj client.Object, field string) []ctrl.Request {
	var list v1alpha1.ServiceIdentityList
	if err := r.List(ctx, &list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{field: obj.GetName()},
	); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list ServiceIdentities", "field", field, "name", obj.GetName())
		return nil
	}
	requests := make([]ctrl.Request, 0, len(list.Items))
	for i := range list.Items {
		si := &list.Items[i]
		if !si.DeletionTimestamp.IsZero() {
			continue
		}
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: si.Name, Namespace: si.Namespace},
		})
	}
	return requests
}
