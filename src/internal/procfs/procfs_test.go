package procfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCmdline(t *testing.T) {
	data := []byte("/usr/bin/qemu-system-x86_64\x00-id\x00100\x00-name\x00myvm\x00-cpu\x00host\x00-smp\x004\x00-m\x002048\x00")
	args := ParseCmdline(data)
	if len(args) != 11 {
		t.Fatalf("expected 11 args, got %d: %v", len(args), args)
	}
	if args[0] != "/usr/bin/qemu-system-x86_64" {
		t.Fatalf("unexpected first arg: %s", args[0])
	}
}

func TestFlagValue(t *testing.T) {
	cmdline := []string{"/usr/bin/qemu-system-x86_64", "-id", "100", "-name", "myvm", "-cpu", "host"}
	tests := []struct {
		flag, want string
	}{
		{"-id", "100"},
		{"-name", "myvm"},
		{"-cpu", "host"},
		{"-missing", ""},
	}
	for _, tc := range tests {
		got := FlagValue(cmdline, tc.flag)
		if got != tc.want {
			t.Errorf("FlagValue(%q) = %q, want %q", tc.flag, got, tc.want)
		}
	}
}

func TestFlagValueBase(t *testing.T) {
	cmdline := []string{"-name", "myvm,debug-threads=on", "-cpu", "host,+kvm_pv_eoi,+kvm_pv_unhalt", "-id", "100"}
	tests := []struct {
		flag, want string
	}{
		{"-name", "myvm"},
		{"-cpu", "host"},
		{"-id", "100"},
		{"-missing", ""},
	}
	for _, tc := range tests {
		got := FlagValueBase(cmdline, tc.flag)
		if got != tc.want {
			t.Errorf("FlagValueBase(%q) = %q, want %q", tc.flag, got, tc.want)
		}
	}
}

func TestParseVcores(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string
		want    int
	}{
		{"simple", []string{"-smp", "4"}, 4},
		{"with_opts", []string{"-smp", "4,sockets=1,cores=4,maxcpus=4"}, 4},
		{"missing", []string{"-m", "1024"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseVcores(tc.cmdline)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseMem(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string
		want    int64
	}{
		{
			"simple",
			[]string{"-m", "1024"},
			1024 * 1024, // kB
		},
		{
			"numa",
			[]string{
				"-m", "size=4096M,slots=255,maxmem=524288M",
				"-object", "memory-backend-ram,id=ram-node0,size=2048M",
				"-object", "memory-backend-ram,id=ram-node1,size=2048M",
			},
			4096 * 1024, // 2048+2048 MB in kB
		},
		{
			"missing",
			[]string{"-smp", "4"},
			0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMem(tc.cmdline)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseStat(t *testing.T) {
	// Realistic /proc/{pid}/stat with QEMU process name containing spaces
	// Fields after ')': state(3) ppid(4) pgrp(5) session(6) tty_nr(7) tpgid(8) flags(9)
	// minflt(10) cminflt(11) majflt(12) cmajflt(13) utime(14) stime(15)
	// cutime(16) cstime(17) priority(18) nice(19) num_threads(20) itrealvalue(21)
	// starttime(22) vsize(23) rss(24) rsslim(25) startcode(26) endcode(27) startstack(28)
	// kstkesp(29) kstkeip(30) signal(31) blocked(32) sigignore(33) sigcatch(34) wchan(35)
	// nswap(36) cnswap(37) exit_signal(38) processor(39) rt_priority(40) policy(41)
	// delayacct_blkio_ticks(42)
	stat := `12345 (qemu-system-x86) S 1 12345 12345 0 -1 4194304 1000 0 0 0 500 200 0 0 20 0 50 0 100 1000000 500 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 42`
	cpu, err := ParseStat(stat)
	if err != nil {
		t.Fatal(err)
	}
	if cpu.User != 5.0 { // 500/100
		t.Errorf("User = %f, want 5.0", cpu.User)
	}
	if cpu.System != 2.0 { // 200/100
		t.Errorf("System = %f, want 2.0", cpu.System)
	}
	if cpu.IOWait != 0.42 { // 42/100
		t.Errorf("IOWait = %f, want 0.42", cpu.IOWait)
	}
}

func TestParseIO(t *testing.T) {
	data := `rchar: 123456
wchar: 789012
syscr: 100
syscw: 200
read_bytes: 4096
write_bytes: 8192
cancelled_write_bytes: 0
`
	io, err := ParseIO(data)
	if err != nil {
		t.Fatal(err)
	}
	if io.ReadChars != 123456 {
		t.Errorf("ReadChars = %d", io.ReadChars)
	}
	if io.WriteChars != 789012 {
		t.Errorf("WriteChars = %d", io.WriteChars)
	}
	if io.ReadSyscalls != 100 {
		t.Errorf("ReadSyscalls = %d", io.ReadSyscalls)
	}
	if io.WriteSyscalls != 200 {
		t.Errorf("WriteSyscalls = %d", io.WriteSyscalls)
	}
	if io.ReadBytes != 4096 {
		t.Errorf("ReadBytes = %d", io.ReadBytes)
	}
	if io.WriteBytes != 8192 {
		t.Errorf("WriteBytes = %d", io.WriteBytes)
	}
}

func TestParseStatus(t *testing.T) {
	data := `Name:	qemu-system-x86
Threads:	50
VmPeak:	1000 kB
VmRSS:	500 kB
VmData:	200 kB
RssAnon:	100 kB
HugetlbPages:	0 kB
voluntary_ctxt_switches:	1234
nonvoluntary_ctxt_switches:	56
`
	info, err := ParseStatus(data)
	if err != nil {
		t.Fatal(err)
	}

	// Threads
	if info.Threads != 50 {
		t.Errorf("Threads = %d, want 50", info.Threads)
	}

	// VmRSS
	if info.VmRSS != 500*1024 {
		t.Errorf("VmRSS = %d, want %d", info.VmRSS, 500*1024)
	}

	// Memory extended
	if info.MemoryExtended["vmpeak"] != 1000*1024 {
		t.Errorf("VmPeak = %d", info.MemoryExtended["vmpeak"])
	}
	if info.MemoryExtended["vmrss"] != 500*1024 {
		t.Errorf("VmRSS = %d", info.MemoryExtended["vmrss"])
	}
	if info.MemoryExtended["vmdata"] != 200*1024 {
		t.Errorf("VmData = %d", info.MemoryExtended["vmdata"])
	}
	if info.MemoryExtended["rssanon"] != 100*1024 {
		t.Errorf("RssAnon = %d", info.MemoryExtended["rssanon"])
	}
	if info.MemoryExtended["hugetlbpages"] != 0 {
		t.Errorf("HugetlbPages = %d", info.MemoryExtended["hugetlbpages"])
	}

	// Context switches
	if info.CtxSwitches.Voluntary != 1234 {
		t.Errorf("Voluntary = %d", info.CtxSwitches.Voluntary)
	}
	if info.CtxSwitches.Involuntary != 56 {
		t.Errorf("Involuntary = %d", info.CtxSwitches.Involuntary)
	}
}

func TestParseStatus_NoThreads(t *testing.T) {
	data := `Name:	qemu
VmRSS:	100 kB
`
	_, err := ParseStatus(data)
	if err == nil {
		t.Fatal("expected error for missing Threads")
	}
}

func TestParseStat_Malformed(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"no_closing_paren", "12345 (qemu S 1 12345"},
		{"truncated", "12345 (qemu) S 1 2 3"},
		{"empty", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseStat(tc.data)
			if err == nil {
				t.Fatal("expected error for malformed stat")
			}
		})
	}
}

func TestParseIO_Empty(t *testing.T) {
	io, err := ParseIO("")
	if err != nil {
		t.Fatal(err)
	}
	if io.ReadChars != 0 || io.WriteChars != 0 {
		t.Errorf("expected zero counters, got %+v", io)
	}
}

func TestParseIO_MalformedLines(t *testing.T) {
	data := "rchar: notanumber\nbadline\nwchar: 100\n"
	io, err := ParseIO(data)
	if err != nil {
		t.Fatal(err)
	}
	if io.ReadChars != 0 {
		t.Errorf("ReadChars = %d, want 0 (parse failure)", io.ReadChars)
	}
	if io.WriteChars != 100 {
		t.Errorf("WriteChars = %d, want 100", io.WriteChars)
	}
}

// TestDiscoverQEMUProcesses_DeletedExe verifies that QEMU processes whose
// /proc/{pid}/exe has a " (deleted)" suffix (common after package upgrades)
// are still discovered.
func TestDiscoverQEMUProcesses_DeletedExe(t *testing.T) {
	// Build a fake /proc tree with two "QEMU" PIDs:
	//   1000 -> normal exe
	//   1001 -> exe with " (deleted)" suffix
	tmpDir := t.TempDir()
	procDir := filepath.Join(tmpDir, "proc")
	pveCfgDir := filepath.Join(tmpDir, "pve")

	cmdline100 := "/usr/bin/qemu-system-x86_64\x00-id\x00100\x00-name\x00vm100,debug-threads=on\x00-cpu\x00host,+kvm_pv_eoi,+kvm_pv_unhalt\x00-smp\x004\x00-m\x002048\x00"
	cmdline101 := "/usr/bin/qemu-system-x86_64\x00-id\x00101\x00-name\x00vm101\x00-cpu\x00host\x00-smp\x002\x00-m\x001024\x00"

	for _, tc := range []struct {
		pid, vmid, exe, cmdline string
	}{
		{"1000", "100", "/usr/bin/qemu-system-x86_64", cmdline100},
		{"1001", "101", "/usr/bin/qemu-system-x86_64 (deleted)", cmdline101},
	} {
		pidDir := filepath.Join(procDir, tc.pid)
		if err := os.MkdirAll(pidDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Create a real file as the symlink target, then symlink "exe" -> that file.
		// os.Readlink returns the target path, which is what DiscoverQEMUProcesses reads.
		target := filepath.Join(tmpDir, "bin-"+tc.pid)
		if err := os.WriteFile(target, nil, 0o755); err != nil {
			t.Fatal(err)
		}
		// We can't make Readlink return an arbitrary string with a real symlink,
		// so instead we write the exe path to a regular file and override the
		// readlink behavior. But DiscoverQEMUProcesses uses os.Readlink...
		// The trick: symlink to the exact path string. On Linux, symlink targets
		// don't need to exist -- Readlink returns the raw target.
		if err := os.Symlink(tc.exe, filepath.Join(pidDir, "exe")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte(tc.cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
		// Create VM config so VMConfigExists returns true
		if err := os.MkdirAll(pveCfgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pveCfgDir, tc.vmid+".conf"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r := &RealProcReader{
		ProcPath:    procDir,
		PVECfgPath: pveCfgDir,
	}
	procs, err := r.DiscoverQEMUProcesses()
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 2 {
		t.Fatalf("expected 2 procs, got %d", len(procs))
	}

	// Collect discovered VMIDs
	vmids := map[string]bool{}
	for _, p := range procs {
		vmids[p.VMID] = true
	}
	if !vmids["100"] {
		t.Error("VM 100 (normal exe) not discovered")
	}
	if !vmids["101"] {
		t.Error("VM 101 (deleted exe) not discovered")
	}

	// Verify comma-separated options are stripped from name and cpu
	for _, p := range procs {
		if p.VMID == "100" {
			if p.Name != "vm100" {
				t.Errorf("VM 100 name = %q, want %q", p.Name, "vm100")
			}
			if p.CPU != "host" {
				t.Errorf("VM 100 cpu = %q, want %q", p.CPU, "host")
			}
		}
	}
}
