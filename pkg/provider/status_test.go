// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pmmeta "github.com/cloudoperators/permission-manager/api/meta"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	return s
}

// ssaInterceptingClient wraps a fake client and intercepts SubResource Patch calls
// to convert Apply patches to MergePatch (since the fake client doesn't support SSA).
// It also records the patch options for assertion.
type ssaPatchRecord struct {
	FieldManager string
	ForceOwn     bool
	PatchType    types.PatchType
}

func newSSAInterceptingClient(t *testing.T, scheme *runtime.Scheme, objs ...client.Object) (client.Client, *[]ssaPatchRecord) {
	t.Helper()
	records := &[]ssaPatchRecord{}

	underlying := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.ApplicationSpecificRole{}).
		Build()

	// Create an interceptor that converts Apply patches on SubResource to MergePatch
	funcs := interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			// Record the patch details
			rec := ssaPatchRecord{}
			patchOpts := &client.SubResourcePatchOptions{}
			patchOpts.ApplyOptions(opts)
			if patchOpts.FieldManager != "" {
				rec.FieldManager = patchOpts.FieldManager
			}
			rec.ForceOwn = patchOpts.Force != nil && *patchOpts.Force
			rec.PatchType = patch.Type()
			*records = append(*records, rec)

			if patch.Type() == types.ApplyPatchType {
				// Convert the unstructured Apply patch to a regular status update
				// Extract the status from the unstructured object and apply it
				u, ok := obj.(*unstructured.Unstructured)
				if !ok {
					t.Fatalf("expected unstructured object for Apply patch, got %T", obj)
				}

				// Get the providers from the patch
				statusMap, _, _ := unstructured.NestedMap(u.Object, "status", "providers")
				if statusMap == nil {
					return nil
				}

				// Get the existing ASR
				nn := types.NamespacedName{Name: u.GetName(), Namespace: u.GetNamespace()}
				existing := &v1alpha1.ApplicationSpecificRole{}
				if err := underlying.Get(ctx, nn, existing); err != nil {
					return err
				}

				// Merge providers
				if existing.Status.Providers == nil {
					existing.Status.Providers = make(map[string]v1alpha1.ProviderStatus)
				}

				for providerKey, rawValue := range statusMap {
					providerMap, ok := rawValue.(map[string]interface{})
					if !ok {
						continue
					}

					ps := v1alpha1.ProviderStatus{}
					if ready, ok := providerMap["ready"].(bool); ok {
						ps.Ready = ready
					}
					if msg, ok := providerMap["message"].(string); ok {
						ps.Message = msg
					}
					if lr, ok := providerMap["lastReconciled"].(string); ok {
						parsed, _ := time.Parse(metav1.RFC3339Micro, lr)
						ps.LastReconciled = metav1.NewTime(parsed)
					}
					if targets, ok := providerMap["targets"].([]interface{}); ok {
						for _, rawTarget := range targets {
							targetMap, ok := rawTarget.(map[string]interface{})
							if !ok {
								continue
							}
							ts := v1alpha1.TargetStatus{}
							if id, ok := targetMap["targetID"].(string); ok {
								ts.TargetID = id
							}
							if s, ok := targetMap["status"].(string); ok {
								ts.Status = pmmeta.TargetStatusValue(s)
							}
							if m, ok := targetMap["message"].(string); ok {
								ts.Message = m
							}
							if lu, ok := targetMap["lastUpdated"].(string); ok {
								parsed, _ := time.Parse(metav1.RFC3339Micro, lu)
								ts.LastUpdated = metav1.NewTime(parsed)
							}
							ps.Targets = append(ps.Targets, ts)
						}
					}
					existing.Status.Providers[providerKey] = ps
				}

				return underlying.Status().Update(ctx, existing)
			}

			// For non-Apply patches (like MergePatch for removal), pass through
			return underlying.Status().Patch(ctx, obj, patch)
		},
	}

	ic := interceptor.NewClient(underlying, funcs)
	return ic, records
}

func TestPatchProviderStatus_NewEntry(t *testing.T) {
	scheme := newTestScheme()
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "test-role",
			ConnectorRefs: []string{"k8s-connector"},
		},
	}

	c, records := newSSAInterceptingClient(t, scheme, asr)
	ctx := context.Background()
	now := metav1.Now()

	status := v1alpha1.ProviderStatus{
		Ready:          true,
		Message:        "All targets provisioned",
		LastReconciled: now,
		Targets: []v1alpha1.TargetStatus{
			{
				TargetID:    "prod-eu",
				Status:      "Success",
				Message:     "RBAC provisioned",
				LastUpdated: now,
			},
			{
				TargetID:    "staging-us",
				Status:      "Success",
				Message:     "RBAC provisioned",
				LastUpdated: now,
			},
		},
	}

	err := PatchProviderStatus(ctx, c, asr, ProviderKubernetes, status)
	if err != nil {
		t.Fatalf("PatchProviderStatus failed: %v", err)
	}

	// Verify the patch was made with correct SSA options
	if len(*records) != 1 {
		t.Fatalf("expected 1 patch call, got %d", len(*records))
	}
	rec := (*records)[0]
	if rec.PatchType != types.ApplyPatchType {
		t.Errorf("patch type = %q, want %q", rec.PatchType, types.ApplyPatchType)
	}
	if rec.FieldManager != "pm-controller-kubernetes" {
		t.Errorf("field manager = %q, want %q", rec.FieldManager, "pm-controller-kubernetes")
	}
	if !rec.ForceOwn {
		t.Error("expected ForceOwnership=true")
	}

	// Verify the status was written
	var updated v1alpha1.ApplicationSpecificRole
	err = c.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updated)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	if updated.Status.Providers == nil {
		t.Fatal("expected Providers map to be non-nil")
	}

	ps, ok := updated.Status.Providers[ProviderKubernetes]
	if !ok {
		t.Fatal("expected 'kubernetes' entry in Providers map")
	}

	if !ps.Ready {
		t.Error("expected Ready=true")
	}
	if ps.Message != "All targets provisioned" {
		t.Errorf("Message = %q, want %q", ps.Message, "All targets provisioned")
	}
	if len(ps.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(ps.Targets))
	}
}

func TestPatchProviderStatus_UpdateExistingEntry(t *testing.T) {
	scheme := newTestScheme()
	now := metav1.Now()

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "test-role",
			ConnectorRefs: []string{"k8s-connector"},
		},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[string]v1alpha1.ProviderStatus{
				ProviderKubernetes: {
					Ready:          false,
					Message:        "Partial failure",
					LastReconciled: now,
					Targets: []v1alpha1.TargetStatus{
						{TargetID: "prod-eu", Status: "Error", Message: "timeout", LastUpdated: now},
					},
				},
			},
		},
	}

	c, _ := newSSAInterceptingClient(t, scheme, asr)
	ctx := context.Background()
	later := metav1.NewTime(now.Add(5 * time.Minute))

	newStatus := v1alpha1.ProviderStatus{
		Ready:          true,
		Message:        "All targets provisioned",
		LastReconciled: later,
		Targets: []v1alpha1.TargetStatus{
			{TargetID: "prod-eu", Status: "Success", Message: "OK", LastUpdated: later},
		},
	}

	err := PatchProviderStatus(ctx, c, asr, ProviderKubernetes, newStatus)
	if err != nil {
		t.Fatalf("PatchProviderStatus failed: %v", err)
	}

	var updated v1alpha1.ApplicationSpecificRole
	err = c.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updated)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	ps := updated.Status.Providers[ProviderKubernetes]
	if !ps.Ready {
		t.Error("expected Ready=true after update")
	}
	if ps.Message != "All targets provisioned" {
		t.Errorf("Message = %q, want %q", ps.Message, "All targets provisioned")
	}
	if len(ps.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(ps.Targets))
	}
	if ps.Targets[0].Status != "Success" {
		t.Errorf("target status = %q, want %q", ps.Targets[0].Status, "Success")
	}
}

func TestPatchProviderStatus_MultipleProviders(t *testing.T) {
	scheme := newTestScheme()
	now := metav1.Now()

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "test-role",
			ConnectorRefs: []string{"k8s-connector", "gh-connector"},
		},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[string]v1alpha1.ProviderStatus{
				ProviderKubernetes: {
					Ready:          true,
					Message:        "OK",
					LastReconciled: now,
				},
			},
		},
	}

	c, _ := newSSAInterceptingClient(t, scheme, asr)
	ctx := context.Background()

	// Patch a second provider
	ghStatus := v1alpha1.ProviderStatus{
		Ready:          true,
		Message:        "Teams synced",
		LastReconciled: now,
		Targets: []v1alpha1.TargetStatus{
			{TargetID: "org/team-a", Status: "Success", LastUpdated: now},
		},
	}

	err := PatchProviderStatus(ctx, c, asr, ProviderGreenhouseTeams, ghStatus)
	if err != nil {
		t.Fatalf("PatchProviderStatus for greenhouse-teams failed: %v", err)
	}

	var updated v1alpha1.ApplicationSpecificRole
	err = c.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updated)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	// Both providers should be present
	if len(updated.Status.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(updated.Status.Providers))
	}
	if _, ok := updated.Status.Providers[ProviderKubernetes]; !ok {
		t.Error("expected kubernetes provider to still exist")
	}
	if _, ok := updated.Status.Providers[ProviderGreenhouseTeams]; !ok {
		t.Error("expected greenhouse-teams provider to exist")
	}
}

func TestRemoveProviderStatus(t *testing.T) {
	scheme := newTestScheme()
	now := metav1.Now()

	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "test-role",
			ConnectorRefs: []string{"k8s-connector"},
		},
		Status: v1alpha1.ApplicationSpecificRoleStatus{
			Providers: map[string]v1alpha1.ProviderStatus{
				ProviderKubernetes: {
					Ready:          true,
					Message:        "OK",
					LastReconciled: now,
					Targets: []v1alpha1.TargetStatus{
						{TargetID: "prod-eu", Status: "Success", LastUpdated: now},
					},
				},
				ProviderGreenhouseTeams: {
					Ready:          true,
					Message:        "Synced",
					LastReconciled: now,
				},
			},
		},
	}

	c, _ := newSSAInterceptingClient(t, scheme, asr)
	ctx := context.Background()

	err := RemoveProviderStatus(ctx, c, asr, ProviderKubernetes)
	if err != nil {
		t.Fatalf("RemoveProviderStatus failed: %v", err)
	}

	var updated v1alpha1.ApplicationSpecificRole
	err = c.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updated)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	if _, ok := updated.Status.Providers[ProviderKubernetes]; ok {
		t.Error("expected kubernetes provider to be removed")
	}
	if _, ok := updated.Status.Providers[ProviderGreenhouseTeams]; !ok {
		t.Error("expected greenhouse-teams provider to still exist")
	}
}

func TestPatchProviderStatus_EmptyTargets(t *testing.T) {
	scheme := newTestScheme()
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "test-role",
			ConnectorRefs: []string{"k8s-connector"},
		},
	}

	c, _ := newSSAInterceptingClient(t, scheme, asr)
	ctx := context.Background()
	now := metav1.Now()

	status := v1alpha1.ProviderStatus{
		Ready:          true,
		Message:        "No targets",
		LastReconciled: now,
		Targets:        nil,
	}

	err := PatchProviderStatus(ctx, c, asr, ProviderKubernetes, status)
	if err != nil {
		t.Fatalf("PatchProviderStatus with empty targets failed: %v", err)
	}

	var updated v1alpha1.ApplicationSpecificRole
	err = c.Get(ctx, types.NamespacedName{Namespace: "pm-system", Name: "test-asr"}, &updated)
	if err != nil {
		t.Fatalf("failed to get updated ASR: %v", err)
	}

	ps := updated.Status.Providers[ProviderKubernetes]
	if !ps.Ready {
		t.Error("expected Ready=true")
	}
	if len(ps.Targets) != 0 {
		t.Errorf("expected 0 targets, got %d", len(ps.Targets))
	}
}

func TestBuildStatusPatch_Structure(t *testing.T) {
	now := metav1.Now()
	status := v1alpha1.ProviderStatus{
		Ready:          true,
		Message:        "all good",
		LastReconciled: now,
		Targets: []v1alpha1.TargetStatus{
			{TargetID: "cluster-a", Status: "Success", Message: "OK", LastUpdated: now},
		},
	}

	patch := buildStatusPatch("my-asr", "pm-system", "kubernetes", status)

	// Verify structure
	if patch.GetAPIVersion() != apiVersion {
		t.Errorf("apiVersion = %q, want %q", patch.GetAPIVersion(), apiVersion)
	}
	if patch.GetKind() != kind {
		t.Errorf("kind = %q, want %q", patch.GetKind(), kind)
	}
	if patch.GetName() != "my-asr" {
		t.Errorf("name = %q, want %q", patch.GetName(), "my-asr")
	}
	if patch.GetNamespace() != "pm-system" {
		t.Errorf("namespace = %q, want %q", patch.GetNamespace(), "pm-system")
	}

	// Check status.providers.kubernetes exists
	statusObj, ok, _ := unstructured.NestedMap(patch.Object, "status", "providers", "kubernetes")
	if !ok {
		t.Fatal("expected status.providers.kubernetes to exist")
	}
	if statusObj["ready"] != true {
		t.Errorf("ready = %v, want true", statusObj["ready"])
	}
	if statusObj["message"] != "all good" {
		t.Errorf("message = %v, want %q", statusObj["message"], "all good")
	}
}

func TestPatchProviderStatus_FieldManagerNaming(t *testing.T) {
	scheme := newTestScheme()
	asr := &v1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-asr",
			Namespace: "pm-system",
		},
		Spec: v1alpha1.ApplicationSpecificRoleSpec{
			Name:          "test-role",
			ConnectorRefs: []string{"connector"},
		},
	}

	c, records := newSSAInterceptingClient(t, scheme, asr)
	ctx := context.Background()
	now := metav1.Now()

	// Test that different providers use different field managers
	_ = PatchProviderStatus(ctx, c, asr, "kubernetes", v1alpha1.ProviderStatus{Ready: true, LastReconciled: now})
	_ = PatchProviderStatus(ctx, c, asr, "greenhouse-teams", v1alpha1.ProviderStatus{Ready: true, LastReconciled: now})

	if len(*records) != 2 {
		t.Fatalf("expected 2 patch records, got %d", len(*records))
	}
	if (*records)[0].FieldManager != "pm-controller-kubernetes" {
		t.Errorf("first field manager = %q, want %q", (*records)[0].FieldManager, "pm-controller-kubernetes")
	}
	if (*records)[1].FieldManager != "pm-controller-greenhouse-teams" {
		t.Errorf("second field manager = %q, want %q", (*records)[1].FieldManager, "pm-controller-greenhouse-teams")
	}
}
