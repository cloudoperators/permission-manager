// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"slices"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/internal/lifecycle"
)

// EnsureSubjects derives the User subjects contributed by ServiceIdentities that reference this
// ASR and share one of its kubernetes connectors (ADR-019). It runs after EnsureConnectorBindings
// (which fills p.cbs) and before EnsureRbacCompiled, so the subjects are baked into every binding
// the compiler builds. Derivation is level-triggered from the live ServiceIdentity list, so it is
// independent of event delivery.
func (p *Phase) EnsureSubjects(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(ctx context.Context) (lifecycle.Result, error) {
		if len(p.cbs) == 0 {
			return lifecycle.Continue(), nil
		}
		k8sCBs := make(map[string]struct{}, len(p.cbs))
		for _, cb := range p.cbs {
			k8sCBs[cb.Name] = struct{}{}
		}

		// Field index ServiceIdentityRolesField is registered once in SetupIndexes.
		var sis v1alpha1.ServiceIdentityList
		if err := p.Client.List(ctx, &sis,
			client.InNamespace(asr.Namespace),
			client.MatchingFields{pmapis.ServiceIdentityRolesField: asr.Name},
		); err != nil {
			return lifecycle.Break(), fmt.Errorf("failed to list ServiceIdentities: %w", err)
		}

		seen := make(map[string]struct{})
		for i := range sis.Items {
			si := &sis.Items[i]
			// Skip deletions, blank usernames, and reserved system: names (also barred by the CRD).
			if !si.DeletionTimestamp.IsZero() || si.Spec.Name == "" || strings.HasPrefix(si.Spec.Name, "system:") {
				continue
			}
			if !sharesK8sConnector(si.Spec.ConnectorRefs, k8sCBs) {
				continue
			}
			if _, dup := seen[si.Spec.Name]; dup {
				continue
			}
			seen[si.Spec.Name] = struct{}{}
			p.extraUsers = append(p.extraUsers, rbacv1.Subject{
				Kind:     "User",
				Name:     si.Spec.Name,
				APIGroup: rbacAPIGroup,
			})
		}
		slices.SortFunc(p.extraUsers, func(a, b rbacv1.Subject) int { return strings.Compare(a.Name, b.Name) })
		return lifecycle.Continue(), nil
	}
}

// sharesK8sConnector reports whether any ref names one of the ASR's kubernetes ConnectorBindings.
func sharesK8sConnector(refs []string, k8sCBs map[string]struct{}) bool {
	for _, ref := range refs {
		if _, ok := k8sCBs[ref]; ok {
			return true
		}
	}
	return false
}
