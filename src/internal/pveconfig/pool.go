package pveconfig

import (
	"strings"

	"pve_local_exporter/internal/cache"
)

// PoolInfo holds parsed pool hierarchy info.
type PoolInfo struct {
	LevelCount int
	Level1     string
	Level2     string
	Level3     string
}

// FileReader abstracts file reading for testability.
type FileReader interface {
	ReadFile(path string) (string, error)
	Stat(path string) (cache.StatFunc, error)
}

// ParsePoolConfig parses /etc/pve/user.cfg for pool definitions.
// Returns (vm_pool_map, pools).
// vm_pool_map: vmid -> pool_name
// pools: pool_name -> PoolInfo
func ParsePoolConfig(data string) (map[string]string, map[string]PoolInfo) {
	vmPoolMap := make(map[string]string)
	pools := make(map[string]PoolInfo)

	for _, line := range strings.Split(data, "\n") {
		if !strings.HasPrefix(line, "pool:") {
			continue
		}
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) < 2 {
			continue
		}
		poolName := parts[1]

		poolParts := strings.Split(poolName, "/")
		info := PoolInfo{
			LevelCount: len(poolParts),
		}
		if len(poolParts) > 0 {
			info.Level1 = poolParts[0]
		}
		if len(poolParts) > 1 {
			info.Level2 = poolParts[1]
		}
		if len(poolParts) > 2 {
			info.Level3 = poolParts[2]
		}
		pools[poolName] = info

		// VM list is in parts[3] if it exists
		if len(parts) > 3 && parts[3] != "" {
			for _, vmid := range strings.Split(parts[3], ",") {
				vmid = strings.TrimSpace(vmid)
				if vmid != "" {
					vmPoolMap[vmid] = poolName
				}
			}
		}
	}

	return vmPoolMap, pools
}
