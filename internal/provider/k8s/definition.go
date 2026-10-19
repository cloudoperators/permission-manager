// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// ConnectorDefinition is the parsed form of a kubernetes ConnectorBinding's
// definition field. capabilityMapping is nested by scope (cluster vs namespace);
// each scope maps a closed set of symbolic capability names to the RBAC the
// controller provisions for them.
type ConnectorDefinition struct {
	CapabilityMapping ScopedCapabilityMapping `json:"capabilityMapping"`
}

// ScopedCapabilityMapping separates capabilities that apply to whole-cluster
// CCRNs from those that apply to namespace CCRNs. A coarse CCRN resolves its
// capability against exactly one of these sections.
type ScopedCapabilityMapping struct {
	Cluster   map[string]CapabilityEntry `json:"cluster,omitempty"`
	Namespace map[string]CapabilityEntry `json:"namespace,omitempty"`
}

// CapabilityEntry is one capability's provisioning instruction. It takes one of
// two mutually exclusive shapes:
//
//   - Direct binding: ClusterRoleName names an existing ClusterRole the
//     controller binds to directly. Valid only for the cluster-scope MANAGE
//     capability (binding to cluster-admin).
//   - Shared aggregated: ClusterRoleSelectors (at least one) drive the
//     aggregated root ClusterRole, and Rules (optional) populate a baseline
//     child ClusterRole the root also selects.
type CapabilityEntry struct {
	ClusterRoleName      string                 `json:"clusterRoleName,omitempty"`
	ClusterRoleSelectors []metav1.LabelSelector `json:"clusterRoleSelectors,omitempty"`
	Rules                []CapabilityRule       `json:"rules,omitempty"`
}

// CapabilityRule is one apiGroups/resources/verbs triple for a baseline child
// ClusterRole. All three fields are required.
type CapabilityRule struct {
	APIGroups []string `json:"apiGroups"`
	Resources []string `json:"resources"`
	Verbs     []string `json:"verbs"`
}

// validate returns an error if any required field is empty.
func (r CapabilityRule) validate() error {
	if len(r.APIGroups) == 0 {
		return errors.New("capabilityRule: apiGroups must be specified")
	}
	if len(r.Resources) == 0 {
		return errors.New("capabilityRule: resources must be specified")
	}
	if len(r.Verbs) == 0 {
		return errors.New("capabilityRule: verbs must be specified")
	}
	return nil
}

// validate checks one capability entry for its scope. It enforces the closed
// vocabulary, the mutually exclusive entry shapes, and that a direct binding is
// only used for cluster MANAGE.
func (e CapabilityEntry) validate(scope, capability string) error {
	allowed := validNamespaceCapabilities
	if scope == scopeCluster {
		allowed = validClusterCapabilities
	}
	// Mapping keys must be canonical uppercase: lookups (resolveCapability,
	// shared-role naming) uppercase the requested capability, so a lowercase key
	// would be accepted here yet never resolve.
	if capability != strings.ToUpper(capability) {
		return fmt.Errorf("%s capability %q must be uppercase", scope, capability)
	}
	if _, ok := allowed[capability]; !ok {
		return fmt.Errorf("%s capability %q is not in the allowed set", scope, capability)
	}

	direct := e.ClusterRoleName != ""
	shared := len(e.ClusterRoleSelectors) > 0 || len(e.Rules) > 0
	switch {
	case direct && shared:
		return errors.New("clusterRoleName is mutually exclusive with clusterRoleSelectors/rules")
	case direct:
		if scope != scopeCluster || capability != "MANAGE" {
			return errors.New("clusterRoleName is only valid for the cluster MANAGE capability")
		}
	case shared:
		if len(e.ClusterRoleSelectors) == 0 {
			return errors.New("shared capability requires at least one clusterRoleSelector")
		}
		for i, r := range e.Rules {
			if err := r.validate(); err != nil {
				return fmt.Errorf("rule %d: %w", i, err)
			}
		}
	default:
		return errors.New("capability entry must set clusterRoleName or clusterRoleSelectors")
	}
	return nil
}

// validate checks every capability in both scope sections.
func (m ScopedCapabilityMapping) validate() error {
	for capability, entry := range m.Cluster {
		if err := entry.validate(scopeCluster, capability); err != nil {
			return err
		}
	}
	for capability, entry := range m.Namespace {
		if err := entry.validate(scopeNamespace, capability); err != nil {
			return err
		}
	}
	return nil
}

// empty reports whether neither scope section defines any capability.
func (m ScopedCapabilityMapping) empty() bool {
	return len(m.Cluster) == 0 && len(m.Namespace) == 0
}

// ParseConnectorDefinition unmarshals cb.Spec.Definition into a typed
// ConnectorDefinition and validates every capability entry.
func ParseConnectorDefinition(raw *runtime.RawExtension) (*ConnectorDefinition, error) {
	if raw == nil || raw.Raw == nil {
		return nil, errors.New("connector binding definition is nil")
	}

	var def ConnectorDefinition
	if err := json.Unmarshal(raw.Raw, &def); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ConnectorBinding definition: %w", err)
	}
	if err := def.CapabilityMapping.validate(); err != nil {
		return nil, err
	}
	return &def, nil
}

// resolvedCapability pairs a capability entry with the ConnectorBinding that
// defined it, so the compiler can name shared roles per CB.
type resolvedCapability struct {
	cbName string
	entry  CapabilityEntry
}

// resolveCapability collects, across every parseable kubernetes CB, the entries
// that define capability within the given scope section. Returns the matches in
// CB order and true if any CB defined it; nil, false otherwise.
func resolveCapability(cbs []*v1alpha1.ConnectorBinding, scope, capability string) ([]resolvedCapability, bool) {
	up := strings.ToUpper(capability)
	var out []resolvedCapability

	for _, cb := range cbs {
		def, err := ParseConnectorDefinition(cb.Spec.Definition)
		if err != nil {
			continue
		}
		section := def.CapabilityMapping.Namespace
		if scope == scopeCluster {
			section = def.CapabilityMapping.Cluster
		}
		if entry, ok := section[up]; ok {
			out = append(out, resolvedCapability{cbName: cb.Spec.Name, entry: entry})
		}
	}

	return out, len(out) > 0
}
