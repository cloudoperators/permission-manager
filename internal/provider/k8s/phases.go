// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"

	"github.com/go-logr/logr"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
)

// RemoteClientFactory builds a client.Client for a remote cluster. Production
// wires pmutils.NewRemoteClient; tests inject a fake or mock.
type RemoteClientFactory func(ctx context.Context, local client.Client, clusterName, namespace string) (client.Client, error)

// RemoteDiscoveryFactory builds a discovery client for a remote cluster, used to
// enumerate a denied API group's resources during deny-driven RBAC expansion.
// Production wires pmutils.NewRemoteDiscoveryClient; tests inject a fake.
type RemoteDiscoveryFactory func(ctx context.Context, local client.Client, clusterName, namespace string) (discovery.DiscoveryInterface, error)

// Phase holds dependencies and per-reconcile accumulators for the kubernetes
// provider subroutine pipeline. A fresh Phase is constructed per reconcile.
type Phase struct {
	Client          client.Client
	RemoteClient    RemoteClientFactory
	RemoteDiscovery RemoteDiscoveryFactory
	AuditLog        *audit.Logger
	Log             logr.Logger
	ClusterStore    cache.Store
	CCRNParser      *ccrn.Parser

	// kubernetes ConnectorBindings referenced by the ASR.
	cbs []*v1alpha1.ConnectorBinding

	// compiled RBAC objects, one entry per matched cluster.
	compiled []compiledTarget

	// degraded-reason accumulators, surfaced via provider status and conditions.
	unmappedCaps             []string
	invalidCCRNs             []string
	unmatchedClusterPatterns []string
	notReadyClusters         []string
	discoveryDegraded        []string

	// per-cluster outcomes from EnsureRbacApplied / EnsureOrphansCleanedUp.
	provisionStatuses []clusterStatus
	orphanStatuses    []clusterStatus

	// clusters whose orphan RBAC was fully removed this reconcile; their stale
	// target entries are pruned from status by EnsureProviderStatus.
	orphanCleaned []string

	// errors collected from each phase; aggregated by EnsureProviderStatus.
	provisionErrs []error
	orphanErrs    []error
}

// compiledTarget is one cluster's fully-built RBAC objects, ready to apply.
type compiledTarget struct {
	clusterName         string
	clusterRoles        []rbacv1.ClusterRole
	clusterRoleBindings []rbacv1.ClusterRoleBinding
	roles               []rbacv1.Role
	roleBindings        []rbacv1.RoleBinding
}

// clusterStatus tracks the provisioning outcome for a single cluster.
type clusterStatus struct {
	name             string
	err              error
	appliedResources []pmmeta.AppliedResourceRef
}

// RBAC resource naming + kind constants.
const (
	resourceNamePrefix = "pm-"

	kindClusterRole        = "ClusterRole"
	kindClusterRoleBinding = "ClusterRoleBinding"
	kindRole               = "Role"
	kindRoleBinding        = "RoleBinding"
)
