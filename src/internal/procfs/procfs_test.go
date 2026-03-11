package procfs

import (
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

func TestParseThreads(t *testing.T) {
	data := `Name:	qemu-system-x86
Threads:	50
VmPeak:	1234 kB
`
	n, err := ParseThreads(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != 50 {
		t.Errorf("got %d, want 50", n)
	}
}

func TestParseMemoryExtended(t *testing.T) {
	data := `Name:	qemu-system-x86
VmPeak:	1000 kB
VmRSS:	500 kB
VmData:	200 kB
RssAnon:	100 kB
HugetlbPages:	0 kB
`
	m := ParseMemoryExtended(data)
	if m["vmpeak:"] != 1000*1024 {
		t.Errorf("VmPeak = %d", m["vmpeak:"])
	}
	if m["vmrss:"] != 500*1024 {
		t.Errorf("VmRSS = %d", m["vmrss:"])
	}
	if m["vmdata:"] != 200*1024 {
		t.Errorf("VmData = %d", m["vmdata:"])
	}
	if m["rssanon:"] != 100*1024 {
		t.Errorf("RssAnon = %d", m["rssanon:"])
	}
	if m["hugetlbpages:"] != 0 {
		t.Errorf("HugetlbPages = %d", m["hugetlbpages:"])
	}
}

func TestParseCtxSwitches(t *testing.T) {
	data := `Name:	qemu
voluntary_ctxt_switches:	1234
nonvoluntary_ctxt_switches:	56
`
	cs, err := ParseCtxSwitches(data)
	if err != nil {
		t.Fatal(err)
	}
	if cs.Voluntary != 1234 {
		t.Errorf("Voluntary = %d", cs.Voluntary)
	}
	if cs.Involuntary != 56 {
		t.Errorf("Involuntary = %d", cs.Involuntary)
	}
}
