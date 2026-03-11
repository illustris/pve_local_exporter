package cache

import (
	"testing"
	"time"
)

func TestTTLCache_SetGet(t *testing.T) {
	c := NewTTLCache[string, int](time.Hour, 0)
	c.Set("a", 42)
	v, ok := c.Get("a")
	if !ok || v != 42 {
		t.Fatalf("expected 42, got %d (ok=%v)", v, ok)
	}
}

func TestTTLCache_Miss(t *testing.T) {
	c := NewTTLCache[string, int](time.Hour, 0)
	_, ok := c.Get("missing")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestTTLCache_Expiry(t *testing.T) {
	c := NewTTLCache[string, int](time.Millisecond, 0)
	c.Set("a", 1)
	time.Sleep(5 * time.Millisecond)
	_, ok := c.Get("a")
	if ok {
		t.Fatal("expected expired entry")
	}
}

func TestTTLCache_Invalidate(t *testing.T) {
	c := NewTTLCache[string, int](time.Hour, 0)
	c.Set("a", 1)
	c.Invalidate("a")
	_, ok := c.Get("a")
	if ok {
		t.Fatal("expected invalidated entry")
	}
}

func TestTTLCache_JitterRange(t *testing.T) {
	c := NewTTLCache[string, int](time.Second, 500*time.Millisecond)
	// jitteredTTL should be in [500ms, 1500ms]
	for range 100 {
		d := c.jitteredTTL()
		if d < 500*time.Millisecond || d > 1500*time.Millisecond {
			t.Fatalf("jitter out of range: %v", d)
		}
	}
}
