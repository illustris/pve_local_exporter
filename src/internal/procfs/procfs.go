package procfs

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"pve_local_exporter/internal/logging"
)

const clkTck = 100 // sysconf(_SC_CLK_TCK) on Linux

// QEMUProcess holds info discovered from /proc for a QEMU VM.
type QEMUProcess struct {
	PID    int
	VMID   string
	Name   string
	CPU    string
	Vcores int
	MaxMem int64 // in kB (parsed from cmdline)
}

// CPUTimes holds parsed CPU times from /proc/{pid}/stat.
type CPUTimes struct {
	User   float64
	System float64
	IOWait float64
}

// IOCounters holds parsed I/O counters from /proc/{pid}/io.
type IOCounters struct {
	ReadChars  uint64
	WriteChars uint64
	ReadSyscalls  uint64
	WriteSyscalls uint64
	ReadBytes  uint64
	WriteBytes uint64
}

// CtxSwitches holds context switch counts from /proc/{pid}/status.
type CtxSwitches struct {
	Voluntary   uint64
	Involuntary uint64
}

// MemoryExtended holds memory info from /proc/{pid}/status (values in bytes).
type MemoryExtended map[string]int64

// StatusInfo holds all fields parsed from /proc/{pid}/status in a single read.
type StatusInfo struct {
	Threads        int
	VmRSS          int64 // bytes
	MemoryExtended MemoryExtended
	CtxSwitches    CtxSwitches
}

// ProcReader abstracts /proc access for testability.
type ProcReader interface {
	DiscoverQEMUProcesses() ([]QEMUProcess, error)
	GetCPUTimes(pid int) (CPUTimes, error)
	GetIOCounters(pid int) (IOCounters, error)
	GetStatus(pid int) (StatusInfo, error)
	GetMemoryPercent(pid int, rssBytes int64) (float64, error)
	VMConfigExists(vmid string) bool
}

// RealProcReader reads from the actual /proc filesystem.
type RealProcReader struct {
	ProcPath    string // default "/proc"
	PVECfgPath string // default "/etc/pve/qemu-server"
}

func NewRealProcReader() *RealProcReader {
	return &RealProcReader{
		ProcPath:    "/proc",
		PVECfgPath: "/etc/pve/qemu-server",
	}
}

func (r *RealProcReader) DiscoverQEMUProcesses() ([]QEMUProcess, error) {
	entries, err := os.ReadDir(r.ProcPath)
	if err != nil {
		return nil, err
	}

	numericPIDs := 0
	var procs []QEMUProcess
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		numericPIDs++

		exe, err := os.Readlink(filepath.Join(r.ProcPath, e.Name(), "exe"))
		if err != nil {
			logging.Trace("proc readlink failed", "pid", pid, "err", err)
			continue
		}
		if exe != "/usr/bin/qemu-system-x86_64" && exe != "/usr/bin/qemu-system-x86_64 (deleted)" {
			logging.Trace("proc exe skip", "pid", pid, "exe", exe)
			continue
		}

		cmdlineBytes, err := os.ReadFile(filepath.Join(r.ProcPath, e.Name(), "cmdline"))
		if err != nil {
			logging.Trace("proc cmdline read failed", "pid", pid, "err", err)
			continue
		}
		cmdline := ParseCmdline(cmdlineBytes)

		vmid := FlagValue(cmdline, "-id")
		if vmid == "" {
			logging.Trace("proc no -id flag", "pid", pid)
			continue
		}
		if !r.VMConfigExists(vmid) {
			logging.Trace("proc no config", "pid", pid, "vmid", vmid)
			continue
		}

		proc := QEMUProcess{
			PID:  pid,
			VMID: vmid,
			Name: FlagValue(cmdline, "-name"),
			CPU:  FlagValue(cmdline, "-cpu"),
		}
		proc.Vcores = ParseVcores(cmdline)
		proc.MaxMem = ParseMem(cmdline)
		logging.Trace("proc discovered VM", "pid", pid, "vmid", vmid, "name", proc.Name)
		procs = append(procs, proc)
	}

	logging.Trace("proc scan complete", "numeric_pids", numericPIDs, "qemu_count", len(procs))
	if len(procs) == 0 {
		slog.Warn("no QEMU processes discovered", "numeric_pids", numericPIDs, "proc_path", r.ProcPath, "pve_cfg_path", r.PVECfgPath)
	}
	return procs, nil
}

func (r *RealProcReader) VMConfigExists(vmid string) bool {
	_, err := os.Stat(filepath.Join(r.PVECfgPath, vmid+".conf"))
	return err == nil
}

func (r *RealProcReader) GetCPUTimes(pid int) (CPUTimes, error) {
	data, err := os.ReadFile(filepath.Join(r.ProcPath, strconv.Itoa(pid), "stat"))
	if err != nil {
		return CPUTimes{}, err
	}
	return ParseStat(string(data))
}

func (r *RealProcReader) GetIOCounters(pid int) (IOCounters, error) {
	data, err := os.ReadFile(filepath.Join(r.ProcPath, strconv.Itoa(pid), "io"))
	if err != nil {
		return IOCounters{}, err
	}
	return ParseIO(string(data))
}

func (r *RealProcReader) GetStatus(pid int) (StatusInfo, error) {
	data, err := os.ReadFile(filepath.Join(r.ProcPath, strconv.Itoa(pid), "status"))
	if err != nil {
		return StatusInfo{}, err
	}
	return ParseStatus(string(data))
}

func (r *RealProcReader) GetMemoryPercent(pid int, rssBytes int64) (float64, error) {
	meminfoData, err := os.ReadFile(filepath.Join(r.ProcPath, "meminfo"))
	if err != nil {
		return 0, err
	}
	totalMem := int64(0)
	for _, line := range strings.Split(string(meminfoData), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				totalMem, _ = strconv.ParseInt(parts[1], 10, 64)
				totalMem *= 1024 // kB to bytes
			}
			break
		}
	}
	if totalMem == 0 {
		return 0, nil
	}
	return float64(rssBytes) / float64(totalMem) * 100.0, nil
}

// ParseCmdline splits a null-byte separated /proc/{pid}/cmdline.
func ParseCmdline(data []byte) []string {
	s := string(data)
	if len(s) == 0 {
		return nil
	}
	// Remove trailing null byte if present
	s = strings.TrimRight(s, "\x00")
	return strings.Split(s, "\x00")
}

// FlagValue returns the value after a flag in cmdline args.
func FlagValue(cmdline []string, flag string) string {
	for i, arg := range cmdline {
		if arg == flag && i+1 < len(cmdline) {
			return cmdline[i+1]
		}
	}
	return ""
}

// ParseVcores extracts vCPU count from -smp flag.
// -smp can be just a number or key=value pairs like "4,sockets=1,cores=4,maxcpus=4"
func ParseVcores(cmdline []string) int {
	smp := FlagValue(cmdline, "-smp")
	if smp == "" {
		return 0
	}
	// Try simple numeric
	parts := strings.Split(smp, ",")
	n, err := strconv.Atoi(parts[0])
	if err == nil {
		return n
	}
	// Try key=value format
	for _, p := range parts {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 && kv[0] == "cpus" {
			n, _ = strconv.Atoi(kv[1])
			return n
		}
	}
	return 0
}

// ParseMem extracts max memory in kB from cmdline.
// Simple: -m 1024 -> 1024*1024 kB
// NUMA: memory-backend-ram...size=NM -> sum * 1024 kB
func ParseMem(cmdline []string) int64 {
	mVal := FlagValue(cmdline, "-m")
	if mVal == "" {
		return 0
	}
	// Simple numeric case
	if n, err := strconv.ParseInt(mVal, 10, 64); err == nil {
		return n * 1024 // MB to kB
	}
	// NUMA case: search for memory-backend-ram in all args
	var total int64
	for _, arg := range cmdline {
		if strings.Contains(arg, "memory-backend-ram") {
			// Format: ...size=XXXM
			for _, part := range strings.Split(arg, ",") {
				if strings.HasPrefix(part, "size=") {
					sizeStr := strings.TrimPrefix(part, "size=")
					if strings.HasSuffix(sizeStr, "M") {
						sizeStr = strings.TrimSuffix(sizeStr, "M")
						if n, err := strconv.ParseInt(sizeStr, 10, 64); err == nil {
							total += n * 1024 // MB to kB
						}
					}
				}
			}
		}
	}
	return total
}

// ParseStat extracts CPU times from /proc/{pid}/stat.
// Fields: (1-indexed) 14=utime, 15=stime, 42=delayacct_blkio_ticks
func ParseStat(data string) (CPUTimes, error) {
	// Find the closing paren of comm field to handle spaces in process names
	closeIdx := strings.LastIndex(data, ")")
	if closeIdx < 0 {
		return CPUTimes{}, fmt.Errorf("malformed stat: no closing paren")
	}
	// Fields after ") " are 1-indexed starting at field 3
	rest := data[closeIdx+2:]
	fields := strings.Fields(rest)
	// field 14 (utime) is at index 14-3=11, field 15 (stime) at 12, field 42 at 39
	if len(fields) < 40 {
		return CPUTimes{}, fmt.Errorf("not enough fields in stat: %d", len(fields))
	}
	utime, _ := strconv.ParseUint(fields[11], 10, 64)
	stime, _ := strconv.ParseUint(fields[12], 10, 64)
	blkio, _ := strconv.ParseUint(fields[39], 10, 64)
	return CPUTimes{
		User:   float64(utime) / clkTck,
		System: float64(stime) / clkTck,
		IOWait: float64(blkio) / clkTck,
	}, nil
}

// ParseIO parses /proc/{pid}/io.
func ParseIO(data string) (IOCounters, error) {
	var io IOCounters
	for _, line := range strings.Split(data, "\n") {
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			continue
		}
		val, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			continue
		}
		switch parts[0] {
		case "rchar":
			io.ReadChars = val
		case "wchar":
			io.WriteChars = val
		case "syscr":
			io.ReadSyscalls = val
		case "syscw":
			io.WriteSyscalls = val
		case "read_bytes":
			io.ReadBytes = val
		case "write_bytes":
			io.WriteBytes = val
		}
	}
	return io, nil
}

// ParseStatus parses /proc/{pid}/status in one pass, extracting threads, VmRSS,
// memory extended fields, and context switches.
func ParseStatus(data string) (StatusInfo, error) {
	var info StatusInfo
	info.MemoryExtended = make(MemoryExtended)
	foundThreads := false

	for _, line := range strings.Split(data, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		switch {
		case parts[0] == "Threads:":
			n, err := strconv.Atoi(parts[1])
			if err != nil {
				return StatusInfo{}, fmt.Errorf("parse Threads: %w", err)
			}
			info.Threads = n
			foundThreads = true
		case parts[0] == "voluntary_ctxt_switches:":
			info.CtxSwitches.Voluntary, _ = strconv.ParseUint(parts[1], 10, 64)
		case parts[0] == "nonvoluntary_ctxt_switches:":
			info.CtxSwitches.Involuntary, _ = strconv.ParseUint(parts[1], 10, 64)
		case strings.HasPrefix(line, "Vm") || strings.HasPrefix(line, "Rss") || strings.HasPrefix(line, "Hugetlb"):
			key := strings.ToLower(strings.TrimSuffix(parts[0], ":"))
			val, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				continue
			}
			if len(parts) >= 3 && parts[2] == "kB" {
				val *= 1024
			}
			info.MemoryExtended[key] = val
			if key == "vmrss" {
				info.VmRSS = val
			}
		}
	}

	if !foundThreads {
		return StatusInfo{}, fmt.Errorf("Threads field not found")
	}
	return info, nil
}
