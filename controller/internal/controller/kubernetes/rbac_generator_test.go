// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = rbacv1.AddToScheme(s)
	_ = pmv1.AddToScheme(s)
	return s
}

func TestBuildClusterScopes_ClusterScope(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "prod-eu" || name == "staging-us" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "reader"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "my-team-readers",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
			},
		},
	}

	scopes, _ := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 1 {
		t.Fatalf("expected 1 cluster scope, got %d", len(scopes))
	}

	scope, ok := scopes["prod-eu"]
	if !ok {
		t.Fatal("expected scope for prod-eu")
	}

	if len(scope.clusterVerbs) != 3 {
		t.Errorf("expected 3 cluster verbs, got %d", len(scope.clusterVerbs))
	}
	for _, v := range []string{"get", "list", "watch"} {
		if _, ok := scope.clusterVerbs[v]; !ok {
			t.Errorf("expected verb %q in cluster verbs", v)
		}
	}
}

func TestBuildClusterScopes_NamespaceScope(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "staging-us" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "ns-writer"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "my-team-writers",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/staging-us/kube-system", Capability: "WRITE"},
			},
		},
	}

	scopes, _ := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 1 {
		t.Fatalf("expected 1 cluster scope, got %d", len(scopes))
	}

	scope := scopes["staging-us"]
	if len(scope.clusterVerbs) != 0 {
		t.Errorf("expected 0 cluster verbs, got %d", len(scope.clusterVerbs))
	}

	nsVerbs, ok := scope.namespaceVerbs["kube-system"]
	if !ok {
		t.Fatal("expected namespace verbs for kube-system")
	}
	for _, v := range []string{"create", "update", "patch", "delete"} {
		if _, ok := nsVerbs[v]; !ok {
			t.Errorf("expected verb %q in namespace verbs", v)
		}
	}
}

func TestBuildClusterScopes_MultiClusterAndMixed(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "prod-eu" || name == "staging-us" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "multi"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "multi-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/staging-us/default", Capability: "WRITE"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu/monitoring", Capability: "READ"},
			},
		},
	}

	scopes, _ := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 2 {
		t.Fatalf("expected 2 cluster scopes, got %d", len(scopes))
	}

	// prod-eu has both cluster-scope READ and namespace-scope READ for monitoring
	prodScope := scopes["prod-eu"]
	if len(prodScope.clusterVerbs) != 3 {
		t.Errorf("expected 3 cluster verbs for prod-eu, got %d", len(prodScope.clusterVerbs))
	}
	if _, ok := prodScope.namespaceVerbs["monitoring"]; !ok {
		t.Error("expected namespace verbs for monitoring in prod-eu")
	}

	// staging-us has only namespace-scope WRITE for default
	stagingScope := scopes["staging-us"]
	if len(stagingScope.clusterVerbs) != 0 {
		t.Errorf("expected 0 cluster verbs for staging-us, got %d", len(stagingScope.clusterVerbs))
	}
	if _, ok := stagingScope.namespaceVerbs["default"]; !ok {
		t.Error("expected namespace verbs for default in staging-us")
	}
}

func TestBuildClusterScopes_UnknownCluster(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "prod-eu" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "unknown"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "unknown-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/unknown-cluster", Capability: "READ"},
			},
		},
	}

	scopes, _ := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 0 {
		t.Fatalf("expected 0 cluster scopes for unknown cluster, got %d", len(scopes))
	}
}

func TestBuildClusterScopes_InvalidCapability(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "prod-eu" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "invalid-cap"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "UNKNOWN_CAP"},
			},
		},
	}

	scopes, unmapped := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 0 {
		t.Fatalf("expected 0 scopes for invalid capability, got %d", len(scopes))
	}

	// Should report unmapped capability
	if len(unmapped) != 1 {
		t.Fatalf("expected 1 unmapped capability, got %d", len(unmapped))
	}
	if unmapped[0] != "UNKNOWN_CAP" {
		t.Errorf("expected unmapped capability UNKNOWN_CAP, got %q", unmapped[0])
	}
}

func TestBuildClusterScopes_UnmappedCapabilityMultiple(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "prod-eu" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-unmapped"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "UNKNOWN_A"},
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "UNKNOWN_B"},
			},
		},
	}

	scopes, unmapped := buildClusterScopes(asr, matchClusters)

	// Valid READ capability should still produce a scope
	if len(scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(scopes))
	}
	scope := scopes["prod-eu"]
	if len(scope.clusterVerbs) != 3 {
		t.Errorf("expected 3 cluster verbs from READ, got %d", len(scope.clusterVerbs))
	}

	// Should report both unmapped capabilities
	if len(unmapped) != 2 {
		t.Fatalf("expected 2 unmapped capabilities, got %d: %v", len(unmapped), unmapped)
	}
}

func TestBuildClusterScopes_UnmappedCapabilityDeduplicated(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "prod-eu" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "dedup-unmapped"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "UNKNOWN_CAP"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu/default", Capability: "UNKNOWN_CAP"},
			},
		},
	}

	_, unmapped := buildClusterScopes(asr, matchClusters)

	// Same capability unmapped twice should be deduplicated
	if len(unmapped) != 1 {
		t.Fatalf("expected 1 deduplicated unmapped capability, got %d: %v", len(unmapped), unmapped)
	}
	if unmapped[0] != "UNKNOWN_CAP" {
		t.Errorf("expected UNKNOWN_CAP, got %q", unmapped[0])
	}
}

func TestBuildClusterScopes_CaseInsensitiveCapability(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "prod-eu" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "lowercase-cap"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "read"},
			},
		},
	}

	scopes, unmapped := buildClusterScopes(asr, matchClusters)

	// "read" should map to READ (case-insensitive)
	if len(scopes) != 1 {
		t.Fatalf("expected 1 scope for lowercase capability, got %d", len(scopes))
	}
	scope := scopes["prod-eu"]
	if len(scope.clusterVerbs) != 3 {
		t.Errorf("expected 3 cluster verbs from read, got %d", len(scope.clusterVerbs))
	}

	// No unmapped capabilities
	if len(unmapped) != 0 {
		t.Errorf("expected 0 unmapped capabilities, got %d: %v", len(unmapped), unmapped)
	}
}

func TestBuildClusterScopes_AllMappedReturnsNilUnmapped(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "prod-eu" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "all-mapped"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
			},
		},
	}

	_, unmapped := buildClusterScopes(asr, matchClusters)

	if len(unmapped) != 0 {
		t.Errorf("expected 0 unmapped capabilities, got %d: %v", len(unmapped), unmapped)
	}
}

func TestBuildRBACPlan_ClusterScopeOnly(t *testing.T) {
	scope := &clusterScope{
		clusterVerbs:   map[string]struct{}{"get": {}, "list": {}, "watch": {}},
		namespaceVerbs: map[string]map[string]struct{}{},
	}

	plan := buildRBACPlan("my-asr", "my-team-group", scope)

	if plan.clusterRole == nil {
		t.Fatal("expected ClusterRole to be created")
	}
	if plan.clusterRole.Name != "pm-my-asr" {
		t.Errorf("ClusterRole name = %q, want %q", plan.clusterRole.Name, "pm-my-asr")
	}
	if plan.clusterRole.Labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("missing managed-by label")
	}
	if plan.clusterRole.Labels[LabelASRName] != "my-asr" {
		t.Errorf("missing ASR label")
	}
	if len(plan.clusterRole.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(plan.clusterRole.Rules))
	}

	// Verify verbs are sorted
	rule := plan.clusterRole.Rules[0]
	expectedVerbs := []string{"get", "list", "watch"}
	if len(rule.Verbs) != len(expectedVerbs) {
		t.Fatalf("expected %d verbs, got %d", len(expectedVerbs), len(rule.Verbs))
	}
	for i, v := range expectedVerbs {
		if rule.Verbs[i] != v {
			t.Errorf("verb[%d] = %q, want %q", i, rule.Verbs[i], v)
		}
	}

	// Verify ClusterRoleBinding
	if plan.clusterRoleBinding == nil {
		t.Fatal("expected ClusterRoleBinding to be created for cluster-scoped permissions")
	}
	if plan.clusterRoleBinding.Name != "pm-my-asr" {
		t.Errorf("ClusterRoleBinding name = %q, want %q", plan.clusterRoleBinding.Name, "pm-my-asr")
	}
	if plan.clusterRoleBinding.RoleRef.Name != "pm-my-asr" {
		t.Errorf("ClusterRoleBinding roleRef = %q, want %q", plan.clusterRoleBinding.RoleRef.Name, "pm-my-asr")
	}
	if len(plan.clusterRoleBinding.Subjects) != 1 {
		t.Fatalf("expected 1 subject, got %d", len(plan.clusterRoleBinding.Subjects))
	}
	subject := plan.clusterRoleBinding.Subjects[0]
	if subject.Kind != "Group" {
		t.Errorf("subject kind = %q, want %q", subject.Kind, "Group")
	}
	if subject.Name != "my-team-group" {
		t.Errorf("subject name = %q, want %q", subject.Name, "my-team-group")
	}
	if subject.APIGroup != "rbac.authorization.k8s.io" {
		t.Errorf("subject apiGroup = %q, want %q", subject.APIGroup, "rbac.authorization.k8s.io")
	}

	// No RoleBindings for cluster-scope only
	if len(plan.roleBindings) != 0 {
		t.Errorf("expected 0 RoleBindings, got %d", len(plan.roleBindings))
	}
}

func TestBuildRBACPlan_NamespaceScopeOnly(t *testing.T) {
	scope := &clusterScope{
		clusterVerbs: map[string]struct{}{},
		namespaceVerbs: map[string]map[string]struct{}{
			"kube-system": {"create": {}, "update": {}, "patch": {}, "delete": {}},
		},
	}

	plan := buildRBACPlan("ns-writer", "ns-writer-group", scope)

	// No cluster-scoped ClusterRole or ClusterRoleBinding for namespace-only scope
	if plan.clusterRole != nil {
		t.Error("expected no cluster-scoped ClusterRole for namespace-only scope")
	}
	if plan.clusterRoleBinding != nil {
		t.Error("expected no ClusterRoleBinding for namespace-only scope")
	}

	// Should have a namespace-scoped Role
	if len(plan.roles) != 1 {
		t.Fatalf("expected 1 Role, got %d", len(plan.roles))
	}
	role := plan.roles[0]
	if role.Name != "pm-ns-writer" {
		t.Errorf("Role name = %q, want %q", role.Name, "pm-ns-writer")
	}
	if role.Namespace != "kube-system" {
		t.Errorf("Role namespace = %q, want %q", role.Namespace, "kube-system")
	}

	if len(plan.roleBindings) != 1 {
		t.Fatalf("expected 1 RoleBinding, got %d", len(plan.roleBindings))
	}

	rb := plan.roleBindings[0]
	if rb.Name != "pm-ns-writer" {
		t.Errorf("RoleBinding name = %q, want %q", rb.Name, "pm-ns-writer")
	}
	if rb.Namespace != "kube-system" {
		t.Errorf("RoleBinding namespace = %q, want %q", rb.Namespace, "kube-system")
	}
	if rb.RoleRef.Name != "pm-ns-writer" {
		t.Errorf("RoleBinding roleRef = %q, want %q", rb.RoleRef.Name, "pm-ns-writer")
	}
	if rb.RoleRef.Kind != "Role" {
		t.Errorf("RoleBinding roleRef kind = %q, want %q", rb.RoleRef.Kind, "Role")
	}
	subject := rb.Subjects[0]
	if subject.Kind != "Group" || subject.Name != "ns-writer-group" {
		t.Errorf("unexpected subject: %+v", subject)
	}
}

func TestBuildRBACPlan_MixedScope(t *testing.T) {
	scope := &clusterScope{
		clusterVerbs: map[string]struct{}{"get": {}, "list": {}, "watch": {}},
		namespaceVerbs: map[string]map[string]struct{}{
			"default":    {"create": {}, "delete": {}},
			"monitoring": {"get": {}, "list": {}},
		},
	}

	plan := buildRBACPlan("mixed", "mixed-group", scope)

	if plan.clusterRole == nil {
		t.Fatal("expected ClusterRole")
	}
	if plan.clusterRoleBinding == nil {
		t.Fatal("expected ClusterRoleBinding")
	}
	if len(plan.roles) != 2 {
		t.Fatalf("expected 2 Roles, got %d", len(plan.roles))
	}
	if len(plan.roleBindings) != 2 {
		t.Fatalf("expected 2 RoleBindings, got %d", len(plan.roleBindings))
	}

	// Verify Roles are sorted by namespace and have correct per-namespace verbs
	if plan.roles[0].Namespace != "default" {
		t.Errorf("first Role namespace = %q, want %q", plan.roles[0].Namespace, "default")
	}
	if len(plan.roles[0].Rules[0].Verbs) != 2 {
		t.Errorf("default Role verbs = %v, want 2 verbs (create, delete)", plan.roles[0].Rules[0].Verbs)
	}
	if plan.roles[1].Namespace != "monitoring" {
		t.Errorf("second Role namespace = %q, want %q", plan.roles[1].Namespace, "monitoring")
	}
	if len(plan.roles[1].Rules[0].Verbs) != 2 {
		t.Errorf("monitoring Role verbs = %v, want 2 verbs (get, list)", plan.roles[1].Rules[0].Verbs)
	}

	// Verify RoleBindings are sorted by namespace and reference Role (not ClusterRole)
	if plan.roleBindings[0].Namespace != "default" {
		t.Errorf("first RoleBinding namespace = %q, want %q", plan.roleBindings[0].Namespace, "default")
	}
	if plan.roleBindings[0].RoleRef.Kind != "Role" {
		t.Errorf("first RoleBinding roleRef kind = %q, want %q", plan.roleBindings[0].RoleRef.Kind, "Role")
	}
	if plan.roleBindings[1].Namespace != "monitoring" {
		t.Errorf("second RoleBinding namespace = %q, want %q", plan.roleBindings[1].Namespace, "monitoring")
	}
}

func TestApplyRBACPlan_CreatesResources(t *testing.T) {
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	scope := &clusterScope{
		clusterVerbs: map[string]struct{}{"get": {}, "list": {}, "watch": {}},
		namespaceVerbs: map[string]map[string]struct{}{
			"default": {"create": {}, "update": {}},
		},
	}
	plan := buildRBACPlan("test-asr", "test-group", scope)

	ctx := context.Background()
	err := applyRBACPlan(ctx, fakeClient, plan, "test-asr", "test-cluster", audit.Disabled())
	if err != nil {
		t.Fatalf("applyRBACPlan failed: %v", err)
	}

	// Verify ClusterRole was created
	var cr rbacv1.ClusterRole
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "pm-test-asr"}, &cr)
	if err != nil {
		t.Fatalf("failed to get ClusterRole: %v", err)
	}
	if cr.Labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("ClusterRole missing managed-by label")
	}
	if cr.Labels[LabelASRName] != "test-asr" {
		t.Errorf("ClusterRole missing ASR label")
	}

	// Verify ClusterRoleBinding was created
	var crb rbacv1.ClusterRoleBinding
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "pm-test-asr"}, &crb)
	if err != nil {
		t.Fatalf("failed to get ClusterRoleBinding: %v", err)
	}
	if crb.Subjects[0].Name != "test-group" {
		t.Errorf("ClusterRoleBinding subject = %q, want %q", crb.Subjects[0].Name, "test-group")
	}

	// Verify RoleBinding was created in the default namespace
	var rb rbacv1.RoleBinding
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "pm-test-asr", Namespace: "default"}, &rb)
	if err != nil {
		t.Fatalf("failed to get RoleBinding: %v", err)
	}
	if rb.Subjects[0].Name != "test-group" {
		t.Errorf("RoleBinding subject = %q, want %q", rb.Subjects[0].Name, "test-group")
	}
	if rb.RoleRef.Name != "pm-test-asr" {
		t.Errorf("RoleBinding roleRef = %q, want %q", rb.RoleRef.Name, "pm-test-asr")
	}
	if rb.RoleRef.Kind != "Role" {
		t.Errorf("RoleBinding roleRef kind = %q, want %q", rb.RoleRef.Kind, "Role")
	}

	// Verify namespace-scoped Role was created in default namespace
	var nsRole rbacv1.Role
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "pm-test-asr", Namespace: "default"}, &nsRole)
	if err != nil {
		t.Fatalf("failed to get namespace-scoped Role: %v", err)
	}
	if nsRole.Labels[LabelASRName] != "test-asr" {
		t.Errorf("Role missing ASR label")
	}
}

func TestApplyRBACPlan_DriftCorrection(t *testing.T) {
	scheme := newTestScheme()

	// Pre-create a ClusterRole with wrong rules (simulating manual drift)
	existingCR := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pm-drift-asr",
			Labels: map[string]string{"some": "old-label"},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get"},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingCR).Build()

	scope := &clusterScope{
		clusterVerbs:   map[string]struct{}{"get": {}, "list": {}, "watch": {}},
		namespaceVerbs: map[string]map[string]struct{}{},
	}
	plan := buildRBACPlan("drift-asr", "drift-group", scope)

	ctx := context.Background()
	err := applyRBACPlan(ctx, fakeClient, plan, "test-asr", "test-cluster", audit.Disabled())
	if err != nil {
		t.Fatalf("applyRBACPlan failed: %v", err)
	}

	// Verify ClusterRole was corrected
	var cr rbacv1.ClusterRole
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "pm-drift-asr"}, &cr)
	if err != nil {
		t.Fatalf("failed to get ClusterRole: %v", err)
	}

	// Labels should be overwritten
	if cr.Labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("ClusterRole label not corrected: managed-by = %q", cr.Labels[LabelManagedBy])
	}

	// Rules should be corrected
	if len(cr.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cr.Rules))
	}
	if cr.Rules[0].APIGroups[0] != "*" {
		t.Errorf("rule apiGroups not corrected: got %v", cr.Rules[0].APIGroups)
	}
	if cr.Rules[0].Resources[0] != "*" {
		t.Errorf("rule resources not corrected: got %v", cr.Rules[0].Resources)
	}
}

func TestApplyRBACPlan_EmptyPlan(t *testing.T) {
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	// An empty plan should not fail
	plan := &rbacPlan{}
	ctx := context.Background()
	err := applyRBACPlan(ctx, fakeClient, plan, "test-asr", "test-cluster", audit.Disabled())
	if err != nil {
		t.Fatalf("applyRBACPlan with empty plan failed: %v", err)
	}
}

func TestRBACIntegration_FullReconcileFlow(t *testing.T) {
	scheme := newTestScheme()

	// Create fake clients for two clusters
	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	stagingClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu":    prodClient,
			"staging-us": stagingClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")
	clusterInv.Update("staging-us")

	// Create an ASR with mixed permissions across clusters
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "team-access",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "my-team",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/staging-us/default", Capability: "WRITE"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/staging-us/monitoring", Capability: "READ"},
			},
		},
	}

	// Create the management cluster fake client with the ASR
	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
		NamespaceLister: &StaticNamespaceLister{
			Namespaces: map[string][]string{
				"prod-eu":    {"default", "monitoring"},
				"staging-us": {"default", "monitoring"},
			},
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "team-access"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue: %v", result.RequeueAfter)
	}

	// Verify prod-eu: ClusterRole + ClusterRoleBinding (READ = cluster-scope)
	var prodCR rbacv1.ClusterRole
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-team-access"}, &prodCR)
	if err != nil {
		t.Fatalf("failed to get ClusterRole on prod-eu: %v", err)
	}
	if len(prodCR.Rules) != 1 {
		t.Fatalf("expected 1 rule on prod-eu ClusterRole, got %d", len(prodCR.Rules))
	}

	var prodCRB rbacv1.ClusterRoleBinding
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-team-access"}, &prodCRB)
	if err != nil {
		t.Fatalf("failed to get ClusterRoleBinding on prod-eu: %v", err)
	}
	if prodCRB.Subjects[0].Name != "my-team" {
		t.Errorf("prod-eu CRB subject = %q, want %q", prodCRB.Subjects[0].Name, "my-team")
	}

	// Verify staging-us: per-namespace Roles + RoleBindings in default and monitoring
	// (no cluster-scoped ClusterRole since staging-us has only namespace permissions)
	var stagingDefaultRole rbacv1.Role
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-team-access", Namespace: "default"}, &stagingDefaultRole)
	if err != nil {
		t.Fatalf("failed to get Role in default on staging-us: %v", err)
	}

	var stagingMonRole rbacv1.Role
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-team-access", Namespace: "monitoring"}, &stagingMonRole)
	if err != nil {
		t.Fatalf("failed to get Role in monitoring on staging-us: %v", err)
	}

	// staging-us should NOT have a cluster-scoped ClusterRole
	var stagingCR rbacv1.ClusterRole
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-team-access"}, &stagingCR)
	if err == nil {
		t.Error("expected no cluster-scoped ClusterRole on staging-us (namespace-only)")
	}

	// staging-us should NOT have a ClusterRoleBinding (no cluster-scope permissions)
	var stagingCRB rbacv1.ClusterRoleBinding
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-team-access"}, &stagingCRB)
	if err == nil {
		t.Error("expected no ClusterRoleBinding on staging-us (namespace-only)")
	}

	// Check RoleBinding in default namespace
	var defaultRB rbacv1.RoleBinding
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-team-access", Namespace: "default"}, &defaultRB)
	if err != nil {
		t.Fatalf("failed to get RoleBinding in default on staging-us: %v", err)
	}
	if defaultRB.Subjects[0].Name != "my-team" {
		t.Errorf("staging-us default RB subject = %q, want %q", defaultRB.Subjects[0].Name, "my-team")
	}
	if defaultRB.RoleRef.Name != "pm-team-access" {
		t.Errorf("staging-us default RB roleRef = %q, want %q", defaultRB.RoleRef.Name, "pm-team-access")
	}
	if defaultRB.RoleRef.Kind != "Role" {
		t.Errorf("staging-us default RB roleRef kind = %q, want %q", defaultRB.RoleRef.Kind, "Role")
	}

	// Check RoleBinding in monitoring namespace
	var monRB rbacv1.RoleBinding
	err = stagingClient.Get(ctx, types.NamespacedName{Name: "pm-team-access", Namespace: "monitoring"}, &monRB)
	if err != nil {
		t.Fatalf("failed to get RoleBinding in monitoring on staging-us: %v", err)
	}
}

func TestRBACIntegration_UnreachableCluster(t *testing.T) {
	scheme := newTestScheme()

	// Only prod-eu is reachable
	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu": prodClient,
			// staging-us is NOT in the map -> unreachable
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")
	clusterInv.Update("staging-us") // known but unreachable

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-cluster-asr",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "multi-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/staging-us", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
		NamespaceLister: &StaticNamespaceLister{
			Namespaces: map[string][]string{
				"prod-eu":    {},
				"staging-us": {},
			},
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "multi-cluster-asr"))

	// Should return error for rate-limited requeue (staging-us unreachable)
	if err == nil {
		t.Fatal("expected Reconcile to return error for unreachable cluster")
	}

	// Should NOT use RequeueAfter (controller-runtime handles backoff via error return)
	if result.RequeueAfter != 0 {
		t.Errorf("expected no RequeueAfter when returning error, got %v", result.RequeueAfter)
	}

	// prod-eu should still have been provisioned
	var prodCR rbacv1.ClusterRole
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-multi-cluster-asr"}, &prodCR)
	if err != nil {
		t.Fatalf("prod-eu should still have ClusterRole despite staging-us being unreachable: %v", err)
	}
}

func TestRBACIntegration_NoK8sConnectorRef(t *testing.T) {
	scheme := newTestScheme()

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-azure"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-azure", Provider: "azure"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-only",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "azure-group",
			ConnectorRefs: []string{"sci-azure"}, // Not kubernetes
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: &StaticClusterClientFactory{Clients: map[string]ClusterClient{}},
		NamespaceLister:      &StaticNamespaceLister{},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "azure-only"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue for non-k8s ASR: %v", result.RequeueAfter)
	}
}

func TestRBACIntegration_ASRNotFound(t *testing.T) {
	scheme := newTestScheme()

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	clusterInv := NewClusterInventory(logr.Discard())

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: &StaticClusterClientFactory{Clients: map[string]ClusterClient{}},
		NamespaceLister:      &StaticNamespaceLister{},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "nonexistent"))
	if err != nil {
		t.Fatalf("Reconcile should not error for not-found ASR: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue: %v", result.RequeueAfter)
	}
}

func TestRBACIntegration_MultiplePermissionsClusterAndNamespace(t *testing.T) {
	scheme := newTestScheme()

	prodClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{
			"prod-eu": prodClient,
		},
	}

	cbIndex := NewConnectorBindingIndexer(logr.Discard())
	cbIndex.Sync([]pmv1.ConnectorBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sci-k8s"},
			Spec:       pmv1.ConnectorBindingSpec{Name: "sci-k8s", Provider: "kubernetes"},
		},
	})

	clusterInv := NewClusterInventory(logr.Discard())
	clusterInv.Update("prod-eu")

	// ASR with both cluster and namespace permissions on the same cluster
	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "full-access",
			Namespace: "pm-system",
		},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "full-access-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "READ"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu/apps", Capability: "WRITE"},
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu/monitoring", Capability: "READ"},
			},
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(asr).Build()

	reconciler := &Reconciler{
		Client:               mgmtClient,
		CBIndex:              cbIndex,
		ClusterInventory:     clusterInv,
		ClusterClientFactory: factory,
		Cleaner:              &RBACCleaner{ClusterInventory: clusterInv, ClusterClientFactory: factory, AuditLog: audit.Disabled()},
		AuditLog:             audit.Disabled(),
		NamespaceLister: &StaticNamespaceLister{
			Namespaces: map[string][]string{
				"prod-eu": {"apps", "monitoring", "default"},
			},
		},
	}

	ctx := context.Background()
	result, err := reconciler.Reconcile(ctx, newReconcileRequest("pm-system", "full-access"))
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("unexpected requeue: %v", result.RequeueAfter)
	}

	// Verify cluster-scoped ClusterRole with only READ verbs
	var cr rbacv1.ClusterRole
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-full-access"}, &cr)
	if err != nil {
		t.Fatalf("failed to get cluster-scoped ClusterRole: %v", err)
	}
	if len(cr.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cr.Rules))
	}
	// Cluster-scoped: only READ verbs (get, list, watch) = 3 verbs
	if len(cr.Rules[0].Verbs) != 3 {
		t.Errorf("expected 3 cluster-scoped verbs (READ only), got %d: %v", len(cr.Rules[0].Verbs), cr.Rules[0].Verbs)
	}

	// Verify per-namespace Roles with exact verb sets (no union leakage)
	var appsRole rbacv1.Role
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-full-access", Namespace: "apps"}, &appsRole)
	if err != nil {
		t.Fatalf("failed to get Role in apps: %v", err)
	}
	if len(appsRole.Rules) != 1 {
		t.Fatalf("expected 1 rule on apps Role, got %d", len(appsRole.Rules))
	}
	// apps: WRITE capability = 7 verbs (create, delete, deletecollection, get, list, patch, update)
	if len(appsRole.Rules[0].Verbs) != 7 {
		t.Errorf("expected 7 verbs on apps Role (WRITE), got %d: %v", len(appsRole.Rules[0].Verbs), appsRole.Rules[0].Verbs)
	}

	var monRole rbacv1.Role
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-full-access", Namespace: "monitoring"}, &monRole)
	if err != nil {
		t.Fatalf("failed to get Role in monitoring: %v", err)
	}
	if len(monRole.Rules) != 1 {
		t.Fatalf("expected 1 rule on monitoring Role, got %d", len(monRole.Rules))
	}
	// monitoring: READ capability = 3 verbs (get, list, watch)
	if len(monRole.Rules[0].Verbs) != 3 {
		t.Errorf("expected 3 verbs on monitoring Role (READ), got %d: %v", len(monRole.Rules[0].Verbs), monRole.Rules[0].Verbs)
	}

	// Verify ClusterRoleBinding (because of cluster-scope READ)
	var crb rbacv1.ClusterRoleBinding
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-full-access"}, &crb)
	if err != nil {
		t.Fatalf("failed to get ClusterRoleBinding: %v", err)
	}
	if crb.RoleRef.Name != "pm-full-access" {
		t.Errorf("CRB roleRef = %q, want %q", crb.RoleRef.Name, "pm-full-access")
	}

	// Verify RoleBindings in both namespaces reference the namespace Role
	var appsRB rbacv1.RoleBinding
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-full-access", Namespace: "apps"}, &appsRB)
	if err != nil {
		t.Fatalf("failed to get RoleBinding in apps: %v", err)
	}
	if appsRB.RoleRef.Name != "pm-full-access" {
		t.Errorf("apps RB roleRef = %q, want %q", appsRB.RoleRef.Name, "pm-full-access")
	}
	if appsRB.RoleRef.Kind != "Role" {
		t.Errorf("apps RB roleRef kind = %q, want %q", appsRB.RoleRef.Kind, "Role")
	}

	var monRB rbacv1.RoleBinding
	err = prodClient.Get(ctx, types.NamespacedName{Name: "pm-full-access", Namespace: "monitoring"}, &monRB)
	if err != nil {
		t.Fatalf("failed to get RoleBinding in monitoring: %v", err)
	}
	if monRB.RoleRef.Name != "pm-full-access" {
		t.Errorf("monitoring RB roleRef = %q, want %q", monRB.RoleRef.Name, "pm-full-access")
	}
	if monRB.RoleRef.Kind != "Role" {
		t.Errorf("monitoring RB roleRef kind = %q, want %q", monRB.RoleRef.Kind, "Role")
	}
}

func TestClusterUnreachableError(t *testing.T) {
	err := &ClusterUnreachableError{ClusterName: "test-cluster"}
	if err.Error() != "cluster unreachable: test-cluster" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestStaticClusterClientFactory_UnknownCluster(t *testing.T) {
	factory := &StaticClusterClientFactory{
		Clients: map[string]ClusterClient{},
	}

	_, err := factory.ClientForCluster(context.Background(), "unknown")
	if err == nil {
		t.Fatal("expected error for unknown cluster")
	}
	if _, ok := err.(*ClusterUnreachableError); !ok {
		t.Errorf("expected ClusterUnreachableError, got %T", err)
	}
}

// newReconcileRequest creates a ctrl.Request for testing.
func newReconcileRequest(namespace, name string) ctrl.Request {
	return ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func TestBuildClusterScopes_WildcardPrefixGlob(t *testing.T) {
	// Simulate a matchClusters function that expands "nw-a-*" to multiple clusters
	matchClusters := func(pattern string) []string {
		switch pattern {
		case "nw-a-prod":
			return []string{"nw-a-prod"}
		case "nw-a-staging":
			return []string{"nw-a-staging"}
		case "nw-a-*":
			return []string{"nw-a-prod", "nw-a-staging"}
		default:
			return nil
		}
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "wildcard-reader"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "team-readers",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/nw-a-*", Capability: "READ"},
			},
		},
	}

	scopes, _ := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 2 {
		t.Fatalf("expected 2 cluster scopes, got %d", len(scopes))
	}

	for _, cluster := range []string{"nw-a-prod", "nw-a-staging"} {
		scope, ok := scopes[cluster]
		if !ok {
			t.Errorf("expected scope for %s", cluster)
			continue
		}
		if len(scope.clusterVerbs) != 3 {
			t.Errorf("expected 3 cluster verbs for %s, got %d", cluster, len(scope.clusterVerbs))
		}
	}
}

func TestBuildClusterScopes_BareWildcard(t *testing.T) {
	allClusters := []string{"prod-eu", "staging-us", "dev-asia"}
	matchClusters := func(pattern string) []string {
		if pattern == "*" {
			return allClusters
		}
		for _, c := range allClusters {
			if c == pattern {
				return []string{c}
			}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "all-clusters"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "admin-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/*", Capability: "*"},
			},
		},
	}

	scopes, _ := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 3 {
		t.Fatalf("expected 3 cluster scopes, got %d", len(scopes))
	}

	for _, cluster := range allClusters {
		scope, ok := scopes[cluster]
		if !ok {
			t.Errorf("expected scope for %s", cluster)
			continue
		}
		if _, ok := scope.clusterVerbs["*"]; !ok {
			t.Errorf("expected verb '*' for wildcard capability on %s", cluster)
		}
	}
}

func TestBuildClusterScopes_SecureCapability(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "prod-eu" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "secure-asr"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "security-team",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "SECURE"},
			},
		},
	}

	scopes, unmapped := buildClusterScopes(asr, matchClusters)
	if len(unmapped) != 0 {
		t.Fatalf("expected no unmapped capabilities, got %v", unmapped)
	}
	if len(scopes) != 1 {
		t.Fatalf("expected 1 cluster scope, got %d", len(scopes))
	}

	scope := scopes["prod-eu"]
	expectedVerbs := []string{"bind", "escalate", "impersonate"}
	if len(scope.clusterVerbs) != len(expectedVerbs) {
		t.Fatalf("expected %d cluster verbs, got %d: %v", len(expectedVerbs), len(scope.clusterVerbs), scope.clusterVerbs)
	}
	for _, v := range expectedVerbs {
		if _, ok := scope.clusterVerbs[v]; !ok {
			t.Errorf("expected verb %q in cluster verbs for SECURE capability", v)
		}
	}
}

func TestBuildClusterScopes_ManageIsUnmapped(t *testing.T) {
	matchClusters := func(name string) []string {
		if name == "prod-eu" {
			return []string{name}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "manage-asr"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "legacy-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu", Capability: "MANAGE"},
			},
		},
	}

	scopes, unmapped := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 0 {
		t.Fatalf("expected 0 scopes for unmapped MANAGE capability, got %d", len(scopes))
	}
	if len(unmapped) != 1 {
		t.Fatalf("expected 1 unmapped capability, got %d", len(unmapped))
	}
	if unmapped[0] != "MANAGE" {
		t.Errorf("expected unmapped capability MANAGE, got %q", unmapped[0])
	}
}

func TestBuildClusterScopes_OverlappingPatternsVerbMerge(t *testing.T) {
	// Two patterns that both match "nw-a-prod": one exact, one wildcard.
	// Verbs should be merged (union).
	matchClusters := func(pattern string) []string {
		switch pattern {
		case "nw-a-prod":
			return []string{"nw-a-prod"}
		case "nw-a-*":
			return []string{"nw-a-prod", "nw-a-staging"}
		default:
			return nil
		}
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "overlap"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "overlap-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				// Exact match gives READ
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/nw-a-prod", Capability: "READ"},
				// Wildcard gives WRITE to both nw-a-prod and nw-a-staging
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/nw-a-*", Capability: "WRITE"},
			},
		},
	}

	scopes, _ := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 2 {
		t.Fatalf("expected 2 cluster scopes, got %d", len(scopes))
	}

	// nw-a-prod should have READ + WRITE verbs merged
	prodScope := scopes["nw-a-prod"]
	if prodScope == nil {
		t.Fatal("expected scope for nw-a-prod")
		return // staticcheck: unreachable but convinces SA5011 that prodScope != nil below
	}

	// nw-a-staging should have only WRITE verbs
	stagingScope := scopes["nw-a-staging"]
	if stagingScope == nil {
		t.Fatal("expected scope for nw-a-staging")
		return // staticcheck: unreachable but convinces SA5011 that stagingScope != nil below
	}

	// READ = get,list,watch; WRITE = create,update,patch,delete -> 7 total
	if len(prodScope.clusterVerbs) != 7 {
		t.Errorf("expected 7 merged cluster verbs for nw-a-prod, got %d", len(prodScope.clusterVerbs))
	}
	if len(stagingScope.clusterVerbs) != 7 {
		t.Errorf("expected 7 cluster verbs (WRITE subsumes READ) for nw-a-staging, got %d", len(stagingScope.clusterVerbs))
	}
}

func TestBuildClusterScopes_ZeroMatchWildcard(t *testing.T) {
	matchClusters := func(pattern string) []string {
		// No clusters match
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "no-match"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "no-match-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/nonexistent-*", Capability: "READ"},
			},
		},
	}

	scopes, _ := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 0 {
		t.Fatalf("expected 0 scopes for zero-match wildcard, got %d", len(scopes))
	}
}

func TestBuildClusterScopes_WildcardNamespaceScope(t *testing.T) {
	// Wildcard in cluster name portion of a namespace CCRN
	matchClusters := func(pattern string) []string {
		if pattern == "prod-*" {
			return []string{"prod-eu", "prod-us"}
		}
		return nil
	}

	asr := &pmv1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: "ns-wildcard"},
		Spec: pmv1.ApplicationSpecificRoleSpec{
			Name:          "ns-wildcard-group",
			ConnectorRefs: []string{"sci-k8s"},
			Permissions: []pmv1.Permission{
				{Resource: "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-*/monitoring", Capability: "READ"},
			},
		},
	}

	scopes, _ := buildClusterScopes(asr, matchClusters)
	if len(scopes) != 2 {
		t.Fatalf("expected 2 cluster scopes, got %d", len(scopes))
	}

	for _, cluster := range []string{"prod-eu", "prod-us"} {
		scope, ok := scopes[cluster]
		if !ok {
			t.Errorf("expected scope for %s", cluster)
			continue
		}
		if len(scope.clusterVerbs) != 0 {
			t.Errorf("expected 0 cluster verbs for %s (namespace-only), got %d", cluster, len(scope.clusterVerbs))
		}
		nsVerbs, ok := scope.namespaceVerbs["monitoring"]
		if !ok {
			t.Errorf("expected namespace verbs for monitoring on %s", cluster)
			continue
		}
		if len(nsVerbs) != 3 {
			t.Errorf("expected 3 namespace verbs (READ) for %s/monitoring, got %d", cluster, len(nsVerbs))
		}
	}
}

// --- expandNamespaceWildcards tests ---

func TestExpandNamespaceWildcards_BareWildcardPromotes(t *testing.T) {
	scope := &clusterScope{
		clusterVerbs: map[string]struct{}{"get": {}},
		namespaceVerbs: map[string]map[string]struct{}{
			"*": {"create": {}, "delete": {}},
		},
	}

	listNs := func(ctx context.Context) ([]string, error) {
		t.Fatal("listNamespaces should not be called for bare wildcard only")
		return nil, nil
	}

	err := expandNamespaceWildcards(context.Background(), listNs, scope, logr.Discard())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verbs should be promoted to clusterVerbs
	for _, v := range []string{"get", "create", "delete"} {
		if _, ok := scope.clusterVerbs[v]; !ok {
			t.Errorf("expected verb %q promoted to clusterVerbs", v)
		}
	}

	// "*" should be removed from namespaceVerbs
	if _, ok := scope.namespaceVerbs["*"]; ok {
		t.Error("expected '*' to be removed from namespaceVerbs")
	}
	if len(scope.namespaceVerbs) != 0 {
		t.Errorf("expected empty namespaceVerbs, got %d entries", len(scope.namespaceVerbs))
	}
}

func TestExpandNamespaceWildcards_PrefixWildcardExpands(t *testing.T) {
	scope := &clusterScope{
		clusterVerbs: map[string]struct{}{},
		namespaceVerbs: map[string]map[string]struct{}{
			"shoot--wireapi--*": {"get": {}, "list": {}},
		},
	}

	allNamespaces := []string{"shoot--wireapi--prod", "shoot--wireapi--staging", "kube-system", "default"}
	listNs := func(ctx context.Context) ([]string, error) {
		return allNamespaces, nil
	}

	err := expandNamespaceWildcards(context.Background(), listNs, scope, logr.Discard())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should expand to the two matching namespaces
	if len(scope.namespaceVerbs) != 2 {
		t.Fatalf("expected 2 expanded namespaces, got %d: %v", len(scope.namespaceVerbs), scope.namespaceVerbs)
	}

	for _, ns := range []string{"shoot--wireapi--prod", "shoot--wireapi--staging"} {
		verbs, ok := scope.namespaceVerbs[ns]
		if !ok {
			t.Errorf("expected namespace %q in expanded results", ns)
			continue
		}
		if _, ok := verbs["get"]; !ok {
			t.Errorf("expected verb 'get' for namespace %q", ns)
		}
		if _, ok := verbs["list"]; !ok {
			t.Errorf("expected verb 'list' for namespace %q", ns)
		}
	}
}

func TestExpandNamespaceWildcards_ExactNamespaceValidated(t *testing.T) {
	scope := &clusterScope{
		clusterVerbs: map[string]struct{}{},
		namespaceVerbs: map[string]map[string]struct{}{
			"kube-system": {"get": {}},
			"nonexistent": {"create": {}},
			"default":     {"list": {}},
		},
	}

	listNs := func(ctx context.Context) ([]string, error) {
		return []string{"kube-system", "default", "monitoring"}, nil
	}

	err := expandNamespaceWildcards(context.Background(), listNs, scope, logr.Discard())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// kube-system and default should remain; nonexistent should be dropped
	if len(scope.namespaceVerbs) != 2 {
		t.Fatalf("expected 2 namespaces (kube-system, default), got %d: %v", len(scope.namespaceVerbs), scope.namespaceVerbs)
	}
	if _, ok := scope.namespaceVerbs["kube-system"]; !ok {
		t.Error("expected kube-system to be kept")
	}
	if _, ok := scope.namespaceVerbs["default"]; !ok {
		t.Error("expected default to be kept")
	}
	if _, ok := scope.namespaceVerbs["nonexistent"]; ok {
		t.Error("expected nonexistent to be removed")
	}
}

func TestExpandNamespaceWildcards_ListErrorReturnsError(t *testing.T) {
	scope := &clusterScope{
		clusterVerbs: map[string]struct{}{},
		namespaceVerbs: map[string]map[string]struct{}{
			"kube-system": {"get": {}},
		},
	}

	listErr := errors.New("cluster unreachable")
	listNs := func(ctx context.Context) ([]string, error) {
		return nil, listErr
	}

	err := expandNamespaceWildcards(context.Background(), listNs, scope, logr.Discard())
	if err == nil {
		t.Fatal("expected error to be returned")
	}
	if err != listErr {
		t.Errorf("expected error %v, got %v", listErr, err)
	}
}

func TestExpandNamespaceWildcards_ZeroMatchWildcardDoesNotFail(t *testing.T) {
	scope := &clusterScope{
		clusterVerbs: map[string]struct{}{},
		namespaceVerbs: map[string]map[string]struct{}{
			"nonexistent-*": {"get": {}},
		},
	}

	listNs := func(ctx context.Context) ([]string, error) {
		return []string{"default", "kube-system"}, nil
	}

	err := expandNamespaceWildcards(context.Background(), listNs, scope, logr.Discard())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No matches, so namespaceVerbs should be empty
	if len(scope.namespaceVerbs) != 0 {
		t.Errorf("expected 0 namespaces after zero-match wildcard, got %d", len(scope.namespaceVerbs))
	}
}

func TestExpandNamespaceWildcards_MixedWildcardAndExact(t *testing.T) {
	scope := &clusterScope{
		clusterVerbs: map[string]struct{}{},
		namespaceVerbs: map[string]map[string]struct{}{
			"*":            {"watch": {}},  // bare wildcard -> promote
			"shoot--app-*": {"get": {}},    // prefix wildcard -> expand
			"kube-system":  {"create": {}}, // exact -> validate
		},
	}

	listNs := func(ctx context.Context) ([]string, error) {
		return []string{"shoot--app-prod", "shoot--app-staging", "kube-system", "default"}, nil
	}

	err := expandNamespaceWildcards(context.Background(), listNs, scope, logr.Discard())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "watch" should be promoted to cluster verbs
	if _, ok := scope.clusterVerbs["watch"]; !ok {
		t.Error("expected 'watch' promoted to clusterVerbs from bare wildcard")
	}

	// Expanded namespaces: shoot--app-prod, shoot--app-staging (from wildcard), kube-system (exact)
	if len(scope.namespaceVerbs) != 3 {
		t.Fatalf("expected 3 namespaces, got %d: %v", len(scope.namespaceVerbs), scope.namespaceVerbs)
	}

	for _, ns := range []string{"shoot--app-prod", "shoot--app-staging"} {
		verbs, ok := scope.namespaceVerbs[ns]
		if !ok {
			t.Errorf("expected namespace %q", ns)
			continue
		}
		if _, ok := verbs["get"]; !ok {
			t.Errorf("expected verb 'get' for %q", ns)
		}
	}
	if _, ok := scope.namespaceVerbs["kube-system"]; !ok {
		t.Error("expected kube-system to be kept (exact match)")
	}
}

func TestExpandNamespaceWildcards_NoNamespaceVerbs(t *testing.T) {
	scope := &clusterScope{
		clusterVerbs:   map[string]struct{}{"get": {}},
		namespaceVerbs: map[string]map[string]struct{}{},
	}

	listNs := func(ctx context.Context) ([]string, error) {
		t.Fatal("listNamespaces should not be called when no namespace verbs")
		return nil, nil
	}

	err := expandNamespaceWildcards(context.Background(), listNs, scope, logr.Discard())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExpandNamespaceWildcards_VerbMergeFromOverlappingWildcards(t *testing.T) {
	// Two wildcard patterns that match the same namespace should merge verbs
	scope := &clusterScope{
		clusterVerbs: map[string]struct{}{},
		namespaceVerbs: map[string]map[string]struct{}{
			"shoot-*":      {"get": {}},
			"shoot-prod-*": {"create": {}},
		},
	}

	listNs := func(ctx context.Context) ([]string, error) {
		return []string{"shoot-prod-eu", "shoot-staging-us"}, nil
	}

	err := expandNamespaceWildcards(context.Background(), listNs, scope, logr.Discard())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// shoot-prod-eu matches both patterns: should have get + create
	verbs, ok := scope.namespaceVerbs["shoot-prod-eu"]
	if !ok {
		t.Fatal("expected shoot-prod-eu in expanded results")
	}
	if _, ok := verbs["get"]; !ok {
		t.Error("expected 'get' from 'shoot-*' pattern")
	}
	if _, ok := verbs["create"]; !ok {
		t.Error("expected 'create' from 'shoot-prod-*' pattern")
	}

	// shoot-staging-us matches only "shoot-*": should have get only
	verbs, ok = scope.namespaceVerbs["shoot-staging-us"]
	if !ok {
		t.Fatal("expected shoot-staging-us in expanded results")
	}
	if _, ok := verbs["get"]; !ok {
		t.Error("expected 'get' for shoot-staging-us")
	}
	if _, ok := verbs["create"]; ok {
		t.Error("did not expect 'create' for shoot-staging-us")
	}
}
