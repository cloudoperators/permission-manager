// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// TestReconcileOrganizations_AdminPermissionLabel: owner membership is decided per
// org against the admin-permission label, covering both admin and admin-ondemand.
func TestReconcileOrganizations_AdminPermissionLabel(t *testing.T) {
	tests := []struct {
		name        string
		orgLabel    string // "" means the label is absent
		adminMapsTo string
		wantOwner   bool
	}{
		{"admin org, ADMIN->admin", "admin", "admin", true},
		{"admin-ondemand org, ADMIN->admin-ondemand", "admin-ondemand", "admin-ondemand", true},
		{"admin org, ADMIN->admin-ondemand mismatch", "admin", "admin-ondemand", false},
		{"admin-ondemand org, ADMIN->admin mismatch", "admin-ondemand", "admin", false},
		{"no label defaults to admin", "", "admin", true},
		{"unrecognized label grants nothing", "bogus", "admin", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org := newGithubOrg("test-ns", "my-org")
			if tt.orgLabel != "" {
				org.Labels = map[string]string{adminPermissionLabel: tt.orgLabel}
			}
			p, c := newPhase(org)
			asr := newASR("asr", "team-x", []v1alpha1.Permission{perm(orgCCRN("my-org"), "ADMIN")})
			mapping := connectorDef(map[string]string{"ADMIN": tt.adminMapsTo})

			if _, _, err := p.reconcileOrganizations(context.Background(), asr, mapping, "test-ns"); err != nil {
				t.Fatalf("reconcileOrganizations: %v", err)
			}

			var got repoguardv1.GithubOrganization
			if err := c.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "my-org"}, &got); err != nil {
				t.Fatalf("get org: %v", err)
			}
			hasOwner := slices.Contains(got.Spec.OrganizationOwnerTeams, "team-x")
			if hasOwner != tt.wantOwner {
				t.Errorf("owner present = %v, want %v (owners=%v)", hasOwner, tt.wantOwner, got.Spec.OrganizationOwnerTeams)
			}
		})
	}
}
