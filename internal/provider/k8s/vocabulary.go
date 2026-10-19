// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"strings"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/internal/naming"
)

// Scope tokens for the two coarse Kubernetes CCRN kinds. They name the section
// of a ConnectorBinding's capabilityMapping that a coarse CCRN resolves against
// and form part of shared ClusterRole names and aggregation labels.
const (
	scopeCluster   = "cluster"
	scopeNamespace = "namespace"
)

// validClusterCapabilities and validNamespaceCapabilities are the closed
// capability vocabularies for the coarse cluster.k8s and namespace.k8s CCRNs.
// A capability outside its scope's set makes the ConnectorBinding invalid.
var (
	validClusterCapabilities = map[string]struct{}{
		"MANAGE": {},
		"READ":   {},
	}
	validNamespaceCapabilities = map[string]struct{}{
		"MANAGE":  {},
		"OPERATE": {},
		"READ":    {},
	}
)

// sharedRoleName returns the aggregated root ClusterRole name for a capability
// on a ConnectorBinding, e.g. "pm-my-cb-cluster-read".
func sharedRoleName(cbName, scope, capability string) string {
	return resourceNamePrefix + cbName + "-" + scope + "-" + strings.ToLower(capability)
}

// baselineChildName returns the baseline child ClusterRole name for a
// capability, e.g. "pm-my-cb-cluster-read-baseline".
func baselineChildName(cbName, scope, capability string) string {
	return sharedRoleName(cbName, scope, capability) + "-baseline"
}

// aggregateToLabel returns the auto-derived aggregation label key that the root
// ClusterRole selects and the baseline child carries, e.g.
// "permission-manager.cloudoperators.dev/aggregate-to-cluster-read".
func aggregateToLabel(scope, capability string) string {
	return pmapis.LabelDomain + "aggregate-to-" + scope + "-" + strings.ToLower(capability)
}

// asrLabelValue renders an ASR name for use as a Kubernetes label value. Label
// values are capped at 63 characters, so a longer name is truncated with a
// stable hash suffix. The orphan-cleanup selector applies the identical
// transform, so label-based selection stays exact.
func asrLabelValue(asrName string) string {
	return naming.TruncateWithHash(asrName, 63)
}
