// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// pm-controller-kubernetes is the Kubernetes provider controller binary.
// It watches ASRs with Kubernetes ConnectorBindings and provisions RBAC
// resources on target clusters.
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/internal/controller/kubernetes"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
	greenhouse "github.com/cloudoperators/permission-manager/controller/pkg/greenhouse"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	version  = "dev"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(pmv1.AddToScheme(scheme))
	utilruntime.Must(greenhouse.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var watchNamespace string
	var enableAuditLog bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election.")
	flag.StringVar(&watchNamespace, "watch-namespace", "", "Namespace to scope watches to.")
	flag.BoolVar(&enableAuditLog, "audit-log", true, "Enable OTel-conformant audit logging to stdout.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgrOpts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "pm-controller-kubernetes.permission-manager.cloudoperators.dev",
	}
	if watchNamespace != "" {
		mgrOpts.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				watchNamespace: {},
			},
		}
		setupLog.Info("scoping watches to namespace", "namespace", watchNamespace)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Initialize shared state
	cbIndex := kubernetes.NewConnectorBindingIndexer(ctrl.Log)
	clusterInventory := kubernetes.NewClusterInventory(ctrl.Log)

	// Initialize audit logger
	var auditLog *audit.Logger
	if enableAuditLog {
		auditLog = audit.New(os.Stdout, "pm-controller-kubernetes", version)
	} else {
		auditLog = audit.Disabled()
	}

	clusterClientFactory := &kubernetes.GreenhouseClusterClientFactory{
		ManagementClient: mgr.GetClient(),
		Namespace:        watchNamespace,
	}

	// Channel for cluster change events
	clusterChangeCh := make(chan kubernetes.GenericEvent, 64)

	// Setup ASR reconciler (Kubernetes provider)
	if err = (&kubernetes.Reconciler{
		Client:               mgr.GetClient(),
		Recorder:             mgr.GetEventRecorderFor("pm-controller-kubernetes"),
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInventory,
		ClusterClientFactory: clusterClientFactory,
		NamespaceLister:      clusterClientFactory,
		Cleaner:              &kubernetes.RBACCleaner{ClusterInventory: clusterInventory, ClusterClientFactory: clusterClientFactory, AuditLog: auditLog},
		AuditLog:             auditLog,
		ClusterChangeCh:      clusterChangeCh,
	}).SetupWithManager("asr", mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ASR")
		os.Exit(1)
	}

	// Setup ClusterReconciler
	if err = (&kubernetes.ClusterReconciler{
		Client:               mgr.GetClient(),
		ClusterInventory:     clusterInventory,
		ClusterClientFactory: clusterClientFactory,
		OnClusterChange: func(clusterName string) {
			kubernetes.NotifyClusterChange(clusterChangeCh, clusterName)
		},
	}).SetupWithManager("cluster", mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Cluster")
		os.Exit(1)
	}

	// Register ConnectorBinding startup syncer
	if err := mgr.Add(kubernetes.NewConnectorBindingStartupSyncer(
		cbIndex, mgr.GetClient(), ctrl.Log,
	)); err != nil {
		setupLog.Error(err, "unable to add ConnectorBinding startup syncer")
		os.Exit(1)
	}

	// Health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting pm-controller-kubernetes")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
