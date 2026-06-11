// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
	"github.com/cloudoperators/permission-manager/controller/pkg/ccrn"
	"github.com/cloudoperators/permission-manager/controller/pkg/naming"
)

const (
	// LabelManagedBy is the standard Kubernetes managed-by label.
	LabelManagedBy = naming.LabelManagedByKey
	// LabelASRName identifies the source ASR for generated RBAC resources.
	LabelASRName = naming.LabelASR
	// ManagedByValue is the value for the managed-by label.
	ManagedByValue = naming.ManagedByPMController
	// ResourceNamePrefix is the prefix for all generated RBAC resource names.
	ResourceNamePrefix = "pm-"
)

// ClusterProvisioningStatus tracks the provisioning state for a single cluster.
type ClusterProvisioningStatus struct {
	ClusterName string
	Status      string // "Success", "Error", "Unreachable"
	Message     string
	LastUpdated metav1.Time
}

// clusterScope holds the parsed permissions for a specific cluster, separated
// into cluster-scoped and namespace-scoped groups.
type clusterScope struct {
	// clusterVerbs are verbs for cluster-wide RBAC (from cluster CCRN resources).
	clusterVerbs map[string]struct{}
	// namespaceVerbs maps namespace -> verb set for namespace-scoped RBAC.
	namespaceVerbs map[string]map[string]struct{}
}

// rbacPlan holds the planned RBAC resources to create for a single cluster.
type rbacPlan struct {
	clusterRole        *rbacv1.ClusterRole        // pm-<asr>: cluster-scoped verbs only
	nsClusterRole      *rbacv1.ClusterRole        // pm-<asr>-ns: namespace-scoped verbs only
	clusterRoleBinding *rbacv1.ClusterRoleBinding // binds pm-<asr> cluster-wide
	roleBindings       []*rbacv1.RoleBinding      // bind pm-<asr>-ns per namespace
}

// buildClusterScopes parses ASR permissions and groups them by cluster,
// splitting into cluster-scoped and namespace-scoped permissions.
// matchClusters returns the list of cluster names matching a given pattern
// (supporting wildcards like "nw-a-*").
//
// Returns the cluster scopes and a deduplicated list of unmapped capability names.
func buildClusterScopes(asr *pmv1.ApplicationSpecificRole, matchClusters func(string) []string) (map[string]*clusterScope, []string) {
	scopes := make(map[string]*clusterScope)
	unmappedSeen := make(map[string]struct{})
	var unmappedCaps []string

	for _, perm := range asr.Spec.Permissions {
		parsed, err := ccrn.Parse(perm.Resource)
		if err != nil {
			continue
		}
		if !parsed.IsKubernetes() {
			continue
		}

		verbs, ok := CapabilityVerbs[strings.ToUpper(perm.Capability)]
		if !ok {
			// Track unmapped capability (deduplicated)
			cap := strings.ToUpper(perm.Capability)
			if _, seen := unmappedSeen[cap]; !seen {
				unmappedSeen[cap] = struct{}{}
				unmappedCaps = append(unmappedCaps, cap)
			}
			continue
		}

		clusters := matchClusters(parsed.ClusterName)
		if len(clusters) == 0 {
			continue
		}

		for _, clusterName := range clusters {
			scope, exists := scopes[clusterName]
			if !exists {
				scope = &clusterScope{
					clusterVerbs:   make(map[string]struct{}),
					namespaceVerbs: make(map[string]map[string]struct{}),
				}
				scopes[clusterName] = scope
			}

			switch parsed.Type {
			case ccrn.ResourceTypeCluster:
				for _, v := range verbs {
					scope.clusterVerbs[v] = struct{}{}
				}
			case ccrn.ResourceTypeNamespace:
				nsVerbs, exists := scope.namespaceVerbs[parsed.Namespace]
				if !exists {
					nsVerbs = make(map[string]struct{})
					scope.namespaceVerbs[parsed.Namespace] = nsVerbs
				}
				for _, v := range verbs {
					nsVerbs[v] = struct{}{}
				}
			}
		}
	}

	return scopes, unmappedCaps
}

// sortedKeys returns sorted string keys from a set.
func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// buildRBACPlan creates the RBAC resource plan for a single cluster.
// It produces two separate ClusterRoles with non-overlapping verb sets:
//   - pm-<asr>: contains ONLY cluster-scoped verbs, bound via ClusterRoleBinding
//   - pm-<asr>-ns: contains ONLY the union of namespace verbs, bound via RoleBindings
//
// When only one scope has verbs, only that ClusterRole is produced.
func buildRBACPlan(asrName, specName string, scope *clusterScope) *rbacPlan {
	plan := &rbacPlan{}
	resourceName := ResourceNamePrefix + asrName
	nsResourceName := resourceName + "-ns"
	labels := map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelASRName:   asrName,
	}

	subject := rbacv1.Subject{
		Kind:     "Group",
		Name:     specName,
		APIGroup: "rbac.authorization.k8s.io",
	}

	// Build cluster-scoped ClusterRole + ClusterRoleBinding if there are cluster verbs
	if len(scope.clusterVerbs) > 0 {
		plan.clusterRole = &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name:   resourceName,
				Labels: labels,
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"*"},
					Resources: []string{"*"},
					Verbs:     sortedKeys(scope.clusterVerbs),
				},
			},
		}

		plan.clusterRoleBinding = &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:   resourceName,
				Labels: labels,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     resourceName,
			},
			Subjects: []rbacv1.Subject{subject},
		}
	}

	// Build namespace-scoped ClusterRole + RoleBindings if there are namespace verbs
	if len(scope.namespaceVerbs) > 0 {
		// Collect the union of all namespace verbs
		allNsVerbs := make(map[string]struct{})
		for _, nsVerbs := range scope.namespaceVerbs {
			for v := range nsVerbs {
				allNsVerbs[v] = struct{}{}
			}
		}

		if len(allNsVerbs) > 0 {
			plan.nsClusterRole = &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name:   nsResourceName,
					Labels: labels,
				},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{"*"},
						Resources: []string{"*"},
						Verbs:     sortedKeys(allNsVerbs),
					},
				},
			}

			// For each namespace, create a RoleBinding referencing the ns ClusterRole
			namespaces := make([]string, 0, len(scope.namespaceVerbs))
			for ns := range scope.namespaceVerbs {
				namespaces = append(namespaces, ns)
			}
			sort.Strings(namespaces)

			for _, ns := range namespaces {
				plan.roleBindings = append(plan.roleBindings, &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: ns,
						Labels:    labels,
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "ClusterRole",
						Name:     nsResourceName,
					},
					Subjects: []rbacv1.Subject{subject},
				})
			}
		}
	}

	return plan
}

// applyRBACPlan applies the RBAC plan to the target cluster using a GET +
// MergeFrom patch approach for existing resources, falling back to Create
// for new ones. It also cleans up stale resources when a scope becomes empty.
// It emits audit records for each resource operation.
func applyRBACPlan(ctx context.Context, c ClusterClient, plan *rbacPlan, asrName, clusterName string, al *audit.Logger) error {
	// Derive resource names from the ASR name in labels
	var resourceName, nsResourceName string
	if plan.clusterRole != nil {
		resourceName = plan.clusterRole.Name
		nsResourceName = resourceName + "-ns"
	} else if plan.nsClusterRole != nil {
		nsResourceName = plan.nsClusterRole.Name
		resourceName = nsResourceName[:len(nsResourceName)-3]
	}

	// Apply cluster-scoped ClusterRole
	if plan.clusterRole != nil {
		cr := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: plan.clusterRole.Name,
			},
		}
		opResult, err := createOrPatch(ctx, c, cr, func() {
			cr.Labels = plan.clusterRole.Labels
			cr.Rules = plan.clusterRole.Rules
		})
		ref := audit.ResourceRef{Kind: "ClusterRole", Name: plan.clusterRole.Name, Cluster: clusterName}
		if err != nil {
			al.Log(ctx, operationResultToAction(opResult), audit.OutcomeFailure, asrName, ref, err)
			return fmt.Errorf("failed to apply ClusterRole %q: %w", plan.clusterRole.Name, err)
		}
		if opResult != OperationResultNone {
			al.Log(ctx, operationResultToAction(opResult), audit.OutcomeSuccess, asrName, ref, nil)
		}
	} else if resourceName != "" {
		// Stale cleanup: delete cluster-scoped ClusterRole and ClusterRoleBinding
		deleteIfExists(ctx, c, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: resourceName}}, asrName, clusterName, "ClusterRole", al)
		deleteIfExists(ctx, c, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: resourceName}}, asrName, clusterName, "ClusterRoleBinding", al)
	}

	// Apply ClusterRoleBinding
	if plan.clusterRoleBinding != nil {
		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: plan.clusterRoleBinding.Name,
			},
		}
		opResult, err := createOrPatch(ctx, c, crb, func() {
			crb.Labels = plan.clusterRoleBinding.Labels
			crb.RoleRef = plan.clusterRoleBinding.RoleRef
			crb.Subjects = plan.clusterRoleBinding.Subjects
		})
		ref := audit.ResourceRef{Kind: "ClusterRoleBinding", Name: plan.clusterRoleBinding.Name, Cluster: clusterName}
		if err != nil {
			al.Log(ctx, operationResultToAction(opResult), audit.OutcomeFailure, asrName, ref, err)
			return fmt.Errorf("failed to apply ClusterRoleBinding %q: %w", plan.clusterRoleBinding.Name, err)
		}
		if opResult != OperationResultNone {
			al.Log(ctx, operationResultToAction(opResult), audit.OutcomeSuccess, asrName, ref, nil)
		}
	}

	// Apply namespace-scoped ClusterRole
	if plan.nsClusterRole != nil {
		cr := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: plan.nsClusterRole.Name,
			},
		}
		opResult, err := createOrPatch(ctx, c, cr, func() {
			cr.Labels = plan.nsClusterRole.Labels
			cr.Rules = plan.nsClusterRole.Rules
		})
		ref := audit.ResourceRef{Kind: "ClusterRole", Name: plan.nsClusterRole.Name, Cluster: clusterName}
		if err != nil {
			al.Log(ctx, operationResultToAction(opResult), audit.OutcomeFailure, asrName, ref, err)
			return fmt.Errorf("failed to apply ClusterRole %q: %w", plan.nsClusterRole.Name, err)
		}
		if opResult != OperationResultNone {
			al.Log(ctx, operationResultToAction(opResult), audit.OutcomeSuccess, asrName, ref, nil)
		}
	} else if nsResourceName != "" {
		// Stale cleanup: delete namespace-scoped ClusterRole
		deleteIfExists(ctx, c, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: nsResourceName}}, asrName, clusterName, "ClusterRole", al)
	}

	// Apply RoleBindings with roleRef migration handling
	for _, rb := range plan.roleBindings {
		target := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rb.Name,
				Namespace: rb.Namespace,
			},
		}
		opResult, err := createOrPatchRoleBinding(ctx, c, target, rb)
		ref := audit.ResourceRef{Kind: "RoleBinding", Name: rb.Name, Namespace: rb.Namespace, Cluster: clusterName}
		if err != nil {
			al.Log(ctx, operationResultToAction(opResult), audit.OutcomeFailure, asrName, ref, err)
			return fmt.Errorf("failed to apply RoleBinding %q in namespace %q: %w", rb.Name, rb.Namespace, err)
		}
		if opResult != OperationResultNone {
			al.Log(ctx, operationResultToAction(opResult), audit.OutcomeSuccess, asrName, ref, nil)
		}
	}

	return nil
}

// createOrPatchRoleBinding handles RoleBinding application with roleRef migration.
// Since roleRef is immutable in Kubernetes, if an existing RoleBinding has a different
// roleRef than desired, it must be deleted and recreated.
func createOrPatchRoleBinding(ctx context.Context, c client.Client, target *rbacv1.RoleBinding, desired *rbacv1.RoleBinding) (OperationResult, error) {
	key := client.ObjectKeyFromObject(target)
	if err := c.Get(ctx, key, target); err != nil {
		if !apierrors.IsNotFound(err) {
			return OperationResultNone, err
		}
		// Object does not exist — create
		target.Labels = desired.Labels
		target.RoleRef = desired.RoleRef
		target.Subjects = desired.Subjects
		return OperationResultCreated, c.Create(ctx, target)
	}

	// Object exists — check if roleRef changed (immutable field)
	if target.RoleRef.Name != desired.RoleRef.Name {
		// Delete and recreate since roleRef cannot be patched
		if err := c.Delete(ctx, target); err != nil {
			return OperationResultNone, fmt.Errorf("failed to delete RoleBinding with stale roleRef: %w", err)
		}
		newRB := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      desired.Name,
				Namespace: desired.Namespace,
			},
		}
		newRB.Labels = desired.Labels
		newRB.RoleRef = desired.RoleRef
		newRB.Subjects = desired.Subjects
		return OperationResultPatched, c.Create(ctx, newRB)
	}

	// roleRef matches — patch other fields
	before := target.DeepCopyObject().(client.Object)
	target.Labels = desired.Labels
	target.RoleRef = desired.RoleRef
	target.Subjects = desired.Subjects
	return OperationResultPatched, c.Patch(ctx, target, client.MergeFrom(before))
}

// deleteIfExists attempts to delete a Kubernetes resource, ignoring NotFound errors.
// It emits an audit record on successful deletion.
func deleteIfExists(ctx context.Context, c ClusterClient, obj client.Object, asrName, clusterName, kind string, al *audit.Logger) {
	name := obj.GetName()
	ns := obj.GetNamespace()
	err := c.Delete(ctx, obj)
	if err != nil && !apierrors.IsNotFound(err) {
		// Log but don't fail — best effort cleanup
		ref := audit.ResourceRef{Kind: kind, Name: name, Namespace: ns, Cluster: clusterName}
		al.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, ref, err)
		return
	}
	if err == nil {
		ref := audit.ResourceRef{Kind: kind, Name: name, Namespace: ns, Cluster: clusterName}
		al.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, ref, nil)
	}
}

// expandNamespaceWildcards expands wildcard namespace patterns in the scope.
//   - namespace "*" promotes its verbs to clusterVerbs (scope promotion)
//   - prefix wildcards expand against the namespace list
//   - exact namespaces are validated to exist
//
// Returns an error if listNamespaces fails (caller should skip the cluster).
func expandNamespaceWildcards(ctx context.Context, listNamespaces func(ctx context.Context) ([]string, error), scope *clusterScope, log logr.Logger) error {
	// Handle bare wildcard first (no namespace list needed)
	if verbs, ok := scope.namespaceVerbs["*"]; ok {
		for v := range verbs {
			scope.clusterVerbs[v] = struct{}{}
		}
		delete(scope.namespaceVerbs, "*")
	}

	if len(scope.namespaceVerbs) == 0 {
		return nil
	}

	// Fetch namespace list (once per cluster)
	allNamespaces, err := listNamespaces(ctx)
	if err != nil {
		return err
	}
	nsSet := make(map[string]struct{}, len(allNamespaces))
	for _, ns := range allNamespaces {
		nsSet[ns] = struct{}{}
	}

	// Process each namespace entry
	expanded := make(map[string]map[string]struct{})
	for ns, verbs := range scope.namespaceVerbs {
		if ccrn.IsWildcard(ns) {
			// Prefix wildcard: expand
			matches := 0
			for _, realNs := range allNamespaces {
				if ccrn.MatchGlob(ns, realNs) {
					matches++
					existing, ok := expanded[realNs]
					if !ok {
						existing = make(map[string]struct{})
						expanded[realNs] = existing
					}
					for v := range verbs {
						existing[v] = struct{}{}
					}
				}
			}
			if matches == 0 {
				log.V(1).Info("namespace wildcard matched zero namespaces", "pattern", ns)
			}
		} else {
			// Exact namespace: validate exists
			if _, exists := nsSet[ns]; !exists {
				log.V(1).Info("namespace does not exist on cluster, skipping", "namespace", ns)
				continue
			}
			existing, ok := expanded[ns]
			if !ok {
				existing = make(map[string]struct{})
				expanded[ns] = existing
			}
			for v := range verbs {
				existing[v] = struct{}{}
			}
		}
	}

	scope.namespaceVerbs = expanded
	return nil
}

// OperationResult indicates what createOrPatch did.
type OperationResult string

const (
	OperationResultCreated OperationResult = "created"
	OperationResultPatched OperationResult = "patched"
	OperationResultNone    OperationResult = "none"
)

// createOrPatch attempts to GET the object; if not found, it calls mutate and
// Creates it. If found, it takes a MergeFrom snapshot, calls mutate, and
// Patches the object. Returns the operation performed.
func createOrPatch(ctx context.Context, c client.Client, obj client.Object, mutate func()) (OperationResult, error) {
	key := client.ObjectKeyFromObject(obj)
	if err := c.Get(ctx, key, obj); err != nil {
		if !apierrors.IsNotFound(err) {
			return OperationResultNone, err
		}
		// Object does not exist — create
		mutate()
		return OperationResultCreated, c.Create(ctx, obj)
	}
	// Object exists — patch
	before := obj.DeepCopyObject().(client.Object)
	mutate()
	return OperationResultPatched, c.Patch(ctx, obj, client.MergeFrom(before))
}

// operationResultToAction converts an OperationResult to an audit.Action.
func operationResultToAction(result OperationResult) audit.Action {
	if result == OperationResultCreated {
		return audit.ActionCreate
	}
	return audit.ActionUpdate
}

