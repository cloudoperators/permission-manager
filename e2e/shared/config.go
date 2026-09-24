// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

// Package shared holds the reusable building blocks for the Permission Manager
// e2e suite: cluster bootstrap and onboarding, client construction, randomised
// name generation, and common Gomega assertions. Everything here is guarded by
// the e2e build tag so it never enters a normal `go test ./...` build.
package shared

import (
	"encoding/json"
	"os"
)

// Environment variables that tune the suite without touching test code.
const (
	// EnvKeepCluster, when set to "1", leaves the kind cluster running after
	// the suite finishes (useful for debugging).
	EnvKeepCluster = "E2E_KEEP_CLUSTER"
	// EnvSkipBuild, when set to "1", skips the docker build + kind load steps
	// and assumes the controller image is already loaded.
	EnvSkipBuild = "E2E_SKIP_BUILD"
	// EnvDownstreamKubeconfig points at a kubeconfig file for a real downstream
	// cluster to onboard instead of self-onboarding the kind cluster.
	EnvDownstreamKubeconfig = "E2E_DOWNSTREAM_KUBECONFIG"
	// EnvDownstreamName overrides the downstream cluster name (defaults to the
	// kind cluster name). The value is used both as the Cluster CR / Secret name
	// and as the cluster segment in generated CCRNs.
	EnvDownstreamName = "E2E_DOWNSTREAM_NAME"
)

// Fixed identifiers shared across bootstrap and specs.
const (
	ClusterName         = "pm-e2e"
	ControllerNamespace = "permission-manager"
	ControllerImageRepo = "pm/manager"
	ControllerImageTag  = "e2e"
	ControllerImage     = ControllerImageRepo + ":" + ControllerImageTag

	// K8sConnectorBinding is the ConnectorBinding referenced by kubernetes specs.
	K8sConnectorBinding = "e2e-k8s"
	// GithubConnectorBinding is the ConnectorBinding referenced by github specs.
	GithubConnectorBinding = "e2e-github"

	// GithubInstance is the GitHub instance segment used in github CCRNs.
	GithubInstance = "github.com"
	// GithubOrg is the organisation pre-seeded as a GithubOrganization CR so the
	// github happy path has an org to target.
	GithubOrg = "e2e-org"
	// TargetNamespace is a downstream namespace pre-created for namespace-scoped
	// RBAC specs (the provider does not create namespaces).
	TargetNamespace = "e2e-apps"
)

// SuiteConfig is the state produced once by Ginkgo node 1 and shared with every
// parallel process. It contains only serialisable data; each process rebuilds
// its own clients from AdminKubeconfig.
type SuiteConfig struct {
	ClusterName          string `json:"clusterName"`
	ControllerNamespace  string `json:"controllerNamespace"`
	AdminKubeconfig      string `json:"adminKubeconfig"`
	DownstreamKubeconfig string `json:"downstreamKubeconfig"`
	DownstreamName       string `json:"downstreamName"`
	GithubOrg            string `json:"githubOrg"`
	GithubInstance       string `json:"githubInstance"`
}

// Marshal serialises the config for transfer from node 1 to all Ginkgo procs.
func (c SuiteConfig) Marshal() []byte {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err) // SuiteConfig is plain data; marshalling cannot fail in practice.
	}
	return b
}

// UnmarshalSuiteConfig reconstructs the config on each Ginkgo proc.
func UnmarshalSuiteConfig(b []byte) (SuiteConfig, error) {
	var c SuiteConfig
	err := json.Unmarshal(b, &c)
	return c, err
}

// selfOnboard reports whether the suite onboards the kind cluster as its own
// downstream target (the default), versus a real cluster supplied via env.
func selfOnboard() bool {
	return os.Getenv(EnvDownstreamKubeconfig) == ""
}
