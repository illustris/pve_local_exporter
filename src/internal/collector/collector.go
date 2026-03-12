package collector

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"pve_local_exporter/internal/cache"
	"pve_local_exporter/internal/config"
	"pve_local_exporter/internal/logging"
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

	// Pre-allocated metric descriptors for fixed-label metrics.
	descCPU          *prometheus.Desc
	descVcores       *prometheus.Desc
	descMaxmem       *prometheus.Desc
	descMemPct       *prometheus.Desc
	descMemExt       *prometheus.Desc
	descThreads      *prometheus.Desc
	descCtxSwitches  *prometheus.Desc
	descNicInfo      *prometheus.Desc
	descNicQueues    *prometheus.Desc
	descDiskInfo     *prometheus.Desc
	descDiskSize     *prometheus.Desc
	descStorageSize  *prometheus.Desc
	descStorageFree  *prometheus.Desc

	// IO counter descriptors (counters).
	descIOReadCount  *prometheus.Desc
	descIOReadBytes  *prometheus.Desc
	descIOReadChars  *prometheus.Desc
	descIOWriteCount *prometheus.Desc
	descIOWriteBytes *prometheus.Desc
	descIOWriteChars *prometheus.Desc

	// Operational metrics.
	descScrapeDuration *prometheus.Desc
	descBuildInfo      *prometheus.Desc
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

	p := cfg.MetricsPrefix
	c := &PVECollector{
		cfg:        cfg,
		proc:       proc,
		sys:        sys,
		qm:         qm,
		statFS:     statFS,
		cmdRunner:  cmd,
		fileReader: fr,
		prefix:     p,

		descCPU:         prometheus.NewDesc(p+"_kvm_cpu_seconds_total", "KVM CPU time", []string{"id", "mode"}, nil),
		descVcores:      prometheus.NewDesc(p+"_kvm_vcores", "vCores allocated", []string{"id"}, nil),
		descMaxmem:      prometheus.NewDesc(p+"_kvm_maxmem_bytes", "Maximum memory bytes", []string{"id"}, nil),
		descMemPct:      prometheus.NewDesc(p+"_kvm_memory_percent", "Memory percent of host", []string{"id"}, nil),
		descMemExt:      prometheus.NewDesc(p+"_kvm_memory_extended", "Extended memory info", []string{"id", "type"}, nil),
		descThreads:     prometheus.NewDesc(p+"_kvm_threads", "Threads used", []string{"id"}, nil),
		descCtxSwitches: prometheus.NewDesc(p+"_kvm_ctx_switches_total", "Context switches", []string{"id", "type"}, nil),
		descNicInfo:     prometheus.NewDesc(p+"_kvm_nic_info", "NIC info", []string{"id", "ifname", "netdev", "queues", "type", "model", "macaddr"}, nil),
		descNicQueues:   prometheus.NewDesc(p+"_kvm_nic_queues", "NIC queue count", []string{"id", "ifname"}, nil),
		descDiskInfo: prometheus.NewDesc(p+"_kvm_disk_info", "Disk info", []string{
			"id", "disk_name", "block_id", "disk_path", "disk_type",
			"vol_name", "pool", "pool_name", "cluster_id", "vg_name",
			"device", "attached_to", "cache_mode", "detect_zeroes", "read_only",
		}, nil),
		descDiskSize: prometheus.NewDesc(p+"_kvm_disk_size_bytes", "Disk size bytes", []string{"id", "disk_name"}, nil),
		descStorageSize: prometheus.NewDesc(p+"_node_storage_size_bytes", "Storage total size", []string{"name", "type"}, nil),
		descStorageFree: prometheus.NewDesc(p+"_node_storage_free_bytes", "Storage free space", []string{"name", "type"}, nil),

		descIOReadCount:  prometheus.NewDesc(p+"_kvm_io_read_count_total", "Read system calls by KVM process", []string{"id"}, nil),
		descIOReadBytes:  prometheus.NewDesc(p+"_kvm_io_read_bytes_total", "Bytes read from disk by KVM process", []string{"id"}, nil),
		descIOReadChars:  prometheus.NewDesc(p+"_kvm_io_read_chars_total", "Bytes read including buffers by KVM process", []string{"id"}, nil),
		descIOWriteCount: prometheus.NewDesc(p+"_kvm_io_write_count_total", "Write system calls by KVM process", []string{"id"}, nil),
		descIOWriteBytes: prometheus.NewDesc(p+"_kvm_io_write_bytes_total", "Bytes written to disk by KVM process", []string{"id"}, nil),
		descIOWriteChars: prometheus.NewDesc(p+"_kvm_io_write_chars_total", "Bytes written including buffers by KVM process", []string{"id"}, nil),

		descScrapeDuration: prometheus.NewDesc(p+"_scrape_duration_seconds", "Duration of metrics collection", nil, nil),
		descBuildInfo:      prometheus.NewDesc(p+"_exporter_build_info", "Build information", []string{"version"}, nil),
	}
	c.poolCache = cache.NewMtimeCache[poolData]("/etc/pve/user.cfg", fileMtime)
	c.storageCache = cache.NewMtimeCache[[]pveconfig.StorageEntry]("/etc/pve/storage.cfg", fileMtime)
	return c
}

func (c *PVECollector) Describe(ch chan<- *prometheus.Desc) {
	// Dynamic metrics - use empty desc to signal unchecked collector
	ch <- c.descCPU
}

func (c *PVECollector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()

	if c.cfg.CollectRunningVMs {
		c.collectVMs(ch)
	}
	if c.cfg.CollectStorage {
		c.collectStorage(ch)
	}

	ch <- prometheus.MustNewConstMetric(c.descScrapeDuration, prometheus.GaugeValue, time.Since(start).Seconds())
	ch <- prometheus.MustNewConstMetric(c.descBuildInfo, prometheus.GaugeValue, 1, c.cfg.Version)
}

func (c *PVECollector) collectVMs(ch chan<- prometheus.Metric) {
	procs, err := c.proc.DiscoverQEMUProcesses()
	if err != nil {
		slog.Error("discover QEMU processes", "err", err)
		return
	}
	logging.Trace("collectVMs", "vm_count", len(procs))

	// Load pool info
	vmPoolMap, pools := c.getPoolInfo()
	logging.Trace("pool info loaded", "vm_pool_map_size", len(vmPoolMap), "pools_count", len(pools))

	for _, proc := range procs {
		c.collectVMMetrics(ch, proc, vmPoolMap, pools)
	}

	// Parallel NIC + disk collection with bounded worker pool
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
			ch <- prometheus.MustNewConstMetric(c.descCPU, prometheus.CounterValue, m.val, id, m.mode)
		}
	}

	// Vcores
	ch <- prometheus.MustNewConstMetric(c.descVcores, prometheus.GaugeValue, float64(proc.Vcores), id)

	// MaxMem (kB to bytes)
	ch <- prometheus.MustNewConstMetric(c.descMaxmem, prometheus.GaugeValue, float64(proc.MaxMem*1024), id)

	// Status (threads, memory, context switches) -- single /proc/{pid}/status read
	if status, err := c.proc.GetStatus(proc.PID); err == nil {
		// Memory percent
		if memPct, err := c.proc.GetMemoryPercent(proc.PID, status.VmRSS); err == nil {
			ch <- prometheus.MustNewConstMetric(c.descMemPct, prometheus.GaugeValue, memPct, id)
		}

		// Memory extended
		for key, val := range status.MemoryExtended {
			ch <- prometheus.MustNewConstMetric(c.descMemExt, prometheus.GaugeValue, float64(val), id, key)
		}

		// Threads
		ch <- prometheus.MustNewConstMetric(c.descThreads, prometheus.GaugeValue, float64(status.Threads), id)

		// Context switches
		ch <- prometheus.MustNewConstMetric(c.descCtxSwitches, prometheus.CounterValue, float64(status.CtxSwitches.Voluntary), id, "voluntary")
		ch <- prometheus.MustNewConstMetric(c.descCtxSwitches, prometheus.CounterValue, float64(status.CtxSwitches.Involuntary), id, "involuntary")
	}

	// IO counters
	if io, err := c.proc.GetIOCounters(proc.PID); err == nil {
		ch <- prometheus.MustNewConstMetric(c.descIOReadCount, prometheus.CounterValue, float64(io.ReadSyscalls), id)
		ch <- prometheus.MustNewConstMetric(c.descIOReadBytes, prometheus.CounterValue, float64(io.ReadBytes), id)
		ch <- prometheus.MustNewConstMetric(c.descIOReadChars, prometheus.CounterValue, float64(io.ReadChars), id)
		ch <- prometheus.MustNewConstMetric(c.descIOWriteCount, prometheus.CounterValue, float64(io.WriteSyscalls), id)
		ch <- prometheus.MustNewConstMetric(c.descIOWriteBytes, prometheus.CounterValue, float64(io.WriteBytes), id)
		ch <- prometheus.MustNewConstMetric(c.descIOWriteChars, prometheus.CounterValue, float64(io.WriteChars), id)
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
	logging.Trace("qm info network response", "vmid", id, "raw_len", len(raw))

	nics := qmmonitor.ParseNetworkInfo(raw)
	logging.Trace("parsed NICs", "vmid", id, "nic_count", len(nics))
	for _, nic := range nics {
		// NIC info metric
		ch <- prometheus.MustNewConstMetric(c.descNicInfo, prometheus.GaugeValue, 1,
			id, nic.Ifname, nic.Netdev, strconv.Itoa(nic.Queues),
			nic.Type, nic.Model, nic.Macaddr,
		)

		// NIC queues
		ch <- prometheus.MustNewConstMetric(c.descNicQueues, prometheus.GaugeValue, float64(nic.Queues), id, nic.Ifname)

		// NIC stats from sysfs
		stats, err := c.sys.ReadInterfaceStats(nic.Ifname)
		if err != nil {
			slog.Debug("read interface stats", "ifname", nic.Ifname, "err", err)
			continue
		}
		for statName, val := range stats {
			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(c.prefix+"_kvm_nic_"+statName+"_total", fmt.Sprintf("NIC statistic %s", statName), []string{"id", "ifname"}, nil),
				prometheus.CounterValue, float64(val), id, nic.Ifname,
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
	logging.Trace("qm info block response", "vmid", id, "raw_len", len(raw))

	disks := qmmonitor.ParseBlockInfo(raw)
	logging.Trace("parsed disks", "vmid", id, "disk_count", len(disks))
	for diskName, disk := range disks {
		// Try to get device symlink target for zvol/rbd/lvm
		if disk.DiskType == "zvol" || disk.DiskType == "rbd" || disk.DiskType == "lvm" {
			target, err := sysfs.GetDeviceSymlinkTarget(disk.DiskPath)
			if err == nil {
				disk.Labels["device"] = filepath.Base(target)
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

		logging.Trace("disk metric", "vmid", id, "disk", diskName, "type", disk.DiskType,
			"path", disk.DiskPath, "size", diskSize, "device", disk.Labels["device"])

		if diskSize > 0 {
			ch <- prometheus.MustNewConstMetric(c.descDiskSize, prometheus.GaugeValue, float64(diskSize), id, diskName)
		}

		// Disk info metric with fixed label set
		ch <- prometheus.MustNewConstMetric(c.descDiskInfo, prometheus.GaugeValue, 1,
			id,
			diskName,
			disk.BlockID,
			disk.DiskPath,
			disk.DiskType,
			disk.Labels["vol_name"],
			disk.Labels["pool"],
			disk.Labels["pool_name"],
			disk.Labels["cluster_id"],
			disk.Labels["vg_name"],
			disk.Labels["device"],
			disk.Labels["attached_to"],
			disk.Labels["cache_mode"],
			disk.Labels["detect_zeroes"],
			disk.Labels["read_only"],
		)
	}
}

func (c *PVECollector) collectStorage(ch chan<- prometheus.Metric) {
	entries := c.getStorageEntries()
	logging.Trace("collectStorage", "entries_count", len(entries))

	// Compute superset of property keys across all entries
	keySet := make(map[string]struct{})
	for _, entry := range entries {
		for k := range entry.Properties {
			keySet[k] = struct{}{}
		}
	}
	allKeys := sortedKeySet(keySet)

	// Create descriptor once with fixed labels for this scrape
	storageInfoDesc := prometheus.NewDesc(
		c.prefix+"_node_storage_info", "Storage info", allKeys, nil,
	)

	for _, entry := range entries {
		storageType := entry.Properties["type"]
		storageName := entry.Properties["name"]
		logging.Trace("storage entry", "name", storageName, "type", storageType)

		// Info metric with consistent labels
		vals := make([]string, len(allKeys))
		for i, k := range allKeys {
			vals[i] = entry.Properties[k] // "" if missing
		}
		ch <- prometheus.MustNewConstMetric(storageInfoDesc, prometheus.GaugeValue, 1, vals...)

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
				slog.Warn("zpool list", "pool", poolName, "err", runErr)
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

		ch <- prometheus.MustNewConstMetric(c.descStorageSize, prometheus.GaugeValue, float64(size.Total), storageName, storageType)
		ch <- prometheus.MustNewConstMetric(c.descStorageFree, prometheus.GaugeValue, float64(size.Free), storageName, storageType)
	}
}

func (c *PVECollector) getPoolInfo() (map[string]string, map[string]pveconfig.PoolInfo) {
	if data, ok := c.poolCache.Get(); ok {
		return data.vmPoolMap, data.pools
	}

	content, err := c.fileReader.ReadFile("/etc/pve/user.cfg")
	if err != nil {
		slog.Error("read user.cfg", "err", err)
		return make(map[string]string), make(map[string]pveconfig.PoolInfo)
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
	slices.Sort(keys)
	return keys
}

func sortedKeySet(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

