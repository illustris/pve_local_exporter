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
	if d.Labels["cache_mode"] != "writeback, direct" {
		t.Errorf("cache_mode = %q, want %q", d.Labels["cache_mode"], "writeback, direct")
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

func TestParseBlockInfo_ReadOnly(t *testing.T) {
	raw := `drive-scsi0 (#block100): /dev/zvol/rpool/data/vm-100-disk-0 (raw, read-only)
`
	disks := ParseBlockInfo(raw)
	d := disks["scsi0"]
	if d.Labels["read_only"] != "true" {
		t.Errorf("expected read_only=true, got %q", d.Labels["read_only"])
	}
}

func TestParseBlockInfo_ReadWrite(t *testing.T) {
	raw := `drive-scsi0 (#block100): /dev/zvol/rpool/data/vm-100-disk-0 (raw, read-write)
`
	disks := ParseBlockInfo(raw)
	d := disks["scsi0"]
	if _, ok := d.Labels["read_only"]; ok {
		t.Error("read_only label should not be set for read-write disks")
	}
}

func TestParseBlockInfo_MalformedHeader(t *testing.T) {
	raw := `drive-scsi0: this is not a valid header
`
	disks := ParseBlockInfo(raw)
	if len(disks) != 0 {
		t.Fatalf("expected 0 disks for malformed header, got %d", len(disks))
	}
}

func TestParseBlockInfo_Empty(t *testing.T) {
	disks := ParseBlockInfo("")
	if len(disks) != 0 {
		t.Fatalf("expected 0 disks for empty input, got %d", len(disks))
	}
}

func TestParseBlockInfo_JSONError(t *testing.T) {
	raw := `drive-scsi0 (#block100): json:{invalid json} (raw, read-write)
`
	disks := ParseBlockInfo(raw)
	if len(disks) != 0 {
		t.Fatalf("expected 0 disks for invalid JSON path, got %d", len(disks))
	}
}

func TestParseBlockInfo_Throttle(t *testing.T) {
	// PVE 9.x / newer QEMU format: no (#blockN)
	raw := `drive-scsi0: json:{"driver":"raw","file":{"driver":"host_device","filename":"/dev/zvol/rpool/data/vm-100-disk-0"}} (throttle, read-write)
    Attached to:      /machine/peripheral/virtioscsi0/virtio-backend
    Cache mode:       writeback, direct
    Detect zeroes:    unmap
`
	disks := ParseBlockInfo(raw)
	if len(disks) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(disks))
	}
	d := disks["scsi0"]
	if d.BlockID != "" {
		t.Errorf("block_id = %q, want empty", d.BlockID)
	}
	if d.DiskType != "zvol" {
		t.Errorf("type = %q, want zvol", d.DiskType)
	}
	if d.DiskPath != "/dev/zvol/rpool/data/vm-100-disk-0" {
		t.Errorf("path = %q", d.DiskPath)
	}
	if d.Labels["pool"] != "rpool/data" {
		t.Errorf("pool = %q", d.Labels["pool"])
	}
	if d.Labels["vol_name"] != "vm-100-disk-0" {
		t.Errorf("vol_name = %q", d.Labels["vol_name"])
	}
	if d.Labels["detect_zeroes"] != "unmap" {
		t.Errorf("detect_zeroes = %q, want unmap", d.Labels["detect_zeroes"])
	}
	if d.Labels["cache_mode"] != "writeback, direct" {
		t.Errorf("cache_mode = %q", d.Labels["cache_mode"])
	}
}

func TestParseBlockInfo_DetectZeroesUnmap(t *testing.T) {
	raw := `drive-scsi0 (#block100): /dev/zvol/rpool/data/vm-100-disk-0 (raw, read-write)
    Detect zeroes:    unmap
`
	disks := ParseBlockInfo(raw)
	d := disks["scsi0"]
	if d.Labels["detect_zeroes"] != "unmap" {
		t.Errorf("detect_zeroes = %q, want unmap", d.Labels["detect_zeroes"])
	}
}

func TestParseBlockInfo_AttachedToVirtio(t *testing.T) {
	raw := `drive-scsi0 (#block100): /dev/zvol/rpool/data/vm-100-disk-0 (raw, read-write)
    Attached to:      /machine/peripheral/virtio0/virtio-backend
`
	disks := ParseBlockInfo(raw)
	d := disks["scsi0"]
	if d.Labels["attached_to"] != "virtio0" {
		t.Errorf("attached_to = %q, want %q", d.Labels["attached_to"], "virtio0")
	}
}

func TestParseBlockInfo_AttachedToVirtioScsi(t *testing.T) {
	raw := `drive-scsi0 (#block100): /dev/zvol/rpool/data/vm-100-disk-0 (raw, read-write)
    Attached to:      /machine/peripheral/virtioscsi0/virtio-backend
`
	disks := ParseBlockInfo(raw)
	d := disks["scsi0"]
	if d.Labels["attached_to"] != "virtioscsi0" {
		t.Errorf("attached_to = %q, want %q", d.Labels["attached_to"], "virtioscsi0")
	}
}

func TestParseBlockInfo_AttachedToBare(t *testing.T) {
	raw := `drive-ide2 (#block100): /path/to/disk.iso (raw, read-only)
    Attached to:      ide2
`
	disks := ParseBlockInfo(raw)
	d := disks["ide2"]
	if d.Labels["attached_to"] != "ide2" {
		t.Errorf("attached_to = %q, want %q", d.Labels["attached_to"], "ide2")
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

func TestParseBlockInfo_ThrottleJSONWithDriveInValue(t *testing.T) {
	// PVE 9.x format: throttle JSON contains "throttle-drive-*" in values,
	// which previously caused strings.Split("drive-") to break mid-JSON.
	raw := `drive-ide2: json:{"throttle-group": "throttle-drive-ide2", "driver": "throttle", "file": {"driver": "raw", "file": {"driver": "host_device", "filename": "/dev/zvol/rpool/data/vm-100-cloudinit"}}} (throttle, read-only)
    Attached to:      ide2
    Removable device: locked, tray closed
    Cache mode:       writeback

drive-virtio0: json:{"throttle-group": "throttle-drive-virtio0", "driver": "throttle", "file": {"driver": "raw", "file": {"driver": "host_device", "filename": "/dev/rbd-pve/00000000-0000-0000-0000-000000000001/pool1/vm-100-disk-0"}}} (throttle)
    Attached to:      /machine/peripheral/virtio0/virtio-backend
    Cache mode:       writeback
    Detect zeroes:    on`
	disks := ParseBlockInfo(raw)
	// ide2 is cloudinit (not efidisk), so it should be included
	if len(disks) != 2 {
		t.Fatalf("expected 2 disks, got %d", len(disks))
	}
	d := disks["virtio0"]
	if d.DiskType != "rbd" {
		t.Errorf("type = %q, want rbd", d.DiskType)
	}
	if d.DiskPath != "/dev/rbd-pve/00000000-0000-0000-0000-000000000001/pool1/vm-100-disk-0" {
		t.Errorf("path = %q", d.DiskPath)
	}
	if d.Labels["attached_to"] != "virtio0" {
		t.Errorf("attached_to = %q", d.Labels["attached_to"])
	}
	if d.Labels["detect_zeroes"] != "on" {
		t.Errorf("detect_zeroes = %q", d.Labels["detect_zeroes"])
	}
}

func TestParseBlockInfo_OldFormatNoMode(t *testing.T) {
	// Old PVE format with (#blockN) but no read-write/read-only suffix
	raw := `drive-virtio0 (#block109): /dev/zvol/data/vm-100-disk-0 (raw)
    Attached to:      /machine/peripheral/virtio0/virtio-backend
    Cache mode:       writeback, direct
    Detect zeroes:    on
`
	disks := ParseBlockInfo(raw)
	if len(disks) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(disks))
	}
	d := disks["virtio0"]
	if d.BlockID != "109" {
		t.Errorf("block_id = %q, want 109", d.BlockID)
	}
	if d.DiskType != "zvol" {
		t.Errorf("type = %q, want zvol", d.DiskType)
	}
	if d.Labels["cache_mode"] != "writeback, direct" {
		t.Errorf("cache_mode = %q", d.Labels["cache_mode"])
	}
}
