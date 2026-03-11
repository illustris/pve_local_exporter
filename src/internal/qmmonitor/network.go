package qmmonitor

import (
	"strconv"
	"strings"
)

// NICInfo holds parsed network interface info from "info network".
type NICInfo struct {
	Netdev  string
	Queues  int
	Type    string
	Model   string
	Macaddr string
	Ifname  string
}

// ParseNetworkInfo parses the output of "info network" from qm monitor.
// Format: "net0: index=0,type=tap,ifname=tap100i0,model=virtio-net-pci,macaddr=AA:BB:CC:DD:EE:FF"
// Multiqueue lines: " \ net0: index=1,type=tap,ifname=tap100i0"
// For multiqueue, same netdev appears multiple times with increasing index; queues = max(index)+1.
func ParseNetworkInfo(raw string) []NICInfo {
	nicsMap := make(map[string]map[string]string)

	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Strip leading "\ " from continuation lines
		line = strings.TrimPrefix(line, "\\ ")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		colonIdx := strings.Index(line, ": ")
		if colonIdx < 0 {
			continue
		}
		netdev := line[:colonIdx]
		if !strings.HasPrefix(netdev, "net") {
			continue
		}

		cfg := line[colonIdx+2:]
		if _, ok := nicsMap[netdev]; !ok {
			nicsMap[netdev] = make(map[string]string)
		}
		for _, pair := range strings.Split(cfg, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			eqIdx := strings.Index(pair, "=")
			if eqIdx < 0 {
				continue
			}
			key := pair[:eqIdx]
			value := pair[eqIdx+1:]
			// Overwrite so last index wins (for multiqueue)
			nicsMap[netdev][key] = value
		}
	}

	var result []NICInfo
	for netdev, cfg := range nicsMap {
		idx, _ := strconv.Atoi(cfg["index"])
		result = append(result, NICInfo{
			Netdev:  netdev,
			Queues:  idx + 1,
			Type:    cfg["type"],
			Model:   cfg["model"],
			Macaddr: cfg["macaddr"],
			Ifname:  cfg["ifname"],
		})
	}
	return result
}
