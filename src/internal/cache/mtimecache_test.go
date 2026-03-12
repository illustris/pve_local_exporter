package cache

import (
	"sync"
	"testing"
	"time"
)

func TestMtimeCache_HitAndMiss(t *testing.T) {
	mtime := time.Now()
	stat := func(path string) (time.Time, error) { return mtime, nil }

	c := NewMtimeCache[string]("/test", stat)

	// Miss before any Set
	_, ok := c.Get()
	if ok {
		t.Fatal("expected miss before Set")
	}

	c.Set("hello")
	v, ok := c.Get()
	if !ok || v != "hello" {
		t.Fatalf("expected hit with 'hello', got %q ok=%v", v, ok)
	}

	// Simulate file change
	mtime = mtime.Add(time.Second)
	_, ok = c.Get()
	if ok {
		t.Fatal("expected miss after mtime change")
	}

	// Set again with new mtime
	c.Set("world")
	v, ok = c.Get()
	if !ok || v != "world" {
		t.Fatalf("expected hit with 'world', got %q ok=%v", v, ok)
	}
}

func TestMtimeCache_Concurrent(t *testing.T) {
	mtime := time.Now()
	stat := func(path string) (time.Time, error) { return mtime, nil }
	c := NewMtimeCache[int]("/test", stat)

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.Set(n)
			c.Get()
		}(i)
	}
	wg.Wait()
}
