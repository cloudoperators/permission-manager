// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package shared

import (
	"context"
	"encoding/json"
	"fmt"

	repoguardv1 "github.com/cloudoperators/repo-guard/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1alpha1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// Verb sets referenced by the k8s capability mapping and by specs asserting grants.
var (
	ReadVerbs  = []string{"get", "list", "watch"}
	WriteVerbs = []string{"get", "list", "watch", "create", "update", "patch", "delete"}
)

// rule builds one capabilityMapping rule (apiGroups/resources/verbs); all three
// are required by the provider (internal/provider/k8s/definition.go).
func rule(groups, resources, verbs []string) map[string]any {
	return map[string]any{"apiGroups": groups, "resources": resources, "verbs": verbs}
}

// EnsureK8sConnectorBinding creates the kubernetes ConnectorBinding referenced by
// all kubernetes specs. Each capability maps to a list of apiGroups/resources/verbs
// rules; the provider copies them verbatim into the generated RBAC. Resource sets
// are kept small and fixed so specs can assert exact verbs.
func EnsureK8sConnectorBinding(ctx context.Context, c client.Client, namespace string) error {
	def := map[string]any{
		"capabilityMapping": map[string]any{
			"READ":  []any{rule([]string{"", "apps"}, []string{"configmaps", "deployments", "pods"}, ReadVerbs)},
			"WRITE": []any{rule([]string{"", "apps"}, []string{"configmaps", "deployments", "pods"}, WriteVerbs)},
			"ADMIN": []any{rule([]string{"", "apps", "rbac.authorization.k8s.io"}, []string{"configmaps", "deployments", "pods", "roles"}, WriteVerbs)},
			// Deny-driven: read everything except secrets. Expanded per-target
			// against the cluster's discovered API groups (needs discoveryCache).
			"READ_EXCEPT_SECRETS": []any{map[string]any{
				"apiGroups":        []string{"*"},
				"resources":        []string{"*"},
				"verbs":            ReadVerbs,
				"excludeResources": []any{map[string]any{"apiGroups": []string{""}, "resources": []string{"secrets"}}},
			}},
		},
	}
	return ensureConnectorBinding(ctx, c, namespace, K8sConnectorBinding, "kubernetes", def)
}

// EnsureGithubConnectorBinding creates the github ConnectorBinding referenced by
// all github specs. Its capabilityMapping maps capability names to single GitHub
// permission strings, plus a default target namespace for the created CRs.
func EnsureGithubConnectorBinding(ctx context.Context, c client.Client, namespace string) error {
	def := map[string]any{
		"defaultTargetNamespace": namespace,
		"capabilityMapping": map[string]any{
			"READ":  "pull",
			"WRITE": "push",
			"ADMIN": "admin",
		},
	}
	return ensureConnectorBinding(ctx, c, namespace, GithubConnectorBinding, "github", def)
}

// ensureConnectorBinding upserts a ConnectorBinding with the given provider and
// definition. Idempotent across reruns.
func ensureConnectorBinding(ctx context.Context, c client.Client, namespace, name, provider string, definition map[string]any) error {
	raw, err := json.Marshal(definition)
	if err != nil {
		return fmt.Errorf("marshal %s definition: %w", name, err)
	}
	cb := &pmv1alpha1.ConnectorBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: pmv1alpha1.ConnectorBindingSpec{
			Name:       name,
			Provider:   provider,
			Definition: &runtime.RawExtension{Raw: raw},
		},
	}
	return applyObject(ctx, c, cb)
}

// EnsureGithubOrganization pre-seeds a GithubOrganization CR so the github happy
// path has an organisation to target. The github provider patches (not creates)
// org CRs, so it must exist beforehand.
func EnsureGithubOrganization(ctx context.Context, c client.Client, namespace, org string) error {
	obj := &repoguardv1.GithubOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: org, Namespace: namespace},
	}
	return applyObject(ctx, c, obj)
}
