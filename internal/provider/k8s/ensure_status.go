// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"strings"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
)

// EnsureProviderStatus is the terminal subroutine: it folds the Phase
// accumulators into the ASR's kubernetes provider status and computes Ready
// from the Provisioned / InvalidConfig conditions. Aggregated provision and
// orphan errors are returned; a non-nil aggregate Breaks the chain.
func (p *Phase) EnsureProviderStatus(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(_ context.Context) (lifecycle.Result, error) {
		p.writeProviderStatus(asr)
		asr.CalculateReady()

		if err := utilerrors.NewAggregate(append(p.provisionErrs, p.orphanErrs...)); err != nil {
			return lifecycle.Break(), err
		}
		return lifecycle.Continue(), nil
	}
}

// writeProviderStatus computes the final ProviderStatus from the accumulators.
// Config errors (invalid CCRNs, unmapped capabilities) degrade the whole
// provider; missing/not-ready clusters appear as per-target skips alongside
// applied targets.
func (p *Phase) writeProviderStatus(asr *v1alpha1.ApplicationSpecificRole) {
	if len(p.unmappedCaps) > 0 || len(p.invalidCCRNs) > 0 {
		parts := make([]string, 0, 2)
		if len(p.unmappedCaps) > 0 {
			parts = append(parts, fmt.Sprintf("unmapped capabilities: %v", p.unmappedCaps))
		}
		if len(p.invalidCCRNs) > 0 {
			parts = append(parts, fmt.Sprintf("invalid CCRNs: %v", p.invalidCCRNs))
		}
		asr.Status.Providers = pmmeta.SetProviderStatus(
			asr.Status.Providers,
			pmmeta.ProviderKubernetes,
			pmmeta.DegradedProviderStatus(strings.Join(parts, "; ")),
		)
		return
	}

	existing := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
	var targets []pmmeta.TargetStatus
	if existing != nil {
		targets = existing.Targets
	}

	// Snapshot before provision writes so orphan error handling can read the
	// original refs (which may include resources still pending deletion).
	priorTargets := make([]pmmeta.TargetStatus, len(targets))
	copy(priorTargets, targets)

	for _, s := range p.provisionStatuses {
		ts := toTargetStatus(s)
		if s.err != nil {
			if prior := pmmeta.GetTargetStatus(targets, s.name); prior != nil {
				ts.AppliedResources = prior.AppliedResources
			}
		}
		targets = pmmeta.SetTargetStatus(targets, ts)
	}
	for _, s := range p.orphanStatuses {
		ts := toTargetStatus(s)
		if s.err != nil {
			if prior := pmmeta.GetTargetStatus(priorTargets, s.name); prior != nil {
				ts.AppliedResources = prior.AppliedResources
			}
		}
		targets = pmmeta.SetTargetStatus(targets, ts)
	}
	// Drop stale entries for clusters whose orphan RBAC was fully removed.
	for _, name := range p.orphanCleaned {
		targets = pmmeta.RemoveTargetStatus(targets, name)
	}
	// Surface skipped clusters: missing ones as Error, not-ready ones as Pending.
	for _, name := range p.unmatchedClusterPatterns {
		targets = pmmeta.SetTargetStatus(targets, pmmeta.TargetStatus{
			TargetID: name,
			Status:   pmmeta.TargetStatusError,
			Message:  "cluster not found",
		})
	}
	for _, name := range p.notReadyClusters {
		ts := pmmeta.TargetStatus{
			TargetID: name,
			Status:   pmmeta.TargetStatusPending,
			Message:  "cluster not Ready",
		}
		// Keep prior refs: a transient blip must not drop applied-resource tracking.
		if prior := pmmeta.GetTargetStatus(priorTargets, name); prior != nil {
			ts.AppliedResources = prior.AppliedResources
		}
		targets = pmmeta.SetTargetStatus(targets, ts)
	}
	asr.Status.Providers = pmmeta.SetProviderStatus(
		asr.Status.Providers,
		pmmeta.ProviderKubernetes,
		pmmeta.ProviderStatusFromTargets(targets),
	)
}

func toTargetStatus(s clusterStatus) pmmeta.TargetStatus {
	ts := pmmeta.TargetStatus{TargetID: s.name, AppliedResources: s.appliedResources}
	if s.err != nil {
		ts.Status = pmmeta.TargetStatusError
		ts.Message = s.err.Error()
	} else {
		ts.Status = pmmeta.TargetStatusReady
	}
	return ts
}
