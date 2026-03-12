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
    sysfs/                         /sys reading: NIC stats, block device sizes
    qmmonitor/                     qm monitor pipe I/O + TTL cache, network/block output parsers
    pveconfig/                     /etc/pve parsers: user.cfg (pools), storage.cfg (storage defs)
    storage/                       statvfs wrapper, zpool list parser
    collector/                     Prometheus Collector wiring all packages together
```

### Key interfaces (defined alongside their real implementations)

- `procfs.ProcReader` -- process discovery and /proc/{pid}/* parsing
- `sysfs.SysReader` -- /sys/class/net stats, /sys/block sizes
- `qmmonitor.QMMonitor` -- `RunCommand(vmid, cmd)`, `InvalidateCache(vmid, cmd)`
- `storage.StatFS` -- `Statfs(path)` for dir/nfs/cephfs sizing
- `collector.CommandRunner` -- `Run(name, args...)` for zpool list
- `collector.FileReaderIface` -- `ReadFile(path)` for config files

### Concurrency

Bounded goroutine pool (16 workers) for parallel NIC + disk collection per VM, matching the Python ThreadPoolExecutor(max_workers=16).

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
| `--version` | | Print version and exit |
