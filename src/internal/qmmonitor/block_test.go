package qmmonitor

import (
	"testing"
)

func TestParseBlockInfo_Qcow2(t *testing.T) {
	raw := `drive-scsi0 (#block100): /mnt/storage/images/100/vm-100-disk-0.qcow2 (qcow2, read-write)
    Attached to:      /machine/peripheral/virtioscsi0/virtio-backend
    Cache mode:       writeback, direct
    Detect zeroes:    on
`
	disks := ParseBlockInfo(raw)
	if len(disks) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(disks))
	}
	d := disks["scsi0"]
	if d.DiskType != "qcow2" {
		t.Errorf("type = %q", d.DiskType)
	}
	if d.BlockID != "100" {
		t.Errorf("block_id = %q", d.BlockID)
	}
	if d.Labels["vol_name"] != "vm-100-disk-0" {
		t.Errorf("vol_name = %q", d.Labels["vol_name"])
	}
	if d.Labels["detect_zeroes"] != "on" {
		t.Errorf("detect_zeroes = %q", d.Labels["detect_zeroes"])
	}
	if d.Labels["cache_mode_writeback"] != "true" {
		t.Errorf("cache_mode_writeback missing")
	}
	if d.Labels["cache_mode_direct"] != "true" {
		t.Errorf("cache_mode_direct missing")
	}
}

func TestParseBlockInfo_Zvol(t *testing.T) {
	raw := `drive-scsi0 (#block200): /dev/zvol/rpool/data/vm-200-disk-0 (raw, read-write)
    Attached to:      /machine/peripheral/virtioscsi0/virtio-backend
`
	disks := ParseBlockInfo(raw)
	d := disks["scsi0"]
	if d.DiskType != "zvol" {
		t.Errorf("type = %q", d.DiskType)
	}
	if d.Labels["pool"] != "rpool/data" {
		t.Errorf("pool = %q", d.Labels["pool"])
	}
	if d.Labels["vol_name"] != "vm-200-disk-0" {
		t.Errorf("vol_name = %q", d.Labels["vol_name"])
	}
}

func TestParseBlockInfo_RBD(t *testing.T) {
	raw := `drive-scsi0 (#block300): /dev/rbd-pve/ceph1/pool1/vm-300-disk-0 (raw, read-write)
`
	disks := ParseBlockInfo(raw)
	d := disks["scsi0"]
	if d.DiskType != "rbd" {
		t.Errorf("type = %q", d.DiskType)
	}
	if d.Labels["cluster_id"] != "ceph1" {
		t.Errorf("cluster_id = %q", d.Labels["cluster_id"])
	}
	if d.Labels["pool"] != "pool1" {
		t.Errorf("pool = %q", d.Labels["pool"])
	}
	if d.Labels["vol_name"] != "vm-300-disk-0" {
		t.Errorf("vol_name = %q", d.Labels["vol_name"])
	}
}

func TestParseBlockInfo_LVM(t *testing.T) {
	raw := `drive-scsi0 (#block400): /dev/myvg/vm-400-disk-0 (raw, read-write)
`
	disks := ParseBlockInfo(raw)
	d := disks["scsi0"]
	if d.DiskType != "lvm" {
		t.Errorf("type = %q", d.DiskType)
	}
	if d.Labels["vg_name"] != "myvg" {
		t.Errorf("vg_name = %q", d.Labels["vg_name"])
	}
	if d.Labels["vol_name"] != "vm-400-disk-0" {
		t.Errorf("vol_name = %q", d.Labels["vol_name"])
	}
}

func TestParseBlockInfo_SkipsEFI(t *testing.T) {
	raw := `drive-efidisk0 (#block500): /dev/zvol/rpool/data/vm-500-disk-1 (raw, read-write)
drive-scsi0 (#block501): /dev/zvol/rpool/data/vm-500-disk-0 (raw, read-write)
`
	disks := ParseBlockInfo(raw)
	if len(disks) != 1 {
		t.Fatalf("expected 1 disk (efidisk skipped), got %d", len(disks))
	}
	if _, ok := disks["efidisk0"]; ok {
		t.Error("efidisk0 should be skipped")
	}
}

func TestHandleJSONPath(t *testing.T) {
	jsonPath := `json:{"driver":"raw","file":{"driver":"host_device","filename":"/dev/zvol/rpool/data/vm-100-disk-0"}}`
	result, err := HandleJSONPath(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if result != "/dev/zvol/rpool/data/vm-100-disk-0" {
		t.Errorf("got %q", result)
	}
}

func TestHandleJSONPath_Nested(t *testing.T) {
	jsonPath := `json:{"driver":"raw","file":{"driver":"copy-on-read","file":{"driver":"host_device","filename":"/dev/rbd-pve/ceph/pool/vm-200-disk-0"}}}`
	result, err := HandleJSONPath(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if result != "/dev/rbd-pve/ceph/pool/vm-200-disk-0" {
		t.Errorf("got %q", result)
	}
}

func TestHandleJSONPath_NoHostDevice(t *testing.T) {
	jsonPath := `json:{"driver":"raw","file":{"driver":"file","filename":"/tmp/test.img"}}`
	_, err := HandleJSONPath(jsonPath)
	if err == nil {
		t.Fatal("expected error for missing host_device")
	}
}

func TestParseBlockInfo_MultiDisk(t *testing.T) {
	raw := `drive-scsi0 (#block100): /dev/zvol/rpool/data/vm-100-disk-0 (raw, read-write)
    Attached to:      /machine/peripheral/virtioscsi0/virtio-backend
drive-scsi1 (#block101): /mnt/storage/images/100/vm-100-disk-1.qcow2 (qcow2, read-write)
    Attached to:      /machine/peripheral/virtioscsi0/virtio-backend
`
	disks := ParseBlockInfo(raw)
	if len(disks) != 2 {
		t.Fatalf("expected 2 disks, got %d", len(disks))
	}
	if disks["scsi0"].DiskType != "zvol" {
		t.Errorf("scsi0 type = %q", disks["scsi0"].DiskType)
	}
	if disks["scsi1"].DiskType != "qcow2" {
		t.Errorf("scsi1 type = %q", disks["scsi1"].DiskType)
	}
}
