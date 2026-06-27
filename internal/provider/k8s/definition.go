// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// ConnectorDefinition is the parsed form of a kubernetes ConnectorBinding's
// definition field. The capability map is operator-curated vocabulary —
// "READ" → list of RBAC verbs — and is read at reconcile time. No hardcoded
// fallback: an empty or missing map means the CB is misconfigured.
type ConnectorDefinition struct {
	CapabilityMapping map[string][]string `json:"capabilityMapping"`
}

// ParseConnectorDefinition unmarshals cb.Spec.Definition into a typed
// ConnectorDefinition. Mirrors github.ParseConnectorDefinition.
func ParseConnectorDefinition(raw *runtime.RawExtension) (*ConnectorDefinition, error) {
	if raw == nil || raw.Raw == nil {
		return nil, errors.New("connector binding definition is nil")
	}

	var def ConnectorDefinition
	if err := json.Unmarshal(raw.Raw, &def); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ConnectorBinding definition: %w", err)
	}
	return &def, nil
}

// UnionVerbsForCapability resolves a capability name to the set-union of verb
// lists from every kubernetes CB the ASR references. Returns the sorted verb
// slice and true if any CB defined the capability; returns nil, false otherwise
// (the caller treats this as an unmapped capability).
//
// Authority is "trust the CB": if a CB declares WRITE without get/list/watch,
// the resulting role won't include them. Misconfigurations surface as runtime
// behavior, not implicit code injection.
func UnionVerbsForCapability(cbs []*v1alpha1.ConnectorBinding, capability string) ([]string, bool) {
	capability = strings.ToUpper(capability)
	set := make(map[string]struct{})
	found := false

	for _, cb := range cbs {
		def, err := ParseConnectorDefinition(cb.Spec.Definition)
		if err != nil {
			continue
		}
		verbs, ok := def.CapabilityMapping[capability]
		if !ok {
			continue
		}
		found = true
		for _, v := range verbs {
			set[v] = struct{}{}
		}
	}

	if !found {
		return nil, false
	}

	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out, true
}
