// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"strings"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/internal/naming"
)

// Scope tokens for the two coarse Kubernetes CCRN kinds. They name the
// capabilityMapping section a coarse CCRN resolves against and seed role/label names.
const (
	scopeCluster   = "cluster"
	scopeNamespace = "namespace"

	capManage  = "MANAGE"
	capOperate = "OPERATE"
	capRead    = "READ"
	capWrite   = "WRITE"
)

// validClusterCapabilities and validNamespaceCapabilities are the closed capability
// vocabularies for cluster.k8s and namespace.k8s; anything outside makes the CB invalid.
var (
	validClusterCapabilities = map[string]struct{}{
		capManage: {},
		capRead:   {},
	}
	validNamespaceCapabilities = map[string]struct{}{
		capManage:  {},
		capOperate: {},
		capRead:    {},
	}
)

// sharedRoleName returns the aggregated root ClusterRole name for a capability
// on a ConnectorBinding, e.g. "pm-s-my-cb-cluster-read".
func sharedRoleName(cbName, scope, capability string) string {
	return sharedNamePrefix + cbName + "-" + scope + "-" + strings.ToLower(capability)
}

// baselineChildName returns the baseline child ClusterRole name for a
// capability, e.g. "pm-s-my-cb-cluster-read-baseline".
func baselineChildName(cbName, scope, capability string) string {
	return sharedRoleName(cbName, scope, capability) + "-baseline"
}

// aggregateToLabel returns the shared aggregation label key the root selects and
// the baseline child carries, e.g. ".../aggregate-to-cluster-read".
func aggregateToLabel(scope, capability string) string {
	return pmapis.LabelDomain + "aggregate-to-" + scope + "-" + strings.ToLower(capability)
}

// cbAggregateToLabel returns a CB-private aggregation label key, so a root never
// aggregates another CB's baseline for the same scope/capability. The CB name is
// hashed to keep the key label-safe and bounded.
func cbAggregateToLabel(cbName, scope, capability string) string {
	return pmapis.LabelDomain + "aggregate-to-" + naming.ShortHash(cbName) + "-" + scope + "-" + strings.ToLower(capability)
}

// asrLabelValue renders an ASR name as a Kubernetes label value (truncated with a
// stable hash past 63 chars). The orphan-cleanup selector applies the same transform.
func asrLabelValue(asrName string) string {
	return naming.TruncateWithHash(asrName, 63)
}
