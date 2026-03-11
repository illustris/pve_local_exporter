package qmmonitor

import (
	"testing"
	"time"

	"pve_local_exporter/internal/cache"
)

// mockQMMonitor is a test double for QMMonitor.
type mockQMMonitor struct {
	responses map[string]string
	cache     *cache.TTLCache[string, string]
}

func newMockQMMonitor(responses map[string]string) *mockQMMonitor {
	return &mockQMMonitor{
		responses: responses,
		cache:     cache.NewTTLCache[string, string](time.Hour, 0),
	}
}

func (m *mockQMMonitor) RunCommand(vmid, cmd string) (string, error) {
	key := cacheKey(vmid, cmd)
	if v, ok := m.cache.Get(key); ok {
		return v, nil
	}
	resp := m.responses[vmid+":"+cmd]
	m.cache.Set(key, resp)
	return resp, nil
}

func (m *mockQMMonitor) InvalidateCache(vmid, cmd string) {
	m.cache.Invalidate(cacheKey(vmid, cmd))
}

func TestMockQMMonitor_CacheHit(t *testing.T) {
	mon := newMockQMMonitor(map[string]string{
		"100:info network": "some output",
	})
	r1, _ := mon.RunCommand("100", "info network")
	r2, _ := mon.RunCommand("100", "info network")
	if r1 != r2 {
		t.Errorf("cache miss: %q != %q", r1, r2)
	}
}

func TestMockQMMonitor_Invalidate(t *testing.T) {
	mon := newMockQMMonitor(map[string]string{
		"100:info network": "some output",
	})
	mon.RunCommand("100", "info network")
	mon.InvalidateCache("100", "info network")
	// After invalidation, it fetches again (same mock response)
	r, _ := mon.RunCommand("100", "info network")
	if r != "some output" {
		t.Errorf("unexpected: %q", r)
	}
}
