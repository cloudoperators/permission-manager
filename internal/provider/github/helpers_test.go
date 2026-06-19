// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"encoding/json"

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
	return fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).Build()
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

// newOrgWithContrib pre-populates an org with one ASR's contribution (spec
// entries plus tracking annotations), as a prior reconcile would have written it.
func newOrgWithContrib(namespace, name, asrName string, contrib *orgContribution) *repoguardv1.GithubOrganization {
	org := newGithubOrg(namespace, name)
	contribJSON, _ := json.Marshal(contrib)
	org.Annotations = map[string]string{
		AnnotationASRManagedConfig:                asrName,
		annotationASRDefaultTeamsPrefix + asrName: string(contribJSON),
	}
	org.Spec.OrganizationOwnerTeams = contrib.OwnerTeams
	org.Spec.DefaultPublicRepositoryTeams = contrib.PublicTeams
	org.Spec.DefaultPrivateRepositoryTeams = contrib.PrivateTeams
	return org
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
