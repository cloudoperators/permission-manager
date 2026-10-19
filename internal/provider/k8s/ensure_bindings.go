// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
)

// EnsureConnectorBindings fetches every kubernetes ConnectorBinding the ASR
// references and stashes them on p.cbs. Skips refs that fail to Get or have
// no usable definition (parseable + non-empty capabilityMapping).
//
// Breaks the chain when no usable CB is available: failUnusableCB revokes RBAC
// from every previously-targeted cluster, then stamps the misconfig conditions.
func (p *Phase) EnsureConnectorBindings(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(ctx context.Context) (lifecycle.Result, error) {
		for _, ref := range asr.Spec.ConnectorRefs {
			cb := &v1alpha1.ConnectorBinding{}
			if err := p.Client.Get(ctx, types.NamespacedName{Name: ref, Namespace: asr.Namespace}, cb); err != nil {
				p.Log.Error(err, "failed to fetch ConnectorBinding, skipping", "namespace", asr.Namespace, "name", asr.Name, "connectorBinding", ref)
				continue
			}
			if cb.Spec.Provider == string(pmmeta.ProviderKubernetes) {
				p.cbs = append(p.cbs, cb)
			}
		}

		if len(p.cbs) == 0 {
			msg := "no kubernetes ConnectorBindings referenced by ASR"
			return p.failUnusableCB(ctx, asr, pmmeta.MissingConnectorBindingReason, msg)
		}

		resourceOnly := asrIsResourceOnly(p.CCRNParser, asr)

		usable := 0
		var invalidNames []string
		for _, cb := range p.cbs {
			def, err := ValidateConnectorBinding(cb)
			if err != nil {
				p.Log.Error(err, "skipping unusable ConnectorBinding", "namespace", asr.Namespace, "name", asr.Name, "connectorBinding", cb.Name)
				if nameErr := validateConnectorBindingName(cb.Spec.Name); nameErr != nil {
					invalidNames = append(invalidNames, cb.Spec.Name)
				}
				continue
			}
			if def.CapabilityMapping.empty() && !resourceOnly {
				p.Log.Info("ConnectorBinding has empty capabilityMapping", "namespace", asr.Namespace, "name", asr.Name, "connectorBinding", cb.Name)
				continue
			}
			usable++
		}

		if usable == 0 {
			if len(invalidNames) > 0 {
				msg := fmt.Sprintf("ConnectorBinding names are not valid DNS-1123 labels: %v", invalidNames)
				return p.failUnusableCB(ctx, asr, pmmeta.InvalidConnectorBindingReason, msg)
			}
			msg := fmt.Sprintf("no usable ConnectorBinding definitions among %d referenced", len(p.cbs))
			return p.failUnusableCB(ctx, asr, pmmeta.MissingConnectorBindingReason, msg)
		}

		asr.ClearInvalidConfigIfReason(pmmeta.MissingConnectorBindingReason, pmmeta.InvalidConnectorBindingReason)
		return lifecycle.Continue(), nil
	}
}

// failUnusableCB is the Break path when no usable kubernetes ConnectorBinding
// is available. It revokes RBAC from every previously-targeted cluster, stamps
// InvalidConfig=True/<reason> (mirrored on Ready=False), and removes the stale
// Provisioned condition. reason distinguishes an absent/empty binding from one
// rejected for a label-unsafe name.
//
// Clusters where revoke fails (unreachable, delete error) remain in
// status.providers.kubernetes.targets so the next reconcile retries them.
func (p *Phase) failUnusableCB(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole, reason pmmeta.ConditionReason, msg string) (lifecycle.Result, error) {
	prior := previouslyTargetedClusters(asr)
	stillTargeted := make([]pmmeta.TargetStatus, 0, len(prior))
	for _, clusterName := range prior {
		remoteClient, err := p.RemoteClient(ctx, p.Client, clusterName, asr.Namespace)
		if err != nil {
			p.Log.Error(err, "cluster unreachable during CB-missing revoke, will retry", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
			stillTargeted = append(stillTargeted, pmmeta.TargetStatus{TargetID: clusterName, Status: pmmeta.TargetStatusError, Message: err.Error()})
			continue
		}
		if err := p.deleteRBACByLabel(ctx, remoteClient, asr.Name, asr.Namespace, clusterName); err != nil {
			p.Log.Error(err, "failed to revoke RBAC during CB-missing revoke, will retry", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
			stillTargeted = append(stillTargeted, pmmeta.TargetStatus{TargetID: clusterName, Status: pmmeta.TargetStatusError, Message: err.Error()})
			continue
		}
		p.Log.Info("RBAC revoked due to missing ConnectorBinding", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
	}

	asr.SetCondition(pmmeta.TrueCondition(
		pmmeta.InvalidConfigCondition,
		reason,
		msg,
	))
	asr.SetCondition(pmmeta.FalseCondition(
		pmmeta.ReadyCondition,
		reason,
		msg,
	))
	asr.RemoveCondition(pmmeta.ProvisionedCondition)

	// Preserve any targets we couldn't successfully revoke so the next
	// reconcile retries them.
	degraded := pmmeta.DegradedProviderStatus(msg)
	degraded.Targets = stillTargeted
	asr.Status.Providers = pmmeta.SetProviderStatus(
		asr.Status.Providers,
		pmmeta.ProviderKubernetes,
		degraded,
	)

	return lifecycle.Break(), errors.New(msg)
}

// asrIsResourceOnly reports whether the ASR's kubernetes permissions are served
// entirely by resource-scoped CCRNs, which never consult the ConnectorBinding
// capabilityMapping — so an empty mapping is acceptable. Non-kubernetes and
// unparseable permissions are skipped: the former are served by other providers,
// and the latter are reported by the validator. A coarse cluster.k8s/namespace.k8s
// CCRN needs the mapping, so it makes the ASR not resource-only. At least one
// resource CCRN must be present; an ASR with no kubernetes resource CCRNs has
// nothing to relax the mapping requirement for.
func asrIsResourceOnly(parser *ccrn.Parser, asr *v1alpha1.ApplicationSpecificRole) bool {
	if parser == nil {
		parser = ccrn.NewParser(nil)
	}
	sawResourceCCRN := false
	for _, perm := range asr.Spec.Permissions {
		parsed, err := parser.Parse(perm.Resource)
		if err != nil || !parsed.IsKubernetes() {
			continue
		}
		if parsed.K8sResource == nil {
			return false
		}
		sawResourceCCRN = true
	}
	return sawResourceCCRN
}

// EnsureSharedClusterRoles creates or patches, on every Ready target cluster the
// ASR's coarse CCRNs resolve to, the aggregated root and baseline child
// ClusterRoles for each capability the referenced CBs define. The cluster MANAGE
// direct-binding capability has no shared role and is skipped. Per-cluster
// failures are logged and recorded but never fatal: a missing shared role surfaces
// later as a binding to a non-existent role, not a reconcile stall.
//
// Runs before EnsureRbacCompiled so compiled bindings reference roles that exist.
func (p *Phase) EnsureSharedClusterRoles(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(ctx context.Context) (lifecycle.Result, error) {
		if asr.HasInvalidConfig() {
			return lifecycle.Continue(), nil
		}
		parser := p.CCRNParser
		if parser == nil {
			parser = ccrn.NewParser(nil)
		}

		// cluster name -> remote client, built at most once per reconcile.
		clients := make(map[string]client.Client)
		remoteFor := func(clusterName string) (client.Client, bool) {
			if c, ok := clients[clusterName]; ok {
				return c, c != nil
			}
			c, err := p.RemoteClient(ctx, p.Client, clusterName, asr.Namespace)
			if err != nil {
				p.Log.Error(err, "cluster unreachable for shared ClusterRole ensure, skipping",
					"namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
				clients[clusterName] = nil
				return nil, false
			}
			clients[clusterName] = c
			return c, true
		}

		// dedupe (cluster, roleName) so one role is ensured once even when many
		// permissions resolve to it.
		ensured := make(map[string]struct{})

		for _, perm := range asr.Spec.Permissions {
			parsed, err := parser.Parse(perm.Resource)
			if err != nil || !parsed.IsKubernetes() || parsed.K8sResource != nil {
				continue
			}
			scope := scopeNamespace
			if parsed.Namespace == "" {
				scope = scopeCluster
			}
			resolved, found := resolveCapability(p.cbs, scope, perm.Capability)
			if !found {
				continue
			}
			matches := matchClusterPattern(p.ClusterStore, parsed.ClusterName, asr.Namespace)
			for _, clusterName := range matches.ready {
				remoteClient, ok := remoteFor(clusterName)
				if !ok {
					continue
				}
				for _, rc := range resolved {
					if rc.entry.ClusterRoleName != "" {
						continue // direct binding: no shared role
					}
					key := clusterName + "/" + sharedRoleName(rc.cbName, scope, strings.ToUpper(perm.Capability))
					if _, done := ensured[key]; done {
						continue
					}
					ensured[key] = struct{}{}
					p.ensureSharedPair(ctx, remoteClient, rc, scope, strings.ToUpper(perm.Capability), asr.Name, clusterName)
				}
			}
		}
		return lifecycle.Continue(), nil
	}
}

// ensureSharedPair create-or-patches the aggregated root and baseline child for
// one capability on one cluster. Failures are logged, not returned.
func (p *Phase) ensureSharedPair(ctx context.Context, remoteClient client.Client, rc resolvedCapability, scope, capability, asrName, clusterName string) {
	root := buildAggregatedRoot(rc.cbName, scope, capability, rc.entry.ClusterRoleSelectors)
	child := buildBaselineChild(rc.cbName, scope, capability, rc.entry.Rules)
	for _, desired := range []*rbacv1.ClusterRole{child, root} {
		existing := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: desired.Name}}
		op, err := controllerutil.CreateOrPatch(ctx, remoteClient, existing, func() error {
			existing.Labels = desired.Labels
			existing.AggregationRule = desired.AggregationRule
			// The aggregated root carries only an AggregationRule; its Rules are
			// owned and continuously repopulated by the Kubernetes aggregation
			// controller, so writing the root's empty Rules here would revoke the
			// aggregated access on every reconcile. Only the baseline child, which
			// has no AggregationRule, owns its Rules.
			if desired.AggregationRule == nil {
				existing.Rules = desired.Rules
			}
			return nil
		})
		ref := audit.ResourceRef{Kind: kindClusterRole, Name: desired.Name, Cluster: clusterName}
		if err != nil {
			p.Log.Error(err, "failed to ensure shared ClusterRole",
				"name", asrName, "cluster", clusterName, "role", desired.Name)
			p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, ref, err)
			continue
		}
		if op != controllerutil.OperationResultNone {
			p.AuditLog.Log(ctx, opToAction(op), audit.OutcomeSuccess, asrName, ref, nil)
		}
	}
}
