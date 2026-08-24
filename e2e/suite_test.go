// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/cloudoperators/permission-manager/e2e/shared"
)

// cfg is the suite configuration, populated on every Ginkgo process by the
// all-nodes phase of SynchronizedBeforeSuite. admin/downstream clients are
// rebuilt per-process (they are not serialisable across procs).
var (
	cfg        shared.SuiteConfig
	adminC     client.Client
	downstream client.Client
)

func TestE2E(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	RegisterFailHandler(Fail)
	RunSpecs(t, "Permission Manager E2E Suite")
}

// SynchronizedBeforeSuite bootstraps once on process 1 (create cluster, install
// CRDs, deploy controller, seed, onboard), returns the serialised SuiteConfig,
// and every process rebuilds its clients from it. This is required for
// `ginkgo --procs`: the cluster is mutated once, not per-proc.
var _ = SynchronizedBeforeSuite(func() []byte {
	ctx := context.Background()

	Expect(shared.Preflight()).To(Succeed())

	root := repoRoot()
	kindConfig := filepath.Join(root, "e2e", "testdata", "kind-config.yaml")
	crdDir := filepath.Join(root, "e2e", "testdata", "crds")
	chartDir := filepath.Join(root, "charts", "permission-manager")

	By("creating the kind cluster")
	Expect(shared.CreateCluster(shared.ClusterName, kindConfig)).To(Succeed())

	By("building and loading the controller image")
	Expect(shared.BuildAndLoadImage(shared.ClusterName, root)).To(Succeed())

	By("installing external (greenhouse + repo-guard) CRD stubs")
	Expect(shared.InstallExternalCRDs(shared.ClusterName, crdDir)).To(Succeed())

	By("deploying the Permission Manager controller via Helm")
	Expect(shared.DeployController(shared.ClusterName, chartDir)).To(Succeed())

	hostKubeconfig, err := shared.HostKubeconfig(shared.ClusterName)
	Expect(err).NotTo(HaveOccurred())

	c := shared.SuiteConfig{
		ClusterName:         shared.ClusterName,
		ControllerNamespace: shared.ControllerNamespace,
		AdminKubeconfig:     hostKubeconfig,
		GithubOrg:           shared.GithubOrg,
		GithubInstance:      shared.GithubInstance,
	}

	name, downstreamKubeconfig, err := shared.ResolveDownstream(c)
	Expect(err).NotTo(HaveOccurred())
	c.DownstreamName = name
	c.DownstreamKubeconfig = downstreamKubeconfig

	admin, err := shared.AdminClient(c)
	Expect(err).NotTo(HaveOccurred())

	By("seeding ConnectorBindings and the GitHub organisation")
	Expect(shared.EnsureK8sConnectorBinding(ctx, admin, c.ControllerNamespace)).To(Succeed())
	Expect(shared.EnsureGithubConnectorBinding(ctx, admin, c.ControllerNamespace)).To(Succeed())
	Expect(shared.EnsureGithubOrganization(ctx, admin, c.ControllerNamespace, c.GithubOrg)).To(Succeed())

	By("onboarding the downstream cluster")
	Expect(shared.OnboardDownstream(ctx, admin, c)).To(Succeed())

	// The downstream target namespace for namespace-scoped RBAC specs.
	down, err := shared.DownstreamClient(c)
	Expect(err).NotTo(HaveOccurred())
	Expect(shared.EnsureNamespace(ctx, down, shared.TargetNamespace)).To(Succeed())

	return c.Marshal()
}, func(data []byte) {
	var err error
	cfg, err = shared.UnmarshalSuiteConfig(data)
	Expect(err).NotTo(HaveOccurred())

	adminC, err = shared.AdminClient(cfg)
	Expect(err).NotTo(HaveOccurred())

	downstream, err = shared.DownstreamClient(cfg)
	Expect(err).NotTo(HaveOccurred())
})

// SynchronizedAfterSuite tears the cluster down once, on process 1, unless
// E2E_KEEP_CLUSTER=1.
var _ = SynchronizedAfterSuite(func() {}, func() {
	if os.Getenv(shared.EnvKeepCluster) == "1" {
		return
	}
	// Only self-managed kind clusters are torn down; a real downstream supplied
	// via env is never deleted by the suite.
	_ = shared.DeleteCluster(shared.ClusterName)
})

// repoRoot returns the repository root, derived from this source file's path
// (e2e/suite_test.go → repo root is two levels up).
func repoRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue(), "unable to determine caller for repo root")
	return filepath.Dir(filepath.Dir(thisFile))
}
