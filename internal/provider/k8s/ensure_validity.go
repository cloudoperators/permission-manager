// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

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
		// capabilities granted per resource CCRN, to flag WRITE without READ.
		caps := map[string]map[string]bool{}
		for _, perm := range asr.Spec.Permissions {
			parsed, err := parser.Parse(perm.Resource)
			if err != nil {
				p.invalidCCRNs = append(p.invalidCCRNs, perm.Resource)
				continue
			}
			if !parsed.IsKubernetes() {
				continue
			}
			// Namespace wildcards are unsupported.
			if parsed.Namespace != "" && ccrn.IsWildcard(parsed.Namespace) {
				p.invalidCCRNs = append(p.invalidCCRNs, perm.Resource)
				continue
			}
			if parsed.K8sResource == nil {
				continue
			}
			if resourceCCRNInvalid(parsed, perm.Capability) {
				p.invalidCCRNs = append(p.invalidCCRNs, perm.Resource)
				continue
			}
			// A namespace-resource targeting a cluster-scoped kind can't bind
			// namespaced RBAC; warn but don't block.
			if parsed.Type == ccrn.ResourceTypeNamespaceResource && parsed.K8sResource.Resource != "*" {
				if clusterScoped, known := p.resolveClusterScoped(parsed.K8sResource.APIGroup, parsed.K8sResource.Resource); known && clusterScoped {
					p.configWarnings = append(p.configWarnings, ConfigWarning{
						Reason:  string(pmmeta.ScopeMismatchReason),
						Message: fmt.Sprintf("namespace-resource CCRN %q names cluster-scoped resource %q; the RoleBinding cannot grant it", perm.Resource, parsed.K8sResource.Resource),
					})
				}
			}
			if caps[perm.Resource] == nil {
				caps[perm.Resource] = map[string]bool{}
			}
			caps[perm.Resource][strings.ToUpper(perm.Capability)] = true
		}

		// WRITE granted on a resource without the matching READ.
		for _, perm := range asr.Spec.Permissions {
			c, ok := caps[perm.Resource]
			if !ok {
				continue
			}
			if c["WRITE"] && !c["READ"] {
				p.configWarnings = append(p.configWarnings, ConfigWarning{
					Reason:  string(pmmeta.WriteWithoutReadReason),
					Message: fmt.Sprintf("resource CCRN %q grants WRITE without READ; apply/server-side-apply tooling may break", perm.Resource),
				})
			}
			delete(caps, perm.Resource)
		}

		if len(p.configWarnings) > 0 {
			msgs := make([]string, 0, len(p.configWarnings))
			for _, w := range p.configWarnings {
				msgs = append(msgs, w.Message)
			}
			asr.SetCondition(pmmeta.TrueCondition(
				pmmeta.ConfigWarningCondition,
				pmmeta.ConfigurationWarningReason,
				strings.Join(msgs, "; "),
			))
		} else {
			asr.RemoveCondition(pmmeta.ConfigWarningCondition)
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

// resolveClusterScoped reports whether apiGroup/resource is cluster-scoped on
// the management cluster. known is false when the mapper can't resolve the
// resource, so callers fail open and skip the scope warning.
func (p *Phase) resolveClusterScoped(apiGroup, resource string) (clusterScoped, known bool) {
	if p.RESTMapper == nil {
		return false, false
	}
	gvks, err := p.RESTMapper.KindsFor(schema.GroupVersionResource{Group: apiGroup, Resource: resource})
	if err != nil || len(gvks) == 0 {
		return false, false
	}
	gvk := gvks[0]
	mapping, err := p.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return false, false
	}
	return mapping.Scope.Name() == meta.RESTScopeNameRoot, true
}

// resourceCCRNInvalid reports whether a parsed resource-scoped CCRN violates a
// hard rule. Namespace-wildcard is handled by the shared check in the main loop.
func resourceCCRNInvalid(parsed *ccrn.ParsedCCRN, capability string) bool {
	k := parsed.K8sResource
	if ccrn.IsWildcard(k.APIGroup) {
		return true
	}
	// A resource may be a bare "*" but not a partial glob like "deploy*".
	if ccrn.IsWildcard(k.Resource) && k.Resource != "*" {
		return true
	}
	// Subresources must name a concrete target: no wildcards, and none on "*".
	if ccrn.IsWildcard(k.SubResource) || (k.Resource == "*" && k.SubResource != "") {
		return true
	}
	switch strings.ToUpper(capability) {
	case "READ", "WRITE":
		return false
	default:
		return true
	}
}
