// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// ConnectorDefinition is the parsed form of a kubernetes ConnectorBinding's
// definition field. The capability map is operator-curated vocabulary —
// a symbolic name ("READ", "OPERATE") → the RBAC it grants — read at reconcile
// time. No hardcoded fallback: an empty or missing map means the CB is
// misconfigured.
type ConnectorDefinition struct {
	CapabilityMapping map[string]Capability `json:"capabilityMapping"`
}

// Capability is the set of RBAC rules a symbolic capability grants. Each rule
// mirrors the native Kubernetes PolicyRule structure (apiGroups + resources +
// verbs), which lets a capability express sub-resource access such as
// pods/exec or pods/log.
type Capability []CapabilityRule

// CapabilityRule is one apiGroups/resources/verbs triple. All three fields are
// required; omitting any is a configuration error (see validate).
type CapabilityRule struct {
	APIGroups []string `json:"apiGroups"`
	Resources []string `json:"resources"`
	Verbs     []string `json:"verbs"`
}

// validate returns an error if any required field is empty. Implicit wildcards
// are intentionally not applied: operators must be explicit about what they
// grant to avoid accidental exposure of sensitive resources.
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

// ParseConnectorDefinition unmarshals cb.Spec.Definition into a typed
// ConnectorDefinition and validates every rule. Mirrors github.ParseConnectorDefinition.
func ParseConnectorDefinition(raw *runtime.RawExtension) (*ConnectorDefinition, error) {
	if raw == nil || raw.Raw == nil {
		return nil, errors.New("connector binding definition is nil")
	}

	var def ConnectorDefinition
	if err := json.Unmarshal(raw.Raw, &def); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ConnectorBinding definition: %w", err)
	}
	for cap, rules := range def.CapabilityMapping {
		for i, r := range rules {
			if err := r.validate(); err != nil {
				return nil, fmt.Errorf("capability %q rule %d: %w", cap, i, err)
			}
		}
	}
	return &def, nil
}

// UnionRulesForCapability resolves a capability name to the rules from every
// kubernetes CB the ASR references, concatenated in CB order. Returns the rules
// and true if any CB defined the capability; returns nil, false otherwise (the
// caller treats this as an unmapped capability). Deduplication and per-target
// verb union happen at compile time in policyRuleSet, so the returned slice may
// contain rules that share a target.
//
// Authority is "trust the CB": if a CB declares WRITE without get/list/watch,
// the resulting role won't include them. Misconfigurations surface as runtime
// behavior, not implicit code injection.
func UnionRulesForCapability(cbs []*v1alpha1.ConnectorBinding, capability string) ([]CapabilityRule, bool) {
	capability = strings.ToUpper(capability)
	found := false
	var rules []CapabilityRule

	for _, cb := range cbs {
		def, err := ParseConnectorDefinition(cb.Spec.Definition)
		if err != nil {
			continue
		}
		capRules, ok := def.CapabilityMapping[capability]
		if !ok {
			continue
		}
		found = true
		rules = append(rules, capRules...)
	}

	if !found {
		return nil, false
	}
	return rules, true
}
