package kubernetes

import (
	"testing"
	"time"
)

func TestNamespaceCache_GetMiss(t *testing.T) {
	cache := NewNamespaceCache(time.Now)

	_, ok := cache.Get("nonexistent")
	if ok {
		t.Error("expected Get to return false for non-existent entry")
	}
}

func TestNamespaceCache_PutAndGetHit(t *testing.T) {
	now := time.Now()
	cache := NewNamespaceCache(func() time.Time { return now })

	cache.Put("cluster-a", []string{"default", "kube-system"})

	names, ok := cache.Get("cluster-a")
	if !ok {
		t.Fatal("expected Get to return true for cached entry")
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 namespaces, got %d", len(names))
	}
	if names[0] != "default" || names[1] != "kube-system" {
		t.Errorf("unexpected namespaces: %v", names)
	}
}

func TestNamespaceCache_TTLExpiry(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	cache := NewNamespaceCache(clock)

	cache.Put("cluster-a", []string{"default"})

	// Within TTL: should hit
	now = now.Add(30 * time.Second)
	cache.clock = func() time.Time { return now }
	_, ok := cache.Get("cluster-a")
	if !ok {
		t.Error("expected Get to return true within TTL")
	}

	// After TTL: should miss
	now = now.Add(31 * time.Second) // total 61s
	cache.clock = func() time.Time { return now }
	_, ok = cache.Get("cluster-a")
	if ok {
		t.Error("expected Get to return false after TTL expiry")
	}
}

func TestNamespaceCache_Invalidate(t *testing.T) {
	now := time.Now()
	cache := NewNamespaceCache(func() time.Time { return now })

	cache.Put("cluster-a", []string{"default"})
	cache.Invalidate("cluster-a")

	_, ok := cache.Get("cluster-a")
	if ok {
		t.Error("expected Get to return false after Invalidate")
	}
}

func TestNamespaceCache_InvalidateNonExistent(t *testing.T) {
	cache := NewNamespaceCache(time.Now)
	// Should not panic
	cache.Invalidate("nonexistent")
}

func TestNamespaceCache_IndependentEntries(t *testing.T) {
	now := time.Now()
	cache := NewNamespaceCache(func() time.Time { return now })

	cache.Put("cluster-a", []string{"ns-a"})
	cache.Put("cluster-b", []string{"ns-b"})

	cache.Invalidate("cluster-a")

	_, ok := cache.Get("cluster-a")
	if ok {
		t.Error("expected cluster-a to be invalidated")
	}

	names, ok := cache.Get("cluster-b")
	if !ok {
		t.Fatal("expected cluster-b to still be cached")
	}
	if len(names) != 1 || names[0] != "ns-b" {
		t.Errorf("unexpected namespaces for cluster-b: %v", names)
	}
}

func TestNamespaceCache_PutUpdatesTimestamp(t *testing.T) {
	now := time.Now()
	cache := NewNamespaceCache(func() time.Time { return now })

	cache.Put("cluster-a", []string{"default"})

	// Advance time to nearly expired
	now = now.Add(59 * time.Second)
	cache.clock = func() time.Time { return now }

	// Re-put refreshes the timestamp
	cache.Put("cluster-a", []string{"default", "new-ns"})

	// Advance past original TTL but within new TTL
	now = now.Add(30 * time.Second) // total 89s from original, 30s from re-put
	cache.clock = func() time.Time { return now }

	names, ok := cache.Get("cluster-a")
	if !ok {
		t.Error("expected Get to return true (re-put refreshed timestamp)")
	}
	if len(names) != 2 {
		t.Errorf("expected 2 namespaces after re-put, got %d", len(names))
	}
}
