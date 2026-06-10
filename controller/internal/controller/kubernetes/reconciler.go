package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
	"github.com/cloudoperators/permission-manager/controller/pkg/provider"
)

// CapabilityVerbs maps permission capabilities to Kubernetes RBAC verbs.
// Higher capabilities subsume lower ones: WRITE includes READ verbs.
var CapabilityVerbs = map[string][]string{
	"READ":    {"get", "list", "watch"},
	"WRITE":   {"get", "list", "watch", "create", "update", "patch", "delete"},
	"OPERATE": {"exec", "port-forward", "proxy"},
	"SECURE":  {"bind", "escalate", "impersonate"},
	"*":       {"*"},
}

// DefaultRequeueDelay is the default delay when requeuing due to cluster errors.
const DefaultRequeueDelay = 30 * time.Second

// Deprecated: FinalizerRBACCleanup is retained only for migration detection.
// Use FinalizerKubernetes instead. Will be removed once all existing ASRs
// have been migrated to the per-provider finalizer.
const FinalizerRBACCleanup = "permission-manager.cloudoperators.dev/rbac-cleanup"

// FinalizerKubernetes is the per-provider finalizer for the Kubernetes provider.
var FinalizerKubernetes = provider.FinalizerName("kubernetes")

// Reconciler reconciles ApplicationSpecificRole objects that reference
// Kubernetes ConnectorBindings. It generates RBAC resources (ClusterRole,
// ClusterRoleBinding, RoleBinding) on target clusters based on ASR permissions.
type Reconciler struct {
	client.Client
	Recorder             record.EventRecorder
	CBIndex              *ConnectorBindingIndexer
	ClusterInventory     *ClusterInventory
	ClusterClientFactory ClusterClientFactory
	NamespaceLister      NamespaceLister
	Cleaner              *RBACCleaner
	AuditLog             *audit.Logger
	// ClusterChangeCh receives events when a cluster is added/updated. The
	// event's Object.GetName() carries the cluster name; the handler maps it
	// to matching ASR reconcile requests.
	ClusterChangeCh <-chan event.GenericEvent
}

// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=applicationspecificroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=applicationspecificroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=applicationspecificroles/finalizers,verbs=update
// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=connectorbindings,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=greenhouse.sap,resources=clusters,verbs=get;list;watch

// Reconcile processes a single ASR. It determines which clusters are affected,
// generates RBAC resources, and applies them to each target cluster.
// It uses a finalizer to ensure RBAC resources are cleaned up when the ASR is deleted.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the ASR
	var asr pmv1.ApplicationSpecificRole
	if err := r.Get(ctx, req.NamespacedName, &asr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info("ASR deleted, skipping reconciliation")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ASR: %w", err)
	}

	// Check if this ASR references a Kubernetes ConnectorBinding
	if !r.CBIndex.HasAnyKubernetesRef(asr.Spec.ConnectorRefs) {
		// ASR no longer references kubernetes. If it still has our finalizer,
		// clean up resources from when it did, then remove the finalizer.
		if controllerutil.ContainsFinalizer(&asr, FinalizerKubernetes) {
			log.Info("ASR no longer references kubernetes CB, cleaning up RBAC resources")
			clusters := extractTargetClusterNames(&asr, r.ClusterInventory)
			skipped, err := r.Cleaner.CleanupAll(ctx, asr.Name, clusters)
			if err != nil {
				log.Error(err, "failed to clean up RBAC, requeueing")
				return ctrl.Result{RequeueAfter: DefaultRequeueDelay}, nil
			}

			if len(skipped) > 0 {
				// Filter: clusters gone from Greenhouse inventory are not "orphaned"
				orphaned := filterOrphanedClusters(skipped, r.ClusterInventory)

				if len(orphaned) > 0 {
					// CB-ref-removed path: always hold finalizer (no abandon annotation check)
					prevTargets := extractPrevStatuses(&asr)
					orphanStatuses := buildOrphanStatuses(orphaned)
					if err := r.patchStatus(ctx, &asr, orphanStatuses); err != nil {
						log.Error(err, "failed to patch orphan status (non-fatal)")
					}
					if orphanSetChanged(orphaned, prevTargets) {
						r.emitEvent(&asr, corev1.EventTypeWarning, "CleanupBlocked",
							fmt.Sprintf("Finalizer held: %d clusters unreachable during RBAC cleanup", len(orphaned)))
					}
					delay := computeRequeueDelay(prevTargets)
					log.Info("holding finalizer due to unreachable clusters", "orphaned", len(orphaned), "requeueAfter", delay)
					return ctrl.Result{RequeueAfter: delay}, nil
				}
				// All skipped clusters are gone from inventory — fall through to remove finalizer
			}

			original := asr.DeepCopy()
			controllerutil.RemoveFinalizer(&asr, FinalizerKubernetes)
			// Also remove legacy finalizer if present
			controllerutil.RemoveFinalizer(&asr, FinalizerRBACCleanup)
			if err := r.Patch(ctx, &asr, client.MergeFrom(original)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
			log.Info("kubernetes finalizer removed after CB reference removed")
		}
		return ctrl.Result{}, nil
	}

	// Migrate legacy finalizer to per-provider finalizer (transparent migration)
	if controllerutil.ContainsFinalizer(&asr, FinalizerRBACCleanup) {
		controllerutil.AddFinalizer(&asr, FinalizerKubernetes)
		controllerutil.RemoveFinalizer(&asr, FinalizerRBACCleanup)
		if err := r.Update(ctx, &asr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to migrate finalizer: %w", err)
		}
		log.Info("migrated finalizer from legacy to per-provider", "old", FinalizerRBACCleanup, "new", FinalizerKubernetes)
	}

	// Handle deletion: if deletionTimestamp is set, run cleanup
	if !asr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&asr, FinalizerKubernetes) {
			log.Info("ASR being deleted, cleaning up RBAC resources")
			clusters := extractTargetClusterNames(&asr, r.ClusterInventory)
			skipped, err := r.Cleaner.CleanupAll(ctx, asr.Name, clusters)
			if err != nil {
				log.Error(err, "failed to clean up RBAC, requeueing")
				return ctrl.Result{RequeueAfter: DefaultRequeueDelay}, nil
			}

			if len(skipped) > 0 {
				// Filter: clusters gone from Greenhouse inventory are not "orphaned"
				orphaned := filterOrphanedClusters(skipped, r.ClusterInventory)

				if len(orphaned) > 0 {
					// Exit path 3: abandon annotation (deletion path only)
					if hasAbandonAnnotation(&asr) {
						log.Info("abandon-cleanup annotation set, proceeding with finalizer removal despite unreachable clusters", "orphaned", len(orphaned))
						r.emitEvent(&asr, corev1.EventTypeWarning, "CleanupAbandoned",
							fmt.Sprintf("Abandoning cleanup of %d unreachable clusters per operator annotation", len(orphaned)))
						// fall through to remove finalizer
					} else {
						// HOLD the finalizer — patch status and requeue
						prevTargets := extractPrevStatuses(&asr)
						orphanStatuses := buildOrphanStatuses(orphaned)
						if err := r.patchStatus(ctx, &asr, orphanStatuses); err != nil {
							log.Error(err, "failed to patch orphan status (non-fatal)")
						}
						if orphanSetChanged(orphaned, prevTargets) {
							r.emitEvent(&asr, corev1.EventTypeWarning, "CleanupBlocked",
								fmt.Sprintf("Finalizer held: %d clusters unreachable during RBAC cleanup", len(orphaned)))
						}
						delay := computeRequeueDelay(prevTargets)
						log.Info("holding finalizer due to unreachable clusters", "orphaned", len(orphaned), "requeueAfter", delay)
						return ctrl.Result{RequeueAfter: delay}, nil
					}
				}
				// All skipped clusters are gone from inventory — fall through to remove finalizer
			}

			// Remove the finalizer to allow Kubernetes to complete deletion
			original := asr.DeepCopy()
			controllerutil.RemoveFinalizer(&asr, FinalizerKubernetes)
			if err := r.Patch(ctx, &asr, client.MergeFrom(original)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
			log.Info("finalizer removed, ASR deletion will proceed")
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&asr, FinalizerKubernetes) {
		original := asr.DeepCopy()
		controllerutil.AddFinalizer(&asr, FinalizerKubernetes)
		if err := r.Patch(ctx, &asr, client.MergeFrom(original)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		log.Info("finalizer added to ASR")
	}

	// Build cluster scopes from ASR permissions
	scopes, unmappedCaps := buildClusterScopes(&asr, r.ClusterInventory.MatchingNames)

	// Warn about unmapped capabilities
	for _, cap := range unmappedCaps {
		log.Info("unmapped capability in CapabilityVerbs",
			"capability", cap,
			"asr", asr.Name,
			"provider", "kubernetes",
		)
	}

	if len(scopes) == 0 {
		// If no clusters matched but we have unmapped caps, still report degraded status
		if len(unmappedCaps) > 0 {
			if err := r.patchUnmappedCapabilityStatus(ctx, &asr, unmappedCaps); err != nil {
				log.Error(err, "failed to patch unmapped capability status")
			}
		}

		// Even with zero scopes, we must still run orphan cleanup: if the ASR
		// previously had targets (per status), those clusters now have orphaned
		// RBAC resources that need to be deleted.
		prevStatuses := extractPrevStatuses(&asr)
		if len(prevStatuses) > 0 {
			log.Info("permissions stripped to zero, running orphan cleanup for previously targeted clusters", "asr", asr.Name)
			statuses, reconcileErrors := r.reconcileWithPlan(ctx, &asr, scopes)
			if err := r.patchStatus(ctx, &asr, statuses); err != nil {
				log.Error(err, "failed to patch ASR status after orphan cleanup")
			}
			if len(reconcileErrors) > 0 {
				return ctrl.Result{}, fmt.Errorf("orphan cleanup failure: %w", errors.Join(reconcileErrors...))
			}
			return ctrl.Result{}, nil
		}

		log.V(1).Info("no clusters matched any ASR permission wildcard", "asr", asr.Name)
		return ctrl.Result{}, nil
	}

	// Plan → Execute → Orphan cleanup
	statuses, reconcileErrors := r.reconcileWithPlan(ctx, &asr, scopes)

	// Patch status subresource with propagation results
	if len(unmappedCaps) > 0 {
		if err := r.patchUnmappedCapabilityStatus(ctx, &asr, unmappedCaps); err != nil {
			log.Error(err, "failed to patch unmapped capability status")
		}
	} else {
		if err := r.patchStatus(ctx, &asr, statuses); err != nil {
			log.Error(err, "failed to patch ASR status")
		}
	}

	// Return error on partial failure — controller-runtime will rate-limit the requeue
	if len(reconcileErrors) > 0 {
		return ctrl.Result{}, fmt.Errorf("partial failure: %w", errors.Join(reconcileErrors...))
	}

	return ctrl.Result{}, nil
}

// patchStatus builds the ProviderStatus and patches the ASR status subresource
// via the shared SSA helper in pkg/provider. It skips the patch if the meaningful
// status content (ready state, targets, messages) has not changed, to avoid
// triggering infinite reconcile loops from timestamp-only updates.
func (r *Reconciler) patchStatus(ctx context.Context, asr *pmv1.ApplicationSpecificRole, statuses []ClusterProvisioningStatus) error {
	// Convert internal statuses to TargetStatus entries
	targets := make([]pmv1.TargetStatus, 0, len(statuses))
	allSucceeded := true
	for _, s := range statuses {
		targets = append(targets, pmv1.TargetStatus{
			TargetID:    s.ClusterName,
			Status:      s.Status,
			Message:     s.Message,
			LastUpdated: s.LastUpdated,
		})
		if s.Status != "Success" {
			allSucceeded = false
		}
	}

	// Build summary message
	var message string
	if allSucceeded {
		message = fmt.Sprintf("All %d clusters provisioned successfully", len(statuses))
	} else {
		errCount := 0
		for _, s := range statuses {
			if s.Status != "Success" {
				errCount++
			}
		}
		message = fmt.Sprintf("%d of %d clusters have errors", errCount, len(statuses))
	}

	// Check if meaningful status content has changed — skip patch if identical
	// to avoid infinite reconcile loops from timestamp-only updates.
	if existing, ok := asr.Status.Providers[provider.ProviderKubernetes]; ok {
		if existing.Ready == allSucceeded && existing.Message == message && targetsEqual(existing.Targets, targets) {
			return nil
		}
	}

	providerStatus := pmv1.ProviderStatus{
		Ready:          allSucceeded,
		Message:        message,
		Targets:        targets,
		LastReconciled: metav1.Now(),
	}

	return provider.PatchProviderStatus(ctx, r.Client, asr, provider.ProviderKubernetes, providerStatus)
}

// targetsEqual compares two TargetStatus slices for semantic equality,
// ignoring LastUpdated timestamps.
func targetsEqual(a, b []pmv1.TargetStatus) bool {
	if len(a) != len(b) {
		return false
	}
	// Build a map for comparison (order-independent)
	am := make(map[string]pmv1.TargetStatus, len(a))
	for _, t := range a {
		am[t.TargetID] = t
	}
	for _, t := range b {
		existing, ok := am[t.TargetID]
		if !ok {
			return false
		}
		if existing.Status != t.Status || existing.Message != t.Message {
			return false
		}
	}
	return true
}

// emitEvent records a Kubernetes event on the ASR if a Recorder is configured.
func (r *Reconciler) emitEvent(asr *pmv1.ApplicationSpecificRole, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(asr, eventType, reason, message)
	}
}

// patchUnmappedCapabilityStatus writes a degraded ProviderStatus to the ASR
// indicating that one or more capabilities could not be mapped to RBAC verbs.
func (r *Reconciler) patchUnmappedCapabilityStatus(ctx context.Context, asr *pmv1.ApplicationSpecificRole, unmapped []string) error {
	return provider.PatchProviderStatus(ctx, r.Client, asr, provider.ProviderKubernetes, provider.UnmappedCapabilityStatus(unmapped))
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(name string, mgr ctrl.Manager) error {
	// Predicate that filters ASRs to only those with k8s ConnectorBinding refs.
	// Predicate that filters ASRs to only those with k8s ConnectorBinding refs
	// OR those that still carry the kubernetes finalizer (needing cleanup).
	// Non-ASR objects (e.g., ConnectorBindings from Watches()) are always allowed
	// through so their event handlers can fire.
	k8sASRPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		asr, ok := obj.(*pmv1.ApplicationSpecificRole)
		if !ok {
			return true // allow non-ASR events (ConnectorBinding watches) through
		}
		if r.CBIndex.HasAnyKubernetesRef(asr.Spec.ConnectorRefs) {
			return true
		}
		// Also allow ASRs that still have our finalizer (needs cleanup)
		return controllerutil.ContainsFinalizer(asr, FinalizerKubernetes) ||
			controllerutil.ContainsFinalizer(asr, FinalizerRBACCleanup)
	})

	// Ignore ASR status-only updates to avoid infinite reconcile loops when SSA
	// patches the status subresource. Status-only changes don't bump generation.
	// Non-ASR objects (ConnectorBinding watches) are always passed through.
	// Deletion timestamps, finalizer changes, and annotation changes pass through.
	ignoreASRStatusUpdates := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return true },
		DeleteFunc: func(e event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			// Always pass non-ASR objects (e.g., ConnectorBinding events)
			if _, ok := e.ObjectNew.(*pmv1.ApplicationSpecificRole); !ok {
				return true
			}
			// Pass through if generation changed (spec change)
			if e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() {
				return true
			}
			// Pass through if object is being deleted (finalizer removal, deletion)
			if !e.ObjectNew.GetDeletionTimestamp().IsZero() {
				return true
			}
			// Pass through if finalizers changed
			if len(e.ObjectOld.GetFinalizers()) != len(e.ObjectNew.GetFinalizers()) {
				return true
			}
			// Pass through if annotations changed (used by triggerReconcile in E2E
			// and by external tooling to force reconciliation)
			if len(e.ObjectOld.GetAnnotations()) != len(e.ObjectNew.GetAnnotations()) {
				return true
			}
			// Otherwise it's a status-only change — skip
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool { return true },
	}

	builder := ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&pmv1.ApplicationSpecificRole{}).
		WithEventFilter(predicate.And(k8sASRPredicate, ignoreASRStatusUpdates)).
		Watches(
			&pmv1.ConnectorBinding{},
			r.CBIndex.EnqueueASRsForConnectorBinding(mgr.GetClient()),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 2,
		})

	// Watch cluster change channel if provided — maps cluster names to matching ASR requests
	if r.ClusterChangeCh != nil {
		clusterChangeHandler := handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				clusterName := obj.GetName()
				return enqueueMatchingASRs(ctx, mgr.GetClient(), log.FromContext(ctx), clusterName)
			},
		)
		builder = builder.WatchesRawSource(source.Channel(r.ClusterChangeCh, clusterChangeHandler))
	}

	return builder.Complete(r)
}
