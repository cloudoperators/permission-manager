// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudoperators/permission-manager/internal/naming"
)

// invalidCharsRegex matches any characters that are not valid in Kubernetes names.
var invalidCharsRegex = regexp.MustCompile(`[^a-z0-9\-]`)

// SanitizeInstance transforms a GitHub instance hostname into a valid
// Kubernetes name segment (lowercase, alphanumeric + hyphens). Still used for the
// provider status TargetID; not part of CR names.
func SanitizeInstance(instance string) string {
	s := strings.ToLower(instance)
	s = strings.ReplaceAll(s, ".", "-")
	s = invalidCharsRegex.ReplaceAllString(s, "")
	// Trim leading/trailing hyphens
	s = strings.Trim(s, "-")
	return s
}

// TeamCRName generates the CR name for a GithubTeam using repo-guard's convention
// (charts/.../githubteam.yaml): <instance-key>--<org>--<team-slug>.
// If the result exceeds 253 chars, it is truncated with a hash suffix.
func TeamCRName(instanceKey, org, team string) string {
	name := fmt.Sprintf("%s--%s--%s", instanceKey, orgSlug(org), teamSlug(team))
	return truncateWithHash(name)
}

// TeamRepoCRName generates the CR name for a GithubTeamRepository using repo-guard's
// convention (charts/.../githubteamrepository.yaml): <instance-key>--<org>--<team-slug>--<permission>.
// If the result exceeds 253 chars, it is truncated with a hash suffix.
func TeamRepoCRName(instanceKey, org, team, permission string) string {
	name := fmt.Sprintf("%s--%s--%s--%s", instanceKey, orgSlug(org), teamSlug(team), sanitizeSegment(permission))
	return truncateWithHash(name)
}

// orgSlug normalizes a GitHub org for CR-name use, mirroring the chart's
// `replace "/" "-" | lower`.
func orgSlug(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "/", "-")
	s = invalidCharsRegex.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	return s
}

// teamSlug normalizes a team name for CR-name use, mirroring the chart's
// `replace "_" "-" | replace " " "-" | lower`.
func teamSlug(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	s = invalidCharsRegex.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	return s
}

// sanitizeSegment makes a string safe for Kubernetes name use.
func sanitizeSegment(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	s = invalidCharsRegex.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	return s
}

// truncateWithHash truncates a name at the Kubernetes max name length (253).
// If truncation is needed, it appends a SHA256 hash suffix for uniqueness.
func truncateWithHash(name string) string {
	return naming.TruncateWithHash(name, naming.MaxK8sNameLength)
}
