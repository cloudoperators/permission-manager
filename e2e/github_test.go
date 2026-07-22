// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("ASR provisioning: github", func() {
	It("provisions a GithubTeam and GithubTeamRepository when an ASR uses a github connector", func() {
		ctx := context.Background()
		const asrName = "provision-gh-reader"

		resource := fmt.Sprintf("repository.github.ccrn.cloudoperators.dev/v1/%s/%s/my-repo", testGithubInstance, testGithubOrg)
		createASR(asrName, []string{"test-github"}, []map[string]string{
			{"resource": resource, "capability": "read"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		teamCRName := fmt.Sprintf("asr-%s-%s-%s", asrName, testGithubInstanceSanitized, testGithubOrg)
		teamRepoCRName := fmt.Sprintf("asr-%s-%s-%s-pull", asrName, testGithubInstanceSanitized, testGithubOrg)

		assertGithubTeamExists(ctx, orgNamespace, teamCRName)
		assertGithubTeamRepoExists(ctx, orgNamespace, teamRepoCRName)
	})
})
