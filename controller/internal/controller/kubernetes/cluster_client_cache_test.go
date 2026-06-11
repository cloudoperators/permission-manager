// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestClusterClientCache_GetMiss(t *testing.T) {
	cache := NewClusterClientCache()

	_, ok := cache.Get("nonexistent")
	if ok {
		t.Error("expected Get to return false for non-existent entry")
	}
}

func TestClusterClientCache_PutAndGetHit(t *testing.T) {
	cache := NewClusterClientCache()
	fakeClient := fake.NewClientBuilder().Build()

	cache.Put("cluster-a", fakeClient, "rv-1")

	got, ok := cache.Get("cluster-a")
	if !ok {
		t.Fatal("expected Get to return true for cached entry")
	}
	if got.Client != fakeClient {
		t.Error("expected cached client to be the same instance")
	}
	if got.ResourceVersion != "rv-1" {
		t.Errorf("expected resourceVersion %q, got %q", "rv-1", got.ResourceVersion)
	}
}

func TestClusterClientCache_PutReplacesOnNewResourceVersion(t *testing.T) {
	cache := NewClusterClientCache()
	client1 := fake.NewClientBuilder().Build()
	client2 := fake.NewClientBuilder().Build()

	cache.Put("cluster-a", client1, "rv-1")
	cache.Put("cluster-a", client2, "rv-2")

	got, ok := cache.Get("cluster-a")
	if !ok {
		t.Fatal("expected Get to return true after replacement")
	}
	if got.Client != client2 {
		t.Error("expected cached client to be the replaced instance")
	}
	if got.ResourceVersion != "rv-2" {
		t.Errorf("expected resourceVersion %q, got %q", "rv-2", got.ResourceVersion)
	}
}

func TestClusterClientCache_Invalidate(t *testing.T) {
	cache := NewClusterClientCache()
	fakeClient := fake.NewClientBuilder().Build()

	cache.Put("cluster-a", fakeClient, "rv-1")
	cache.Invalidate("cluster-a")

	_, ok := cache.Get("cluster-a")
	if ok {
		t.Error("expected Get to return false after Invalidate")
	}
}

func TestClusterClientCache_InvalidateNonExistent(t *testing.T) {
	cache := NewClusterClientCache()
	// Should not panic
	cache.Invalidate("nonexistent")
}

func TestClusterClientCache_IndependentEntries(t *testing.T) {
	cache := NewClusterClientCache()
	clientA := fake.NewClientBuilder().Build()
	clientB := fake.NewClientBuilder().Build()

	cache.Put("cluster-a", clientA, "rv-1")
	cache.Put("cluster-b", clientB, "rv-2")

	cache.Invalidate("cluster-a")

	_, ok := cache.Get("cluster-a")
	if ok {
		t.Error("expected cluster-a to be invalidated")
	}

	got, ok := cache.Get("cluster-b")
	if !ok {
		t.Fatal("expected cluster-b to still be cached")
	}
	if got.Client != clientB {
		t.Error("expected cluster-b client to be unchanged")
	}
}
