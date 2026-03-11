package collector

import (
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"pve_local_exporter/internal/cache"
	"pve_local_exporter/internal/config"
	"pve_local_exporter/internal/procfs"
	"pve_local_exporter/internal/pveconfig"
	"pve_local_exporter/internal/qmmonitor"
	"pve_local_exporter/internal/storage"
	"pve_local_exporter/internal/sysfs"
)

const maxWorkers = 16

// PVECollector implements prometheus.Collector.
type PVECollector struct {
	cfg        config.Config
	proc       procfs.ProcReader
	sys        sysfs.SysReader
	qm         qmmonitor.QMMonitor
	statFS     storage.StatFS
	cmdRunner  CommandRunner
	fileReader FileReaderIface

	poolCache    *cache.MtimeCache[poolData]
	storageCache *cache.MtimeCache[[]pveconfig.StorageEntry]

	prefix string
}

type poolData struct {
	vmPoolMap map[string]string
	pools     map[string]pveconfig.PoolInfo
}

// CommandRunner for executing shell commands.
type CommandRunner interface {
	Run(name string, args ...string) (string, error)
}

// FileReaderIface for reading files.
type FileReaderIface interface {
	ReadFile(path string) (string, error)
}

// RealCommandRunner executes real commands.
type RealCommandRunner struct{}

func (RealCommandRunner) Run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// RealFileReader reads real files.
type RealFileReader struct{}

func (RealFileReader) ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}

func fileMtime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// New creates a PVECollector with real I/O implementations.
func New(cfg config.Config) *PVECollector {
	return NewWithDeps(cfg,
		procfs.NewRealProcReader(),
		sysfs.NewRealSysReader(),
		qmmonitor.NewRealQMMonitor(cfg.QMTerminalTimeout, cfg.QMMaxTTL, cfg.QMRand, cfg.QMMonitorDeferClose),
		storage.RealStatFS{},
		RealCommandRunner{},
		RealFileReader{},
	)
}

// NewWithDeps creates a PVECollector with injected dependencies (for testing).
func NewWithDeps(cfg config.Config, proc procfs.ProcReader, sys sysfs.SysReader,
	qm qmmonitor.QMMonitor, statFS storage.StatFS, cmd CommandRunner, fr FileReaderIface) *PVECollector {

	c := &PVECollector{
		cfg:        cfg,
		proc:       proc,
		sys:        sys,
		qm:         qm,
		statFS:     statFS,
		cmdRunner:  cmd,
		fileReader: fr,
		prefix:     cfg.MetricsPrefix,
	}
	c.poolCache = cache.NewMtimeCache[poolData]("/etc/pve/user.cfg", fileMtime)
	c.storageCache = cache.NewMtimeCache[[]pveconfig.StorageEntry]("/etc/pve/storage.cfg", fileMtime)
	return c
}

func (c *PVECollector) Describe(ch chan<- *prometheus.Desc) {
	// Dynamic metrics - use empty desc to signal unchecked collector
	ch <- prometheus.NewDesc(c.prefix+"_kvm_cpu", "KVM CPU time", nil, nil)
}

func (c *PVECollector) Collect(ch chan<- prometheus.Metric) {
	if c.cfg.CollectRunningVMs {
		c.collectVMs(ch)
	}
	if c.cfg.CollectStorage {
		c.collectStorage(ch)
	}
}

func (c *PVECollector) collectVMs(ch chan<- prometheus.Metric) {
	procs, err := c.proc.DiscoverQEMUProcesses()
	if err != nil {
		slog.Error("discover QEMU processes", "err", err)
		return
	}

	// Load pool info
	vmPoolMap, pools := c.getPoolInfo()

	for _, proc := range procs {
		c.collectVMMetrics(ch, proc, vmPoolMap, pools)
	}

	// Parallel NIC + disk collection with bounded worker pool
	type workItem struct {
		proc procfs.QEMUProcess
		fn   func()
	}

	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for _, proc := range procs {
		proc := proc
		wg.Add(2)

		go func() {
			sem <- struct{}{}
			defer func() { <-sem; wg.Done() }()
			c.collectNICMetrics(ch, proc)
		}()

		go func() {
			sem <- struct{}{}
			defer func() { <-sem; wg.Done() }()
			c.collectDiskMetrics(ch, proc)
		}()
	}
	wg.Wait()
}

func (c *PVECollector) collectVMMetrics(ch chan<- prometheus.Metric, proc procfs.QEMUProcess,
	vmPoolMap map[string]string, pools map[string]pveconfig.PoolInfo) {

	id := proc.VMID

	// CPU times
	if cpu, err := c.proc.GetCPUTimes(proc.PID); err == nil {
		for _, m := range []struct {
			mode string
			val  float64
		}{
			{"user", cpu.User},
			{"system", cpu.System},
			{"iowait", cpu.IOWait},
		} {
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(c.prefix+"_kvm_cpu", "KVM CPU time", []string{"id", "mode"}, nil),
				prometheus.GaugeValue, m.val, id, m.mode,
			)
		}
	}

	// Vcores
	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc(c.prefix+"_kvm_vcores", "vCores allocated", []string{"id"}, nil),
		prometheus.GaugeValue, float64(proc.Vcores), id,
	)

	// MaxMem (kB to bytes)
	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc(c.prefix+"_kvm_maxmem", "Maximum memory bytes", []string{"id"}, nil),
		prometheus.GaugeValue, float64(proc.MaxMem*1024), id,
	)

	// Memory percent
	if memPct, err := c.proc.GetMemoryPercent(proc.PID); err == nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc(c.prefix+"_kvm_memory_percent", "Memory percent of host", []string{"id"}, nil),
			prometheus.GaugeValue, memPct, id,
		)
	}

	// Memory extended
	if memExt, err := c.proc.GetMemoryExtended(proc.PID); err == nil {
		desc := prometheus.NewDesc(c.prefix+"_kvm_memory_extended", "Extended memory info", []string{"id", "type"}, nil)
		for key, val := range memExt {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(val), id, key)
		}
	}

	// Threads
	if threads, err := c.proc.GetNumThreads(proc.PID); err == nil {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc(c.prefix+"_kvm_threads", "Threads used", []string{"id"}, nil),
			prometheus.GaugeValue, float64(threads), id,
		)
	}

	// IO counters
	if io, err := c.proc.GetIOCounters(proc.PID); err == nil {
		for _, m := range []struct {
			name string
			val  uint64
		}{
			{"kvm_io_read_count", io.ReadSyscalls},
			{"kvm_io_read_bytes", io.ReadBytes},
			{"kvm_io_read_chars", io.ReadChars},
			{"kvm_io_write_count", io.WriteSyscalls},
			{"kvm_io_write_bytes", io.WriteBytes},
			{"kvm_io_write_chars", io.WriteChars},
		} {
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(c.prefix+"_"+m.name, "", []string{"id"}, nil),
				prometheus.GaugeValue, float64(m.val), id,
			)
		}
	}

	// Context switches
	if cs, err := c.proc.GetCtxSwitches(proc.PID); err == nil {
		desc := prometheus.NewDesc(c.prefix+"_kvm_ctx_switches", "Context switches", []string{"id", "type"}, nil)
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(cs.Voluntary), id, "voluntary")
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(cs.Involuntary), id, "involuntary")
	}

	// VM info metric
	poolName := vmPoolMap[id]
	poolInfo := pools[poolName]
	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc(c.prefix+"_kvm", "VM info", []string{
			"id", "name", "cpu", "pid", "pool", "pool_levels", "pool1", "pool2", "pool3",
		}, nil),
		prometheus.GaugeValue, 1,
		id, proc.Name, proc.CPU, strconv.Itoa(proc.PID),
		poolName, strconv.Itoa(poolInfo.LevelCount),
		poolInfo.Level1, poolInfo.Level2, poolInfo.Level3,
	)
}

func (c *PVECollector) collectNICMetrics(ch chan<- prometheus.Metric, proc procfs.QEMUProcess) {
	id := proc.VMID

	raw, err := c.qm.RunCommand(id, "info network")
	if err != nil {
		slog.Error("qm info network", "vmid", id, "err", err)
		return
	}

	nics := qmmonitor.ParseNetworkInfo(raw)
	for _, nic := range nics {
		// NIC info metric
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc(c.prefix+"_kvm_nic", "NIC info", []string{
				"id", "ifname", "netdev", "queues", "type", "model", "macaddr",
			}, nil),
			prometheus.GaugeValue, 1,
			id, nic.Ifname, nic.Netdev, strconv.Itoa(nic.Queues),
			nic.Type, nic.Model, nic.Macaddr,
		)

		// NIC queues
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc(c.prefix+"_kvm_nic_queues", "NIC queue count", []string{"id", "ifname"}, nil),
			prometheus.GaugeValue, float64(nic.Queues), id, nic.Ifname,
		)

		// NIC stats from sysfs
		stats, err := c.sys.ReadInterfaceStats(nic.Ifname)
		if err != nil {
			slog.Debug("read interface stats", "ifname", nic.Ifname, "err", err)
			continue
		}
		for statName, val := range stats {
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(c.prefix+"_kvm_nic_"+statName, "", []string{"id", "ifname"}, nil),
				prometheus.GaugeValue, float64(val), id, nic.Ifname,
			)
		}
	}
}

func (c *PVECollector) collectDiskMetrics(ch chan<- prometheus.Metric, proc procfs.QEMUProcess) {
	id := proc.VMID

	raw, err := c.qm.RunCommand(id, "info block")
	if err != nil {
		slog.Error("qm info block", "vmid", id, "err", err)
		return
	}

	disks := qmmonitor.ParseBlockInfo(raw)
	for diskName, disk := range disks {
		// Try to get device symlink target for zvol/rbd/lvm
		if disk.DiskType == "zvol" || disk.DiskType == "rbd" || disk.DiskType == "lvm" {
			target, err := sysfs.GetDeviceSymlinkTarget(disk.DiskPath)
			if err == nil {
				disk.Labels["device"] = target
			} else {
				slog.Debug("resolve device symlink", "path", disk.DiskPath, "err", err)
				// Retry with cache invalidation
				c.qm.InvalidateCache(id, "info block")
			}
		}

		// Disk size
		var diskSize int64
		switch disk.DiskType {
		case "qcow2":
			// File-backed: use file size
			if fi, err := os.Stat(disk.DiskPath); err == nil {
				diskSize = fi.Size()
			}
		default:
			// Block device
			size, err := c.sys.GetBlockDeviceSize(disk.DiskPath)
			if err == nil {
				diskSize = size
			}
		}

		if diskSize > 0 {
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(c.prefix+"_kvm_disk_size", "Disk size bytes", []string{"id", "disk_name"}, nil),
				prometheus.GaugeValue, float64(diskSize), id, diskName,
			)
		}

		// Disk info metric - collect all labels
		labelNames := []string{"id", "disk_name", "block_id", "disk_path", "disk_type"}
		labelValues := []string{id, diskName, disk.BlockID, disk.DiskPath, disk.DiskType}

		// Add variable labels in sorted-ish order
		for _, key := range sortedKeys(disk.Labels) {
			labelNames = append(labelNames, key)
			labelValues = append(labelValues, disk.Labels[key])
		}

		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc(c.prefix+"_kvm_disk", "Disk info", labelNames, nil),
			prometheus.GaugeValue, 1, labelValues...,
		)
	}
}

func (c *PVECollector) collectStorage(ch chan<- prometheus.Metric) {
	entries := c.getStorageEntries()

	for _, entry := range entries {
		storageType := entry.Properties["type"]
		storageName := entry.Properties["name"]

		// Info metric
		labelNames := make([]string, 0, len(entry.Properties))
		labelValues := make([]string, 0, len(entry.Properties))
		for _, key := range sortedKeys(entry.Properties) {
			labelNames = append(labelNames, key)
			labelValues = append(labelValues, entry.Properties[key])
		}
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc(c.prefix+"_node_storage", "Storage info", labelNames, nil),
			prometheus.GaugeValue, 1, labelValues...,
		)

		// Size metrics
		var size storage.StorageSize
		var err error

		switch storageType {
		case "dir", "nfs", "cephfs":
			path := entry.Properties["path"]
			if path == "" {
				continue
			}
			size, err = storage.GetDirStorageSize(c.statFS, path)
		case "zfspool":
			pool := entry.Properties["pool"]
			if pool == "" {
				continue
			}
			// Extract base pool name (before any /)
			poolName := strings.Split(pool, "/")[0]
			out, runErr := c.cmdRunner.Run("zpool", "list", "-p", poolName)
			if runErr != nil {
				slog.Error("zpool list", "pool", poolName, "err", runErr)
				continue
			}
			size, err = storage.GetZPoolSize(out)
		default:
			continue
		}

		if err != nil {
			slog.Error("storage size", "name", storageName, "err", err)
			continue
		}

		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc(c.prefix+"_node_storage_size", "Storage total size", []string{"name", "type"}, nil),
			prometheus.GaugeValue, float64(size.Total), storageName, storageType,
		)
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc(c.prefix+"_node_storage_free", "Storage free space", []string{"name", "type"}, nil),
			prometheus.GaugeValue, float64(size.Free), storageName, storageType,
		)
	}
}

func (c *PVECollector) getPoolInfo() (map[string]string, map[string]pveconfig.PoolInfo) {
	if data, ok := c.poolCache.Get(); ok {
		return data.vmPoolMap, data.pools
	}

	content, err := c.fileReader.ReadFile("/etc/pve/user.cfg")
	if err != nil {
		slog.Error("read user.cfg", "err", err)
		return nil, nil
	}

	vmPoolMap, pools := pveconfig.ParsePoolConfig(content)
	c.poolCache.Set(poolData{vmPoolMap: vmPoolMap, pools: pools})
	return vmPoolMap, pools
}

func (c *PVECollector) getStorageEntries() []pveconfig.StorageEntry {
	if data, ok := c.storageCache.Get(); ok {
		return data
	}

	content, err := c.fileReader.ReadFile("/etc/pve/storage.cfg")
	if err != nil {
		slog.Error("read storage.cfg", "err", err)
		return nil
	}

	entries := pveconfig.ParseStorageConfig(content)
	c.storageCache.Set(entries)
	return entries
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort for typically small maps
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

