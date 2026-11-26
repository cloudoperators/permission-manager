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
	"github.com/cloudoperators/permission-manager/internal/audit"
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

// clusterReady reports whether the named cluster exists in the store and is Ready — the same
// readiness check the compile phase uses. A nil store (unit tests) is treated as ready.
func (p *Phase) clusterReady(name, namespace string) bool {
	if p.ClusterStore == nil {
		return true
	}
	c, ok := getClusterFromStore(p.ClusterStore, namespace, name)
	return ok && isClusterReady(c)
}

// EnsureSubjectsReconciled narrows the subjects of the ASR's already-applied bindings to the live
// ServiceIdentity set. Unlike EnsureRbacApplied it is NOT gated on HasInvalidConfig, so a User
// subject whose ServiceIdentity was deleted is removed even when the ASR is unreconcilable
// (ADR-019 §10). It only removes User subjects — never adds and never creates bindings — so while
// the ASR is invalid it can only narrow access, never widen it. Failures are collected into
// p.subjectErrs and aggregated by EnsureProviderStatus; returning Continue here keeps the chain
// running so status is still written (a returned error would abort it and force Ready=false).
func (p *Phase) EnsureSubjectsReconciled(asr *v1alpha1.ApplicationSpecificRole) lifecycle.SubRoutine {
	return func(ctx context.Context) (lifecycle.Result, error) {
		want := make(map[string]struct{}, len(p.extraUsers))
		for _, u := range p.extraUsers {
			want[u.Name] = struct{}{}
		}
		// Skip clusters the apply phase already handled this reconcile (applied or failed — a failed
		// apply is retried via provisionErrs) and clusters that are not Ready (unreachable).
		skip := make(map[string]struct{})
		for _, s := range p.provisionStatuses {
			skip[s.name] = struct{}{}
		}
		for _, n := range p.notReadyClusters {
			skip[n] = struct{}{}
		}
		sel := client.MatchingLabels{
			pmapis.LabelASR:             asrLabelValue(asr.Name),
			pmapis.LabelSourceNamespace: asr.Namespace,
		}

		for _, clusterName := range previouslyTargetedClusters(asr) {
			if _, ok := skip[clusterName]; ok {
				continue
			}
			if !p.clusterReady(clusterName, asr.Namespace) {
				continue // cluster gone from the inventory or not Ready; nothing to reach
			}
			remote, err := p.RemoteClient(ctx, p.Client, clusterName, asr.Namespace)
			if err != nil {
				p.Log.Error(err, "cluster unreachable, skipping subject reconcile", "namespace", asr.Namespace, "name", asr.Name, "cluster", clusterName)
				p.subjectErrs = append(p.subjectErrs, err)
				continue
			}

			var crbs rbacv1.ClusterRoleBindingList
			if err := remote.List(ctx, &crbs, sel); err != nil {
				p.subjectErrs = append(p.subjectErrs, fmt.Errorf("list ClusterRoleBindings on %s: %w", clusterName, err))
			}
			for i := range crbs.Items {
				b := &crbs.Items[i]
				if err := p.narrowBindingSubjects(ctx, remote, b, kindClusterRoleBinding, b.Name, b.Namespace, asr.Name, clusterName, b.Subjects, want, func(s []rbacv1.Subject) { b.Subjects = s }); err != nil {
					p.subjectErrs = append(p.subjectErrs, err)
				}
			}

			var rbs rbacv1.RoleBindingList
			if err := remote.List(ctx, &rbs, sel); err != nil {
				p.subjectErrs = append(p.subjectErrs, fmt.Errorf("list RoleBindings on %s: %w", clusterName, err))
			}
			for i := range rbs.Items {
				b := &rbs.Items[i]
				if err := p.narrowBindingSubjects(ctx, remote, b, kindRoleBinding, b.Name, b.Namespace, asr.Name, clusterName, b.Subjects, want, func(s []rbacv1.Subject) { b.Subjects = s }); err != nil {
					p.subjectErrs = append(p.subjectErrs, err)
				}
			}
		}
		return lifecycle.Continue(), nil
	}
}

// narrowBindingSubjects patches obj's subjects to the narrowed set when a User was dropped, writing
// an audit record for the change. setSubjects assigns the new slice onto the concrete binding.
func (p *Phase) narrowBindingSubjects(ctx context.Context, remote client.Client, obj client.Object, kind, name, namespace, asrName, clusterName string, subjects []rbacv1.Subject, want map[string]struct{}, setSubjects func([]rbacv1.Subject)) error {
	narrowed, changed := narrowSubjects(subjects, want)
	if !changed {
		return nil
	}
	patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	setSubjects(narrowed)
	ref := audit.ResourceRef{Kind: kind, Name: name, Namespace: namespace, Cluster: clusterName}
	if err := remote.Patch(ctx, obj, patch); err != nil {
		p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeFailure, asrName, ref, err)
		return fmt.Errorf("narrow %s %s/%s on %s: %w", kind, namespace, name, clusterName, err)
	}
	p.AuditLog.Log(ctx, audit.ActionUpdate, audit.OutcomeSuccess, asrName, ref, nil)
	return nil
}

// narrowSubjects drops User subjects whose name is absent from wantUsers, keeping every other
// subject (notably the Group). Returns the filtered slice and whether anything was removed.
func narrowSubjects(subjects []rbacv1.Subject, wantUsers map[string]struct{}) ([]rbacv1.Subject, bool) {
	out := make([]rbacv1.Subject, 0, len(subjects))
	for _, s := range subjects {
		if s.Kind == "User" {
			if _, ok := wantUsers[s.Name]; !ok {
				continue
			}
		}
		out = append(out, s)
	}
	return out, len(out) != len(subjects)
}
