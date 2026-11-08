package qmmonitor

import "testing"

// Captured from PVE 9.2 / pve-qemu-kvm 11.0 with a NixOS guest.
const guestStatsFull = `{
    "stats": {
        "stat-htlb-pgalloc": 0,
        "stat-swap-out": 0,
        "stat-alloc-stalls": 0,
        "stat-available-memory": 3628638208,
        "stat-direct-reclaims": 0,
        "stat-direct-scans": 0,
        "stat-htlb-pgfail": 0,
        "stat-free-memory": 3337859072,
        "stat-minor-faults": 295953,
        "stat-major-faults": 419,
        "stat-total-memory": 4102778880,
        "stat-oom-kills": 0,
        "stat-async-reclaims": 0,
        "stat-swap-in": 0,
        "stat-async-scans": 0,
        "stat-disk-caches": 500019200
    },
    "last-update": 1794114458
}`

func TestParseGuestStats_Full(t *testing.T) {
	g, err := ParseGuestStats(guestStatsFull)
	if err != nil {
		t.Fatal(err)
	}
	if g.LastUpdate != 1794114458 {
		t.Errorf("LastUpdate = %d", g.LastUpdate)
	}
	for name, want := range map[string]int64{
		"stat-total-memory":     4102778880,
		"stat-free-memory":      3337859072,
		"stat-available-memory": 3628638208,
		"stat-disk-caches":      500019200,
		"stat-major-faults":     419,
		"stat-swap-in":          0,
	} {
		if v, ok := g.Get(name); !ok || v != want {
			t.Errorf("%s = %d, %v; want %d", name, v, ok, want)
		}
	}
}

func TestParseGuestStats_Unsupported(t *testing.T) {
	raw := `{"stats": {"stat-total-memory": 1000, "stat-available-memory": -1}, "last-update": 5}`
	g, err := ParseGuestStats(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Get("stat-available-memory"); ok {
		t.Error("expected -1 stat to be unsupported")
	}
	if _, ok := g.Get("stat-disk-caches"); ok {
		t.Error("expected missing stat to be unsupported")
	}
	if v, ok := g.Get("stat-total-memory"); !ok || v != 1000 {
		t.Errorf("stat-total-memory = %d, %v", v, ok)
	}
}

func TestParseGuestStats_NoUpdate(t *testing.T) {
	raw := `{"stats": {"stat-total-memory": -1, "stat-free-memory": -1}, "last-update": 0}`
	g, err := ParseGuestStats(raw)
	if err != nil {
		t.Fatal(err)
	}
	if g.LastUpdate != 0 {
		t.Errorf("LastUpdate = %d", g.LastUpdate)
	}
}

func TestParseGuestStats_Error(t *testing.T) {
	for _, raw := range []string{
		"Error: Device 'balloon0' not found",
		"",
		"{not json",
	} {
		if _, err := ParseGuestStats(raw); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
}
