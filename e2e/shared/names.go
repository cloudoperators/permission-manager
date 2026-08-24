// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package shared

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/rand"
)

// RandSuffix returns a short random lowercase-alphanumeric suffix. It is used to
// fuzz resource names per spec so interleaved or repeated runs never collide.
func RandSuffix() string {
	return rand.String(5)
}

// ASRName returns base with a random suffix, e.g. "k8s-cluster-read-a1b2c".
func ASRName(base string) string {
	return fmt.Sprintf("%s-%s", base, RandSuffix())
}

// ClusterCCRN builds a cluster-scoped kubernetes CCRN for the given cluster.
func ClusterCCRN(cluster string) string {
	return "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/" + cluster
}

// NamespaceCCRN builds a namespace-scoped kubernetes CCRN.
func NamespaceCCRN(cluster, namespace string) string {
	return fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/%s", cluster, namespace)
}

// ClusterWildcardCCRN builds a cluster-scoped CCRN whose cluster segment is a
// trailing-glob pattern (e.g. "pm-*").
func ClusterWildcardCCRN(pattern string) string {
	return "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/" + pattern
}

// NamespaceWildcardCCRN builds a namespace-scoped CCRN whose namespace segment is
// a glob pattern. Namespace wildcards are unsupported and rejected as InvalidCCRN.
func NamespaceWildcardCCRN(cluster, nsPattern string) string {
	return fmt.Sprintf("urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/%s/%s", cluster, nsPattern)
}

// OrgCCRN builds a github organization CCRN.
func OrgCCRN(instance, org string) string {
	return fmt.Sprintf("urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/%s/%s", instance, org)
}

// RepoCCRN builds a github repository CCRN.
func RepoCCRN(instance, org, repo string) string {
	return fmt.Sprintf("urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/%s/%s/%s", instance, org, repo)
}

// InvalidTypeCCRN returns a well-formed URN with an unsupported type segment
// ("pod"), which the CCRN parser rejects with "unsupported CCRN type".
func InvalidTypeCCRN(cluster, namespace string) string {
	return fmt.Sprintf("urn:ccrn:pod.k8s.ccrn.cloudoperators.dev/v1/%s/%s/my-pod", cluster, namespace)
}
