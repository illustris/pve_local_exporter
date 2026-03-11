package storage

import (
	"testing"
)

type mockStatFS struct {
	sizes map[string]StorageSize
}

func (m mockStatFS) Statfs(path string) (StorageSize, error) {
	if s, ok := m.sizes[path]; ok {
		return s, nil
	}
	return StorageSize{}, nil
}

func TestGetDirStorageSize(t *testing.T) {
	fs := mockStatFS{sizes: map[string]StorageSize{
		"/var/lib/vz": {Total: 1000000, Free: 500000},
	}}
	s, err := GetDirStorageSize(fs, "/var/lib/vz")
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 1000000 {
		t.Errorf("total = %d", s.Total)
	}
	if s.Free != 500000 {
		t.Errorf("free = %d", s.Free)
	}
}

func TestGetZPoolSize(t *testing.T) {
	output := `NAME    SIZE          ALLOC         FREE          CKPOINT  EXPANDSZ   FRAG    CAP  DEDUP    HEALTH  ALTROOT
rpool   1073741824    536870912     536870912     -        -          10%     50%  1.00x    ONLINE  -
`
	s, err := GetZPoolSize(output)
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 1073741824 {
		t.Errorf("total = %d", s.Total)
	}
	if s.Free != 536870912 {
		t.Errorf("free = %d", s.Free)
	}
}

func TestGetZPoolSize_BadOutput(t *testing.T) {
	_, err := GetZPoolSize("bad")
	if err == nil {
		t.Fatal("expected error")
	}
}
