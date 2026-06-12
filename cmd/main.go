// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/tls"
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/controller/aggregator"
	"github.com/cloudoperators/permission-manager/internal/controller/github"
	"github.com/cloudoperators/permission-manager/internal/controller/greenhouseteams"
	"github.com/cloudoperators/permission-manager/internal/controller/kubernetes"
	"github.com/cloudoperators/permission-manager/pkg/audit"
	"github.com/cloudoperators/permission-manager/pkg/cbindex"
	pmmetrics "github.com/cloudoperators/permission-manager/pkg/metrics"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	greenhousev1alpha2 "github.com/cloudoperators/greenhouse/api/v1alpha2"
	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	version  = "dev"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(greenhousev1alpha1.AddToScheme(scheme))
	utilruntime.Must(greenhousev1alpha2.AddToScheme(scheme))
	utilruntime.Must(repoguardv1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme

	ctrlmetrics.Registry.MustRegister(pmmetrics.ASRConditionGauge)
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var enableAuditLog bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.BoolVar(&enableAuditLog, "audit-log", true, "Enable audit logging to stdout.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "0389e250.cloudoperators.dev",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	if err := (&aggregator.Reconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("aggregator"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "applicationspecificrole")
		os.Exit(1)
	}
	err = startGitHubReconciler(mgr, enableAuditLog)
	if err != nil {
		os.Exit(1)
	}

	err = startGreenhouseTeamsReconciler(mgr, enableAuditLog)
	if err != nil {
		os.Exit(1)
	}

	err = startKubernetesReconciler(mgr, enableAuditLog)
	if err != nil {
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}

func startGitHubReconciler(mgr manager.Manager, enableAuditLog bool) error {
	auditLogger := audit.Disabled()
	if enableAuditLog {
		auditLogger = audit.New(os.Stdout, "github-provider", version)
	}

	// Initialize the GitHub ConnectorBinding indexer
	cbIndex := github.NewGithubIndexer(ctrl.Log)

	// Register ConnectorBinding startup syncer (runs after cache sync)
	err := mgr.Add(cbindex.NewStartupSyncer[v1alpha1.ConnectorBindingSpec](cbIndex.Index, mgr.GetClient(), ctrl.Log))
	if err != nil {
		setupLog.Error(err, "unable to add ConnectorBinding startup syncer")
		return err
	}

	// Setup GitHub ASR reconciler
	if err = (&github.GithubReconciler{
		Client:   mgr.GetClient(),
		Log:      ctrl.Log.WithName("controllers").WithName("GithubASR"),
		CBIndex:  cbIndex,
		AuditLog: auditLogger,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "GithubASR")
		return err
	}
	return nil
}

func startGreenhouseTeamsReconciler(mgr manager.Manager, enableAuditLog bool) error {
	auditLogger := audit.Disabled()
	if enableAuditLog {
		auditLogger = audit.New(os.Stdout, "greenhouse-teams", version)
	}

	// Initialize shared state
	cbIndex := greenhouseteams.NewConnectorBindingIndexer(ctrl.Log)

	// Register startup syncer to seed ConnectorBinding index before reconciliation
	err := mgr.Add(greenhouseteams.NewConnectorBindingStartupSyncer(cbIndex, mgr.GetClient(), ctrl.Log))
	if err != nil {
		setupLog.Error(err, "unable to add startup syncer")
		return err
	}

	// Setup greenhouse-teams reconciler
	if err = (&greenhouseteams.Reconciler{
		Client:   mgr.GetClient(),
		Log:      ctrl.Log.WithName("controllers").WithName("greenhouse-teams"),
		CBIndex:  cbIndex,
		AuditLog: auditLogger,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "greenhouse-teams")
		return err
	}
	return nil
}

func startKubernetesReconciler(mgr manager.Manager, enableAuditLog bool) error {
	auditLogger := audit.Disabled()
	if enableAuditLog {
		auditLogger = audit.New(os.Stdout, "pm-controller-kubernetes", version)
	}

	cbIndex := kubernetes.NewConnectorBindingIndexer(ctrl.Log)
	clusterInventory := kubernetes.NewClusterInventory(ctrl.Log)
	clusterClientFactory := &kubernetes.GreenhouseClusterClientFactory{
		ManagementClient: mgr.GetClient(),
		Namespace:        "",
	}
	err := mgr.Add(kubernetes.NewConnectorBindingStartupSyncer(
		cbIndex, mgr.GetClient(), ctrl.Log,
	))
	if err != nil {
		setupLog.Error(err, "unable to add ConnectorBinding startup syncer")
		return err
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
		Cleaner:              &kubernetes.RBACCleaner{ClusterInventory: clusterInventory, ClusterClientFactory: clusterClientFactory, AuditLog: auditLogger},
		AuditLog:             auditLogger,
		ClusterChangeCh:      clusterChangeCh,
	}).SetupWithManager("asr", mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ASR")
		return err
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
		return err
	}
	return nil
}
