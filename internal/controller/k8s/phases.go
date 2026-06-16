// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/pkg/audit"
	pmutils "github.com/cloudoperators/permission-manager/pkg/utils"
)

// Phase holds dependencies shared across kubernetes provider reconcile calls.
type Phase struct {
	Client       client.Client
	AuditLog     *audit.Logger
	Log          logr.Logger
	ClusterStore cache.Store
}

// New returns a Phase with the given dependencies.
func New(c client.Client, al *audit.Logger, log logr.Logger, store cache.Store) *Phase {
	return &Phase{
		Client:       c,
		AuditLog:     al,
		Log:          log,
		ClusterStore: store,
	}
}

// clusterStatus tracks the provisioning outcome for a single cluster.
type clusterStatus struct {
	name string
	err  error
}

// Reconcile runs the kubernetes provider create/update logic for one ConnectorBinding.
func (p *Phase) Reconcile(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole) error {
	scopes, unmappedCaps := p.buildClusterScopes(asr)

	provisionStatuses, provisionErrs := p.ensureRBACProvisioned(ctx, asr, scopes)
	orphanStatuses, orphanErrs := p.ensureOrphanCleanup(ctx, asr, scopes)

	allStatuses := append(provisionStatuses, orphanStatuses...)
	p.ensureProviderStatus(asr, allStatuses, unmappedCaps)

	return utilerrors.NewAggregate(append(provisionErrs, orphanErrs...))
}

// ReconcileDelete runs the kubernetes provider cleanup logic for one ConnectorBinding.
func (p *Phase) ReconcileDelete(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole) error {
	targets := previouslyTargetedClusters(asr)

	var errs []error
	for _, clusterName := range targets {
		remoteClient, err := pmutils.NewRemoteClient(ctx, p.Client, clusterName, asr.Namespace)
		if err != nil {
			p.Log.Error(err, "cluster unreachable during cleanup, will retry", "cluster", clusterName)
			errs = append(errs, err)
			continue
		}

		if err := deleteRBACByLabel(ctx, remoteClient, asr.Name, clusterName, p.AuditLog); err != nil {
			p.Log.Error(err, "failed to delete RBAC during cleanup", "cluster", clusterName)
			errs = append(errs, err)
			continue
		}

		p.Log.Info("RBAC cleaned up", "cluster", clusterName, "asr", asr.Name)
	}

	if len(errs) == 0 {
		asr.Status.Providers = pmmeta.RemoveProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
	}

	return utilerrors.NewAggregate(errs)
}

// previouslyTargetedClusters returns the cluster names from status.providers["kubernetes"].targets.
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

// ensureRBACProvisioned applies RBAC resources to each cluster in scopes.
// Returns per-cluster statuses and collected errors (partial failure semantics).
func (p *Phase) ensureRBACProvisioned(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole, scopes map[string]*ClusterScope) ([]clusterStatus, []error) {
	var statuses []clusterStatus
	var errs []error

	for clusterName, scope := range scopes {
		remoteClient, err := pmutils.NewRemoteClient(ctx, p.Client, clusterName, asr.Namespace)
		if err != nil {
			p.Log.Error(err, "cluster unreachable, skipping", "cluster", clusterName)
			statuses = append(statuses, clusterStatus{name: clusterName, err: err})
			errs = append(errs, err)
			continue
		}

		if err := expandNamespaceScopes(ctx, remoteClient, scope); err != nil {
			p.Log.Error(err, "failed to expand namespace scopes", "cluster", clusterName)
			statuses = append(statuses, clusterStatus{name: clusterName, err: err})
			errs = append(errs, err)
			continue
		}

		if len(scope.ClusterVerbs) > 0 {
			cr, crb := buildClusterScopedRBAC(asr.Name, asr.Spec.Name, scope.ClusterVerbs)
			if err := applyClusterScopedRBAC(ctx, remoteClient, cr, crb, asr.Name, clusterName, p.AuditLog); err != nil {
				p.Log.Error(err, "failed to apply cluster-scoped RBAC", "cluster", clusterName)
				statuses = append(statuses, clusterStatus{name: clusterName, err: err})
				errs = append(errs, err)
				continue
			}
		}

		if len(scope.NamespaceVerbs) > 0 {
			namespaces := sortedKeys(scope.NamespaceVerbs)
			var nsErrs []error
			for _, ns := range namespaces {
				role, rb := buildNamespaceScopedRBAC(asr.Name, asr.Spec.Name, ns, scope.NamespaceVerbs[ns])
				if err := applyNamespaceScopedRBAC(ctx, remoteClient, role, rb, asr.Name, clusterName, p.AuditLog); err != nil {
					p.Log.Error(err, "failed to apply namespace-scoped RBAC", "cluster", clusterName, "namespace", ns)
					nsErrs = append(nsErrs, err)
				}
			}
			if len(nsErrs) > 0 {
				nsErr := utilerrors.NewAggregate(nsErrs)
				statuses = append(statuses, clusterStatus{name: clusterName, err: nsErr})
				errs = append(errs, nsErr)
				continue
			}
		}

		statuses = append(statuses, clusterStatus{name: clusterName})
	}

	if len(errs) == 0 {
		p.Log.Info("rbac(s) provisioned", "asr", asr.Name)
	} else {
		p.Log.Info("not all rbac(s) were provisioned", "asr", asr.Name, "errors", len(errs))
	}

	return statuses, errs
}

// ensureOrphanCleanup deletes RBAC from clusters that were previously targeted
// (per status.providers["kubernetes"].targets) but are no longer in current scopes.
func (p *Phase) ensureOrphanCleanup(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole, currentScopes map[string]*ClusterScope) ([]clusterStatus, []error) {
	orphans := detectOrphanClusters(asr, currentScopes)
	if len(orphans) == 0 {
		return nil, nil
	}

	var statuses []clusterStatus
	var errs []error

	for _, clusterName := range orphans {
		remoteClient, err := pmutils.NewRemoteClient(ctx, p.Client, clusterName, asr.Namespace)
		if err != nil {
			p.Log.Error(err, "orphan cluster unreachable, will retry", "cluster", clusterName)
			statuses = append(statuses, clusterStatus{name: clusterName, err: err})
			errs = append(errs, err)
			continue
		}

		if err := deleteRBACByLabel(ctx, remoteClient, asr.Name, clusterName, p.AuditLog); err != nil {
			p.Log.Error(err, "failed to clean up orphan RBAC", "cluster", clusterName)
			statuses = append(statuses, clusterStatus{name: clusterName, err: err})
			errs = append(errs, err)
			continue
		}

		p.Log.Info("orphan RBAC cleaned up", "cluster", clusterName, "asr", asr.Name)
	}

	return statuses, errs
}

// detectOrphanClusters returns cluster names from status.providers["kubernetes"].targets
// that are no longer present in currentScopes.
func detectOrphanClusters(asr *v1alpha1.ApplicationSpecificRole, currentScopes map[string]*ClusterScope) []string {
	if asr.Status.Providers == nil {
		return nil
	}
	ps, ok := asr.Status.Providers[string(pmmeta.ProviderKubernetes)]
	if !ok {
		return nil
	}
	var orphans []string
	for _, t := range ps.Targets {
		if _, stillTargeted := currentScopes[t.TargetID]; !stillTargeted {
			orphans = append(orphans, t.TargetID)
		}
	}
	return orphans
}

// ensureProviderStatus sets status.providers["kubernetes"] on the ASR in-memory.
// The lifecycle engine patches the full status at the end of reconcile.
// If unmapped capabilities exist, reports degraded status instead.
func (p *Phase) ensureProviderStatus(asr *v1alpha1.ApplicationSpecificRole, statuses []clusterStatus, unmappedCaps []string) {
	var ps pmmeta.ProviderStatus

	if len(unmappedCaps) > 0 {
		ps = pmmeta.DegradedProviderStatus(fmt.Sprintf("unmapped capabilities: %v", unmappedCaps))
	} else {
		existing := pmmeta.GetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes)
		var targets []pmmeta.TargetStatus
		if existing != nil {
			targets = existing.Targets
		}
		for _, s := range statuses {
			ts := pmmeta.TargetStatus{TargetID: s.name}
			if s.err != nil {
				ts.Status = pmmeta.TargetStatusError
				ts.Message = s.err.Error()
			} else {
				ts.Status = pmmeta.TargetStatusReady
			}
			targets = pmmeta.SetTargetStatus(targets, ts)
		}
		ps = pmmeta.ProviderStatusFromTargets(targets)
	}

	asr.Status.Providers = pmmeta.SetProviderStatus(asr.Status.Providers, pmmeta.ProviderKubernetes, ps)
}
