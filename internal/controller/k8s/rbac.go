package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/pkg/audit"
)

const resourceNamePrefix = "pm-"

const (
	kindClusterRole        = "ClusterRole"
	kindClusterRoleBinding = "ClusterRoleBinding"
	kindRole               = "Role"
	kindRoleBinding        = "RoleBinding"
)

func buildClusterScopedRBAC(asrName, specName string, verbs map[string]struct{}) (*rbacv1.ClusterRole, *rbacv1.ClusterRoleBinding) {
	resourceName := resourceNamePrefix + asrName
	labels := map[string]string{
		pmapis.LabelManagedByKey: pmapis.ManagedByPMController,
		pmapis.LabelASR:          asrName,
	}
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Labels: labels},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"*"},
			Resources: []string{"*"},
			Verbs:     sortedKeys(verbs),
		}},
	}
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Labels: labels},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     kindClusterRole,
			Name:     resourceName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:     "Group",
			Name:     specName,
			APIGroup: "rbac.authorization.k8s.io",
		}},
	}
	return cr, crb
}

func buildNamespaceScopedRBAC(asrName, specName, ns string, verbs map[string]struct{}) (*rbacv1.Role, *rbacv1.RoleBinding) {
	resourceName := resourceNamePrefix + asrName
	labels := map[string]string{
		pmapis.LabelManagedByKey: pmapis.ManagedByPMController,
		pmapis.LabelASR:          asrName,
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: ns, Labels: labels},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"*"},
			Resources: []string{"*"},
			Verbs:     sortedKeys(verbs),
		}},
	}
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: ns, Labels: labels},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     kindRole,
			Name:     resourceName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:     "Group",
			Name:     specName,
			APIGroup: "rbac.authorization.k8s.io",
		}},
	}
	return role, rb
}

func applyClusterScopedRBAC(ctx context.Context, c client.Client, cr *rbacv1.ClusterRole, crb *rbacv1.ClusterRoleBinding, asrName, clusterName string, al *audit.Logger) error {
	existing := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: cr.Name}}
	op, err := controllerutil.CreateOrPatch(ctx, c, existing, func() error {
		existing.Labels = cr.Labels
		existing.Rules = cr.Rules
		return nil
	})
	ref := audit.ResourceRef{Kind: kindClusterRole, Name: cr.Name, Cluster: clusterName}
	if err != nil {
		al.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, ref, err)
		return fmt.Errorf("%s %q: %w", kindClusterRole, cr.Name, err)
	}
	if op != controllerutil.OperationResultNone {
		al.Log(ctx, opToAction(op), audit.OutcomeSuccess, asrName, ref, nil)
	}

	existingCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: crb.Name}}
	op, err = controllerutil.CreateOrPatch(ctx, c, existingCRB, func() error {
		existingCRB.Labels = crb.Labels
		existingCRB.RoleRef = crb.RoleRef
		existingCRB.Subjects = crb.Subjects
		return nil
	})
	refCRB := audit.ResourceRef{Kind: kindClusterRoleBinding, Name: crb.Name, Cluster: clusterName}
	if err != nil {
		al.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, refCRB, err)
		return fmt.Errorf("%s %q: %w", kindClusterRoleBinding, crb.Name, err)
	}
	if op != controllerutil.OperationResultNone {
		al.Log(ctx, opToAction(op), audit.OutcomeSuccess, asrName, refCRB, nil)
	}

	return nil
}

func applyNamespaceScopedRBAC(ctx context.Context, c client.Client, role *rbacv1.Role, rb *rbacv1.RoleBinding, asrName, clusterName string, al *audit.Logger) error {
	existing := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: role.Name, Namespace: role.Namespace}}
	op, err := controllerutil.CreateOrPatch(ctx, c, existing, func() error {
		existing.Labels = role.Labels
		existing.Rules = role.Rules
		return nil
	})
	ref := audit.ResourceRef{Kind: kindRole, Name: role.Name, Namespace: role.Namespace, Cluster: clusterName}
	if err != nil {
		al.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, ref, err)
		return fmt.Errorf("%s %q in %q: %w", kindRole, role.Name, role.Namespace, err)
	}
	if op != controllerutil.OperationResultNone {
		al.Log(ctx, opToAction(op), audit.OutcomeSuccess, asrName, ref, nil)
	}

	if err := applyRoleBinding(ctx, c, rb, asrName, clusterName, al); err != nil {
		return err
	}

	return nil
}

// applyRoleBinding handles RoleBinding create/patch with roleRef migration (delete+recreate on roleRef change).
func applyRoleBinding(ctx context.Context, c client.Client, desired *rbacv1.RoleBinding, asrName, clusterName string, al *audit.Logger) error {
	existing := &rbacv1.RoleBinding{}
	ref := audit.ResourceRef{Kind: kindRoleBinding, Name: desired.Name, Namespace: desired.Namespace, Cluster: clusterName}

	err := c.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := c.Create(ctx, desired); err != nil {
			al.Log(ctx, audit.ActionCreate, audit.OutcomeFailure, asrName, ref, err)
			return fmt.Errorf("%s %q in %q: %w", kindRoleBinding, desired.Name, desired.Namespace, err)
		}
		al.Log(ctx, audit.ActionCreate, audit.OutcomeSuccess, asrName, ref, nil)
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s %q in %q: %w", kindRoleBinding, desired.Name, desired.Namespace, err)
	}

	// roleRef is immutable — delete and recreate if changed
	if existing.RoleRef != desired.RoleRef {
		if err := c.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			al.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, ref, err)
			return fmt.Errorf("%s %q in %q (roleRef migration delete): %w", kindRoleBinding, desired.Name, desired.Namespace, err)
		}
		if err := c.Create(ctx, desired); err != nil {
			al.Log(ctx, audit.ActionCreate, audit.OutcomeFailure, asrName, ref, err)
			return fmt.Errorf("%s %q in %q (roleRef migration create): %w", kindRoleBinding, desired.Name, desired.Namespace, err)
		}
		al.Log(ctx, audit.ActionCreate, audit.OutcomeSuccess, asrName, ref, nil)
		return nil
	}

	patch := client.MergeFrom(existing.DeepCopy())
	existing.Labels = desired.Labels
	existing.Subjects = desired.Subjects
	if err := c.Patch(ctx, existing, patch); err != nil {
		al.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, ref, err)
		return fmt.Errorf("%s %q in %q: %w", kindRoleBinding, desired.Name, desired.Namespace, err)
	}
	al.Log(ctx, audit.ActionUpdate, audit.OutcomeSuccess, asrName, ref, nil)
	return nil
}

// deleteIfExists deletes a resource if it exists, ignoring not-found errors.
func deleteIfExists(ctx context.Context, c client.Client, obj client.Object, asrName, clusterName, kind string, al *audit.Logger) {
	if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		ref := audit.ResourceRef{Kind: kind, Name: obj.GetName(), Cluster: clusterName}
		al.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, ref, err)
	}
}

// opToAction maps controllerutil.OperationResult to audit.Action.
func opToAction(op controllerutil.OperationResult) audit.Action {
	if op == controllerutil.OperationResultCreated {
		return audit.ActionCreate
	}
	return audit.ActionUpdate
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// deleteRBACByLabel deletes all RBAC resources owned by the given ASR on a remote
// cluster, identified by the pm-managed label. Aggregates errors across resource types.
func deleteRBACByLabel(ctx context.Context, c client.Client, asrName, clusterName string, al *audit.Logger) error {
	labelSel := client.MatchingLabels{pmapis.LabelASR: asrName}
	var errs []error

	var crList rbacv1.ClusterRoleList
	if err := c.List(ctx, &crList, labelSel); err != nil {
		return fmt.Errorf("list %s: %w", kindClusterRole, err)
	}
	for i := range crList.Items {
		deleteIfExists(ctx, c, &crList.Items[i], asrName, clusterName, kindClusterRole, al)
	}

	var crbList rbacv1.ClusterRoleBindingList
	if err := c.List(ctx, &crbList, labelSel); err != nil {
		return fmt.Errorf("list %s: %w", kindClusterRoleBinding, err)
	}
	for i := range crbList.Items {
		deleteIfExists(ctx, c, &crbList.Items[i], asrName, clusterName, kindClusterRoleBinding, al)
	}

	var roleList rbacv1.RoleList
	if err := c.List(ctx, &roleList, labelSel); err != nil {
		return fmt.Errorf("list %s: %w", kindRole, err)
	}
	for i := range roleList.Items {
		deleteIfExists(ctx, c, &roleList.Items[i], asrName, clusterName, kindRole, al)
	}

	var rbList rbacv1.RoleBindingList
	if err := c.List(ctx, &rbList, labelSel); err != nil {
		return fmt.Errorf("list %s: %w", kindRoleBinding, err)
	}
	for i := range rbList.Items {
		deleteIfExists(ctx, c, &rbList.Items[i], asrName, clusterName, kindRoleBinding, al)
	}

	return errors.Join(errs...)
}
