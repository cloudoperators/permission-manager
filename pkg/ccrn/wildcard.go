// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package ccrn

import "strings"

// IsWildcard returns true if the pattern contains a wildcard character.
func IsWildcard(s string) bool {
	return strings.Contains(s, "*")
}

// MatchGlob matches a pattern against a name. The only supported wildcard is
// a trailing `*` which matches any suffix (prefix-glob). A bare `*` matches
// everything. Non-wildcard patterns require exact equality.
func MatchGlob(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == name
	}
	// CRD regex guarantees * only at end
	prefix := strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(name, prefix)
}
