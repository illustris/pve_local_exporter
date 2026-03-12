package qmmonitor

import (
	"bufio"
	"io"
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

func TestReadUntilMarker_Success(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	go func() {
		pw.Write([]byte("banner\nqm> "))
		pw.Close()
	}()

	reader := bufio.NewReader(pr)
	deadline := time.Now().Add(5 * time.Second)
	got, err := readUntilMarker(reader, "qm>", deadline)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "banner\n" {
		t.Errorf("got %q, want %q", got, "banner\n")
	}
}

func TestReadUntilMarker_Timeout(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	// Write nothing -- should timeout
	reader := bufio.NewReader(pr)
	deadline := time.Now().Add(50 * time.Millisecond)
	start := time.Now()
	_, err := readUntilMarker(reader, "qm>", deadline)
	elapsed := time.Since(start)

	if err != errTimeout {
		t.Fatalf("expected errTimeout, got: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestReadUntilMarker_EOF(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	go func() {
		pw.Write([]byte("partial data"))
		pw.Close()
	}()

	reader := bufio.NewReader(pr)
	deadline := time.Now().Add(5 * time.Second)
	_, err := readUntilMarker(reader, "qm>", deadline)
	if err == nil {
		t.Fatal("expected error on EOF before marker")
	}
	if err == errTimeout {
		t.Fatal("expected EOF error, not timeout")
	}
}

func TestParseQMResponse(t *testing.T) {
	// Simulate: command echo + response lines with \r + trailing newline
	raw := "info network\r\n" +
		"net0: index=0,type=tap,ifname=tap100i0\r\n" +
		"net1: index=1,type=tap,ifname=tap100i1\r\n"

	got := parseQMResponse(raw)
	want := "net0: index=0,type=tap,ifname=tap100i0\n" +
		"net1: index=1,type=tap,ifname=tap100i1"
	if got != want {
		t.Errorf("parseQMResponse:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestParseQMResponse_Empty(t *testing.T) {
	// Command echo only, no response data
	got := parseQMResponse("info version\r\n")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
