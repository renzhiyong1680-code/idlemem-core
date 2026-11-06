package governor

import (
	"testing"

	"github.com/renzhiyong1680-code/idlemem-core/meminfo"
)

func mustNew(t *testing.T, cfg Config) *Governor {
	t.Helper()
	g, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func TestShare_Basic(t *testing.T) {
	g := mustNew(t, Config{
		ReservedBytes: 4 << 30,
		MaxShareBytes: 32 << 30,
		HighUsedPct:   85,
		LowUsedPct:    70,
	})
	info := meminfo.Info{
		Total:     32 << 30,
		Available: 24 << 30,
		Used:      8 << 30,
		UsedPct:   25,
	}
	got := g.Share(info)
	want := uint64(20 << 30) // 24GiB 可用 - 4GiB 预留 = 20GiB，未超上限
	if got != want {
		t.Fatalf("Share = %d, want %d", got, want)
	}
	if g.StateOf() != StateContributing {
		t.Fatalf("state = %v, want contributing", g.StateOf())
	}
}

func TestShare_CappedByMax(t *testing.T) {
	g := mustNew(t, Config{
		ReservedBytes: 1 << 30,
		MaxShareBytes: 8 << 30,
		HighUsedPct:   85,
		LowUsedPct:    70,
	})
	info := meminfo.Info{
		Total:     64 << 30,
		Available: 48 << 30,
		Used:      16 << 30,
		UsedPct:   25,
	}
	got := g.Share(info)
	if got != 8<<30 {
		t.Fatalf("Share = %d, want capped %d", got, 8<<30)
	}
}

func TestShare_SuspendWhenHighLoad(t *testing.T) {
	g := mustNew(t, Config{
		ReservedBytes: 4 << 30,
		HighUsedPct:   85,
		LowUsedPct:    70,
	})
	// 高负载：使用率 92% > 85%，必须挂起。
	info := meminfo.Info{
		Total:     32 << 30,
		Available: 2 << 30,
		Used:      30 << 30,
		UsedPct:   92,
	}
	if got := g.Share(info); got != 0 {
		t.Fatalf("Share at high load = %d, want 0", got)
	}
	if g.StateOf() != StateSuspended {
		t.Fatalf("state = %v, want suspended", g.StateOf())
	}
}

func TestShare_HysteresisStaysSuspended(t *testing.T) {
	g := mustNew(t, Config{
		ReservedBytes: 4 << 30,
		HighUsedPct:   85,
		LowUsedPct:    70,
	})
	// 先进入挂起。
	g.Share(meminfo.Info{Total: 32 << 30, Available: 2 << 30, Used: 30 << 30, UsedPct: 92})
	// 使用率回落到 75%（处于 70-85 迟滞区间），应保持挂起。
	info := meminfo.Info{
		Total:     32 << 30,
		Available: 8 << 30,
		Used:      24 << 30,
		UsedPct:   75,
	}
	if got := g.Share(info); got != 0 {
		t.Fatalf("Share in hysteresis = %d, want 0 (stay suspended)", got)
	}
}

func TestShare_RecoverBelowLow(t *testing.T) {
	g := mustNew(t, Config{
		ReservedBytes: 4 << 30,
		HighUsedPct:   85,
		LowUsedPct:    70,
	})
	g.Share(meminfo.Info{Total: 32 << 30, Available: 2 << 30, Used: 30 << 30, UsedPct: 92})
	// 使用率回落到 50% < 70%，恢复贡献。
	info := meminfo.Info{
		Total:     32 << 30,
		Available: 16 << 30,
		Used:      16 << 30,
		UsedPct:   50,
	}
	if got := g.Share(info); got != 12<<30 {
		t.Fatalf("Share after recover = %d, want %d", got, 12<<30)
	}
	if g.StateOf() != StateContributing {
		t.Fatalf("state = %v, want contributing", g.StateOf())
	}
}

func TestShare_NoRoomReturnsZero(t *testing.T) {
	g := mustNew(t, Config{
		ReservedBytes: 4 << 30,
		HighUsedPct:   85,
		LowUsedPct:    70,
	})
	// 可用内存小于预留，贡献 0。
	info := meminfo.Info{
		Total:     32 << 30,
		Available: 3 << 30,
		Used:      29 << 30,
		UsedPct:   90,
	}
	if got := g.Share(info); got != 0 {
		t.Fatalf("Share = %d, want 0", got)
	}
}

func TestValidate_Errors(t *testing.T) {
	cases := []Config{
		{HighUsedPct: 120, LowUsedPct: 10},
		{HighUsedPct: 60, LowUsedPct: 80},
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
}
