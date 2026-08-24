// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package shared

import (
	"context"
	"time"

	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1alpha1 "github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// asrCleanupFinalizer is the finalizer the lifecycle engine adds to ASRs
// (internal/lifecycle/reconcile.go). DeleteASR strips it as a fallback so
// teardown never hangs when the controller is unavailable.
const asrCleanupFinalizer = "pm.cloudoperators.dev/cleanup"

// Perm is a convenience alias for building ASR permission lists.
type Perm = pmv1alpha1.Permission

// NewASR builds an ApplicationSpecificRole. specName becomes the RBAC subject
// group; connectorRefs and perms wire it to a provider.
func NewASR(namespace, name, specName string, connectorRefs []string, perms []Perm) *pmv1alpha1.ApplicationSpecificRole {
	return &pmv1alpha1.ApplicationSpecificRole{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: pmv1alpha1.ApplicationSpecificRoleSpec{
			Name:          specName,
			ConnectorRefs: connectorRefs,
			Permissions:   perms,
		},
	}
}

// UpdateASRPermissions replaces the ASR's permissions, retrying on conflict
// because the controller writes status on the same object.
func UpdateASRPermissions(ctx context.Context, c client.Client, namespace, name string, perms []Perm) {
	key := types.NamespacedName{Namespace: namespace, Name: name}
	Eventually(func() error {
		asr := &pmv1alpha1.ApplicationSpecificRole{}
		if err := c.Get(ctx, key, asr); err != nil {
			return err
		}
		asr.Spec.Permissions = perms
		return c.Update(ctx, asr)
	}).WithTimeout(30*time.Second).WithPolling(time.Second).
		Should(Succeed(), "update permissions of ASR %s/%s", namespace, name)
}

// DeleteASR removes an ASR and blocks until it is gone. It first attempts a
// graceful delete (letting the controller's finalizer cleanup run); if the
// object lingers past the timeout it strips the finalizer and retries so a
// failed or slow controller cannot leak the object. Safe to call when the ASR
// is already absent. Intended for use in DeferCleanup.
func DeleteASR(ctx context.Context, c client.Client, namespace, name string) {
	key := types.NamespacedName{Namespace: namespace, Name: name}

	current := &pmv1alpha1.ApplicationSpecificRole{}
	if err := c.Get(ctx, key, current); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
	}

	Expect(client.IgnoreNotFound(c.Delete(ctx, current))).To(Succeed())

	// Give the finalizer-based cleanup a short window, then force if needed.
	if !waitGone(ctx, c, key, 30*time.Second) {
		stripFinalizer(ctx, c, key)
		fresh := &pmv1alpha1.ApplicationSpecificRole{}
		if err := c.Get(ctx, key, fresh); err == nil {
			Expect(client.IgnoreNotFound(c.Delete(ctx, fresh))).To(Succeed())
		}
		Expect(waitGone(ctx, c, key, 30*time.Second)).To(BeTrue(),
			"ASR %s/%s not deleted even after stripping finalizer", namespace, name)
	}
}

// waitGone polls until the ASR no longer exists or the timeout elapses.
func waitGone(ctx context.Context, c client.Client, key types.NamespacedName, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := c.Get(ctx, key, &pmv1alpha1.ApplicationSpecificRole{})
		if apierrors.IsNotFound(err) {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

// stripFinalizer clears the cleanup finalizer so a stuck ASR can be deleted.
func stripFinalizer(ctx context.Context, c client.Client, key types.NamespacedName) {
	asr := &pmv1alpha1.ApplicationSpecificRole{}
	if err := c.Get(ctx, key, asr); err != nil {
		return
	}
	patch := client.MergeFrom(asr.DeepCopy())
	asr.Finalizers = removeString(asr.Finalizers, asrCleanupFinalizer)
	// Best-effort: a conflict or NotFound here is retried/re-checked by the caller.
	Expect(client.IgnoreNotFound(c.Patch(ctx, asr, patch))).To(Succeed())
}

func removeString(ss []string, target string) []string {
	out := ss[:0]
	for _, s := range ss {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}
