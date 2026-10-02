// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"

	pmgithub "github.com/cloudoperators/permission-manager/internal/provider/github"
)

var _ = Describe("ASR provisioning: github", func() {
	It("provisions a GithubTeam and GithubTeamRepository when an ASR uses a github connector", func() {
		ctx := context.Background()
		const asrName = "provision-gh-reader"

		resource := fmt.Sprintf("urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/%s/%s/my-repo", testGithubInstance, testGithubOrg)
		createASR(asrName, []string{"test-github"}, []map[string]string{
			{"resource": resource, "capability": "read"},
		})
		DeferCleanup(func() { deleteASR(asrName) })

		// read maps to the pull permission in the fixture; the team name is the ASR name.
		teamCRName := pmgithub.TeamCRName(testGithubInstanceKey, testGithubOrg, asrName)
		teamRepoCRName := pmgithub.TeamRepoCRName(testGithubInstanceKey, testGithubOrg, asrName, "pull")

		assertGithubTeamExists(ctx, orgNamespace, teamCRName)
		assertGithubTeamRepoExists(ctx, orgNamespace, teamRepoCRName)
	})
})
