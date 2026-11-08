# pve-local-exporter

Proxmox VE local metrics exporter for Prometheus, written in Go. Collects VM and storage metrics directly from the host (no PVE API) by reading /proc, /sys, /etc/pve, and running `qm monitor`.

Reimplementation of the Python exporter at `../pvemon`.

## Build & Test

```sh
nix develop             # shell with go + gopls
nix develop -c go test ./...   # run tests (from src/)
nix build               # build static binary
nix flake check         # evaluate + check flake outputs
```

Go source lives in `src/`. The Nix build uses `buildGoModule` with `CGO_ENABLED=0`.

After changing go.mod dependencies, run `go mod tidy` then update `vendorHash` in `default.nix` (set to a bogus hash, run `nix build`, copy the correct hash from the error).

## Architecture

All I/O is behind interfaces for testability. No test touches the real filesystem or runs real commands.

```
src/
  main.go                          HTTP server, signal handling, log setup
  internal/
    config/                        CLI flag parsing -> Config struct
    cache/                         TTLCache[K,V] (with jitter), MtimeCache[V] (file change detection)
    procfs/                        /proc parsing: process discovery, CPU, IO, memory, threads, ctx switches
    sysfs/                         /sys reading: NIC stats, block device sizes, VM cgroup memory (qemu.slice/<vmid>.scope)
    qmmonitor/                     qm monitor pipe I/O + TTL caches, network/block/balloon guest-stats parsers
    pveconfig/                     /etc/pve parsers: user.cfg (pools), storage.cfg (storage defs)
    storage/                       statvfs wrapper, zpool list parser
    collector/                     Prometheus Collector wiring all packages together
```

### Key interfaces (defined alongside their real implementations)

- `procfs.ProcReader` -- process discovery and /proc/{pid}/* parsing
- `sysfs.SysReader` -- /sys/class/net stats, /sys/block sizes, cgroup v2 memory
- `qmmonitor.QMMonitor` -- `RunCommand(vmid, cmd)`, `InvalidateCache(vmid, cmd)`. `RealQMMonitor` keeps a second short-TTL cache for "volatile" commands (currently only `GuestStatsCommand`)
- `storage.StatFS` -- `Statfs(path)` for dir/nfs/cephfs sizing
- `collector.CommandRunner` -- `Run(name, args...)` for zpool list
- `collector.FileReaderIface` -- `ReadFile(path)` for config files

### Concurrency

Bounded goroutine pool (16 workers) for parallel NIC + disk + guest memory collection per VM, matching the Python ThreadPoolExecutor(max_workers=16).

## Conventions

- Indent nix files with tabs
- No emoji in code or text
- Tests use fixture data / mock interfaces, never real /proc or /sys
- Parser functions are exported and pure (take string, return parsed struct) so tests are straightforward
- `storage.cfg` parser: section headers start at column 0, properties are indented (tab or space)
- `user.cfg` pool format: `pool:name:comment:vmlist` (colon-separated, vmlist is comma-separated VM IDs)
- Memory extended label keys are lowercase without trailing colon (e.g., `vmrss`, `vmpeak`)

## CLI Flags

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
| `--qm-guest-mem-ttl` | 30s | TTL cache for balloon guest memory stats (jitter TTL/6) |
| `--version` | | Print version and exit |

## Memory metrics

- RSS-based metrics (`_kvm_memory_percent`, `_kvm_memory_extended`) overstate usage: touched guest pages stay resident on the host
- Guest view comes from the virtio-balloon device: `qom-get /machine/peripheral/balloon0 guest-stats` via `qm monitor` (JSON, `stat-*` fields, -1 = unsupported, `last-update` unix ts). PVE itself uses `query-balloon` total_mem - free_mem, which includes guest page cache; we also export `stat-available-memory`
- Guest metrics are skipped when balloon0 is absent (`balloon: 0`), `last-update` is 0, or older than `guestStatsMaxAge` (5m)
- Host view `_kvm_memory_host_used_bytes` = cgroup `memory.current - memory.stat:file`, identical to PVE `memhost`
