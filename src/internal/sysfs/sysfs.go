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
