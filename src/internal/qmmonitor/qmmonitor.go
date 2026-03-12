package qmmonitor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"pve_local_exporter/internal/cache"
	"pve_local_exporter/internal/logging"
)

var errTimeout = errors.New("timeout waiting for qm monitor")

// QMMonitor runs commands against qm monitor and caches results.
type QMMonitor interface {
	RunCommand(vmid, cmd string) (string, error)
	InvalidateCache(vmid, cmd string)
}

// RealQMMonitor spawns `qm monitor` on a PTY via creack/pty.
type RealQMMonitor struct {
	timeout    time.Duration
	deferClose bool
	cache      *cache.TTLCache[string, string]

	mu            sync.Mutex
	deferredProcs []deferredProc
}

type deferredProc struct {
	cmd       *exec.Cmd
	timestamp time.Time
}

func NewRealQMMonitor(timeout, maxTTL, randRange time.Duration, deferClose bool) *RealQMMonitor {
	return &RealQMMonitor{
		timeout:    timeout,
		deferClose: deferClose,
		cache:      cache.NewTTLCache[string, string](maxTTL, randRange),
	}
}

func cacheKey(vmid, cmd string) string {
	return vmid + "\x00" + cmd
}

func (m *RealQMMonitor) InvalidateCache(vmid, cmd string) {
	m.cache.Invalidate(cacheKey(vmid, cmd))
}

func (m *RealQMMonitor) RunCommand(vmid, cmd string) (string, error) {
	key := cacheKey(vmid, cmd)
	if v, ok := m.cache.Get(key); ok {
		slog.Debug("qm cache hit", "vmid", vmid, "cmd", cmd)
		return v, nil
	}

	result, err := m.execQMMonitor(vmid, cmd)
	if err != nil {
		return "", err
	}
	m.cache.Set(key, result)
	m.cleanupDeferred()
	return result, nil
}

func (m *RealQMMonitor) execQMMonitor(vmid, cmd string) (string, error) {
	slog.Debug("qm monitor exec", "vmid", vmid, "cmd", cmd)
	start := time.Now()

	logging.Trace("qm pty spawn start", "vmid", vmid)
	qmCmd := exec.Command("qm", "monitor", vmid)
	qmCmd.Env = append(os.Environ(), "TERM=dumb")

	ptmx, err := pty.Start(qmCmd)
	if err != nil {
		return "", fmt.Errorf("start qm monitor: %w", err)
	}
	logging.Trace("qm pty spawn success", "vmid", vmid, "pid", qmCmd.Process.Pid)

	reader := bufio.NewReader(ptmx)

	// Wait for initial "qm>" prompt
	deadline := time.Now().Add(m.timeout)
	_, err = readUntilMarker(reader, "qm>", deadline)
	if err != nil {
		slog.Debug("qm monitor initial prompt failed", "vmid", vmid, "err", err)
		m.killOrDefer(qmCmd, ptmx)
		return "", fmt.Errorf("initial prompt: %w", err)
	}
	logging.Trace("qm initial prompt received", "vmid", vmid)

	// Send command
	logging.Trace("qm send command", "vmid", vmid, "cmd", cmd)
	fmt.Fprintf(ptmx, "%s\n", cmd)

	// Read response until next "qm>" prompt
	deadline = time.Now().Add(m.timeout)
	raw, err := readUntilMarker(reader, "qm>", deadline)
	if err != nil {
		slog.Debug("qm monitor response failed", "vmid", vmid, "cmd", cmd, "err", err)
		m.killOrDefer(qmCmd, ptmx)
		return "", fmt.Errorf("read response: %w", err)
	}
	logging.Trace("qm raw response", "vmid", vmid, "raw_len", len(raw))

	response := parseQMResponse(raw)

	// Close cleanly: closing ptmx sends SIGHUP to child
	ptmx.Close()
	if err := qmCmd.Wait(); err != nil {
		slog.Debug("qm monitor wait error", "vmid", vmid, "err", err)
	}

	slog.Debug("qm monitor done", "vmid", vmid, "cmd", cmd,
		"duration", time.Since(start), "responseLen", len(response))

	return response, nil
}

func (m *RealQMMonitor) killOrDefer(cmd *exec.Cmd, closer io.Closer) {
	closer.Close()
	if m.deferClose {
		m.mu.Lock()
		m.deferredProcs = append(m.deferredProcs, deferredProc{cmd: cmd, timestamp: time.Now()})
		m.mu.Unlock()
		slog.Warn("deferred closing qm monitor process", "pid", cmd.Process.Pid)
	} else {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

func (m *RealQMMonitor) cleanupDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()

	var still []deferredProc
	for _, dp := range m.deferredProcs {
		if time.Since(dp.timestamp) > 10*time.Second {
			if err := dp.cmd.Process.Kill(); err != nil {
				still = append(still, dp)
			} else {
				dp.cmd.Wait()
			}
		} else {
			still = append(still, dp)
		}
	}
	m.deferredProcs = still
}

// readUntilMarker reads from r byte-by-byte until the buffer ends with marker
// or the deadline expires. Returns everything read before the marker.
// Uses a goroutine for reads so the deadline is enforced even when ReadByte blocks.
func readUntilMarker(r *bufio.Reader, marker string, deadline time.Time) (string, error) {
	type result struct {
		data string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		var buf []byte
		markerBytes := []byte(marker)
		for {
			b, err := r.ReadByte()
			if err != nil {
				ch <- result{"", err}
				return
			}
			buf = append(buf, b)
			if len(buf) >= len(markerBytes) &&
				string(buf[len(buf)-len(markerBytes):]) == marker {
				// Return everything before the marker
				ch <- result{string(buf[:len(buf)-len(markerBytes)]), nil}
				return
			}
		}
	}()

	remaining := time.Until(deadline)
	if remaining <= 0 {
		remaining = time.Millisecond
	}
	select {
	case res := <-ch:
		return res.data, res.err
	case <-time.After(remaining):
		return "", errTimeout
	}
}

// parseQMResponse takes the raw output before the "qm>" marker from a command
// response, skips the command echo (first line), and trims \r characters.
func parseQMResponse(raw string) string {
	lines := strings.Split(raw, "\n")
	// Skip the command echo (first line)
	if len(lines) > 0 {
		lines = lines[1:]
	}
	var out []string
	for _, line := range lines {
		cleaned := strings.TrimRight(line, "\r")
		out = append(out, cleaned)
	}
	// Trim trailing empty lines
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
