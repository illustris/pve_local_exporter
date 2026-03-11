package qmmonitor

import (
	"testing"
)

func TestParseNetworkInfo_Single(t *testing.T) {
	raw := `net0: index=0,type=tap,ifname=tap100i0,script=/var/lib/qemu-server/pve-bridge,downscript=/var/lib/qemu-server/pve-bridgedown,model=virtio-net-pci,macaddr=AA:BB:CC:DD:EE:FF`

	nics := ParseNetworkInfo(raw)
	if len(nics) != 1 {
		t.Fatalf("expected 1 NIC, got %d", len(nics))
	}
	nic := nics[0]
	if nic.Netdev != "net0" {
		t.Errorf("netdev = %q", nic.Netdev)
	}
	if nic.Queues != 1 {
		t.Errorf("queues = %d", nic.Queues)
	}
	if nic.Type != "tap" {
		t.Errorf("type = %q", nic.Type)
	}
	if nic.Model != "virtio-net-pci" {
		t.Errorf("model = %q", nic.Model)
	}
	if nic.Macaddr != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("macaddr = %q", nic.Macaddr)
	}
	if nic.Ifname != "tap100i0" {
		t.Errorf("ifname = %q", nic.Ifname)
	}
}

func TestParseNetworkInfo_Multiqueue(t *testing.T) {
	raw := `net0: index=0,type=tap,ifname=tap100i0,model=virtio-net-pci,macaddr=AA:BB:CC:DD:EE:FF
 \ net0: index=1,type=tap,ifname=tap100i0
 \ net0: index=2,type=tap,ifname=tap100i0
 \ net0: index=3,type=tap,ifname=tap100i0
net1: index=0,type=tap,ifname=tap100i1,model=virtio-net-pci,macaddr=11:22:33:44:55:66`

	nics := ParseNetworkInfo(raw)
	if len(nics) != 2 {
		t.Fatalf("expected 2 NICs, got %d", len(nics))
	}

	byName := map[string]NICInfo{}
	for _, n := range nics {
		byName[n.Netdev] = n
	}

	if byName["net0"].Queues != 4 {
		t.Errorf("net0 queues = %d, want 4", byName["net0"].Queues)
	}
	if byName["net1"].Queues != 1 {
		t.Errorf("net1 queues = %d, want 1", byName["net1"].Queues)
	}
}

func TestParseNetworkInfo_Empty(t *testing.T) {
	nics := ParseNetworkInfo("")
	if len(nics) != 0 {
		t.Fatalf("expected 0 NICs, got %d", len(nics))
	}
}
