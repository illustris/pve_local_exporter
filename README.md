# pve-local-exporter

Prometheus exporter for Proxmox VE that collects VM and storage metrics directly from the host without using the PVE API. Written in Go.

Metrics are gathered by reading `/proc`, `/sys`, `/etc/pve`, and running `qm monitor` commands.

> **Disclaimer:** This is a heavily vibe-coded rewrite of [pvemon](https://github.com/illustris/pvemon) for better maintainability and easier distribution. This disclaimer will remain up until the codebase has been reviewed and validated.

## Building

Requires [Nix](https://nixos.org/):

```sh
nix build               # build static binary
nix flake check         # evaluate flake + run go vet
```

The output binary is statically linked (`CGO_ENABLED=0`).

## Development

```sh
nix develop                          # shell with go + gopls
nix develop -c go test ./... -race   # run tests with race detector
nix develop -c go vet ./...          # static analysis
```

Go source lives in `src/`. After changing `go.mod` dependencies, run `go mod tidy` then update `vendorHash` in `default.nix` (set to a bogus hash, run `nix build`, copy the correct hash from the error).

## Usage

```sh
pve_local_exporter [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | 9116 | HTTP listen port |
| `--host` | 0.0.0.0 | Bind address |
| `--collect-running-vms` | true | Collect KVM VM metrics |
| `--collect-storage` | true | Collect storage pool metrics |
| `--metrics-prefix` | pve | Prefix for all metric names |
| `--loglevel` | INFO | DEBUG, INFO, WARNING, ERROR |
| `--qm-terminal-timeout` | 10s | qm monitor command timeout |
| `--qm-max-ttl` | 600s | TTL cache for qm monitor data |
| `--qm-rand` | 60s | Jitter range for cache expiry |
| `--qm-monitor-defer-close` | true | Defer closing unresponsive qm sessions |
| `--qm-guest-mem-ttl` | 30s | Cache TTL for guest memory stats from the balloon device (jitter is TTL/6) |
| `--version` | | Print version and exit |

## Exported metrics

All metric names are prefixed with the configured `--metrics-prefix` (default `pve`).

### Per-VM metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `_kvm_info` | gauge | id, name, cpu, pid, pool, pool_levels, pool1, pool2, pool3 | VM info (value 1) |
| `_kvm_cpu_seconds_total` | counter | id, mode | KVM CPU time (mode: user, system, iowait) |
| `_kvm_vcores` | gauge | id | vCores allocated |
| `_kvm_maxmem_bytes` | gauge | id | Maximum memory in bytes |
| `_kvm_memory_percent` | gauge | id | Memory percent of host |
| `_kvm_memory_extended` | gauge | id, type | Extended memory info from /proc status (vmrss, vmpeak, etc.) |
| `_kvm_memory_host_used_bytes` | gauge | id | VM cgroup `memory.current` minus host page cache (same as PVE `memhost`) |
| `_kvm_memory_host_swap_bytes` | gauge | id | VM cgroup `memory.swap.current` |
| `_kvm_guest_memory_total_bytes` | gauge | id | Guest MemTotal, reported by the balloon driver |
| `_kvm_guest_memory_free_bytes` | gauge | id | Guest MemFree (PVE shows total - free as "used") |
| `_kvm_guest_memory_available_bytes` | gauge | id | Guest MemAvailable (free + reclaimable cache) |
| `_kvm_guest_memory_cached_bytes` | gauge | id | Guest page cache |
| `_kvm_guest_memory_swap_in_bytes_total` | counter | id | Guest swap-in |
| `_kvm_guest_memory_swap_out_bytes_total` | counter | id | Guest swap-out |
| `_kvm_guest_memory_major_faults_total` | counter | id | Guest major page faults |
| `_kvm_guest_memory_minor_faults_total` | counter | id | Guest minor page faults |
| `_kvm_guest_memory_oom_kills_total` | counter | id | Guest OOM kills (newer guest kernels only) |
| `_kvm_guest_memory_alloc_stalls_total` | counter | id | Guest allocation stalls (newer guest kernels only) |
| `_kvm_guest_memory_stats_last_update_timestamp_seconds` | gauge | id | When the guest last reported balloon stats |
| `_kvm_threads` | gauge | id | Threads used |
| `_kvm_ctx_switches_total` | counter | id, type | Context switches (type: voluntary, involuntary) |
| `_kvm_io_read_count_total` | counter | id | Read system calls by KVM process |
| `_kvm_io_read_bytes_total` | counter | id | Bytes read from disk by KVM process |
| `_kvm_io_read_chars_total` | counter | id | Bytes read including buffers by KVM process |
| `_kvm_io_write_count_total` | counter | id | Write system calls by KVM process |
| `_kvm_io_write_bytes_total` | counter | id | Bytes written to disk by KVM process |
| `_kvm_io_write_chars_total` | counter | id | Bytes written including buffers by KVM process |
| `_kvm_nic_info` | gauge | id, ifname, netdev, queues, type, model, macaddr | NIC info (value 1) |
| `_kvm_nic_queues` | gauge | id, ifname | NIC queue count |
| `_kvm_nic_{stat}_total` | counter | id, ifname | Per-NIC sysfs counters (rx_bytes, tx_bytes, rx_packets, etc.) |
| `_kvm_disk_info` | gauge | id, disk_name, block_id, disk_path, disk_type, vol_name, pool, pool_name, cluster_id, vg_name, device, attached_to, cache_mode, detect_zeroes, read_only | Disk info (value 1) |
| `_kvm_disk_size_bytes` | gauge | id, disk_name | Disk size in bytes |

### Memory utilization for rightsizing

Host-side metrics like `_kvm_memory_percent` and `_kvm_memory_extended{type="vmrss"}` show how much guest RAM the host has backed with real pages. Once the guest touches a page (page cache, a past spike) it usually stays resident, so RSS creeps toward `maxmem` and stays there. `_kvm_memory_host_used_bytes` has the same problem. Free page reporting (on by default for machine version 6.2+) gives some of it back, but not page cache.

PVE's own "memory usage" is the guest's `MemTotal - MemFree`, read from the balloon device. That's better than RSS but still counts guest page cache as used.

For rightsizing, use the guest's `MemTotal - MemAvailable`, and fall back to the host view for VMs that don't report guest stats:

```promql
(pve_kvm_guest_memory_total_bytes - pve_kvm_guest_memory_available_bytes)
  or on(id) pve_kvm_memory_host_used_bytes
```

To spot VMs that are too small, look at `rate(pve_kvm_guest_memory_major_faults_total[5m])`, `rate(pve_kvm_guest_memory_swap_out_bytes_total[5m])` and `increase(pve_kvm_guest_memory_oom_kills_total[1d])`.

Guest stats need the virtio balloon device, which PVE adds unless `balloon: 0`, and a balloon driver in the guest. Linux has one built in. Windows needs the VirtIO balloon driver and service. PVE enables stats polling (every 2s) when the VM starts. Guest metrics are left out when the device is missing, the guest never reported, or the last report is more than 5 minutes old. Fields the guest kernel doesn't support (QEMU reports -1) are also left out.

Each refresh runs one `qm monitor` per VM, costing about 0.4s of CPU, every `--qm-guest-mem-ttl`.

### Storage metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `_node_storage_info` | gauge | (dynamic, varies by storage config) | Storage pool info (value 1) |
| `_node_storage_size_bytes` | gauge | name, type | Storage total size in bytes |
| `_node_storage_free_bytes` | gauge | name, type | Storage free space in bytes |

### Operational metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `_scrape_duration_seconds` | gauge | | Duration of metrics collection |
| `_exporter_build_info` | gauge | version | Build information (value 1) |

## Architecture

All I/O is behind interfaces for testability. No test touches the real filesystem or runs real commands.

```
src/
  main.go                          HTTP server, signal handling
  internal/
    config/                        CLI flag parsing
    cache/                         TTLCache (with jitter), MtimeCache (file change detection)
    procfs/                        /proc parsing: process discovery, CPU, IO, memory, threads
    sysfs/                         /sys reading: NIC stats, block device sizes, VM cgroup memory
    qmmonitor/                     qm monitor pipe I/O + TTL caches, network/block/balloon guest-stats parsers
    pveconfig/                     /etc/pve parsers: pools (user.cfg), storage (storage.cfg)
    storage/                       statvfs wrapper, zpool list parser
    collector/                     Prometheus Collector wiring all packages together
```

Concurrent NIC, disk and guest memory collection uses a bounded goroutine pool (16 workers).

## License

See [LICENSE](LICENSE) if present.
