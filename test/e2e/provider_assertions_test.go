// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// providerAssertTimeout is the default timeout for provider assertion helpers.
// Callers can override via the WithTimeout option.
const providerAssertTimeout = 30 * time.Second

// expectASRReady asserts that the ASR's Ready condition matches the expected
// readiness state. It polls until the condition is met or the timeout expires.
//
// In Phase 1 this reads from status.conditions[type=Ready].
// In Phase 2 the implementation may derive readiness from the providers map,
// but the function signature remains unchanged.
func expectASRReady(t *testing.T, ctx context.Context, c client.Client, namespace, asrName string, ready bool) {
	t.Helper()

	err := pollUntil(t, ctx, providerAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: namespace, Name: asrName}
		if err := c.Get(ctx, key, &asr); err != nil {
			return false, fmt.Errorf("failed to get ASR %s/%s: %w", namespace, asrName, err)
		}

		for _, cond := range asr.Status.Conditions {
			if cond.Type == "Ready" {
				isTrue := cond.Status == metav1.ConditionTrue
				if isTrue == ready {
					return true, nil
				}
				return false, fmt.Errorf("ASR %s Ready=%q (reason=%s, message=%s), want ready=%v",
					asrName, cond.Status, cond.Reason, cond.Message, ready)
			}
		}
		return false, fmt.Errorf("ASR %s has no Ready condition yet (conditions: %d)", asrName, len(asr.Status.Conditions))
	})
	if err != nil {
		t.Fatalf("expectASRReady(%s/%s, ready=%v): %v", namespace, asrName, ready, err)
	}
}

// expectClusterStatus asserts that the per-cluster provisioning status for a
// specific cluster on the named ASR matches expectedStatus.
//
// Reads from providers["kubernetes"].targets matching by TargetID.
func expectClusterStatus(t *testing.T, ctx context.Context, c client.Client, namespace, asrName, clusterName, expectedStatus string) {
	t.Helper()

	err := pollUntil(t, ctx, providerAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: namespace, Name: asrName}
		if err := c.Get(ctx, key, &asr); err != nil {
			return false, fmt.Errorf("failed to get ASR %s/%s: %w", namespace, asrName, err)
		}

		k8sProvider, ok := asr.Status.Providers["kubernetes"]
		if !ok {
			return false, fmt.Errorf("ASR %s has no kubernetes provider in status", asrName)
		}

		for _, entry := range k8sProvider.Targets {
			if entry.TargetID == clusterName {
				if string(entry.Status) == expectedStatus {
					return true, nil
				}
				return false, fmt.Errorf("ASR %s cluster %q status=%q, want %q (message: %s)",
					asrName, clusterName, entry.Status, expectedStatus, entry.Message)
			}
		}
		return false, fmt.Errorf("ASR %s has no target entry for cluster %q (targets: %d)",
			asrName, clusterName, len(k8sProvider.Targets))
	})
	if err != nil {
		t.Fatalf("expectClusterStatus(%s/%s, cluster=%q, expected=%q): %v",
			namespace, asrName, clusterName, expectedStatus, err)
	}
}

// expectProviderReady asserts that the named provider is considered "ready"
// for the given ASR. Reads from providers[provider].Ready.
func expectProviderReady(t *testing.T, ctx context.Context, c client.Client, namespace, asrName, provider string, ready bool) {
	t.Helper()

	err := pollUntil(t, ctx, providerAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: namespace, Name: asrName}
		if err := c.Get(ctx, key, &asr); err != nil {
			return false, fmt.Errorf("failed to get ASR %s/%s: %w", namespace, asrName, err)
		}

		providerStatus, ok := asr.Status.Providers[provider]
		if !ok {
			if !ready {
				// No provider entry yet — if expecting not-ready, that counts
				return true, nil
			}
			return false, fmt.Errorf("ASR %s has no provider %q in status", asrName, provider)
		}

		if providerStatus.Ready == ready {
			return true, nil
		}
		return false, fmt.Errorf("ASR %s provider %q: ready=%v, want ready=%v (message: %s)",
			asrName, provider, providerStatus.Ready, ready, providerStatus.Message)
	})
	if err != nil {
		t.Fatalf("expectProviderReady(%s/%s, provider=%q, ready=%v): %v",
			namespace, asrName, provider, ready, err)
	}
}

// expectCondition asserts that the named ASR has a condition of the given type
// with the expected metav1.ConditionStatus (e.g., metav1.ConditionTrue).
//
// This is a generic condition checker that works for any condition type,
// not just "Ready".
func expectCondition(t *testing.T, ctx context.Context, c client.Client, namespace, asrName, condType string, expectedStatus metav1.ConditionStatus) {
	t.Helper()

	err := pollUntil(t, ctx, providerAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: namespace, Name: asrName}
		if err := c.Get(ctx, key, &asr); err != nil {
			return false, fmt.Errorf("failed to get ASR %s/%s: %w", namespace, asrName, err)
		}

		for _, cond := range asr.Status.Conditions {
			if cond.Type == condType {
				if cond.Status == expectedStatus {
					return true, nil
				}
				return false, fmt.Errorf("ASR %s condition %q status=%q, want %q (reason=%s, message=%s)",
					asrName, condType, cond.Status, expectedStatus, cond.Reason, cond.Message)
			}
		}
		return false, fmt.Errorf("ASR %s has no condition of type %q (conditions: %d)",
			asrName, condType, len(asr.Status.Conditions))
	})
	if err != nil {
		t.Fatalf("expectCondition(%s/%s, type=%q, expected=%q): %v",
			namespace, asrName, condType, expectedStatus, err)
	}
}
