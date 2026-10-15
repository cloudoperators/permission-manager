// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
)

// EnsureConnectorBindings fetches every kubernetes ConnectorBinding the ASR
// references and stashes them on p.cbs. Skips refs that fail to Get or have
// no usable definition (parseable + non-empty capabilityMapping).
//
// Breaks the chain when no usable CB is available: failMissingCB revokes RBAC
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
			return p.failMissingCB(ctx, asr, msg)
		}

		resourceOnly := asrIsResourceOnly(p.CCRNParser, asr)

		usable := 0
		for _, cb := range p.cbs {
			def, err := ParseConnectorDefinition(cb.Spec.Definition)
			if err != nil {
				p.Log.Error(err, "skipping unparseable ConnectorBinding definition", "namespace", asr.Namespace, "name", asr.Name, "connectorBinding", cb.Name)
				continue
			}
			if def.CapabilityMapping.empty() && !resourceOnly {
				p.Log.Info("ConnectorBinding has empty capabilityMapping", "namespace", asr.Namespace, "name", asr.Name, "connectorBinding", cb.Name)
				continue
			}
			usable++
		}

		if usable == 0 {
			msg := fmt.Sprintf("no usable ConnectorBinding definitions among %d referenced", len(p.cbs))
			return p.failMissingCB(ctx, asr, msg)
		}

		asr.ClearInvalidConfigIfReason(pmmeta.MissingConnectorBindingReason)
		return lifecycle.Continue(), nil
	}
}

// failMissingCB is the Break path when no usable kubernetes ConnectorBinding
// is available. It revokes RBAC from every previously-targeted cluster, stamps
// InvalidConfig=True/MissingConnectorBinding (mirrored on Ready=False), and
// removes the stale Provisioned condition.
//
// Clusters where revoke fails (unreachable, delete error) remain in
// status.providers.kubernetes.targets so the next reconcile retries them.
func (p *Phase) failMissingCB(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole, msg string) (lifecycle.Result, error) {
	prior := previouslyTargetedClusters(asr)
	stillTargeted := make([]pmmeta.TargetStatus, 0, len(prior))
	for _, clusterName := range prior {
		remoteClient, err := p.RemoteClient(ctx, p.Client, clusterName, asr.Namespace)
		if err != nil {
			p.Log.Error(err, "cluster unreachable during CB-missing revoke, will retry", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
			stillTargeted = append(stillTargeted, pmmeta.TargetStatus{TargetID: clusterName, Status: pmmeta.TargetStatusError, Message: err.Error()})
			continue
		}
		if err := p.deleteRBACByLabel(ctx, remoteClient, asr.Name, clusterName); err != nil {
			p.Log.Error(err, "failed to revoke RBAC during CB-missing revoke, will retry", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
			stillTargeted = append(stillTargeted, pmmeta.TargetStatus{TargetID: clusterName, Status: pmmeta.TargetStatusError, Message: err.Error()})
			continue
		}
		p.Log.Info("RBAC revoked due to missing ConnectorBinding", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
	}

	asr.SetCondition(pmmeta.TrueCondition(
		pmmeta.InvalidConfigCondition,
		pmmeta.MissingConnectorBindingReason,
		msg,
	))
	asr.SetCondition(pmmeta.FalseCondition(
		pmmeta.ReadyCondition,
		pmmeta.MissingConnectorBindingReason,
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
