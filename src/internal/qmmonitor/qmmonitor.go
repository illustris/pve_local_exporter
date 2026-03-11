package qmmonitor

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"pve_local_exporter/internal/cache"
)

// QMMonitor runs commands against qm monitor and caches results.
type QMMonitor interface {
	RunCommand(vmid, cmd string) (string, error)
	InvalidateCache(vmid, cmd string)
}

// RealQMMonitor spawns `qm monitor` via os/exec with pipe-based I/O.
type RealQMMonitor struct {
	timeout    time.Duration
	deferClose bool
	cache      *cache.TTLCache[string, string]

	mu             sync.Mutex
	deferredProcs  []deferredProc
}

type deferredProc struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
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
	qmCmd := exec.Command("qm", "monitor", vmid)

	stdin, err := qmCmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := qmCmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}

	if err := qmCmd.Start(); err != nil {
		return "", fmt.Errorf("start qm monitor: %w", err)
	}

	reader := bufio.NewReader(stdout)

	// Wait for initial "qm>" prompt
	if err := readUntilPrompt(reader, m.timeout); err != nil {
		m.deferCloseProcess(qmCmd, stdin)
		return "", fmt.Errorf("initial prompt: %w", err)
	}

	// Send command
	fmt.Fprintf(stdin, "%s\n", cmd)

	// Read response until next "qm>" prompt
	response, err := readResponseUntilPrompt(reader, m.timeout)
	if err != nil {
		m.deferCloseProcess(qmCmd, stdin)
		return "", fmt.Errorf("read response: %w", err)
	}

	// Close cleanly
	stdin.Close()
	if err := qmCmd.Wait(); err != nil {
		slog.Debug("qm monitor wait error", "vmid", vmid, "err", err)
	}

	return response, nil
}

func (m *RealQMMonitor) deferCloseProcess(cmd *exec.Cmd, stdin io.WriteCloser) {
	stdin.Close()
	if m.deferClose {
		m.mu.Lock()
		m.deferredProcs = append(m.deferredProcs, deferredProc{cmd: cmd, stdin: stdin, timestamp: time.Now()})
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

func readUntilPrompt(r *bufio.Reader, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for qm> prompt")
		}
		line, err := r.ReadString('\n')
		if err != nil {
			// Check if we got the prompt without newline
			if strings.Contains(line, "qm>") {
				return nil
			}
			return err
		}
		if strings.Contains(line, "qm>") {
			return nil
		}
	}
}

func readResponseUntilPrompt(r *bufio.Reader, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lines []string
	firstLine := true
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for qm> prompt")
		}
		line, err := r.ReadString('\n')
		if err != nil {
			if strings.Contains(line, "qm>") {
				break
			}
			return "", err
		}
		if strings.Contains(line, "qm>") {
			break
		}
		// Skip the echo of the command (first line)
		if firstLine {
			firstLine = false
			continue
		}
		lines = append(lines, strings.TrimRight(line, "\r\n"))
	}
	return strings.Join(lines, "\n"), nil
}
