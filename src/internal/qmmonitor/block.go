package qmmonitor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// DiskInfo holds parsed block device info from "info block".
type DiskInfo struct {
	DiskName string
	BlockID  string
	DiskPath string
	DiskType string
	Labels   map[string]string // additional labels: vol_name, pool, device, etc.
}

// blockHeaderRe matches block device headers in both old and new QEMU formats:
//   Old: "disk_name (#blockN): /path/to/disk (type, mode)"
//   New: "disk_name: /path/to/disk (type, mode)"
var blockHeaderRe = regexp.MustCompile(`^(\w+)(?:\s+\(#block(\d+)\))?: (.+) \(([\w, -]+)\)$`)

// lvmRe matches: /dev/{vg_name}/vm-{N}-disk-{N}
var lvmRe = regexp.MustCompile(`^/dev/([^/]+)/(vm-\d+-disk-\d+)$`)

// ParseBlockInfo parses "info block" output from qm monitor.
// Returns map of disk_name -> DiskInfo. Skips efidisk entries.
func ParseBlockInfo(raw string) map[string]DiskInfo {
	result := make(map[string]DiskInfo)

	// Split by "drive-" prefix to get individual disk blocks
	parts := strings.Split(raw, "drive-")
	if len(parts) < 2 {
		return result
	}

	for _, part := range parts[1:] {
		lines := strings.Split(strings.TrimSpace(part), "\n")
		if len(lines) == 0 {
			continue
		}

		match := blockHeaderRe.FindStringSubmatch(strings.TrimSpace(lines[0]))
		if match == nil {
			continue
		}

		diskName := match[1]
		blockID := match[2]
		diskPath := match[3]
		diskTypeAndMode := match[4]
		modeParts := strings.Split(diskTypeAndMode, ", ")
		diskType := modeParts[0]
		readOnly := false
		for _, p := range modeParts[1:] {
			if p == "read-only" {
				readOnly = true
			}
		}

		// Skip EFI disks
		if strings.Contains(diskName, "efidisk") {
			continue
		}

		// Handle json: paths
		if strings.HasPrefix(diskPath, "json:") {
			resolved, err := HandleJSONPath(diskPath)
			if err != nil {
				continue
			}
			diskPath = resolved
		}

		info := DiskInfo{
			DiskName: diskName,
			BlockID:  blockID,
			DiskPath: diskPath,
			DiskType: diskType,
			Labels:   make(map[string]string),
		}

		if readOnly {
			info.Labels["read_only"] = "true"
		}

		// Detect disk type from path
		classifyDisk(&info)

		// Parse additional info from remaining lines
		for _, line := range lines[1:] {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Attached to:") {
				// Extract device ID, e.g. "Attached to:      /machine/peripheral/virtio0/virtio-backend"
				val := strings.TrimSpace(strings.TrimPrefix(line, "Attached to:"))
				info.Labels["attached_to"] = val
			} else if strings.HasPrefix(line, "Cache mode:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "Cache mode:"))
				info.Labels["cache_mode"] = val
			} else if strings.HasPrefix(line, "Detect zeroes:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "Detect zeroes:"))
				info.Labels["detect_zeroes"] = val
			}
		}

		result[diskName] = info
	}

	if len(result) == 0 && raw != "" {
		slog.Debug("ParseBlockInfo found no disks", "rawLen", len(raw))
	}
	return result
}

// classifyDisk sets DiskType and extra labels based on the disk path.
func classifyDisk(info *DiskInfo) {
	path := info.DiskPath

	if info.DiskType == "qcow2" {
		// Extract volume name: filename without extension
		parts := strings.Split(path, "/")
		filename := parts[len(parts)-1]
		dotIdx := strings.Index(filename, ".")
		if dotIdx > 0 {
			info.Labels["vol_name"] = filename[:dotIdx]
		}
	}

	if strings.HasPrefix(path, "/dev/zvol/") {
		info.DiskType = "zvol"
		// /dev/zvol/pool_name/vol_name
		trimmed := strings.TrimPrefix(path, "/dev/zvol/")
		parts := strings.Split(trimmed, "/")
		if len(parts) >= 2 {
			info.Labels["pool"] = strings.Join(parts[:len(parts)-1], "/")
			info.Labels["vol_name"] = parts[len(parts)-1]
		}
	} else if strings.HasPrefix(path, "/dev/rbd-pve/") {
		info.DiskType = "rbd"
		// /dev/rbd-pve/cluster_id/pool/vol_name
		parts := strings.Split(path, "/")
		if len(parts) >= 5 {
			info.Labels["cluster_id"] = parts[len(parts)-3]
			info.Labels["pool"] = parts[len(parts)-2]
			info.Labels["pool_name"] = parts[len(parts)-2]
			info.Labels["vol_name"] = parts[len(parts)-1]
		}
	} else if m := lvmRe.FindStringSubmatch(path); m != nil {
		info.DiskType = "lvm"
		info.Labels["vg_name"] = m[1]
		info.Labels["vol_name"] = m[2]
	}
}

// HandleJSONPath resolves a "json:{...}" disk path by searching for
// a driver == "host_device" entry and extracting its filename.
func HandleJSONPath(path string) (string, error) {
	jsonStr := strings.TrimPrefix(path, "json:")
	var data map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return "", fmt.Errorf("parse json path: %w", err)
	}
	if result := searchHostDevice(data); result != "" {
		return result, nil
	}
	return "", fmt.Errorf("no host_device driver found in json path")
}

func searchHostDevice(data map[string]any) string {
	driver, _ := data["driver"].(string)
	if driver == "host_device" {
		if filename, ok := data["filename"].(string); ok {
			return filename
		}
	}
	for _, v := range data {
		if sub, ok := v.(map[string]any); ok {
			if result := searchHostDevice(sub); result != "" {
				return result
			}
		}
	}
	return ""
}
