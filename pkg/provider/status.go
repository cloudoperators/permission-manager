// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

const (
	// apiVersion is the API version for ApplicationSpecificRole resources.
	apiVersion = "permission-manager.cloudoperators.dev/v1"
	// kind is the Kind for ApplicationSpecificRole resources.
	kind = "ApplicationSpecificRole"
	// fieldManagerPrefix is the prefix for SSA field managers.
	fieldManagerPrefix = "pm-controller-"
)

// PatchProviderStatus patches the status.providers[providerKey] field of an ASR
// using Server-Side Apply (SSA). Each provider controller owns its own slice of
// the status map, identified by the field manager "pm-controller-<providerKey>".
func PatchProviderStatus(ctx context.Context, c client.Client, asr *v1alpha1.ApplicationSpecificRole, providerKey string, status pmmeta.ProviderStatus) error {
	// Build the unstructured patch object with only the fields we own.
	patch := buildStatusPatch(asr.Name, asr.Namespace, providerKey, status)

	fieldManager := fieldManagerPrefix + providerKey
	return c.Status().Patch(ctx, patch,
		client.Apply,
		client.FieldOwner(fieldManager),
		client.ForceOwnership,
	)
}

// RemoveProviderStatus removes a provider entry from status.providers using a
// MergePatch that sets the key to null. SSA does not support removing map keys
// natively, so we use a strategic merge patch instead.
func RemoveProviderStatus(ctx context.Context, c client.Client, asr *v1alpha1.ApplicationSpecificRole, providerKey string) error {
	// Build a merge patch that sets the provider key to null.
	patchData, err := json.Marshal(map[string]interface{}{
		"status": map[string]interface{}{
			"providers": map[string]interface{}{
				providerKey: nil,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal remove patch: %w", err)
	}

	obj := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      asr.Name,
			Namespace: asr.Namespace,
		},
	}

	return c.Status().Patch(ctx, obj,
		client.RawPatch(types.MergePatchType, patchData),
	)
}

// UnmappedCapabilityStatus returns a ProviderStatus indicating that one or more
// capabilities could not be mapped to provider-specific permissions. This reports
// ready=false with an error message listing the unmapped capabilities.
func UnmappedCapabilityStatus(unmapped []string) pmmeta.ProviderStatus {
	return pmmeta.ProviderStatus{
		Ready:          false,
		Message:        fmt.Sprintf("Unmapped capabilities: %v", unmapped),
		LastReconciled: metav1.Now(),
	}
}

// buildStatusPatch constructs an unstructured object suitable for SSA patching
// of a single provider's status entry.
func buildStatusPatch(name, namespace, providerKey string, status pmmeta.ProviderStatus) *unstructured.Unstructured {
	// Convert targets to unstructured format
	targets := make([]interface{}, 0, len(status.Targets))
	for _, t := range status.Targets {
		target := map[string]interface{}{
			"targetID":    t.TargetID,
			"status":      string(t.Status),
			"lastUpdated": t.LastUpdated.UTC().Format(metav1.RFC3339Micro),
		}
		if t.Message != "" {
			target["message"] = t.Message
		}
		targets = append(targets, target)
	}

	providerStatus := map[string]interface{}{
		"ready":          status.Ready,
		"lastReconciled": status.LastReconciled.UTC().Format(metav1.RFC3339Micro),
	}
	if status.Message != "" {
		providerStatus["message"] = status.Message
	}
	if len(targets) > 0 {
		providerStatus["targets"] = targets
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"status": map[string]interface{}{
				"providers": map[string]interface{}{
					providerKey: providerStatus,
				},
			},
		},
	}

	return obj
}
