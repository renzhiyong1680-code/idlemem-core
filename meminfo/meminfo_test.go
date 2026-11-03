package meminfo

import "testing"

// TestSystemProviderSnapshot 在本机真实采集一次内存信息做冒烟验证。
func TestSystemProviderSnapshot(t *testing.T) {
	info, err := (SystemProvider{}).Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if info.Total == 0 {
		t.Fatal("Total is 0, unexpected on a real machine")
	}
	if info.Available > info.Total {
		t.Fatalf("Available %d > Total %d", info.Available, info.Total)
	}
	if info.UsedPct < 0 || info.UsedPct > 100 {
		t.Fatalf("UsedPct %v out of range", info.UsedPct)
	}
	if info.Used > info.Total {
		t.Fatalf("Used %d > Total %d", info.Used, info.Total)
	}
}
