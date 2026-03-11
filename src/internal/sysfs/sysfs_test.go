package sysfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadInterfaceStats(t *testing.T) {
	// Create temp sysfs-like structure
	tmpDir := t.TempDir()
	statsDir := filepath.Join(tmpDir, "class", "net", "tap100i0", "statistics")
	os.MkdirAll(statsDir, 0755)

	os.WriteFile(filepath.Join(statsDir, "rx_bytes"), []byte("123456\n"), 0644)
	os.WriteFile(filepath.Join(statsDir, "tx_bytes"), []byte("789012\n"), 0644)
	os.WriteFile(filepath.Join(statsDir, "rx_packets"), []byte("100\n"), 0644)

	reader := &RealSysReader{SysPath: tmpDir}
	stats, err := reader.ReadInterfaceStats("tap100i0")
	if err != nil {
		t.Fatal(err)
	}

	if stats["rx_bytes"] != 123456 {
		t.Errorf("rx_bytes = %d", stats["rx_bytes"])
	}
	if stats["tx_bytes"] != 789012 {
		t.Errorf("tx_bytes = %d", stats["tx_bytes"])
	}
	if stats["rx_packets"] != 100 {
		t.Errorf("rx_packets = %d", stats["rx_packets"])
	}
}

func TestReadInterfaceStats_NotFound(t *testing.T) {
	reader := &RealSysReader{SysPath: t.TempDir()}
	_, err := reader.ReadInterfaceStats("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent interface")
	}
}

func TestGetBlockDeviceSize(t *testing.T) {
	tmpDir := t.TempDir()

	// Create /sys/block/dm-0/size
	blockDir := filepath.Join(tmpDir, "block", "dm-0")
	os.MkdirAll(blockDir, 0755)
	// 1GB = 2097152 sectors of 512 bytes
	os.WriteFile(filepath.Join(blockDir, "size"), []byte("2097152\n"), 0644)

	// Create a "device" symlink that points to dm-0
	devDir := filepath.Join(tmpDir, "dev")
	os.MkdirAll(devDir, 0755)
	os.Symlink(filepath.Join(devDir, "dm-0"), filepath.Join(devDir, "mydev"))
	// Create the actual "device" file so symlink resolves
	os.WriteFile(filepath.Join(devDir, "dm-0"), []byte{}, 0644)

	reader := &RealSysReader{SysPath: tmpDir}
	size, err := reader.GetBlockDeviceSize(filepath.Join(devDir, "dm-0"))
	if err != nil {
		t.Fatal(err)
	}
	expected := int64(2097152 * 512)
	if size != expected {
		t.Errorf("size = %d, want %d", size, expected)
	}
}
