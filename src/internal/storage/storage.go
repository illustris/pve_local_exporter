package storage

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

// StorageSize holds the total and free bytes of a storage pool.
type StorageSize struct {
	Total int64
	Free  int64
}

// StatFS abstracts the statfs syscall for testability.
type StatFS interface {
	Statfs(path string) (StorageSize, error)
}

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	Run(name string, args ...string) (string, error)
}

// RealStatFS uses the real syscall.
type RealStatFS struct{}

func (RealStatFS) Statfs(path string) (StorageSize, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return StorageSize{}, fmt.Errorf("statfs %s: %w", path, err)
	}
	return StorageSize{
		Total: int64(stat.Frsize) * int64(stat.Blocks),
		Free:  int64(stat.Frsize) * int64(stat.Bavail),
	}, nil
}

// GetDirStorageSize returns size info for dir/nfs/cephfs storage using statfs.
func GetDirStorageSize(fs StatFS, path string) (StorageSize, error) {
	return fs.Statfs(path)
}

// GetZPoolSize parses `zpool list -p {poolName}` output for size and free.
func GetZPoolSize(output string) (StorageSize, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return StorageSize{}, fmt.Errorf("unexpected zpool output: %q", output)
	}

	// Header: NAME  SIZE  ALLOC  FREE ...
	// Data:   pool  1234  567    890  ...
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return StorageSize{}, fmt.Errorf("not enough fields in zpool output: %q", lines[1])
	}

	total, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return StorageSize{}, fmt.Errorf("parse total: %w", err)
	}
	free, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return StorageSize{}, fmt.Errorf("parse free: %w", err)
	}

	return StorageSize{Total: total, Free: free}, nil
}
