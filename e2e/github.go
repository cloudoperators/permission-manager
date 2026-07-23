// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// The GithubOrganization is pre-seeded by applyFixtures.
const (
	testGithubInstance = "github.example.com"
	testGithubOrg      = "e2e-org"
)

func repoGuardObj(kind string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "repo-guard.cloudoperators.dev",
		Version: "v1",
		Kind:    kind,
	})
	return obj
}

func assertGithubTeamExists(ctx context.Context, namespace, name string) {
	GinkgoHelper()
	Eventually(func() error {
		return adminClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, repoGuardObj("GithubTeam"))
	}).WithTimeout(defaultAssertTimeout).WithPolling(assertPollInterval).
		Should(Succeed(), "GithubTeam %s/%s not found", namespace, name)
}

func assertGithubTeamRepoExists(ctx context.Context, namespace, name string) {
	GinkgoHelper()
	Eventually(func() error {
		return adminClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, repoGuardObj("GithubTeamRepository"))
	}).WithTimeout(defaultAssertTimeout).WithPolling(assertPollInterval).
		Should(Succeed(), "GithubTeamRepository %s/%s not found", namespace, name)
}
