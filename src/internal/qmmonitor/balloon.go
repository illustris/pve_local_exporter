package qmmonitor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GuestStatsCommand reads the virtio-balloon guest memory statistics. The
// stats are reported by the in-guest balloon driver and refreshed every
// guest-stats-polling-interval seconds (PVE sets 2s at VM start).
const GuestStatsCommand = "qom-get /machine/peripheral/balloon0 guest-stats"

// GuestStats holds parsed virtio-balloon guest statistics.
// Stats keys are the QEMU names (e.g. "stat-available-memory").
type GuestStats struct {
	LastUpdate int64
	Stats      map[string]int64
}

// Get returns the named stat. ok is false when the stat is missing or the
// guest does not support it (QEMU reports -1).
func (g GuestStats) Get(name string) (int64, bool) {
	v, ok := g.Stats[name]
	if !ok || v < 0 {
		return 0, false
	}
	return v, true
}

// ParseGuestStats parses the JSON output of GuestStatsCommand.
// Format: {"stats": {"stat-total-memory": 4102778880, ...}, "last-update": 1794114458}
// Non-JSON output (e.g. "Error: Device 'balloon0' not found" when ballooning
// is disabled) is returned as an error.
func ParseGuestStats(raw string) (GuestStats, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") {
		return GuestStats{}, fmt.Errorf("unexpected guest-stats output: %q", firstLine(raw))
	}
	var parsed struct {
		Stats      map[string]int64 `json:"stats"`
		LastUpdate int64            `json:"last-update"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return GuestStats{}, fmt.Errorf("parse guest-stats: %w", err)
	}
	return GuestStats{LastUpdate: parsed.LastUpdate, Stats: parsed.Stats}, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
