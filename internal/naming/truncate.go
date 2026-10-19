// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package naming provides shared naming utilities for Kubernetes resource names
// across all provider controllers.
package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// MaxK8sNameLength is the maximum length for Kubernetes resource names.
	MaxK8sNameLength = 253

	// HashSuffixLength is the total length of the hash suffix: "-" + 8 hex chars = 9.
	HashSuffixLength = 9
)

// TruncateWithHash returns name unchanged if it fits within maxLen.
// If the name exceeds maxLen, it truncates the name and appends a SHA256-based
// hash suffix ("-" + 8 hex chars) to ensure uniqueness. Trailing hyphens on the
// truncated prefix are trimmed to produce valid Kubernetes names.
func TruncateWithHash(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}

	// Compute hash of the full (untruncated) name for uniqueness.
	hash := sha256.Sum256([]byte(name))
	suffix := fmt.Sprintf("-%x", hash[:4]) // 8 hex chars = 4 bytes

	// Leave room for the suffix.
	truncLen := max(maxLen-len(suffix), 0)

	// Trim trailing hyphens from the truncated prefix to avoid invalid K8s names.
	truncated := strings.TrimRight(name[:truncLen], "-")

	return truncated + suffix
}

// ShortHash returns 8 lowercase hex chars of a SHA256 over the NUL-joined parts.
// The NUL separator makes the result sensitive to field boundaries, so distinct
// tuples that would concatenate to the same string still hash differently. Use it
// to embed a collision-resistant identity in a resource name or label value.
func ShortHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:4])
}
