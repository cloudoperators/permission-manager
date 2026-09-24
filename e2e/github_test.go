// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/cloudoperators/permission-manager/e2e/shared"
	pmgithub "github.com/cloudoperators/permission-manager/internal/provider/github"
)

// The github provider runs in its own Ordered top-level container, parallel to
// the kubernetes provider. Unlike the kubernetes provider it does NOT set the
// ASR's top-level InvalidConfig condition — it reconciles repo-guard CRs and
// records only status.providers["github"]; hard failures surface as Ready=False
// with reason FailingCreation via the lifecycle engine. Specs therefore assert
// on provider-scoped signals, and every ASR references only the github binding
// so the kubernetes provider never touches them.
var _ = Describe("github provider", Ordered, func() {
	ctx := context.Background()

	applyASR := func(name, specName string, perms []shared.Perm) {
		asr := shared.NewASR(cfg.ControllerNamespace, name, specName,
			[]string{shared.GithubConnectorBinding}, perms)
		Expect(adminC.Create(ctx, asr)).To(Succeed())
		DeferCleanup(func() {
			shared.DeleteASR(ctx, adminC, cfg.ControllerNamespace, name)
		})
	}

	Context("happy path", func() {
		It("creates a GithubTeam CR for an organization CCRN", func() {
			name := shared.ASRName("gh-org-read")
			applyASR(name, "E2E_GH_ORG_READ", []shared.Perm{
				{Resource: shared.OrgCCRN(shared.GithubInstance, shared.GithubOrg), Capability: "READ"},
			})

			// Build the expected CR name with the provider's own naming helper so
			// the assertion tracks any change to the naming scheme.
			teamCRName := pmgithub.TeamCRName(name, shared.GithubInstance, shared.GithubOrg)
			shared.ExpectGithubTeam(ctx, adminC, cfg.ControllerNamespace, teamCRName)

			// The github provider does not set the ASR's top-level InvalidConfig
			// condition; its happy-path terminal signal is the provider-scoped
			// status reaching Ready=True.
			shared.ExpectGithubProviderReady(ctx, adminC, cfg.ControllerNamespace, name)
		})

		It("creates a GithubTeamRepository CR for a repository CCRN", func() {
			name := shared.ASRName("gh-repo-write")
			applyASR(name, "E2E_GH_REPO_WRITE", []shared.Perm{
				{Resource: shared.RepoCCRN(shared.GithubInstance, shared.GithubOrg, "e2e-repo"), Capability: "WRITE"},
			})

			// A repo CCRN creates a team plus a team-repository; WRITE maps to "push".
			shared.ExpectGithubTeam(ctx, adminC, cfg.ControllerNamespace,
				pmgithub.TeamCRName(name, shared.GithubInstance, shared.GithubOrg))
			shared.ExpectGithubTeamRepository(ctx, adminC, cfg.ControllerNamespace,
				pmgithub.TeamRepoCRName(name, shared.GithubInstance, shared.GithubOrg, "push"), "push")
			shared.ExpectGithubProviderReady(ctx, adminC, cfg.ControllerNamespace, name)
		})

		It("adds the team as an org owner for an organization ADMIN CCRN", func() {
			name := shared.ASRName("gh-org-admin")
			specName := "E2E_GH_ORG_ADMIN"
			applyASR(name, specName, []shared.Perm{
				{Resource: shared.OrgCCRN(shared.GithubInstance, shared.GithubOrg), Capability: "ADMIN"},
			})

			// ADMIN on an existing org appends the ASR's team (spec.Name) to the
			// GithubOrganization's organizationOwnerTeams.
			shared.ExpectGithubOrgOwnerTeam(ctx, adminC, cfg.ControllerNamespace, shared.GithubOrg, specName)
			shared.ExpectGithubProviderReady(ctx, adminC, cfg.ControllerNamespace, name)
		})
	})

	Context("fail path", func() {
		It("reports Ready=False/FailingCreation when the target organization is missing", func() {
			// ADMIN on an org maps to an org-owner contribution, which patches the
			// GithubOrganization CR. Targeting a non-existent org makes the patch
			// fail, so the lifecycle engine records Ready=False/FailingCreation.
			name := shared.ASRName("gh-ghost-org")
			applyASR(name, "E2E_GH_GHOST_ORG", []shared.Perm{
				{Resource: shared.OrgCCRN(shared.GithubInstance, "ghost-org"), Capability: "ADMIN"},
			})

			shared.ExpectReadyFalseReason(ctx, adminC, cfg.ControllerNamespace, name, "FailingCreation")
		})
	})
})
