// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/audit"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = repoguardv1.AddToScheme(s)
	return s
}

func newClient(objs ...client.Object) client.Client {
	// WithReturnManagedFields so orgManagedByASR can read the per-ASR SSA field manager.
	return fake.NewClientBuilder().WithScheme(newScheme()).WithReturnManagedFields().WithObjects(objs...).Build()
}

func newPhase(objs ...client.Object) (*Phase, client.Client) {
	c := newClient(objs...)
	return New(c, audit.Disabled(), logr.Discard()), c
}

func perm(resource, capability string) v1alpha1.Permission {
	return v1alpha1.Permission{Resource: resource, Capability: capability}
}

func repoCCRN(instance, org, repo string) string {
	return "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/" + instance + "/" + org + "/" + repo
}

// orgCCRN and repoTypeCCRN fix the parts that never vary across tests (the
// instance, and the org for repository_type); only repoCCRN needs a
// parametrized instance because team grouping is tested across instances.
func orgCCRN(org string) string {
	return "urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/github.example.com/" + org
}

func repoTypeCCRN(visibility string) string {
	return "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/" + visibility
}

func newASR(name, specName string, perms []v1alpha1.Permission) *v1alpha1.ApplicationSpecificRole {
	return &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          specName,
			ConnectorRefs: []string{"github-cb"},
			Permissions:   perms,
		},
	}
}

func connectorDef(mapping map[string]string) *GithubConnectorDefinition {
	return &GithubConnectorDefinition{CapabilityMapping: mapping, DefaultTargetNamespace: "test-ns"}
}

func buildConnectorDef(mapping map[string]string) *runtime.RawExtension {
	raw, _ := json.Marshal(connectorDef(mapping))
	return &runtime.RawExtension{Raw: raw}
}

func newConnectorBinding(mapping map[string]string) *v1alpha1.ConnectorBinding {
	return &v1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "github-cb", Namespace: "default"},
		Spec: v1alpha1.ConnectorBindingSpec{
			Name:       "github-cb",
			Provider:   string(pmmeta.ProviderGithub),
			Definition: buildConnectorDef(mapping),
		},
	}
}

func newGithubOrg(namespace, name string) *repoguardv1.GithubOrganization {
	return &repoguardv1.GithubOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

func newOwnedTeam(name, namespace, asrName string) *repoguardv1.GithubTeam {
	return &repoguardv1.GithubTeam{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: BuildLabels(asrName, "default")},
		Spec:       repoguardv1.GithubTeamSpec{Team: "my-team"},
	}
}

func newOwnedTeamRepo(name, namespace, asrName string, repos []string, permission string) *repoguardv1.GithubTeamRepository {
	return &repoguardv1.GithubTeamRepository{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: BuildLabels(asrName, "default")},
		Spec: repoguardv1.GithubTeamRepositorySpec{
			Repository: repos,
			Permission: repoguardv1.GithubTeamPermission(permission),
		},
	}
}

// seedOrgViaSSA creates an empty org and applies each ASR's contribution via
// patchOrganization, so the team-list fields carry that ASR's SSA field manager.
func seedOrgViaSSA(t *testing.T, p *Phase, c client.Client, ns, name string, contribs map[string]*orgContribution) {
	t.Helper()
	if err := c.Create(context.Background(), newGithubOrg(ns, name)); err != nil {
		t.Fatalf("create org %s/%s: %v", ns, name, err)
	}
	asrNames := make([]string, 0, len(contribs))
	for asr := range contribs {
		asrNames = append(asrNames, asr)
	}
	slices.Sort(asrNames) // deterministic apply order
	for _, asr := range asrNames {
		if err := p.patchOrganization(context.Background(), "default", asr, name, ns, contribs[asr]); err != nil {
			t.Fatalf("seed contribution %q into %s/%s: %v", asr, ns, name, err)
		}
	}
}
