package sysfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"pve_local_exporter/internal/logging"
)

// SysReader abstracts /sys access for testability.
type SysReader interface {
	ReadInterfaceStats(ifname string) (map[string]int64, error)
	GetBlockDeviceSize(devPath string) (int64, error)
	ReadCgroupMemory(vmid string) (CgroupMemory, error)
}

// CgroupMemory holds cgroup v2 memory accounting for a VM scope.
type CgroupMemory struct {
	Current int64 // memory.current
	Swap    int64 // memory.swap.current
	Anon    int64 // memory.stat anon
	File    int64 // memory.stat file (host page cache charged to the VM)
}

// Used returns memory.current minus page cache, matching PVE's "memhost".
func (m CgroupMemory) Used() int64 {
	return m.Current - m.File
}

// RealSysReader reads from the actual /sys filesystem.
type RealSysReader struct {
	SysPath string // default "/sys"
}

func NewRealSysReader() *RealSysReader {
	return &RealSysReader{SysPath: "/sys"}
}

// ReadInterfaceStats reads all statistics files from /sys/class/net/{ifname}/statistics/.
func (r *RealSysReader) ReadInterfaceStats(ifname string) (map[string]int64, error) {
	dir := filepath.Join(r.SysPath, "class", "net", ifname, "statistics")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	stats := make(map[string]int64)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		val, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			continue
		}
		stats[e.Name()] = val
	}
	logging.Trace("interface stats", "ifname", ifname, "stat_count", len(stats))
	return stats, nil
}

// GetBlockDeviceSize returns the size in bytes of a block device.
// For symlinks (e.g., /dev/zvol/...), resolves to the real device first.
// Reads size from /sys/block/{dev}/size (in 512-byte sectors).
func (r *RealSysReader) GetBlockDeviceSize(devPath string) (int64, error) {
	// Resolve symlinks
	resolved, err := filepath.EvalSymlinks(devPath)
	if err != nil {
		return 0, fmt.Errorf("resolve symlink %s: %w", devPath, err)
	}
	logging.Trace("block device resolved", "path", devPath, "resolved", resolved)

	// Extract device name from /dev/XXX
	devName := filepath.Base(resolved)

	// Try /sys/block/{devName}/size
	sizeFile := filepath.Join(r.SysPath, "block", devName, "size")
	data, err := os.ReadFile(sizeFile)
	if err != nil {
		// For partition devices like dm-0, try without partition suffix
		return 0, fmt.Errorf("read size %s: %w", sizeFile, err)
	}

	sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse size: %w", err)
	}

	return sectors * 512, nil
}

// GetDeviceSymlinkTarget resolves a device symlink and returns the target path.
func GetDeviceSymlinkTarget(devPath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(devPath)
	if err != nil {
		return "", err
	}
	logging.Trace("device symlink resolved", "path", devPath, "target", resolved)
	return resolved, nil
}

// ReadCgroupMemory reads cgroup v2 memory accounting from
// /sys/fs/cgroup/qemu.slice/{vmid}.scope/.
func (r *RealSysReader) ReadCgroupMemory(vmid string) (CgroupMemory, error) {
	dir := filepath.Join(r.SysPath, "fs", "cgroup", "qemu.slice", vmid+".scope")

	var m CgroupMemory
	current, err := readInt(filepath.Join(dir, "memory.current"))
	if err != nil {
		return m, err
	}
	m.Current = current

	// memory.swap.current is absent when swap accounting is disabled.
	if swap, err := readInt(filepath.Join(dir, "memory.swap.current")); err == nil {
		m.Swap = swap
	}

	data, err := os.ReadFile(filepath.Join(dir, "memory.stat"))
	if err != nil {
		return m, err
	}
	stat := ParseMemoryStat(string(data))
	m.Anon = stat["anon"]
	m.File = stat["file"]

	logging.Trace("cgroup memory", "vmid", vmid, "current", m.Current, "file", m.File, "swap", m.Swap)
	return m, nil
}

// ParseMemoryStat parses a cgroup v2 memory.stat file ("key value" per line).
func ParseMemoryStat(data string) map[string]int64 {
	stat := make(map[string]int64)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		val, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		stat[fields[0]] = val
	}
	return stat
}

func readInt(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}
