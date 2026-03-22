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
| `--version` | | Print version and exit |

## Exported metrics

All metric names are prefixed with the configured `--metrics-prefix` (default `pve`).

### Per-VM metrics

| Metric | Labels | Description |
|--------|--------|-------------|
| `_kvm` | id, name, cpu, pid, pool, pool_levels, pool1-3 | VM info (value 1) |
| `_kvm_cpu` | id, mode | CPU time (user/system/iowait) |
| `_kvm_vcores` | id | Allocated vCPU count |
| `_kvm_maxmem` | id | Maximum memory in bytes |
| `_kvm_memory_percent` | id | RSS as percent of host memory |
| `_kvm_memory_extended` | id, type | Detailed memory fields from /proc status |
| `_kvm_threads` | id | Thread count |
| `_kvm_ctx_switches` | id, type | Context switches (voluntary/involuntary) |
| `_kvm_io_read_bytes` | id | I/O read bytes |
| `_kvm_io_write_bytes` | id | I/O write bytes |
| `_kvm_io_read_chars` | id | I/O read chars |
| `_kvm_io_write_chars` | id | I/O write chars |
| `_kvm_io_read_count` | id | I/O read syscalls |
| `_kvm_io_write_count` | id | I/O write syscalls |
| `_kvm_nic` | id, ifname, netdev, queues, type, model, macaddr | NIC info (value 1) |
| `_kvm_nic_queues` | id, ifname | NIC queue count |
| `_kvm_nic_*` | id, ifname | Per-NIC sysfs counters (rx_bytes, tx_bytes, etc.) |
| `_kvm_disk` | id, disk_name, block_id, disk_path, disk_type, ... | Disk info (value 1) |
| `_kvm_disk_size` | id, disk_name | Disk size in bytes |

### Storage metrics

| Metric | Labels | Description |
|--------|--------|-------------|
| `_node_storage` | name, type, ... | Storage pool info (value 1) |
| `_node_storage_size` | name, type | Total storage size in bytes |
| `_node_storage_free` | name, type | Free storage space in bytes |

## Architecture

All I/O is behind interfaces for testability. No test touches the real filesystem or runs real commands.

```
src/
  main.go                          HTTP server, signal handling
  internal/
    config/                        CLI flag parsing
    cache/                         TTLCache (with jitter), MtimeCache (file change detection)
    procfs/                        /proc parsing: process discovery, CPU, IO, memory, threads
    sysfs/                         /sys reading: NIC stats, block device sizes
    qmmonitor/                     qm monitor pipe I/O + TTL cache, network/block parsers
    pveconfig/                     /etc/pve parsers: pools (user.cfg), storage (storage.cfg)
    storage/                       statvfs wrapper, zpool list parser
    collector/                     Prometheus Collector wiring all packages together
```

Concurrent NIC and disk collection uses a bounded goroutine pool (16 workers).

## License

See [LICENSE](LICENSE) if present.
