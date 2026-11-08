package collector

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"pve_local_exporter/internal/config"
	"pve_local_exporter/internal/procfs"
	"pve_local_exporter/internal/qmmonitor"
	"pve_local_exporter/internal/storage"
	"pve_local_exporter/internal/sysfs"
)

// Mock implementations

type mockProcReader struct {
	procs    []procfs.QEMUProcess
	procsErr error
	cpuTimes map[int]procfs.CPUTimes
	ioCount  map[int]procfs.IOCounters
	status   map[int]procfs.StatusInfo
	memPct   map[int]float64
}

func (m *mockProcReader) DiscoverQEMUProcesses() ([]procfs.QEMUProcess, error) {
	return m.procs, m.procsErr
}
func (m *mockProcReader) GetCPUTimes(pid int) (procfs.CPUTimes, error) {
	return m.cpuTimes[pid], nil
}
func (m *mockProcReader) GetIOCounters(pid int) (procfs.IOCounters, error) {
	return m.ioCount[pid], nil
}
func (m *mockProcReader) GetStatus(pid int) (procfs.StatusInfo, error) {
	return m.status[pid], nil
}
func (m *mockProcReader) GetMemoryPercent(pid int, rssBytes int64) (float64, error) {
	return m.memPct[pid], nil
}
func (m *mockProcReader) VMConfigExists(vmid string) bool { return true }

type mockSysReader struct {
	ifStats   map[string]map[string]int64
	blockSize map[string]int64
	cgroupMem map[string]sysfs.CgroupMemory
}

func (m *mockSysReader) ReadInterfaceStats(ifname string) (map[string]int64, error) {
	return m.ifStats[ifname], nil
}
func (m *mockSysReader) GetBlockDeviceSize(devPath string) (int64, error) {
	return m.blockSize[devPath], nil
}
func (m *mockSysReader) ReadCgroupMemory(vmid string) (sysfs.CgroupMemory, error) {
	mem, ok := m.cgroupMem[vmid]
	if !ok {
		return sysfs.CgroupMemory{}, fmt.Errorf("no cgroup for %s", vmid)
	}
	return mem, nil
}

type mockQMMonitor struct {
	responses map[string]string
}

func (m *mockQMMonitor) RunCommand(vmid, cmd string) (string, error) {
	return m.responses[vmid+":"+cmd], nil
}
func (m *mockQMMonitor) InvalidateCache(vmid, cmd string) {}

type mockStatFS struct {
	sizes map[string]storage.StorageSize
}

func (m *mockStatFS) Statfs(path string) (storage.StorageSize, error) {
	return m.sizes[path], nil
}

type mockCmdRunner struct {
	outputs map[string]string
}

func (m *mockCmdRunner) Run(name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	return m.outputs[key], nil
}

type mockFileReader struct {
	files map[string]string
}

func (m *mockFileReader) ReadFile(path string) (string, error) {
	return m.files[path], nil
}

// metricValue extracts the numeric value from a dto.Metric, whether it is a Gauge or Counter.
func metricValue(m *dto.Metric) float64 {
	if m.Gauge != nil {
		return m.Gauge.GetValue()
	}
	if m.Counter != nil {
		return m.Counter.GetValue()
	}
	return 0
}

// collectMetrics collects all metrics from a collector into a map keyed by metric name.
func collectMetrics(c prometheus.Collector) map[string][]*dto.Metric {
	ch := make(chan prometheus.Metric, 200)
	go func() {
		c.Collect(ch)
		close(ch)
	}()

	result := make(map[string][]*dto.Metric)
	for m := range ch {
		d := &dto.Metric{}
		m.Write(d)
		desc := m.Desc()
		// Extract fqName from desc string
		name := desc.String()
		// Format: Desc{fqName: "name", ...}
		if idx := strings.Index(name, "fqName: \""); idx >= 0 {
			name = name[idx+9:]
			if end := strings.Index(name, "\""); end >= 0 {
				name = name[:end]
			}
		}
		result[name] = append(result[name], d)
	}
	return result
}

func findMetricWithLabels(metrics []*dto.Metric, labels map[string]string) *dto.Metric {
	for _, m := range metrics {
		match := true
		for wantName, wantVal := range labels {
			found := false
			for _, l := range m.Label {
				if l.GetName() == wantName && l.GetValue() == wantVal {
					found = true
					break
				}
			}
			if !found {
				match = false
				break
			}
		}
		if match {
			return m
		}
	}
	return nil
}

func TestCollector_BasicVMMetrics(t *testing.T) {
	cfg := config.Config{
		CollectRunningVMs: true,
		CollectStorage:    false,
		MetricsPrefix:     "pve",
	}

	proc := &mockProcReader{
		procs: []procfs.QEMUProcess{
			{PID: 1234, VMID: "100", Name: "testvm", CPU: "host", Vcores: 4, MaxMem: 4194304},
		},
		cpuTimes: map[int]procfs.CPUTimes{
			1234: {User: 5.0, System: 2.0, IOWait: 0.5},
		},
		ioCount: map[int]procfs.IOCounters{
			1234: {ReadChars: 1000, WriteChars: 2000, ReadSyscalls: 10, WriteSyscalls: 20, ReadBytes: 500, WriteBytes: 1000},
		},
		status: map[int]procfs.StatusInfo{
			1234: {
				Threads:        50,
				VmRSS:          1048576,
				MemoryExtended: procfs.MemoryExtended{"vmrss": 1048576, "vmpeak": 2097152},
				CtxSwitches:    procfs.CtxSwitches{Voluntary: 100, Involuntary: 10},
			},
		},
		memPct: map[int]float64{1234: 25.5},
	}

	sys := &mockSysReader{
		ifStats:   map[string]map[string]int64{},
		blockSize: map[string]int64{},
	}

	qm := &mockQMMonitor{responses: map[string]string{
		"100:info network": "",
		"100:info block":   "",
	}}

	fr := &mockFileReader{files: map[string]string{
		"/etc/pve/user.cfg": "pool:prod:Production:100\n",
	}}

	c := NewWithDeps(cfg, proc, sys, qm, &mockStatFS{}, &mockCmdRunner{}, fr)
	metrics := collectMetrics(c)

	// Check CPU metrics (counter)
	cpuMetrics := metrics["pve_kvm_cpu_seconds_total"]
	if len(cpuMetrics) != 3 {
		t.Fatalf("expected 3 cpu metrics, got %d", len(cpuMetrics))
	}
	m := findMetricWithLabels(cpuMetrics, map[string]string{"mode": "user"})
	if m == nil || metricValue(m) != 5.0 {
		t.Errorf("cpu user = %v", m)
	}
	m = findMetricWithLabels(cpuMetrics, map[string]string{"mode": "system"})
	if m == nil || metricValue(m) != 2.0 {
		t.Errorf("cpu system = %v", m)
	}
	m = findMetricWithLabels(cpuMetrics, map[string]string{"mode": "iowait"})
	if m == nil || metricValue(m) != 0.5 {
		t.Errorf("cpu iowait = %v", m)
	}

	// Check vcores
	vcoreMetrics := metrics["pve_kvm_vcores"]
	if len(vcoreMetrics) != 1 || metricValue(vcoreMetrics[0]) != 4 {
		t.Errorf("vcores = %v", vcoreMetrics)
	}

	// Check threads
	threadMetrics := metrics["pve_kvm_threads"]
	if len(threadMetrics) != 1 || metricValue(threadMetrics[0]) != 50 {
		t.Errorf("threads = %v", threadMetrics)
	}

	// Check memory percent
	memPctMetrics := metrics["pve_kvm_memory_percent"]
	if len(memPctMetrics) != 1 || metricValue(memPctMetrics[0]) != 25.5 {
		t.Errorf("memory_percent = %v", memPctMetrics)
	}

	// Check maxmem (renamed with _bytes)
	maxmemMetrics := metrics["pve_kvm_maxmem_bytes"]
	if len(maxmemMetrics) != 1 || metricValue(maxmemMetrics[0]) != float64(4194304*1024) {
		t.Errorf("maxmem_bytes = %v", maxmemMetrics)
	}

	// Check IO (counters, renamed with _total)
	if m := metrics["pve_kvm_io_read_count_total"]; len(m) != 1 || metricValue(m[0]) != 10 {
		t.Errorf("io_read_count_total = %v", m)
	}
	if m := metrics["pve_kvm_io_write_bytes_total"]; len(m) != 1 || metricValue(m[0]) != 1000 {
		t.Errorf("io_write_bytes_total = %v", m)
	}

	// Check context switches (counter, renamed with _total)
	csMetrics := metrics["pve_kvm_ctx_switches_total"]
	if len(csMetrics) != 2 {
		t.Fatalf("expected 2 ctx_switches_total metrics, got %d", len(csMetrics))
	}

	// Check VM info metric
	infoMetrics := metrics["pve_kvm_info"]
	if len(infoMetrics) != 1 {
		t.Fatalf("expected 1 kvm info metric, got %d", len(infoMetrics))
	}
	m = findMetricWithLabels(infoMetrics, map[string]string{"id": "100", "name": "testvm", "pool": "prod"})
	if m == nil {
		t.Error("kvm info metric not found with expected labels")
	}

	// Check scrape duration exists
	if sd := metrics["pve_scrape_duration_seconds"]; len(sd) != 1 {
		t.Errorf("expected 1 scrape_duration_seconds, got %d", len(sd))
	}

	// Check build info exists
	if bi := metrics["pve_exporter_build_info"]; len(bi) != 1 {
		t.Errorf("expected 1 build_info, got %d", len(bi))
	}
}

func TestCollector_StorageMetrics(t *testing.T) {
	cfg := config.Config{
		CollectRunningVMs: false,
		CollectStorage:    true,
		MetricsPrefix:     "pve",
	}

	fr := &mockFileReader{files: map[string]string{
		"/etc/pve/storage.cfg": `dir: local
	path /var/lib/vz
	content iso,vztmpl,backup
`,
		"/etc/pve/user.cfg": "",
	}}

	statFS := &mockStatFS{sizes: map[string]storage.StorageSize{
		"/var/lib/vz": {Total: 1000000000, Free: 500000000},
	}}

	c := NewWithDeps(cfg, &mockProcReader{}, &mockSysReader{}, &mockQMMonitor{responses: map[string]string{}},
		statFS, &mockCmdRunner{}, fr)

	metrics := collectMetrics(c)

	// Check storage size (renamed with _bytes)
	sizeMetrics := metrics["pve_node_storage_size_bytes"]
	if len(sizeMetrics) != 1 || metricValue(sizeMetrics[0]) != 1e9 {
		t.Errorf("storage_size_bytes = %v", sizeMetrics)
	}

	// Check storage free (renamed with _bytes)
	freeMetrics := metrics["pve_node_storage_free_bytes"]
	if len(freeMetrics) != 1 || metricValue(freeMetrics[0]) != 5e8 {
		t.Errorf("storage_free_bytes = %v", freeMetrics)
	}

	// Check storage info
	infoMetrics := metrics["pve_node_storage_info"]
	if len(infoMetrics) != 1 {
		t.Fatalf("expected 1 storage info metric, got %d", len(infoMetrics))
	}
}

func TestCollector_NICMetrics(t *testing.T) {
	cfg := config.Config{
		CollectRunningVMs: true,
		CollectStorage:    false,
		MetricsPrefix:     "pve",
	}

	proc := &mockProcReader{
		procs: []procfs.QEMUProcess{
			{PID: 1, VMID: "100", Name: "vm", Vcores: 1, MaxMem: 1024},
		},
		cpuTimes: map[int]procfs.CPUTimes{1: {}},
		ioCount:  map[int]procfs.IOCounters{1: {}},
		status: map[int]procfs.StatusInfo{
			1: {Threads: 1, MemoryExtended: procfs.MemoryExtended{}},
		},
		memPct: map[int]float64{1: 0},
	}

	sys := &mockSysReader{
		ifStats: map[string]map[string]int64{
			"tap100i0": {"rx_bytes": 1000, "tx_bytes": 2000},
		},
	}

	qm := &mockQMMonitor{responses: map[string]string{
		"100:info network": "net0: index=0,type=tap,ifname=tap100i0,model=virtio-net-pci,macaddr=AA:BB:CC:DD:EE:FF",
		"100:info block":   "",
	}}

	fr := &mockFileReader{files: map[string]string{"/etc/pve/user.cfg": ""}}
	c := NewWithDeps(cfg, proc, sys, qm, &mockStatFS{}, &mockCmdRunner{}, fr)
	metrics := collectMetrics(c)

	// NIC info
	nicInfo := metrics["pve_kvm_nic_info"]
	if len(nicInfo) != 1 {
		t.Fatalf("expected 1 nic info, got %d", len(nicInfo))
	}

	// NIC stats (counter, renamed with _total)
	rxBytes := metrics["pve_kvm_nic_rx_bytes_total"]
	if len(rxBytes) != 1 || metricValue(rxBytes[0]) != 1000 {
		t.Errorf("rx_bytes_total = %v", rxBytes)
	}
	txBytes := metrics["pve_kvm_nic_tx_bytes_total"]
	if len(txBytes) != 1 || metricValue(txBytes[0]) != 2000 {
		t.Errorf("tx_bytes_total = %v", txBytes)
	}
}

// mockFileReaderErr returns an error for a specific path.
type mockFileReaderErr struct {
	files   map[string]string
	errPath string
}

func (m *mockFileReaderErr) ReadFile(path string) (string, error) {
	if path == m.errPath {
		return "", fmt.Errorf("read error: %s", path)
	}
	return m.files[path], nil
}

func TestCollector_PoolReadError(t *testing.T) {
	cfg := config.Config{
		CollectRunningVMs: true,
		CollectStorage:    false,
		MetricsPrefix:     "pve",
	}

	proc := &mockProcReader{
		procs: []procfs.QEMUProcess{
			{PID: 1, VMID: "100", Name: "vm", Vcores: 1, MaxMem: 1024},
		},
		cpuTimes: map[int]procfs.CPUTimes{1: {}},
		ioCount:  map[int]procfs.IOCounters{1: {}},
		status: map[int]procfs.StatusInfo{
			1: {Threads: 1, MemoryExtended: procfs.MemoryExtended{}},
		},
		memPct: map[int]float64{1: 0},
	}

	fr := &mockFileReaderErr{
		files:   map[string]string{},
		errPath: "/etc/pve/user.cfg",
	}

	c := NewWithDeps(cfg, proc, &mockSysReader{}, &mockQMMonitor{responses: map[string]string{
		"100:info network": "",
		"100:info block":   "",
	}}, &mockStatFS{}, &mockCmdRunner{}, fr)

	metrics := collectMetrics(c)

	// Should still produce VM info with empty pool
	infoMetrics := metrics["pve_kvm_info"]
	if len(infoMetrics) != 1 {
		t.Fatalf("expected 1 kvm info metric, got %d", len(infoMetrics))
	}
	m := findMetricWithLabels(infoMetrics, map[string]string{"pool": ""})
	if m == nil {
		t.Error("expected empty pool label when user.cfg unreadable")
	}
}

func TestCollector_ProcessDiscoveryError(t *testing.T) {
	cfg := config.Config{
		CollectRunningVMs: true,
		CollectStorage:    false,
		MetricsPrefix:     "pve",
	}

	proc := &mockProcReader{
		procsErr: fmt.Errorf("permission denied"),
	}

	fr := &mockFileReader{files: map[string]string{"/etc/pve/user.cfg": ""}}
	c := NewWithDeps(cfg, proc, &mockSysReader{}, &mockQMMonitor{responses: map[string]string{}},
		&mockStatFS{}, &mockCmdRunner{}, fr)

	metrics := collectMetrics(c)

	// No VM metrics should be emitted, but scrape_duration + build_info are always present
	expectedNames := map[string]bool{
		"pve_scrape_duration_seconds": true,
		"pve_exporter_build_info":     true,
	}
	for name := range metrics {
		if !expectedNames[name] {
			t.Errorf("unexpected metric %q on discovery error", name)
		}
	}
	if len(metrics) != 2 {
		t.Errorf("expected 2 metrics (scrape_duration + build_info) on discovery error, got %d", len(metrics))
	}
}

func TestCollector_BuildInfo(t *testing.T) {
	cfg := config.Config{
		CollectRunningVMs: false,
		CollectStorage:    false,
		MetricsPrefix:     "pve",
		Version:           "1.2.3",
	}

	c := NewWithDeps(cfg, &mockProcReader{}, &mockSysReader{}, &mockQMMonitor{responses: map[string]string{}},
		&mockStatFS{}, &mockCmdRunner{}, &mockFileReader{files: map[string]string{}})

	metrics := collectMetrics(c)

	bi := metrics["pve_exporter_build_info"]
	if len(bi) != 1 {
		t.Fatalf("expected 1 build_info metric, got %d", len(bi))
	}
	if metricValue(bi[0]) != 1 {
		t.Errorf("build_info value = %v, want 1", metricValue(bi[0]))
	}
	m := findMetricWithLabels(bi, map[string]string{"version": "1.2.3"})
	if m == nil {
		t.Error("build_info missing version label")
	}
}

func TestCollector_DiskInfoMetrics(t *testing.T) {
	cfg := config.Config{
		CollectRunningVMs: true,
		CollectStorage:    false,
		MetricsPrefix:     "pve",
	}

	proc := &mockProcReader{
		procs: []procfs.QEMUProcess{
			{PID: 1, VMID: "100", Name: "vm", Vcores: 1, MaxMem: 1024},
		},
		cpuTimes: map[int]procfs.CPUTimes{1: {}},
		ioCount:  map[int]procfs.IOCounters{1: {}},
		status: map[int]procfs.StatusInfo{
			1: {Threads: 1, MemoryExtended: procfs.MemoryExtended{}},
		},
		memPct: map[int]float64{1: 0},
	}

	blockOutput := `drive-scsi0 (#block100): /dev/zvol/rpool/data/vm-100-disk-0 (raw, read-write)
    Attached to:      /machine/peripheral/virtioscsi0/virtio-backend
    Cache mode:       writeback, direct
    Detect zeroes:    on
drive-scsi1 (#block101): /mnt/storage/images/100/vm-100-disk-1.qcow2 (qcow2, read-only)
    Attached to:      /machine/peripheral/virtioscsi0/virtio-backend
`

	sys := &mockSysReader{
		blockSize: map[string]int64{
			"/dev/zvol/rpool/data/vm-100-disk-0": 10737418240,
		},
	}

	qm := &mockQMMonitor{responses: map[string]string{
		"100:info network": "",
		"100:info block":   blockOutput,
	}}

	fr := &mockFileReader{files: map[string]string{"/etc/pve/user.cfg": ""}}
	c := NewWithDeps(cfg, proc, sys, qm, &mockStatFS{}, &mockCmdRunner{}, fr)
	metrics := collectMetrics(c)

	diskInfo := metrics["pve_kvm_disk_info"]
	if len(diskInfo) != 2 {
		t.Fatalf("expected 2 disk info metrics, got %d", len(diskInfo))
	}

	// Check zvol disk
	m := findMetricWithLabels(diskInfo, map[string]string{
		"id":            "100",
		"disk_name":     "scsi0",
		"disk_type":     "zvol",
		"cache_mode":    "writeback, direct",
		"detect_zeroes": "on",
		"read_only":     "",
		"vol_name":      "vm-100-disk-0",
		"pool":          "rpool/data",
	})
	if m == nil {
		t.Error("zvol disk info metric not found with expected labels")
	}

	// Check qcow2 disk (read-only, no cache_mode)
	m = findMetricWithLabels(diskInfo, map[string]string{
		"id":         "100",
		"disk_name":  "scsi1",
		"disk_type":  "qcow2",
		"read_only":  "true",
		"cache_mode": "",
		"vol_name":   "vm-100-disk-1",
	})
	if m == nil {
		t.Error("qcow2 disk info metric not found with expected labels")
	}

	// Verify disk size for zvol
	diskSize := metrics["pve_kvm_disk_size_bytes"]
	if len(diskSize) < 1 {
		t.Fatal("expected at least 1 disk size metric")
	}
	m = findMetricWithLabels(diskSize, map[string]string{"disk_name": "scsi0"})
	if m == nil || metricValue(m) != 10737418240 {
		t.Errorf("disk size for scsi0 = %v", m)
	}
}

const testGuestStats = `{
    "stats": {
        "stat-swap-out": 0,
        "stat-available-memory": 3628638208,
        "stat-free-memory": 3337859072,
        "stat-minor-faults": 295953,
        "stat-major-faults": 419,
        "stat-total-memory": 4102778880,
        "stat-oom-kills": -1,
        "stat-swap-in": 0,
        "stat-disk-caches": 500019200
    },
    "last-update": 1794114458
}`

func newMemTestCollector(guestStats string, cgroup map[string]sysfs.CgroupMemory, now time.Time) *PVECollector {
	cfg := config.Config{CollectRunningVMs: true, MetricsPrefix: "pve"}
	proc := &mockProcReader{
		procs: []procfs.QEMUProcess{{PID: 1234, VMID: "100", Name: "vm", Vcores: 1, MaxMem: 4194304}},
	}
	sys := &mockSysReader{cgroupMem: cgroup}
	qm := &mockQMMonitor{responses: map[string]string{
		"100:" + qmmonitor.GuestStatsCommand: guestStats,
	}}
	fr := &mockFileReader{files: map[string]string{"/etc/pve/user.cfg": ""}}
	c := NewWithDeps(cfg, proc, sys, qm, &mockStatFS{}, &mockCmdRunner{}, fr)
	c.now = func() time.Time { return now }
	return c
}

func TestCollector_MemoryMetrics(t *testing.T) {
	c := newMemTestCollector(testGuestStats,
		map[string]sysfs.CgroupMemory{"100": {Current: 1061707776, File: 1474560, Swap: 8192}},
		time.Unix(1794114458+10, 0))
	metrics := collectMetrics(c)

	for name, want := range map[string]float64{
		"pve_kvm_memory_host_used_bytes":                           1061707776 - 1474560,
		"pve_kvm_memory_host_swap_bytes":                           8192,
		"pve_kvm_guest_memory_total_bytes":                         4102778880,
		"pve_kvm_guest_memory_free_bytes":                          3337859072,
		"pve_kvm_guest_memory_available_bytes":                     3628638208,
		"pve_kvm_guest_memory_cached_bytes":                        500019200,
		"pve_kvm_guest_memory_major_faults_total":                  419,
		"pve_kvm_guest_memory_swap_in_bytes_total":                 0,
		"pve_kvm_guest_memory_stats_last_update_timestamp_seconds": 1794114458,
	} {
		m := metrics[name]
		if len(m) != 1 || metricValue(m[0]) != want {
			t.Errorf("%s = %v, want %v", name, m, want)
		}
	}
	// -1 (unsupported) and missing stats are omitted
	for _, name := range []string{"pve_kvm_guest_memory_oom_kills_total", "pve_kvm_guest_memory_alloc_stalls_total"} {
		if m := metrics[name]; len(m) != 0 {
			t.Errorf("expected no %s, got %v", name, m)
		}
	}
}

func TestCollector_GuestMemoryUnavailable(t *testing.T) {
	cases := map[string]struct {
		raw string
		now time.Time
	}{
		"no balloon device": {"Error: Device 'balloon0' not found", time.Unix(1794114458, 0)},
		"never updated":     {`{"stats": {"stat-total-memory": -1}, "last-update": 0}`, time.Unix(1794114458, 0)},
		"stale":             {testGuestStats, time.Unix(1794114458, 0).Add(guestStatsMaxAge + time.Second)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := newMemTestCollector(tc.raw,
				map[string]sysfs.CgroupMemory{"100": {Current: 100, File: 10}}, tc.now)
			metrics := collectMetrics(c)
			for n := range metrics {
				if strings.HasPrefix(n, "pve_kvm_guest_memory_") {
					t.Errorf("unexpected guest metric %s", n)
				}
			}
			// Host fallback is still present
			if m := metrics["pve_kvm_memory_host_used_bytes"]; len(m) != 1 || metricValue(m[0]) != 90 {
				t.Errorf("host_used_bytes = %v", m)
			}
		})
	}
}

func TestCollector_NoCgroup(t *testing.T) {
	c := newMemTestCollector("", nil, time.Now())
	metrics := collectMetrics(c)
	if m := metrics["pve_kvm_memory_host_used_bytes"]; len(m) != 0 {
		t.Errorf("expected no host_used_bytes, got %v", m)
	}
}
