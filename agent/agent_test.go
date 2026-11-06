package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/renzhiyong1680-code/idlemem-core/governor"
	"github.com/renzhiyong1680-code/idlemem-core/heartbeat"
	"github.com/renzhiyong1680-code/idlemem-core/meminfo"
)

// fakeMem 可控的内存快照源。
type fakeMem struct {
	mu   sync.Mutex
	info meminfo.Info
}

func (f *fakeMem) Snapshot() (meminfo.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info, nil
}

func (f *fakeMem) set(i meminfo.Info) {
	f.mu.Lock()
	f.info = i
	f.mu.Unlock()
}

// fakeReporter 记录收到的所有心跳。
type fakeReporter struct {
	mu    sync.Mutex
	snaps []heartbeat.Snapshot
}

func (f *fakeReporter) Report(_ context.Context, s heartbeat.Snapshot) error {
	f.mu.Lock()
	f.snaps = append(f.snaps, s)
	f.mu.Unlock()
	return nil
}

func (f *fakeReporter) Close() error { return nil }

func (f *fakeReporter) last() (heartbeat.Snapshot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.snaps) == 0 {
		return heartbeat.Snapshot{}, false
	}
	return f.snaps[len(f.snaps)-1], true
}

func (f *fakeReporter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.snaps)
}

func TestAgent_RunsAndReports(t *testing.T) {
	mem := &fakeMem{info: meminfo.Info{
		Total:     32 << 30,
		Available: 24 << 30,
		Used:      8 << 30,
		UsedPct:   25,
	}}
	rep := &fakeReporter{}
	a, err := New(Config{
		NodeID:         "test-node",
		MemProvider:    mem,
		Reporter:       rep,
		ReportInterval: 20 * time.Millisecond,
		GovernorCfg:    governorConfigForTest(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		cancel()
		a.Stop()
	}()

	// 等待若干心跳。
	deadline := time.Now().Add(2 * time.Second)
	for rep.count() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	snap, ok := rep.last()
	if !ok {
		t.Fatal("no heartbeat received")
	}
	if snap.NodeID != "test-node" {
		t.Fatalf("NodeID = %q", snap.NodeID)
	}
	if snap.CapacityBytes == 0 {
		t.Fatalf("CapacityBytes = 0, want > 0 (idle mem)")
	}
	if snap.Status != heartbeat.StatusOnline {
		t.Fatalf("Status = %q, want online", snap.Status)
	}
}

func TestAgent_SuspendOnHighLoad(t *testing.T) {
	mem := &fakeMem{info: meminfo.Info{
		Total:     32 << 30,
		Available: 24 << 30,
		Used:      8 << 30,
		UsedPct:   25,
	}}
	rep := &fakeReporter{}
	a, err := New(Config{
		NodeID:         "test-node",
		MemProvider:    mem,
		Reporter:       rep,
		ReportInterval: 20 * time.Millisecond,
		GovernorCfg:    governorConfigForTest(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_ = a.Start(ctx)
	defer func() {
		cancel()
		a.Stop()
	}()

	// 先确认贡献中。
	deadline := time.Now().Add(time.Second)
	for a.Capacity() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.Capacity() == 0 {
		t.Fatal("expected contributing before load spike")
	}

	// 模拟本机高负载。
	mem.set(meminfo.Info{
		Total:     32 << 30,
		Available: 2 << 30,
		Used:      30 << 30,
		UsedPct:   94,
	})

	deadline = time.Now().Add(2 * time.Second)
	for a.Capacity() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.Capacity() != 0 {
		t.Fatalf("capacity after high load = %d, want 0", a.Capacity())
	}
	if a.Status() != heartbeat.StatusSuspended {
		t.Fatalf("status = %q, want suspended", a.Status())
	}
}

func TestAgent_CapacityCallback(t *testing.T) {
	mem := &fakeMem{info: meminfo.Info{
		Total:     32 << 30,
		Available: 24 << 30,
		Used:      8 << 30,
		UsedPct:   25,
	}}
	rep := &fakeReporter{}

	var cbMu sync.Mutex
	changes := []uint64{}
	cb := func(old, new uint64) {
		cbMu.Lock()
		changes = append(changes, new)
		cbMu.Unlock()
	}

	a, err := New(Config{
		NodeID:           "test-node",
		MemProvider:      mem,
		Reporter:         rep,
		ReportInterval:   20 * time.Millisecond,
		GovernorCfg:      governorConfigForTest(),
		CapacityChangeCb: cb,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_ = a.Start(ctx)
	defer func() {
		cancel()
		a.Stop()
	}()

	deadline := time.Now().Add(time.Second)
	for {
		cbMu.Lock()
		n := len(changes)
		cbMu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cbMu.Lock()
	defer cbMu.Unlock()
	if len(changes) == 0 {
		t.Fatal("capacity callback never fired")
	}
	if changes[0] == 0 {
		t.Fatalf("first capacity change should be non-zero, got 0")
	}
}

func TestAgent_Validate(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected validation error for empty config")
	}
}

// governorConfigForTest 返回测试用的贡献治理配置（保底 4GiB）。
func governorConfigForTest() governor.Config { return governor.DefaultConfig() }
