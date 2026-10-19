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
//
// ExcludeResources turns the rule into a deny-driven grant: rather than granting
// the literal apiGroups/resources, the controller expands the rule against the
// target cluster's discovered API groups and subtracts the named resources (see
// expandRuleForCluster). It is only valid alongside resources: ["*"].
type CapabilityRule struct {
	APIGroups        []string           `json:"apiGroups"`
	Resources        []string           `json:"resources"`
	Verbs            []string           `json:"verbs"`
	ExcludeResources []ResourceSelector `json:"excludeResources,omitempty"`
}

// ResourceSelector names resources to remove from a deny-driven CapabilityRule's
// expansion. It mirrors the apiGroups/resources shape of a PolicyRule but carries
// no verbs — verbs are copied from the parent rule onto every emitted grant.
type ResourceSelector struct {
	APIGroups []string `json:"apiGroups"`
	Resources []string `json:"resources"`
}

// wildcard reports whether a slice is exactly the RBAC wildcard ["*"].
func wildcard(ss []string) bool {
	return len(ss) == 1 && ss[0] == "*"
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
	if len(r.ExcludeResources) > 0 {
		// Deny-driven expansion enumerates every resource in a group and removes
		// the excluded ones, so it only makes sense when the rule would otherwise
		// grant all resources. Subtracting from an explicit list is a no-op or an
		// operator mistake; reject it to keep expansion unambiguous.
		if !wildcard(r.Resources) {
			return errors.New(`capabilityRule: excludeResources requires resources: ["*"]`)
		}
		for i, sel := range r.ExcludeResources {
			if err := sel.validate(); err != nil {
				return fmt.Errorf("excludeResources[%d]: %w", i, err)
			}
		}
	}
	return nil
}

// validate returns an error if a selector is missing apiGroups or resources, or
// if either is the wildcard ["*"]. Wildcards are rejected because expansion
// matches selector entries against discovered group/resource names literally: a
// wildcard would match nothing, so the intended exclusion would silently not
// apply and grant access the operator meant to deny. Rejecting fails closed.
func (s ResourceSelector) validate() error {
	if len(s.APIGroups) == 0 {
		return errors.New("apiGroups must be specified")
	}
	if wildcard(s.APIGroups) {
		return errors.New(`apiGroups: ["*"] is not supported in excludeResources selectors`)
	}
	if len(s.Resources) == 0 {
		return errors.New("resources must be specified")
	}
	if wildcard(s.Resources) {
		return errors.New(`resources: ["*"] is not supported in excludeResources selectors`)
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
	for capability, rules := range def.CapabilityMapping {
		for i, r := range rules {
			if err := r.validate(); err != nil {
				return nil, fmt.Errorf("capability %q rule %d: %w", capability, i, err)
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
