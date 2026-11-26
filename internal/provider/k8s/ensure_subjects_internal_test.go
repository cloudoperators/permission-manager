// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
)

func TestBindingSubjects(t *testing.T) {
	got := bindingSubjects("my-group", []rbacv1.Subject{
		{Kind: "User", Name: "ci1", APIGroup: rbacAPIGroup},
		{Kind: "User", Name: "ci2", APIGroup: rbacAPIGroup},
	})
	if len(got) != 3 {
		t.Fatalf("expected Group + 2 users, got %d: %+v", len(got), got)
	}
	if got[0].Kind != "Group" || got[0].Name != "my-group" {
		t.Errorf("subject[0] should be the Group, got %+v", got[0])
	}
	if got[1].Kind != "User" || got[1].Name != "ci1" || got[2].Name != "ci2" {
		t.Errorf("users should follow the group in order, got %+v", got[1:])
	}
}

// TestBuildersIncludeExtraUsers confirms every binding builder appends the extra User subjects
// after the Group subject.
func TestBuildersIncludeExtraUsers(t *testing.T) {
	users := []rbacv1.Subject{{Kind: "User", Name: "ci1", APIGroup: rbacAPIGroup}}

	assertGroupThenUser := func(t *testing.T, name string, subjects []rbacv1.Subject) {
		t.Helper()
		if len(subjects) != 2 || subjects[0].Kind != "Group" || subjects[1].Kind != "User" || subjects[1].Name != "ci1" {
			t.Errorf("%s subjects = %+v, want [Group, User ci1]", name, subjects)
		}
	}

	_, crb := buildClusterScopedRBAC("a", "ns", "grp", users, &policyRuleSet{})
	assertGroupThenUser(t, "buildClusterScopedRBAC", crb.Subjects)

	_, rb := buildNamespaceScopedRBAC("a", "ns", "grp", "team-ns", users, &policyRuleSet{})
	assertGroupThenUser(t, "buildNamespaceScopedRBAC", rb.Subjects)

	// Coarse cluster binding to cluster-admin gets the user too (ADR-019 §6: uniform treatment).
	ccrb := buildCoarseClusterRoleBinding("a", "ns", "grp", users, "bind", "cluster-admin")
	assertGroupThenUser(t, "buildCoarseClusterRoleBinding(cluster-admin)", ccrb.Subjects)

	crb2 := buildCoarseRoleBinding("a", "ns", "grp", users, "team-ns", "bind", "role")
	assertGroupThenUser(t, "buildCoarseRoleBinding", crb2.Subjects)
}

// TestBuilders_NoServiceIdentityUnchanged: with no ServiceIdentity users every builder emits only
// the Group subject — byte-identical to pre-ADR-019 output.
func TestBuilders_NoServiceIdentityUnchanged(t *testing.T) {
	onlyGroup := func(t *testing.T, name string, subjects []rbacv1.Subject) {
		t.Helper()
		if len(subjects) != 1 || subjects[0].Kind != "Group" || subjects[0].Name != "grp" {
			t.Errorf("%s subjects = %+v, want [Group grp]", name, subjects)
		}
	}
	_, crb := buildClusterScopedRBAC("a", "ns", "grp", nil, &policyRuleSet{})
	onlyGroup(t, "buildClusterScopedRBAC", crb.Subjects)
	_, rb := buildNamespaceScopedRBAC("a", "ns", "grp", "team-ns", nil, &policyRuleSet{})
	onlyGroup(t, "buildNamespaceScopedRBAC", rb.Subjects)
	ccrb := buildCoarseClusterRoleBinding("a", "ns", "grp", nil, "bind", "cluster-admin")
	onlyGroup(t, "buildCoarseClusterRoleBinding", ccrb.Subjects)
	crb2 := buildCoarseRoleBinding("a", "ns", "grp", nil, "team-ns", "bind", "role")
	onlyGroup(t, "buildCoarseRoleBinding", crb2.Subjects)
}

func TestNarrowSubjects(t *testing.T) {
	subjects := []rbacv1.Subject{
		{Kind: "Group", Name: "my-asr", APIGroup: rbacAPIGroup},
		{Kind: "User", Name: "ci1", APIGroup: rbacAPIGroup},
		{Kind: "User", Name: "ci2", APIGroup: rbacAPIGroup},
	}

	// ci2 no longer granted: dropped; Group and ci1 kept.
	got, changed := narrowSubjects(subjects, map[string]struct{}{"ci1": {}})
	if !changed || len(got) != 2 || got[0].Kind != "Group" || got[1].Name != "ci1" {
		t.Errorf("narrow = %+v (changed=%v), want [Group, User ci1]", got, changed)
	}

	// All still granted: unchanged.
	if _, stillChanged := narrowSubjects(subjects, map[string]struct{}{"ci1": {}, "ci2": {}}); stillChanged {
		t.Error("no stale users should mean no change")
	}

	// Never adds: a lone Group stays a lone Group even though ci1 is wanted.
	got, changed = narrowSubjects([]rbacv1.Subject{{Kind: "Group", Name: "my-asr", APIGroup: rbacAPIGroup}}, map[string]struct{}{"ci1": {}})
	if changed || len(got) != 1 {
		t.Errorf("narrow must never add subjects, got %+v (changed=%v)", got, changed)
	}
}
