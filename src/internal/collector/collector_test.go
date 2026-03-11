package collector

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"pve_local_exporter/internal/config"
	"pve_local_exporter/internal/procfs"
	"pve_local_exporter/internal/storage"
)

// Mock implementations

type mockProcReader struct {
	procs     []procfs.QEMUProcess
	cpuTimes  map[int]procfs.CPUTimes
	ioCount   map[int]procfs.IOCounters
	threads   map[int]int
	memPct    map[int]float64
	memExt    map[int]procfs.MemoryExtended
	ctxSwitch map[int]procfs.CtxSwitches
}

func (m *mockProcReader) DiscoverQEMUProcesses() ([]procfs.QEMUProcess, error) {
	return m.procs, nil
}
func (m *mockProcReader) GetCPUTimes(pid int) (procfs.CPUTimes, error) {
	return m.cpuTimes[pid], nil
}
func (m *mockProcReader) GetIOCounters(pid int) (procfs.IOCounters, error) {
	return m.ioCount[pid], nil
}
func (m *mockProcReader) GetNumThreads(pid int) (int, error) {
	return m.threads[pid], nil
}
func (m *mockProcReader) GetMemoryPercent(pid int) (float64, error) {
	return m.memPct[pid], nil
}
func (m *mockProcReader) GetMemoryExtended(pid int) (procfs.MemoryExtended, error) {
	return m.memExt[pid], nil
}
func (m *mockProcReader) GetCtxSwitches(pid int) (procfs.CtxSwitches, error) {
	return m.ctxSwitch[pid], nil
}
func (m *mockProcReader) VMConfigExists(vmid string) bool { return true }

type mockSysReader struct {
	ifStats   map[string]map[string]int64
	blockSize map[string]int64
}

func (m *mockSysReader) ReadInterfaceStats(ifname string) (map[string]int64, error) {
	return m.ifStats[ifname], nil
}
func (m *mockSysReader) GetBlockDeviceSize(devPath string) (int64, error) {
	return m.blockSize[devPath], nil
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
		threads: map[int]int{1234: 50},
		memPct:  map[int]float64{1234: 25.5},
		memExt: map[int]procfs.MemoryExtended{
			1234: {"vmrss:": 1048576, "vmpeak:": 2097152},
		},
		ctxSwitch: map[int]procfs.CtxSwitches{
			1234: {Voluntary: 100, Involuntary: 10},
		},
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

	// Check CPU metrics
	cpuMetrics := metrics["pve_kvm_cpu"]
	if len(cpuMetrics) != 3 {
		t.Fatalf("expected 3 cpu metrics, got %d", len(cpuMetrics))
	}
	m := findMetricWithLabels(cpuMetrics, map[string]string{"mode": "user"})
	if m == nil || m.Gauge.GetValue() != 5.0 {
		t.Errorf("cpu user = %v", m)
	}
	m = findMetricWithLabels(cpuMetrics, map[string]string{"mode": "system"})
	if m == nil || m.Gauge.GetValue() != 2.0 {
		t.Errorf("cpu system = %v", m)
	}
	m = findMetricWithLabels(cpuMetrics, map[string]string{"mode": "iowait"})
	if m == nil || m.Gauge.GetValue() != 0.5 {
		t.Errorf("cpu iowait = %v", m)
	}

	// Check vcores
	vcoreMetrics := metrics["pve_kvm_vcores"]
	if len(vcoreMetrics) != 1 || vcoreMetrics[0].Gauge.GetValue() != 4 {
		t.Errorf("vcores = %v", vcoreMetrics)
	}

	// Check threads
	threadMetrics := metrics["pve_kvm_threads"]
	if len(threadMetrics) != 1 || threadMetrics[0].Gauge.GetValue() != 50 {
		t.Errorf("threads = %v", threadMetrics)
	}

	// Check memory percent
	memPctMetrics := metrics["pve_kvm_memory_percent"]
	if len(memPctMetrics) != 1 || memPctMetrics[0].Gauge.GetValue() != 25.5 {
		t.Errorf("memory_percent = %v", memPctMetrics)
	}

	// Check IO
	if m := metrics["pve_kvm_io_read_count"]; len(m) != 1 || m[0].Gauge.GetValue() != 10 {
		t.Errorf("io_read_count = %v", m)
	}
	if m := metrics["pve_kvm_io_write_bytes"]; len(m) != 1 || m[0].Gauge.GetValue() != 1000 {
		t.Errorf("io_write_bytes = %v", m)
	}

	// Check context switches
	csMetrics := metrics["pve_kvm_ctx_switches"]
	if len(csMetrics) != 2 {
		t.Fatalf("expected 2 ctx_switches metrics, got %d", len(csMetrics))
	}

	// Check VM info metric
	infoMetrics := metrics["pve_kvm"]
	if len(infoMetrics) != 1 {
		t.Fatalf("expected 1 kvm info metric, got %d", len(infoMetrics))
	}
	m = findMetricWithLabels(infoMetrics, map[string]string{"id": "100", "name": "testvm", "pool": "prod"})
	if m == nil {
		t.Error("kvm info metric not found with expected labels")
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

	// Check storage size
	sizeMetrics := metrics["pve_node_storage_size"]
	if len(sizeMetrics) != 1 || sizeMetrics[0].Gauge.GetValue() != 1e9 {
		t.Errorf("storage_size = %v", sizeMetrics)
	}

	// Check storage free
	freeMetrics := metrics["pve_node_storage_free"]
	if len(freeMetrics) != 1 || freeMetrics[0].Gauge.GetValue() != 5e8 {
		t.Errorf("storage_free = %v", freeMetrics)
	}

	// Check storage info
	infoMetrics := metrics["pve_node_storage"]
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
		cpuTimes:  map[int]procfs.CPUTimes{1: {}},
		ioCount:   map[int]procfs.IOCounters{1: {}},
		threads:   map[int]int{1: 1},
		memPct:    map[int]float64{1: 0},
		memExt:    map[int]procfs.MemoryExtended{1: {}},
		ctxSwitch: map[int]procfs.CtxSwitches{1: {}},
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
	nicInfo := metrics["pve_kvm_nic"]
	if len(nicInfo) != 1 {
		t.Fatalf("expected 1 nic info, got %d", len(nicInfo))
	}

	// NIC stats
	rxBytes := metrics["pve_kvm_nic_rx_bytes"]
	if len(rxBytes) != 1 || rxBytes[0].Gauge.GetValue() != 1000 {
		t.Errorf("rx_bytes = %v", rxBytes)
	}
	txBytes := metrics["pve_kvm_nic_tx_bytes"]
	if len(txBytes) != 1 || txBytes[0].Gauge.GetValue() != 2000 {
		t.Errorf("tx_bytes = %v", txBytes)
	}
}
