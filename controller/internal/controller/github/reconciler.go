// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
	"github.com/cloudoperators/permission-manager/controller/pkg/provider"
)

// GithubReconciler reconciles ApplicationSpecificRole objects that reference
// GitHub ConnectorBindings. It creates GithubTeam and GithubTeamRepository CRs,
// and patches GithubOrganization resources based on ASR permissions.
type GithubReconciler struct {
	client.Client
	Log      logr.Logger
	CBIndex  *GithubIndexer
	AuditLog *audit.Logger
}

// Reconcile processes a single ASR. It:
// 1. Skips ASRs that don't reference a github ConnectorBinding
// 2. Adds the github finalizer
// 3. Parses advancedConfig and ConnectorBinding definition
// 4. Resolves target namespace
// 5. Builds the desired state (GithubTeam + GithubTeamRepository CRs)
// 6. Reconciles the actual state to match the desired state
// 7. Patches GithubOrganization resources for wildcard/org CCRNs
func (r *GithubReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("asr", req.NamespacedName)

	// Fetch the ASR
	var asr pmv1.ApplicationSpecificRole
	if err := r.Get(ctx, req.NamespacedName, &asr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info("ASR deleted, skipping reconciliation")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ASR: %w", err)
	}

	// Check if this ASR references a GitHub ConnectorBinding
	cbName, cbSpec, found := r.CBIndex.FindGithubRef(asr.Spec.ConnectorRefs)
	if !found {
		// ASR no longer references github. If it still has our finalizer,
		// clean up resources from when it did, then remove the finalizer.
		if controllerutil.ContainsFinalizer(&asr, FinalizerGithub) {
			log.Info("ASR no longer references GitHub CB, cleaning up stale resources")
			// Use label-based cleanup across all namespaces (no CB info available)
			var teamList GithubTeamList
			if err := r.List(ctx, &teamList, client.MatchingLabels{LabelSourceASR: asr.Name}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to list GithubTeams for cleanup: %w", err)
			}
			for i := range teamList.Items {
				if err := r.Delete(ctx, &teamList.Items[i]); client.IgnoreNotFound(err) != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete GithubTeam: %w", err)
				}
			}
			var repoList GithubTeamRepositoryList
			if err := r.List(ctx, &repoList, client.MatchingLabels{LabelSourceASR: asr.Name}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to list GithubTeamRepositories for cleanup: %w", err)
			}
			for i := range repoList.Items {
				if err := r.Delete(ctx, &repoList.Items[i]); client.IgnoreNotFound(err) != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete GithubTeamRepository: %w", err)
				}
			}
			if err := r.cleanupOrgEntries(ctx, &asr); err != nil {
				log.Error(err, "failed to clean up org entries, requeueing")
				return ctrl.Result{RequeueAfter: DefaultOrgRequeueDelay}, nil
			}
			controllerutil.RemoveFinalizer(&asr, FinalizerGithub)
			if err := r.Update(ctx, &asr); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
			// Remove github provider entry from ASR status
			if err := provider.RemoveProviderStatus(ctx, r.Client, &asr, provider.ProviderGithub); err != nil {
				log.Error(err, "failed to remove github provider status")
			}
		}
		return ctrl.Result{}, nil
	}

	// Handle deletion: clean up owned CRs and remove finalizer
	if !asr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&asr, FinalizerGithub) {
			log.Info("ASR being deleted, cleaning up GitHub resources")
			if err := r.cleanupOwnedResources(ctx, &asr, cbName, cbSpec); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to clean up GitHub resources: %w", err)
			}
			// Clean up organization entries contributed by this ASR
			if err := r.cleanupOrgEntries(ctx, &asr); err != nil {
				log.Error(err, "failed to clean up org entries, requeueing")
				return ctrl.Result{RequeueAfter: DefaultOrgRequeueDelay}, nil
			}
			controllerutil.RemoveFinalizer(&asr, FinalizerGithub)
			if err := r.Update(ctx, &asr); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&asr, FinalizerGithub) {
		controllerutil.AddFinalizer(&asr, FinalizerGithub)
		if err := r.Update(ctx, &asr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		log.Info("finalizer added to ASR")
	}

	// Parse advancedConfig.github from the ASR
	advConfig, err := ParseAdvancedConfig(asr.Spec.AdvancedConfig)
	if err != nil {
		log.Error(err, "failed to parse advancedConfig")
		return ctrl.Result{}, fmt.Errorf("failed to parse advancedConfig: %w", err)
	}

	// Parse capabilityMapping from ConnectorBinding definition
	cbDef, err := ParseConnectorDefinition(cbSpec.Definition)
	if err != nil {
		log.Error(err, "failed to parse ConnectorBinding definition", "connectorBinding", cbName)
		return ctrl.Result{}, fmt.Errorf("failed to parse ConnectorBinding definition: %w", err)
	}

	// Resolve target namespace
	targetNamespace := ResolveTargetNamespace(advConfig, cbDef, &asr)

	// Build desired state
	desired, unmappedCaps := BuildDesiredState(&asr, cbDef.CapabilityMapping)

	// Warn about unmapped capabilities
	for _, cap := range unmappedCaps {
		log.Info("unmapped capability in ConnectorBinding capabilityMapping",
			"capability", cap,
			"asr", asr.Name,
			"provider", "github",
		)
	}

	log.Info("reconciling GitHub resources",
		"asr", asr.Name,
		"connectorBinding", cbName,
		"targetNamespace", targetNamespace,
		"teams", len(desired.Teams),
		"teamRepos", len(desired.TeamRepos),
	)

	// Reconcile GithubTeam CRs
	if err := ReconcileTeams(ctx, r.Client, desired.Teams, asr.Spec.Name, targetNamespace, asr.Name, asr.Namespace, r.AuditLog); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile GithubTeams: %w", err)
	}

	// Reconcile GithubTeamRepository CRs
	if err := ReconcileTeamRepos(ctx, r.Client, desired.TeamRepos, targetNamespace, asr.Name, asr.Namespace, r.AuditLog); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile GithubTeamRepositories: %w", err)
	}

	// Patch GithubOrganization resources (wildcard + org-owner)
	result, err := r.reconcileOrganizations(ctx, &asr, cbDef, targetNamespace)
	if err != nil {
		return result, err
	}
	if result.RequeueAfter > 0 || result.Requeue {
		return result, nil
	}

	log.Info("successfully reconciled GitHub resources",
		"asr", asr.Name,
		"teams", len(desired.Teams),
		"teamRepos", len(desired.TeamRepos),
	)

	// Compute and write provider status
	var providerStatus pmv1.ProviderStatus
	if len(unmappedCaps) > 0 {
		providerStatus = computeUnmappedCapabilityStatus(unmappedCaps)
	} else {
		providerStatus = computeProviderStatus(ctx, r.Client, &asr, desired)
	}
	if err := patchProviderStatus(ctx, r.Client, &asr, providerStatus); err != nil {
		log.Error(err, "failed to patch provider status")
		return ctrl.Result{}, fmt.Errorf("failed to patch provider status: %w", err)
	}

	return ctrl.Result{}, nil
}

// cleanupOwnedResources deletes all GithubTeam and GithubTeamRepository CRs
// owned by this ASR. Called during ASR deletion.
func (r *GithubReconciler) cleanupOwnedResources(ctx context.Context, asr *pmv1.ApplicationSpecificRole, cbName string, cbSpec pmv1.ConnectorBindingSpec) error {
	// Resolve namespace so we know where to look for owned CRs
	advConfig, _ := ParseAdvancedConfig(asr.Spec.AdvancedConfig)
	cbDef, err := ParseConnectorDefinition(cbSpec.Definition)
	if err != nil {
		// If we can't parse the definition, fall back to ASR namespace
		cbDef = &GithubConnectorDefinition{}
	}
	targetNamespace := ResolveTargetNamespace(advConfig, cbDef, asr)

	// Delete all teams owned by this ASR (pass empty desired set to delete all)
	if err := deleteStaleTeams(ctx, r.Client, targetNamespace, asr.Name, nil, r.AuditLog); err != nil {
		return err
	}

	// Delete all team repos owned by this ASR
	if err := deleteStaleTeamRepos(ctx, r.Client, targetNamespace, asr.Name, nil, r.AuditLog); err != nil {
		return err
	}

	return nil
}

// cleanupOrgEntries removes all organization entries contributed by this ASR
// across all GithubOrganization resources in all namespaces.
func (r *GithubReconciler) cleanupOrgEntries(ctx context.Context, asr *pmv1.ApplicationSpecificRole) error {
	var orgList GithubOrganizationList
	if err := r.List(ctx, &orgList); err != nil {
		return fmt.Errorf("failed to list GithubOrganizations: %w", err)
	}

	for i := range orgList.Items {
		org := &orgList.Items[i]
		if hasASRContribution(org, asr.Spec.Name) {
			if err := r.removeASRFromOrg(ctx, org, asr.Spec.Name); err != nil {
				return fmt.Errorf("failed to clean up org %s/%s: %w", org.Namespace, org.Name, err)
			}
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GithubReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Predicate that filters ASRs to only those with github ConnectorBinding refs
	// OR those that still carry the github finalizer (needing cleanup).
	// Non-ASR objects (ConnectorBinding watches) are always allowed through.
	githubASRPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		asr, ok := obj.(*pmv1.ApplicationSpecificRole)
		if !ok {
			return true // Allow non-ASR objects (ConnectorBindings) through
		}
		if r.CBIndex.HasAnyGithubRef(asr.Spec.ConnectorRefs) {
			return true
		}
		return controllerutil.ContainsFinalizer(asr, FinalizerGithub)
	})

	// Predicate that filters GithubTeam CRs to only those managed by the github provider
	githubTeamPredicate := predicate.TypedFuncs[*GithubTeam]{
		CreateFunc: func(e event.TypedCreateEvent[*GithubTeam]) bool {
			labels := e.Object.GetLabels()
			return labels != nil && labels[LabelManagedByKey] == LabelManagedByGithub
		},
		UpdateFunc: func(e event.TypedUpdateEvent[*GithubTeam]) bool {
			labels := e.ObjectNew.GetLabels()
			return labels != nil && labels[LabelManagedByKey] == LabelManagedByGithub
		},
		DeleteFunc: func(e event.TypedDeleteEvent[*GithubTeam]) bool {
			labels := e.Object.GetLabels()
			return labels != nil && labels[LabelManagedByKey] == LabelManagedByGithub
		},
		GenericFunc: func(e event.TypedGenericEvent[*GithubTeam]) bool {
			labels := e.Object.GetLabels()
			return labels != nil && labels[LabelManagedByKey] == LabelManagedByGithub
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&pmv1.ApplicationSpecificRole{}).
		WithEventFilter(githubASRPredicate).
		Watches(
			&pmv1.ConnectorBinding{},
			r.CBIndex.EnqueueASRsForConnectorBinding(mgr.GetClient()),
		).
		WatchesRawSource(
			source.Kind(mgr.GetCache(), &GithubTeam{},
				handler.TypedEnqueueRequestsFromMapFunc(r.mapGithubTeamToASR),
				githubTeamPredicate,
			),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		Complete(r)
}

// mapGithubTeamToASR maps a GithubTeam CR change to the owning ASR for re-reconciliation.
// It reads the source-asr and source-namespace labels from the GithubTeam CR.
func (r *GithubReconciler) mapGithubTeamToASR(ctx context.Context, team *GithubTeam) []reconcile.Request {
	keys := extractASRFromGithubTeam(team)
	if len(keys) == 0 {
		return nil
	}

	requests := make([]reconcile.Request, len(keys))
	for i, key := range keys {
		requests[i] = reconcile.Request{NamespacedName: key}
	}
	return requests
}
