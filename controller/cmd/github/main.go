// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// github-provider is a standalone Kubernetes controller that watches Permission Manager
// ApplicationSpecificRole CRDs referencing GitHub ConnectorBindings.
//
// It is deployed as a separate binary and will eventually provision GitHub
// team/repository permissions based on ASR specifications.
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	github "github.com/cloudoperators/permission-manager/controller/internal/controller/github"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
	"github.com/cloudoperators/permission-manager/controller/pkg/cbindex"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	version  = "dev"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(pmv1.AddToScheme(scheme))
	utilruntime.Must(github.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var enableAuditLog bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableAuditLog, "audit-log", true, "Enable audit logging to stdout.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Construct audit logger
	var auditLogger *audit.Logger
	if enableAuditLog {
		auditLogger = audit.New(os.Stdout, "github-provider", version)
	} else {
		auditLogger = audit.Disabled()
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "github-provider.permission-manager.cloudoperators.dev",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Initialize the GitHub ConnectorBinding indexer
	cbIndex := github.NewGithubIndexer(ctrl.Log)

	// Register ConnectorBinding startup syncer (runs after cache sync)
	if err := mgr.Add(cbindex.NewStartupSyncer[pmv1.ConnectorBindingSpec](cbIndex.Index, mgr.GetClient(), ctrl.Log)); err != nil {
		setupLog.Error(err, "unable to add ConnectorBinding startup syncer")
		os.Exit(1)
	}

	// Setup GitHub ASR reconciler
	if err = (&github.GithubReconciler{
		Client:   mgr.GetClient(),
		Log:      ctrl.Log.WithName("controllers").WithName("GithubASR"),
		CBIndex:  cbIndex,
		AuditLog: auditLogger,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "GithubASR")
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

	setupLog.Info("starting github-provider manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
