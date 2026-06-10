//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

const (
	// defaultAssertTimeout is the default timeout for assertion helpers that poll.
	defaultAssertTimeout = 30 * time.Second

	// assertPollInterval is the poll interval for assertion helpers.
	assertPollInterval = 2 * time.Second
)

// pollUntil polls condFn at the given interval until it returns true, ctx is
// cancelled, or timeout expires. Returns an error if the condition was never met.
func pollUntil(t *testing.T, ctx context.Context, timeout, interval time.Duration, condFn func() (bool, error)) error {
	t.Helper()

	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastErr error
	for {
		select {
		case <-deadline.Done():
			if lastErr != nil {
				return fmt.Errorf("timed out after %s: last error: %w", timeout, lastErr)
			}
			return fmt.Errorf("timed out after %s waiting for condition", timeout)
		case <-ticker.C:
			ok, err := condFn()
			if err != nil {
				lastErr = err
				continue
			}
			if ok {
				return nil
			}
		}
	}
}

// assertClusterRoleExists verifies that a ClusterRole with the given name
// exists and carries the pm-controller managed-by label.
func assertClusterRoleExists(t *testing.T, ctx context.Context, c client.Client, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var cr rbacv1.ClusterRole
		if err := c.Get(ctx, types.NamespacedName{Name: name}, &cr); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		// Verify managed-by label
		if cr.Labels["app.kubernetes.io/managed-by"] != "pm-controller" {
			return false, fmt.Errorf("ClusterRole %q exists but missing managed-by label (labels: %v)", name, cr.Labels)
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("assertClusterRoleExists(%q): %v", name, err)
	}
}

// assertClusterRoleAbsent polls until the ClusterRole with the given name
// is no longer found (NotFound).
func assertClusterRoleAbsent(t *testing.T, ctx context.Context, c client.Client, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var cr rbacv1.ClusterRole
		if err := c.Get(ctx, types.NamespacedName{Name: name}, &cr); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("assertClusterRoleAbsent(%q): %v", name, err)
	}
}

// assertClusterRoleBindingExists verifies that a ClusterRoleBinding with the
// given name exists and carries the pm-controller managed-by label.
// If subjectGroup is non-empty, it also verifies a Subject with that Group name.
func assertClusterRoleBindingExists(t *testing.T, ctx context.Context, c client.Client, name, subjectGroup string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var crb rbacv1.ClusterRoleBinding
		if err := c.Get(ctx, types.NamespacedName{Name: name}, &crb); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		// Verify managed-by label
		if crb.Labels["app.kubernetes.io/managed-by"] != "pm-controller" {
			return false, fmt.Errorf("ClusterRoleBinding %q missing managed-by label", name)
		}
		// If subjectGroup is empty, just check existence + label (don't verify subject)
		if subjectGroup == "" {
			return true, nil
		}
		// Verify subject group
		for _, subj := range crb.Subjects {
			if subj.Kind == "Group" && subj.Name == subjectGroup {
				return true, nil
			}
		}
		return false, fmt.Errorf("ClusterRoleBinding %q does not have subject group %q (subjects: %+v)", name, subjectGroup, crb.Subjects)
	})
	if err != nil {
		t.Fatalf("assertClusterRoleBindingExists(%q, group=%q): %v", name, subjectGroup, err)
	}
}

// assertRoleBindingExists verifies that a RoleBinding with the given name
// exists in the specified namespace and carries the pm-controller label.
// If subjectGroup is non-empty, it also verifies a Subject with that Group name.
func assertRoleBindingExists(t *testing.T, ctx context.Context, c client.Client, namespace, name, subjectGroup string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var rb rbacv1.RoleBinding
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, &rb); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		// Verify managed-by label
		if rb.Labels["app.kubernetes.io/managed-by"] != "pm-controller" {
			return false, fmt.Errorf("RoleBinding %s/%s missing managed-by label", namespace, name)
		}
		// If subjectGroup is empty, just check existence + label
		if subjectGroup == "" {
			return true, nil
		}
		// Verify subject group
		for _, subj := range rb.Subjects {
			if subj.Kind == "Group" && subj.Name == subjectGroup {
				return true, nil
			}
		}
		return false, fmt.Errorf("RoleBinding %s/%s does not have subject group %q", namespace, name, subjectGroup)
	})
	if err != nil {
		t.Fatalf("assertRoleBindingExists(%s/%s, group=%q): %v", namespace, name, subjectGroup, err)
	}
}

// assertRoleBindingAbsent polls until the RoleBinding with the given name
// is gone from the specified namespace.
func assertRoleBindingAbsent(t *testing.T, ctx context.Context, c client.Client, namespace, name string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var rb rbacv1.RoleBinding
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := c.Get(ctx, key, &rb); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("assertRoleBindingAbsent(%s/%s): %v", namespace, name, err)
	}
}

// assertVerbsInClusterRole checks that the first rule of the named ClusterRole
// contains exactly the expected verbs (order-independent comparison).
func assertVerbsInClusterRole(t *testing.T, ctx context.Context, c client.Client, name string, expectedVerbs []string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var cr rbacv1.ClusterRole
		if err := c.Get(ctx, types.NamespacedName{Name: name}, &cr); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		if len(cr.Rules) == 0 {
			return false, fmt.Errorf("ClusterRole %q has no rules", name)
		}

		got := make([]string, len(cr.Rules[0].Verbs))
		copy(got, cr.Rules[0].Verbs)
		sort.Strings(got)

		want := make([]string, len(expectedVerbs))
		copy(want, expectedVerbs)
		sort.Strings(want)

		if len(got) != len(want) {
			return false, fmt.Errorf("verb count mismatch: got %v, want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				return false, fmt.Errorf("verb mismatch at [%d]: got %q, want %q (full: got=%v, want=%v)", i, got[i], want[i], got, want)
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("assertVerbsInClusterRole(%q, %v): %v", name, expectedVerbs, err)
	}
}

// assertRBACEnforced performs a SubjectAccessReview to verify that RBAC
// enforcement matches the expected allowed state for the given group,
// verb, resource, and optional namespace (empty for cluster-scope).
func assertRBACEnforced(t *testing.T, ctx context.Context, k8sClient kubernetes.Interface, group, verb, resource, namespace string, allowed bool) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		sar := &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				Groups: []string{group},
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Verb:      verb,
					Resource:  resource,
					Namespace: namespace,
				},
			},
		}
		result, err := k8sClient.AuthorizationV1().SubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
		if err != nil {
			return false, fmt.Errorf("SubjectAccessReview failed: %w", err)
		}
		if result.Status.Allowed == allowed {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("assertRBACEnforced(group=%q, verb=%q, resource=%q, ns=%q, allowed=%v): %v",
			group, verb, resource, namespace, allowed, err)
	}
}

// assertASRStatus verifies the TargetStatus entry for a specific cluster
// on the named ASR matches the expectedStatus (e.g., "Success", "Error", "Unreachable").
func assertASRStatus(t *testing.T, ctx context.Context, mgmtClient client.Client, namespace, asrName, clusterName, expectedStatus string) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: namespace, Name: asrName}
		if err := mgmtClient.Get(ctx, key, &asr); err != nil {
			return false, fmt.Errorf("failed to get ASR %s/%s: %w", namespace, asrName, err)
		}

		k8sProvider, ok := asr.Status.Providers["kubernetes"]
		if !ok {
			return false, fmt.Errorf("ASR %s has no kubernetes provider in status", asrName)
		}

		for _, entry := range k8sProvider.Targets {
			if entry.TargetID == clusterName {
				if entry.Status == expectedStatus {
					return true, nil
				}
				return false, fmt.Errorf("ASR %s cluster %q status is %q, want %q (message: %s)",
					asrName, clusterName, entry.Status, expectedStatus, entry.Message)
			}
		}
		return false, fmt.Errorf("ASR %s has no target entry for cluster %q (entries: %d)",
			asrName, clusterName, len(k8sProvider.Targets))
	})
	if err != nil {
		t.Fatalf("assertASRStatus(%s/%s, cluster=%q, expected=%q): %v", namespace, asrName, clusterName, expectedStatus, err)
	}
}

// assertASRReadyCondition checks the "Ready" condition on the ASR matches
// the expected readiness state.
func assertASRReadyCondition(t *testing.T, ctx context.Context, mgmtClient client.Client, namespace, asrName string, expectedReady bool) {
	t.Helper()

	err := pollUntil(t, ctx, defaultAssertTimeout, assertPollInterval, func() (bool, error) {
		var asr pmv1.ApplicationSpecificRole
		key := types.NamespacedName{Namespace: namespace, Name: asrName}
		if err := mgmtClient.Get(ctx, key, &asr); err != nil {
			return false, fmt.Errorf("failed to get ASR %s/%s: %w", namespace, asrName, err)
		}

		for _, cond := range asr.Status.Conditions {
			if cond.Type == "Ready" {
				isTrue := cond.Status == metav1.ConditionTrue
				if isTrue == expectedReady {
					return true, nil
				}
				return false, fmt.Errorf("ASR %s Ready condition is %q (reason=%s), want ready=%v",
					asrName, cond.Status, cond.Reason, expectedReady)
			}
		}
		return false, fmt.Errorf("ASR %s has no Ready condition (conditions: %d)", asrName, len(asr.Status.Conditions))
	})
	if err != nil {
		t.Fatalf("assertASRReadyCondition(%s/%s, expectedReady=%v): %v", namespace, asrName, expectedReady, err)
	}
}
