// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Permission Manager E2E Suite")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	initClients()
	By("verifying the shared dev environment is up")
	verifyEnvironment()
	By("creating the remote test namespace")
	ensureRemoteTestNamespace()
	By("applying connector fixtures to the org namespace")
	applyFixtures()
	By("cleaning up stale resources from previous runs")
	cleanupStaleResources()
	return nil
}, func(_ []byte) {
	initClients()
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
})

var _ = ReportAfterSuite("controller logs on failure", func(report Report) {
	if report.SuiteSucceeded {
		return
	}
	dumpControllerLogs()
})
