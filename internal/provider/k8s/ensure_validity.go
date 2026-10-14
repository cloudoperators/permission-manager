// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"strings"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/ccrn"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
)

// EnsureASRValidity validates each kubernetes CCRN in asr.Spec.Permissions.
// Hard parse failures and namespace wildcards are collected into p.invalidCCRNs;
// non-k8s permissions are ignored. Always returns Continue so downstream
// subroutines can still process the valid entries.
//
// Writes InvalidConfig=True/InvalidCCRN (mirrored on Ready=False) when invalid
// entries are present; clears the InvalidCCRN reason otherwise.
func (p *Phase) EnsureASRValidity(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(_ context.Context) (lifecycle.Result, error) {
		parser := p.CCRNParser
		if parser == nil {
			parser = ccrn.NewParser(nil)
		}
		for _, perm := range asr.Spec.Permissions {
			parsed, err := parser.Parse(perm.Resource)
			if err != nil {
				p.invalidCCRNs = append(p.invalidCCRNs, perm.Resource)
				continue
			}
			if !parsed.IsKubernetes() {
				continue
			}
			// Namespace wildcards are unsupported (scenarios doc §373).
			if parsed.Namespace != "" && ccrn.IsWildcard(parsed.Namespace) {
				p.invalidCCRNs = append(p.invalidCCRNs, perm.Resource)
				continue
			}
			if parsed.K8sResource != nil && resourceCCRNInvalid(parsed, perm.Capability) {
				p.invalidCCRNs = append(p.invalidCCRNs, perm.Resource)
				continue
			}
		}

		if len(p.invalidCCRNs) > 0 {
			msg := fmt.Sprintf("invalid CCRNs: %v", p.invalidCCRNs)
			asr.SetCondition(pmmeta.TrueCondition(
				pmmeta.InvalidConfigCondition,
				pmmeta.InvalidCCRNReason,
				msg,
			))
			asr.SetCondition(pmmeta.FalseCondition(
				pmmeta.ReadyCondition,
				pmmeta.InvalidCCRNReason,
				msg,
			))
			return lifecycle.Continue(), nil
		}

		asr.ClearInvalidConfigIfReason(pmmeta.InvalidCCRNReason)
		return lifecycle.Continue(), nil
	}
}

// resourceCCRNInvalid reports whether a parsed resource-scoped CCRN violates a
// hard rule. Namespace-wildcard is handled by the shared check in the main loop.
func resourceCCRNInvalid(parsed *ccrn.ParsedCCRN, capability string) bool {
	k := parsed.K8sResource
	if ccrn.IsWildcard(k.APIGroup) {
		return true
	}
	if k.Resource == "*" && k.SubResource != "" {
		return true
	}
	switch strings.ToUpper(capability) {
	case "READ", "WRITE":
		return false
	default:
		return true
	}
}
