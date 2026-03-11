package pveconfig

import (
	"testing"
)

func TestSanitizeKey(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"with-dash", "with_dash"},
		{"with.dot", "with_dot"},
		{"with space", "with_space"},
		{"key123", "key123"},
	}
	for _, tc := range tests {
		got := SanitizeKey(tc.in)
		if got != tc.want {
			t.Errorf("SanitizeKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseStorageConfig_Basic(t *testing.T) {
	data := `dir: local
	path /var/lib/vz
	content iso,vztmpl,backup
	maxfiles 3

zfspool: local-zfs
	pool rpool/data
	content images,rootdir
	sparse 1

nfs: nas-backup
	export /mnt/backup
	path /mnt/pve/nas-backup
	server 10.0.0.1
	content backup
`
	entries := ParseStorageConfig(data)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Check dir entry
	e := entries[0]
	if e.Properties["type"] != "dir" {
		t.Errorf("type = %q", e.Properties["type"])
	}
	if e.Properties["name"] != "local" {
		t.Errorf("name = %q", e.Properties["name"])
	}
	if e.Properties["path"] != "/var/lib/vz" {
		t.Errorf("path = %q", e.Properties["path"])
	}
	if e.Properties["content"] != "iso,vztmpl,backup" {
		t.Errorf("content = %q", e.Properties["content"])
	}

	// Check zfspool entry
	e = entries[1]
	if e.Properties["type"] != "zfspool" {
		t.Errorf("type = %q", e.Properties["type"])
	}
	if e.Properties["name"] != "local_zfs" {
		t.Errorf("name = %q, want local_zfs", e.Properties["name"])
	}
	if e.Properties["pool"] != "rpool/data" {
		t.Errorf("pool = %q", e.Properties["pool"])
	}

	// Check nfs entry
	e = entries[2]
	if e.Properties["type"] != "nfs" {
		t.Errorf("type = %q", e.Properties["type"])
	}
	if e.Properties["server"] != "10.0.0.1" {
		t.Errorf("server = %q", e.Properties["server"])
	}
}

func TestParseStorageConfig_Comments(t *testing.T) {
	data := `# This is a comment
dir: local
	path /var/lib/vz
	# inline comment
	content iso
`
	entries := ParseStorageConfig(data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestParseStorageConfig_BooleanValue(t *testing.T) {
	data := `zfspool: tank
	pool rpool/data
	sparse
`
	entries := ParseStorageConfig(data)
	if entries[0].Properties["sparse"] != "true" {
		t.Errorf("sparse = %q, want 'true'", entries[0].Properties["sparse"])
	}
}
