// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
)

// executeReconcilePlanSimple is a test helper that wraps executeReconcilePlan
// without namespace expansion or event recording. Returns (statuses, hasErrors).
func executeReconcilePlanSimple(ctx context.Context, plan ReconcilePlan, factory ClusterClientFactory) ([]ClusterProvisioningStatus, bool) {
	statuses, errs := executeReconcilePlan(ctx, plan, factory, nil, nil, audit.Disabled())
	return statuses, len(errs) > 0
}

// --- planReconcile tests (pure, no I/O) ---

func TestPlanReconcile_BasicProvision(t *testing.T) {
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name: "test-group",
		},
	}

	scopes := map[string]*clusterScope{
		"prod-eu": {
			clusterVerbs:   map[string]struct{}{"get": {}, "list": {}, "watch": {}},
			namespaceVerbs: map[string]map[string]struct{}{},
		},
	}

	plan := planReconcile(asr, scopes, nil)

	if plan.ASRName != "test-asr" {
		t.Errorf("ASRName = %q, want %q", plan.ASRName, "test-asr")
	}
	if plan.SpecName != "test-group" {
		t.Errorf("SpecName = %q, want %q", plan.SpecName, "test-group")
	}
	if len(plan.ClustersToProvision) != 1 {
		t.Fatalf("expected 1 cluster to provision, got %d", len(plan.ClustersToProvision))
	}
	if _, ok := plan.ClustersToProvision["prod-eu"]; !ok {
		t.Error("expected prod-eu in ClustersToProvision")
	}
	if len(plan.OrphanClusters) != 0 {
		t.Errorf("expected 0 orphan clusters, got %d", len(plan.OrphanClusters))
	}
}

func TestPlanReconcile_DetectsOrphans(t *testing.T) {
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr"},
		Spec:       pmv1.ApplicationSpecificRoleSpec{Name: "test-group"},
	}

	scopes := map[string]*clusterScope{
		"prod-eu": {clusterVerbs: map[string]struct{}{"get": {}}, namespaceVerbs: map[string]map[string]struct{}{}},
	}

	prevStatuses := []ClusterProvisioningStatus{
		{ClusterName: "prod-eu", Status: "Success"},
		{ClusterName: "staging-us", Status: "Success"},
		{ClusterName: "dev-asia", Status: "Success"},
	}

	plan := planReconcile(asr, scopes, prevStatuses)

	if len(plan.OrphanClusters) != 2 {
		t.Fatalf("expected 2 orphan clusters, got %d: %v", len(plan.OrphanClusters), plan.OrphanClusters)
	}

	orphanSet := make(map[string]bool)
	for _, o := range plan.OrphanClusters {
		orphanSet[o] = true
	}
	if !orphanSet["staging-us"] {
		t.Error("expected staging-us in orphan clusters")
	}
	if !orphanSet["dev-asia"] {
		t.Error("expected dev-asia in orphan clusters")
	}
}

func TestPlanReconcile_NoOrphansWhenAllStillTargeted(t *testing.T) {
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr"},
		Spec:       pmv1.ApplicationSpecificRoleSpec{Name: "test-group"},
	}

	scopes := map[string]*clusterScope{
		"prod-eu":    {clusterVerbs: map[string]struct{}{"get": {}}, namespaceVerbs: map[string]map[string]struct{}{}},
		"staging-us": {clusterVerbs: map[string]struct{}{"list": {}}, namespaceVerbs: map[string]map[string]struct{}{}},
	}

	prevStatuses := []ClusterProvisioningStatus{
		{ClusterName: "prod-eu", Status: "Success"},
		{ClusterName: "staging-us", Status: "Success"},
	}

	plan := planReconcile(asr, scopes, prevStatuses)

	if len(plan.OrphanClusters) != 0 {
		t.Errorf("expected 0 orphan clusters, got %d: %v", len(plan.OrphanClusters), plan.OrphanClusters)
	}
}

func TestPlanReconcile_EmptyScopes(t *testing.T) {
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr"},
		Spec:       pmv1.ApplicationSpecificRoleSpec{Name: "test-group"},
	}

	scopes := map[string]*clusterScope{}
	prevStatuses := []ClusterProvisioningStatus{
		{ClusterName: "prod-eu", Status: "Success"},
	}

	plan := planReconcile(asr, scopes, prevStatuses)

	if len(plan.ClustersToProvision) != 0 {
		t.Errorf("expected 0 clusters to provision, got %d", len(plan.ClustersToProvision))
	}
	if len(plan.OrphanClusters) != 1 || plan.OrphanClusters[0] != "prod-eu" {
		t.Errorf("expected orphan [prod-eu], got %v", plan.OrphanClusters)
	}
}

func TestPlanReconcile_UnreachablePrevStatusStillOrphan(t *testing.T) {
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-asr"},
		Spec:       pmv1.ApplicationSpecificRoleSpec{Name: "test-group"},
	}

	scopes := map[string]*clusterScope{
		"prod-eu": {clusterVerbs: map[string]struct{}{"get": {}}, namespaceVerbs: map[string]map[string]struct{}{}},
	}
	prevStatuses := []ClusterProvisioningStatus{
		{ClusterName: "prod-eu", Status: "Success"},
		{ClusterName: "staging-us", Status: "Unreachable"},
	}

	plan := planReconcile(asr, scopes, prevStatuses)

	if len(plan.OrphanClusters) != 1 || plan.OrphanClusters[0] != "staging-us" {
		t.Errorf("expected orphan [staging-us], got %v", plan.OrphanClusters)
	}
}

func TestPlanReconcile_PreservesClusterScopes(t *testing.T) {
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "mixed-asr"},
		Spec:       pmv1.ApplicationSpecificRoleSpec{Name: "mixed-group"},
	}

	scopes := map[string]*clusterScope{
		"prod-eu": {
			clusterVerbs: map[string]struct{}{"get": {}, "list": {}, "watch": {}},
			namespaceVerbs: map[string]map[string]struct{}{
				"monitoring": {"get": {}, "list": {}},
			},
		},
		"staging-us": {
			clusterVerbs: map[string]struct{}{},
			namespaceVerbs: map[string]map[string]struct{}{
				"default": {"create": {}, "update": {}},
			},
		},
	}

	plan := planReconcile(asr, scopes, nil)

	if len(plan.ClustersToProvision) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(plan.ClustersToProvision))
	}
	if len(plan.ClustersToProvision["prod-eu"].clusterVerbs) != 3 {
		t.Errorf("expected 3 cluster verbs for prod-eu")
	}
	if _, ok := plan.ClustersToProvision["staging-us"].namespaceVerbs["default"]; !ok {
		t.Error("expected namespace verbs for default in staging-us")
	}
}

// --- executeReconcilePlan tests ---

func TestExecuteReconcilePlan_SingleClusterSuccess(t *testing.T) {
	scheme := newTestScheme()
	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{"prod-eu": prodClient},
	}

	plan := ReconcilePlan{
		ASRName:  "test-asr",
		SpecName: "test-group",
		ClustersToProvision: map[string]*clusterScope{
			"prod-eu": {
				clusterVerbs:   map[string]struct{}{"get": {}, "list": {}, "watch": {}},
				namespaceVerbs: map[string]map[string]struct{}{},
			},
		},
	}

	statuses, hasErrors := executeReconcilePlanSimple(context.Background(), plan, factory)

	if hasErrors {
		t.Error("expected no errors")
	}
	if len(statuses) != 1 || statuses[0].Status != "Success" {
		t.Errorf("expected 1 Success status, got %v", statuses)
	}
}

func TestExecuteReconcilePlan_UnreachableCluster(t *testing.T) {
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{},
	}

	plan := ReconcilePlan{
		ASRName:  "test-asr",
		SpecName: "test-group",
		ClustersToProvision: map[string]*clusterScope{
			"prod-eu": {
				clusterVerbs:   map[string]struct{}{"get": {}},
				namespaceVerbs: map[string]map[string]struct{}{},
			},
		},
	}

	statuses, hasErrors := executeReconcilePlanSimple(context.Background(), plan, factory)

	if !hasErrors {
		t.Error("expected errors for unreachable cluster")
	}
	if len(statuses) != 1 || statuses[0].Status != "Unreachable" {
		t.Errorf("expected 1 Unreachable status, got %v", statuses)
	}
}

func TestExecuteReconcilePlan_MixedReachability(t *testing.T) {
	scheme := newTestScheme()
	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{"prod-eu": prodClient},
	}

	plan := ReconcilePlan{
		ASRName:  "test-asr",
		SpecName: "test-group",
		ClustersToProvision: map[string]*clusterScope{
			"prod-eu": {
				clusterVerbs:   map[string]struct{}{"get": {}, "list": {}, "watch": {}},
				namespaceVerbs: map[string]map[string]struct{}{},
			},
			"staging-us": {
				clusterVerbs:   map[string]struct{}{"get": {}},
				namespaceVerbs: map[string]map[string]struct{}{},
			},
		},
	}

	statuses, hasErrors := executeReconcilePlanSimple(context.Background(), plan, factory)

	if !hasErrors {
		t.Error("expected errors for unreachable staging-us")
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	statusMap := make(map[string]string)
	for _, s := range statuses {
		statusMap[s.ClusterName] = s.Status
	}
	if statusMap["prod-eu"] != "Success" {
		t.Errorf("prod-eu status = %q, want Success", statusMap["prod-eu"])
	}
	if statusMap["staging-us"] != "Unreachable" {
		t.Errorf("staging-us status = %q, want Unreachable", statusMap["staging-us"])
	}
}

func TestExecuteReconcilePlan_EmptyPlan(t *testing.T) {
	factory := &StaticClusterClientFactory{Clients: map[string]ClusterClient{}}

	plan := ReconcilePlan{
		ASRName:             "test-asr",
		SpecName:            "test-group",
		ClustersToProvision: map[string]*clusterScope{},
	}

	statuses, hasErrors := executeReconcilePlanSimple(context.Background(), plan, factory)

	if hasErrors {
		t.Error("expected no errors for empty plan")
	}
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses, got %d", len(statuses))
	}
}

func TestExecuteReconcilePlan_NamespaceScopedRBAC(t *testing.T) {
	scheme := newTestScheme()
	stagingClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{"staging-us": stagingClient},
	}

	plan := ReconcilePlan{
		ASRName:  "ns-asr",
		SpecName: "ns-group",
		ClustersToProvision: map[string]*clusterScope{
			"staging-us": {
				clusterVerbs: map[string]struct{}{},
				namespaceVerbs: map[string]map[string]struct{}{
					"default":    {"create": {}, "update": {}},
					"monitoring": {"get": {}, "list": {}},
				},
			},
		},
	}

	statuses, hasErrors := executeReconcilePlanSimple(context.Background(), plan, factory)

	if hasErrors {
		t.Error("expected no errors")
	}
	if len(statuses) != 1 || statuses[0].Status != "Success" {
		t.Errorf("expected 1 Success status, got %v", statuses)
	}
}
