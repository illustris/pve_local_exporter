package pveconfig

import (
	"testing"
)

func TestParsePoolConfig_Basic(t *testing.T) {
	data := `user:root@pam:1:0:::root@pam:
pool:production:Some comment:100,200,300
pool:staging:Staging env:400
pool:production/tier1:Tier 1:500,600
`
	vmMap, pools := ParsePoolConfig(data)

	// Check VM mappings
	if vmMap["100"] != "production" {
		t.Errorf("VM 100 pool = %q, want production", vmMap["100"])
	}
	if vmMap["200"] != "production" {
		t.Errorf("VM 200 pool = %q", vmMap["200"])
	}
	if vmMap["400"] != "staging" {
		t.Errorf("VM 400 pool = %q", vmMap["400"])
	}
	if vmMap["500"] != "production/tier1" {
		t.Errorf("VM 500 pool = %q", vmMap["500"])
	}

	// Check pool info
	prod := pools["production"]
	if prod.LevelCount != 1 || prod.Level1 != "production" {
		t.Errorf("production pool = %+v", prod)
	}

	tier1 := pools["production/tier1"]
	if tier1.LevelCount != 2 || tier1.Level1 != "production" || tier1.Level2 != "tier1" {
		t.Errorf("production/tier1 pool = %+v", tier1)
	}
}

func TestParsePoolConfig_NoVMs(t *testing.T) {
	data := `pool:empty:No VMs:
`
	vmMap, pools := ParsePoolConfig(data)
	if len(vmMap) != 0 {
		t.Errorf("expected no VM mappings, got %d", len(vmMap))
	}
	if _, ok := pools["empty"]; !ok {
		t.Error("expected 'empty' pool to exist")
	}
}

func TestParsePoolConfig_ThreeLevels(t *testing.T) {
	data := `pool:a/b/c::deep:100
`
	_, pools := ParsePoolConfig(data)
	p := pools["a/b/c"]
	if p.LevelCount != 3 || p.Level1 != "a" || p.Level2 != "b" || p.Level3 != "c" {
		t.Errorf("pool = %+v", p)
	}
}
