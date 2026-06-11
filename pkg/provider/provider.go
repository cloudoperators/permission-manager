// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package provider defines shared provider ID constants and helper functions
// used across all provider controllers.
package provider

const (
	// ProviderKubernetes is the provider ID for the Kubernetes RBAC provider.
	ProviderKubernetes = "kubernetes"

	// ProviderGithub is the provider ID for the GitHub provider.
	ProviderGithub = "github"

	// ProviderGreenhouseTeams is the provider ID for the Greenhouse Teams provider.
	ProviderGreenhouseTeams = "greenhouse-teams"

	// finalizerPrefix is the base domain used for all provider finalizers.
	finalizerPrefix = "permission-manager.cloudoperators.dev/"
)

// FinalizerName returns the finalizer string for a given provider.
// The format is "permission-manager.cloudoperators.dev/<provider>".
func FinalizerName(provider string) string {
	return finalizerPrefix + provider
}
