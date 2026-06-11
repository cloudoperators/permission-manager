// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudoperators/permission-manager/controller/pkg/naming"
)

const (
	// crNamePrefix is the prefix for all generated CR names.
	crNamePrefix = "asr-"
)

// invalidCharsRegex matches any characters that are not valid in Kubernetes names.
var invalidCharsRegex = regexp.MustCompile(`[^a-z0-9\-]`)

// SanitizeInstance transforms a GitHub instance hostname into a valid
// Kubernetes name segment (lowercase, alphanumeric + hyphens).
func SanitizeInstance(instance string) string {
	s := strings.ToLower(instance)
	s = strings.ReplaceAll(s, ".", "-")
	s = invalidCharsRegex.ReplaceAllString(s, "")
	// Trim leading/trailing hyphens
	s = strings.Trim(s, "-")
	return s
}

// TeamCRName generates the CR name for a GithubTeam.
// Format: asr-<asrName>-<sanitizedInstance>-<org>
// If the result exceeds 253 chars, it is truncated with a hash suffix.
func TeamCRName(asrName, instance, org string) string {
	name := fmt.Sprintf("%s%s-%s-%s", crNamePrefix, asrName, SanitizeInstance(instance), sanitizeSegment(org))
	return truncateWithHash(name)
}

// TeamRepoCRName generates the CR name for a GithubTeamRepository.
// Format: asr-<asrName>-<sanitizedInstance>-<org>-<capability>
// If the result exceeds 253 chars, it is truncated with a hash suffix.
func TeamRepoCRName(asrName, instance, org, capability string) string {
	name := fmt.Sprintf("%s%s-%s-%s-%s", crNamePrefix, asrName, SanitizeInstance(instance), sanitizeSegment(org), sanitizeSegment(capability))
	return truncateWithHash(name)
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
