package greenhouseteams

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
	greenhouse "github.com/cloudoperators/permission-manager/controller/pkg/greenhouse"
)

const (
	// FinalizerGreenhouseTeams is the finalizer added to ASRs processed by the greenhouse-teams provider.
	FinalizerGreenhouseTeams = "permission-manager.cloudoperators.dev/greenhouse-teams"
)

// Reconciler reconciles ApplicationSpecificRole objects that reference
// greenhouse-teams ConnectorBindings. It creates/updates Team and TeamRoleBinding
// CRs based on ASR permissions.
type Reconciler struct {
	client.Client
	Log      logr.Logger
	CBIndex  *ConnectorBindingIndexer
	AuditLog *audit.Logger
}

// Reconcile processes a single ASR for the greenhouse-teams provider.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	// Check if this ASR references a greenhouse-teams ConnectorBinding
	if !r.CBIndex.HasAnyProviderRef(asr.Spec.ConnectorRefs) {
		// ASR no longer references greenhouse-teams. If it still has our finalizer,
		// clean up resources from when it did, then remove the finalizer.
		if controllerutil.ContainsFinalizer(&asr, FinalizerGreenhouseTeams) {
			log.Info("ASR no longer references greenhouse-teams CB, cleaning up stale resources")
			if err := r.cleanup(ctx, &asr); err != nil {
				log.Error(err, "cleanup of stale resources failed, requeueing")
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&asr, FinalizerGreenhouseTeams)
			if err := r.Update(ctx, &asr); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Handle deletion: if deletionTimestamp is set, handle cleanup
	if !asr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&asr, FinalizerGreenhouseTeams) {
			log.Info("ASR being deleted, cleaning up greenhouse-teams resources")
			if err := r.cleanup(ctx, &asr); err != nil {
				log.Error(err, "cleanup failed, requeueing")
				return ctrl.Result{}, err
			}

			// Remove the finalizer to allow Kubernetes to complete deletion
			controllerutil.RemoveFinalizer(&asr, FinalizerGreenhouseTeams)
			if err := r.Update(ctx, &asr); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
			log.Info("finalizer removed, ASR deletion will proceed")
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&asr, FinalizerGreenhouseTeams) {
		controllerutil.AddFinalizer(&asr, FinalizerGreenhouseTeams)
		if err := r.Update(ctx, &asr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		log.Info("finalizer added to ASR")
	}

	// Find the greenhouse-teams ConnectorBinding for this ASR
	var cbDef *GreenhouseTeamsConnectorDefinition
	for _, ref := range asr.Spec.ConnectorRefs {
		if r.CBIndex.IsProviderConnector(ref) {
			// Get the ConnectorBinding to parse its definition
			var cb pmv1.ConnectorBinding
			if err := r.Get(ctx, client.ObjectKey{Name: ref, Namespace: asr.Namespace}, &cb); err != nil {
				log.Error(err, "failed to get ConnectorBinding", "name", ref)
				continue
			}
			parsed, err := ParseConnectorDefinition(cb.Spec.Definition)
			if err != nil {
				log.Error(err, "failed to parse ConnectorBinding definition", "name", ref)
				continue
			}
			cbDef = parsed
			break
		}
	}

	if cbDef == nil {
		return ctrl.Result{}, fmt.Errorf("no valid greenhouse-teams ConnectorBinding found for ASR %s", asr.Name)
	}

	// Parse advanced config
	advCfg, err := ParseAdvancedConfig(asr.Spec.AdvancedConfig)
	if err != nil {
		log.Error(err, "failed to parse advancedConfig")
		return ctrl.Result{}, fmt.Errorf("failed to parse advancedConfig: %w", err)
	}

	// Resolve target namespaces
	targetNamespaces, err := ResolveTargetNamespaces(advCfg, cbDef)
	if err != nil {
		log.Error(err, "failed to resolve target namespaces")
		return ctrl.Result{}, fmt.Errorf("target namespace resolution failed: %w", err)
	}

	// Resolve team name (from advancedConfig or derive from ASR)
	teamName := advCfg.TeamName
	if teamName == "" {
		teamName = asr.Spec.Name
	}

	// Compute desired state
	capMapping := greenhouse.CapabilityMapping(cbDef.CapabilityMapping)
	state, warnings := ComputeDesiredState(&asr, teamName, capMapping)

	for _, w := range warnings {
		log.V(1).Info("desired state warning", "warning", w)
	}

	// Check for unmapped capabilities (desired state warnings that indicate unmapped caps)
	var unmappedCaps []string
	for _, w := range warnings {
		// Warnings from ComputeDesiredState about unmapped capabilities contain "unmapped capability"
		if len(w) > 0 {
			unmappedCaps = append(unmappedCaps, w)
		}
	}

	// Team-only mode: if no TRBs are desired across all namespaces AND we had unmapped caps,
	// report the unmapped capability status
	if len(state.TRBs) == 0 && len(unmappedCaps) > 0 {
		providerStatus := ComputeUnmappedCapabilityStatus(unmappedCaps)
		if err := PatchASRProviderStatus(ctx, r.Client, &asr, providerStatus); err != nil {
			log.Error(err, "failed to patch provider status for unmapped capabilities")
		}
		return ctrl.Result{}, nil
	}

	// Reconcile across all target namespaces
	var isTeamOnlyMode bool
	for _, ns := range targetNamespaces {
		if len(state.TRBs) == 0 {
			// No TRBs desired — delete Team and all TRBs in this namespace
			if err := r.cleanupNamespace(ctx, asr.Name, asr.Namespace, ns); err != nil {
				return ctrl.Result{}, err
			}
			continue
		}

		// Create/update Team
		if err := ReconcileTeam(ctx, r.Client, state, ns, asr.Name, asr.Namespace, r.AuditLog); err != nil {
			return ctrl.Result{}, err
		}

		// Determine conflict mode: if K8s provider also targets this ASR, skip TRBs
		mode, _, err := DetermineMode(ctx, r.Client, &asr, log)
		if err != nil {
			log.Error(err, "conflict detection failed, defaulting to full reconcile")
			mode = ModeFullReconcile
		}

		if ShouldReconcileTRBs(mode) {
			// Create/update TRBs and remove stale ones
			if err := ReconcileAllTRBs(ctx, r.Client, state, ns, asr.Name, asr.Namespace, r.AuditLog); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			isTeamOnlyMode = true
			// Team-only mode: clean up any existing TRBs
			if err := CleanupTRBs(ctx, r.Client, &asr, log, r.AuditLog); err != nil {
				log.Error(err, "failed to clean up TRBs in team-only mode")
			}
		}

		log.Info("reconciled greenhouse-teams resources",
			"namespace", ns,
			"teamName", state.TeamName,
			"trbCount", len(state.TRBs),
			"mode", mode.String(),
		)
	}

	// Patch provider status
	var providerStatus pmv1.ProviderStatus
	if isTeamOnlyMode {
		providerStatus = ComputeTeamOnlyStatus()
	} else {
		providerStatus = ComputeProviderStatus(ctx, r.Client, &asr)
	}
	if err := PatchASRProviderStatus(ctx, r.Client, &asr, providerStatus); err != nil {
		log.Error(err, "failed to patch provider status")
	}

	return ctrl.Result{}, nil
}

// cleanup removes all managed Team and TRB resources for the given ASR.
// It resolves target namespaces from the ConnectorBinding to find where CRs were created.
func (r *Reconciler) cleanup(ctx context.Context, asr *pmv1.ApplicationSpecificRole) error {
	// Find the greenhouse-teams ConnectorBinding to determine target namespaces
	var cbDef *GreenhouseTeamsConnectorDefinition
	for _, ref := range asr.Spec.ConnectorRefs {
		if r.CBIndex.IsProviderConnector(ref) {
			var cb pmv1.ConnectorBinding
			if err := r.Get(ctx, client.ObjectKey{Name: ref, Namespace: asr.Namespace}, &cb); err != nil {
				continue
			}
			parsed, err := ParseConnectorDefinition(cb.Spec.Definition)
			if err != nil {
				continue
			}
			cbDef = parsed
			break
		}
	}

	advCfg, _ := ParseAdvancedConfig(asr.Spec.AdvancedConfig)
	targetNamespaces, err := ResolveTargetNamespaces(advCfg, cbDef)
	if err != nil {
		// If we can't resolve namespaces, try a broader search approach:
		// List across all namespaces with label selectors
		return r.cleanupAllNamespaces(ctx, asr.Name, asr.Namespace)
	}

	for _, ns := range targetNamespaces {
		if err := r.cleanupNamespace(ctx, asr.Name, asr.Namespace, ns); err != nil {
			return err
		}
	}
	return nil
}

// cleanupNamespace removes all managed Team and TRB resources in a single namespace.
func (r *Reconciler) cleanupNamespace(ctx context.Context, asrName, asrNamespace, targetNamespace string) error {
	// Delete all managed TRBs
	if err := DeleteAllManagedTRBs(ctx, r.Client, asrName, asrNamespace, targetNamespace, r.AuditLog); err != nil {
		return err
	}

	// Delete all managed Teams
	teams, err := ListManagedTeams(ctx, r.Client, asrName, asrNamespace, targetNamespace)
	if err != nil {
		return err
	}
	for i := range teams {
		if err := r.Delete(ctx, &teams[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete Team %s/%s: %w", targetNamespace, teams[i].Name, err)
			}
		} else {
			r.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, audit.ResourceRef{
				Kind:      "Team",
				Name:      teams[i].Name,
				Namespace: targetNamespace,
				Cluster:   "",
			}, nil)
		}
	}
	return nil
}

// cleanupAllNamespaces performs a broader cleanup across all namespaces when target
// namespace resolution fails (e.g., ConnectorBinding is already deleted).
func (r *Reconciler) cleanupAllNamespaces(ctx context.Context, asrName, asrNamespace string) error {
	labels := client.MatchingLabels{
		LabelManagedBy:       LabelManagedByValue,
		LabelSourceASR:       asrName,
		LabelSourceNamespace: asrNamespace,
	}

	// Delete TRBs across all namespaces
	var trbList greenhouse.TeamRoleBindingList
	if err := r.List(ctx, &trbList, labels); err != nil {
		return fmt.Errorf("failed to list TRBs for cleanup: %w", err)
	}
	for i := range trbList.Items {
		if err := r.Delete(ctx, &trbList.Items[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete TRB %s/%s: %w", trbList.Items[i].Namespace, trbList.Items[i].Name, err)
			}
		} else {
			r.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, audit.ResourceRef{
				Kind:      "TeamRoleBinding",
				Name:      trbList.Items[i].Name,
				Namespace: trbList.Items[i].Namespace,
				Cluster:   "",
			}, nil)
		}
	}

	// Delete Teams across all namespaces
	var teamList greenhouse.TeamList
	if err := r.List(ctx, &teamList, labels); err != nil {
		return fmt.Errorf("failed to list Teams for cleanup: %w", err)
	}
	for i := range teamList.Items {
		if err := r.Delete(ctx, &teamList.Items[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete Team %s/%s: %w", teamList.Items[i].Namespace, teamList.Items[i].Name, err)
			}
		} else {
			r.AuditLog.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, audit.ResourceRef{
				Kind:      "Team",
				Name:      teamList.Items[i].Name,
				Namespace: teamList.Items[i].Namespace,
				Cluster:   "",
			}, nil)
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Predicate that filters ASRs to only those with greenhouse-teams ConnectorBinding refs
	// OR those that still carry the greenhouse-teams finalizer (needing cleanup).
	ghASRPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		asr, ok := obj.(*pmv1.ApplicationSpecificRole)
		if !ok {
			return true // Allow non-ASR objects (ConnectorBinding, TRB) to pass through
		}
		if r.CBIndex.HasAnyProviderRef(asr.Spec.ConnectorRefs) {
			return true
		}
		// Also allow ASRs that still have our finalizer (needs cleanup)
		return controllerutil.ContainsFinalizer(asr, FinalizerGreenhouseTeams)
	})

	// Predicate that filters TRBs to only those managed by greenhouse-teams
	managedTRBPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		labels := obj.GetLabels()
		return labels != nil && labels[LabelManagedBy] == LabelManagedByValue
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&pmv1.ApplicationSpecificRole{}).
		WithEventFilter(ghASRPredicate).
		Watches(
			&pmv1.ConnectorBinding{},
			r.CBIndex.EnqueueASRsForConnectorBinding(mgr.GetClient()),
		).
		Watches(
			&greenhouse.TeamRoleBinding{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueASRForTRB),
			builder.WithPredicates(managedTRBPredicate),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		}).
		Complete(r)
}

// enqueueASRForTRB maps a TeamRoleBinding change back to the owning ASR reconcile request
// using the source-asr and source-namespace labels.
func (r *Reconciler) enqueueASRForTRB(_ context.Context, obj client.Object) []reconcile.Request {
	labels := obj.GetLabels()
	if labels == nil {
		return nil
	}

	asrName := labels[LabelSourceASR]
	asrNamespace := labels[LabelSourceNamespace]
	if asrName == "" || asrNamespace == "" {
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: client.ObjectKey{
				Name:      asrName,
				Namespace: asrNamespace,
			},
		},
	}
}
