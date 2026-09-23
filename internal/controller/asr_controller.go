// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package controller contains the single controller that reconciles ApplicationSpecificRole.
package controller

import (
	"context"
	"errors"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	pmapis "github.com/cloudoperators/permission-manager/api"
	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	pmccrn "github.com/cloudoperators/permission-manager/internal/ccrn"
	pmconfig "github.com/cloudoperators/permission-manager/internal/config"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
	"github.com/cloudoperators/permission-manager/internal/provider/github"
	pmk8s "github.com/cloudoperators/permission-manager/internal/provider/k8s"
	pmutils "github.com/cloudoperators/permission-manager/internal/utils"
)

// ASRReconciler is the single controller for ApplicationSpecificRole.
type ASRReconciler struct {
	client.Client
	Cfg                     *pmconfig.ProviderConfig
	PodNamespace            string
	ProviderConfigName      string
	Log                     logr.Logger
	AuditLog                *audit.Logger
	K8sCCRNParser           *pmccrn.Parser
	MaxConcurrentReconciles int

	k8sClusterStore toolscache.Store
	ghPhase         *github.Phase
	recorder        events.EventRecorder
}

// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=applicationspecificroles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=applicationspecificroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=applicationspecificroles/finalizers,verbs=update
// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=connectorbindings,verbs=get;list;watch
// +kubebuilder:rbac:groups=permission-manager.cloudoperators.dev,resources=functionalroles,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="events.k8s.io",resources=events,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=greenhouse.sap,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
// +kubebuilder:rbac:groups=repo-guard.cloudoperators.dev,resources=githubteams;githubteamrepositories;githuborganizations,verbs=get;list;watch;create;update;patch;delete

// SetupWithManager registers the controller with the manager.
func (r *ASRReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorder("asr-controller")

	clusterInformer, err := mgr.GetCache().GetInformer(context.Background(), &greenhousev1alpha1.Cluster{})
	if err != nil {
		return err
	}
	sharedInformer, ok := clusterInformer.(toolscache.SharedIndexInformer)
	if !ok {
		return errors.New("cluster informer does not implement SharedIndexInformer")
	}
	r.k8sClusterStore = sharedInformer.GetStore()
	r.ghPhase = github.New(r.Client, r.AuditLog, r.Log)

	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ApplicationSpecificRole{},
		pmapis.ASRConnectorRefsField,
		func(obj client.Object) []string {
			asr, ok := obj.(*v1alpha1.ApplicationSpecificRole)
			if !ok {
				return nil
			}
			return asr.Spec.ConnectorRefs
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ApplicationSpecificRole{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.reconcileAllASRs),
			builder.WithPredicates(lifecycle.ProviderConfigPredicate(r.ProviderConfigName, r.PodNamespace)),
		).
		Watches(
			&v1alpha1.ConnectorBinding{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueASRsForConnectorBinding),
		).
		Watches(
			&repoguardv1.GithubTeam{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueASRForGithubTeam),
		).
		Watches(
			&greenhousev1alpha1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueASRsForCluster),
		).
		Complete(r)
}

// Reconcile loads the ASR and routes to EnsureCreated or EnsureDeleted via the lifecycle engine.
func (r *ASRReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return lifecycle.Reconcile(ctx, r.Client, req.NamespacedName, &v1alpha1.ApplicationSpecificRole{}, r, nil)
}

// EnsureCreated reconciles the ASR towards its desired state.
func (r *ASRReconciler) EnsureCreated(ctx context.Context, obj lifecycle.RuntimeObject) (ctrl.Result, lifecycle.ReconcileResult, error) {
	asr := obj.(*v1alpha1.ApplicationSpecificRole)

	if r.Cfg.Enabled(string(pmmeta.ProviderGithub)) {
		var errs []error
		for _, ref := range asr.Spec.ConnectorRefs {
			cb := &v1alpha1.ConnectorBinding{}
			if err := r.Get(ctx, types.NamespacedName{Name: ref, Namespace: asr.Namespace}, cb); err != nil {
				continue
			}
			if cb.Spec.Provider == string(pmmeta.ProviderGithub) {
				if err := r.ghPhase.Reconcile(ctx, asr, cb); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if len(errs) > 0 {
			return ctrl.Result{}, lifecycle.Failed, errors.Join(errs...)
		}
	}

	if r.Cfg.Enabled(string(pmmeta.ProviderKubernetes)) {
		return r.reconcileKubernetes(ctx, asr)
	}

	return ctrl.Result{}, lifecycle.Success, nil
}

// EnsureDeleted cleans up resources owned by the ASR.
func (r *ASRReconciler) EnsureDeleted(ctx context.Context, obj lifecycle.RuntimeObject) (ctrl.Result, lifecycle.ReconcileResult, error) {
	asr := obj.(*v1alpha1.ApplicationSpecificRole)

	if r.Cfg.Enabled(string(pmmeta.ProviderGithub)) {
		var errs []error
		for _, ref := range asr.Spec.ConnectorRefs {
			cb := &v1alpha1.ConnectorBinding{}
			if err := r.Get(ctx, types.NamespacedName{Name: ref, Namespace: asr.Namespace}, cb); err != nil {
				continue
			}
			if cb.Spec.Provider == string(pmmeta.ProviderGithub) {
				if err := r.ghPhase.ReconcileDelete(ctx, asr); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if len(errs) > 0 {
			return ctrl.Result{}, lifecycle.Failed, errors.Join(errs...)
		}
	}

	if r.Cfg.Enabled(string(pmmeta.ProviderKubernetes)) {
		return r.cleanUpKubernetes(ctx, asr)
	}

	return ctrl.Result{}, lifecycle.Success, nil
}

// defaultRemoteClientFactory adapts pmutils.NewRemoteClient's variadic
// signature to the non-variadic pmk8s.RemoteClientFactory.
func defaultRemoteClientFactory(ctx context.Context, c client.Client, clusterName, namespace string) (client.Client, error) {
	return pmutils.NewRemoteClient(ctx, c, clusterName, namespace)
}

// defaultRemoteDiscoveryFactory adapts pmutils.NewRemoteDiscoveryClient to the
// pmk8s.RemoteDiscoveryFactory signature.
func defaultRemoteDiscoveryFactory(ctx context.Context, c client.Client, clusterName, namespace string) (discovery.DiscoveryInterface, error) {
	return pmutils.NewRemoteDiscoveryClient(ctx, c, clusterName, namespace)
}

// reconcileKubernetes constructs a fresh k8s Phase and runs the create/update
// subroutine chain. The Phase fetches its own ConnectorBindings via
// EnsureConnectorBindings — the controller passes nothing beyond the ASR.
func (r *ASRReconciler) reconcileKubernetes(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole) (ctrl.Result, lifecycle.ReconcileResult, error) {
	p := &pmk8s.Phase{
		Client:          r.Client,
		RemoteClient:    defaultRemoteClientFactory,
		RemoteDiscovery: defaultRemoteDiscoveryFactory,
		AuditLog:        r.AuditLog,
		Log:             r.Log,
		ClusterStore:    r.k8sClusterStore,
		CCRNParser:      r.K8sCCRNParser,
	}
	routines := []lifecycle.SubRoutine{
		p.EnsureASRValidity(asr),
		p.EnsureConnectorBindings(asr),
		p.EnsureRbacCompiled(asr),
		p.EnsureOrphansCleanedUp(asr),
		p.EnsureRbacApplied(asr),
		p.EnsureProviderStatus(asr),
	}
	return lifecycle.ExecuteSubRoutine(ctx, routines)
}

// cleanUpKubernetes constructs a fresh k8s Phase and runs the delete-path
// subroutine chain. Operates off status alone — no CBs needed.
func (r *ASRReconciler) cleanUpKubernetes(ctx context.Context, asr *v1alpha1.ApplicationSpecificRole) (ctrl.Result, lifecycle.ReconcileResult, error) {
	p := &pmk8s.Phase{
		Client:       r.Client,
		RemoteClient: defaultRemoteClientFactory,
		AuditLog:     r.AuditLog,
		Log:          r.Log,
		ClusterStore: r.k8sClusterStore,
		CCRNParser:   r.K8sCCRNParser,
	}
	routines := []lifecycle.SubRoutine{
		p.EnsureRbacRemoved(asr),
	}
	return lifecycle.ExecuteSubRoutine(ctx, routines)
}

// GetEventRecorder returns the event recorder.
func (r *ASRReconciler) GetEventRecorder() events.EventRecorder {
	return r.recorder
}

// reconcileAllASRs reloads pm-provider-cm and enqueues all ASRs.
func (r *ASRReconciler) reconcileAllASRs(ctx context.Context, _ client.Object) []ctrl.Request {
	newCfg, err := pmconfig.ReloadProviderConfig(ctx, r.Client, r.ProviderConfigName, r.PodNamespace)
	if err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to reload provider config")
		return nil
	}
	r.Cfg = newCfg

	var list v1alpha1.ApplicationSpecificRoleList
	if err := r.List(ctx, &list); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list ASRs")
		return nil
	}
	requests := make([]ctrl.Request, 0, len(list.Items))
	for _, asr := range list.Items {
		if !asr.DeletionTimestamp.IsZero() {
			continue
		}
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: asr.Name, Namespace: asr.Namespace},
		})
	}
	return requests
}

// enqueueASRsForConnectorBinding enqueues all ASRs that reference the changed ConnectorBinding.
func (r *ASRReconciler) enqueueASRsForConnectorBinding(ctx context.Context, obj client.Object) []ctrl.Request {
	var list v1alpha1.ApplicationSpecificRoleList
	if err := r.List(ctx, &list, client.MatchingFields{pmapis.ASRConnectorRefsField: obj.GetName()}); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list ASRs for ConnectorBinding", "name", obj.GetName())
		return nil
	}
	requests := make([]ctrl.Request, 0, len(list.Items))
	for _, asr := range list.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: asr.Name, Namespace: asr.Namespace},
		})
	}
	return requests
}

// enqueueASRForGithubTeam enqueues the ASR that owns the changed GithubTeam via labels.
func (r *ASRReconciler) enqueueASRForGithubTeam(_ context.Context, obj client.Object) []ctrl.Request {
	labels := obj.GetLabels()
	if labels == nil {
		return nil
	}
	asrName := labels[pmapis.LabelSourceASR]
	asrNamespace := labels[pmapis.LabelSourceNamespace]
	if asrName == "" || asrNamespace == "" {
		return nil
	}
	return []ctrl.Request{
		{NamespacedName: types.NamespacedName{Name: asrName, Namespace: asrNamespace}},
	}
}

// enqueueASRsForCluster enqueues all ASRs when a Cluster changes.
func (r *ASRReconciler) enqueueASRsForCluster(ctx context.Context, _ client.Object) []ctrl.Request {
	var list v1alpha1.ApplicationSpecificRoleList
	if err := r.List(ctx, &list); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list ASRs for cluster change")
		return nil
	}
	requests := make([]ctrl.Request, 0, len(list.Items))
	for _, asr := range list.Items {
		if !asr.DeletionTimestamp.IsZero() {
			continue
		}
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: asr.Name, Namespace: asr.Namespace},
		})
	}
	return requests
}
