// Package agent 把内存采集、贡献治理、KV 引擎与心跳上报整合为
// 一个完整的"闲置内存贡献节点"核心，供各端（Windows/macOS/Linux）
// 外壳程序嵌入复用。
package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/renzhiyong1680-code/idlemem-core/governor"
	"github.com/renzhiyong1680-code/idlemem-core/heartbeat"
	"github.com/renzhiyong1680-code/idlemem-core/kvstore"
	"github.com/renzhiyong1680-code/idlemem-core/meminfo"
)

// Version 核心库版本号，会随心跳上报。
const Version = "0.1.0"

// Config 组装一个贡献节点所需的全部依赖与参数。
type Config struct {
	// NodeID 节点唯一标识（由 nodeid.Generate 生成）。
	NodeID string
	// Version 客户端版本号（默认为 Version）。
	Version string

	// MemProvider 系统内存信息来源。
	MemProvider meminfo.Provider
	// GovernorCfg 贡献治理参数。
	GovernorCfg governor.Config
	// Reporter 心跳上报器。
	Reporter heartbeat.Reporter

	// Store 内存 KV 引擎（可为 nil，此时创建默认引擎）。
	Store *kvstore.Store
	// StoreOpts 创建默认 KV 引擎时的选项。
	StoreOpts kvstore.Options

	// ReportInterval 心跳上报间隔；0 时取默认 10s。
	ReportInterval time.Duration
	// CapacityChangeCb 容量变化回调（贡献量增加/减少/归零时触发）。
	CapacityChangeCb func(oldBytes, newBytes uint64)
	// KVAddr 本节点 KV 服务监听地址（随心跳上报，供网关转发）。
	KVAddr string
}

// Validate 校验必要依赖，缺失时报错。
func (c Config) Validate() error {
	if c.NodeID == "" {
		return errors.New("agent: NodeID is required")
	}
	if c.MemProvider == nil {
		return errors.New("agent: MemProvider is required")
	}
	if c.Reporter == nil {
		return errors.New("agent: Reporter is required")
	}
	return nil
}

// Agent 是闲置内存贡献节点的核心控制器。
type Agent struct {
	cfg Config

	gov     *governor.Governor
	store   *kvstore.Store
	started time.Time

	mu        sync.Mutex
	capacity  uint64 // 当前可贡献字节数
	usedBytes uint64 // KV 引擎当前占用
	status    heartbeat.Status

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New 创建 Agent 并完成依赖校验与默认值填充。
func New(cfg Config) (*Agent, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Version == "" {
		cfg.Version = Version
	}
	gov, err := governor.New(cfg.GovernorCfg)
	if err != nil {
		return nil, fmt.Errorf("agent: governor: %w", err)
	}
	if cfg.Store == nil {
		cfg.Store = kvstore.New(cfg.StoreOpts)
	}
	if cfg.ReportInterval <= 0 {
		cfg.ReportInterval = 10 * time.Second
	}
	if cfg.CapacityChangeCb == nil {
		cfg.CapacityChangeCb = func(uint64, uint64) {}
	}
	return &Agent{
		cfg:     cfg,
		gov:     gov,
		store:   cfg.Store,
		status:  heartbeat.StatusOnline,
		started: time.Now(),
	}, nil
}

// Start 启动后台采样与上报循环。ctx 取消后循环退出。
func (a *Agent) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	a.wg.Add(1)
	go a.run(ctx)
	return nil
}

// Stop 停止后台循环并释放资源。
func (a *Agent) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	if a.cfg.Reporter != nil {
		_ = a.cfg.Reporter.Close()
	}
}

// run 周期性执行：采样内存 → 计算贡献容量 → 上报心跳。
func (a *Agent) run(ctx context.Context) {
	defer a.wg.Done()

	ticker := time.NewTicker(a.cfg.ReportInterval)
	defer ticker.Stop()

	// 启动即先跑一次，避免首个周期空窗。
	a.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.tick(ctx)
		}
	}
}

// tick 执行一轮采样、容量决策与心跳上报。
func (a *Agent) tick(ctx context.Context) {
	info, err := a.cfg.MemProvider.Snapshot()
	if err != nil {
		a.setStatus(heartbeat.StatusSuspended)
		// 采样失败时保守处理：容量归零，避免过度承诺。
		a.setCapacity(0)
		return
	}

	share := a.gov.Share(info)
	status := heartbeat.StatusOnline
	if share == 0 {
		status = heartbeat.StatusSuspended
	}
	a.setStatus(status)
	a.setCapacity(share)

	// 上报心跳。
	snap := heartbeat.Snapshot{
		NodeID:        a.cfg.NodeID,
		Version:       a.cfg.Version,
		Status:        status,
		CapacityBytes: share,
		UsedBytes:     a.usedBytes,
		SystemUsedPct: info.UsedPct,
		TotalBytes:    info.Total,
		UptimeSeconds: int64(time.Since(a.started).Seconds()),
		ReportedAt:    time.Now(),
		KVAddr:        a.cfg.KVAddr,
	}
	if err := a.cfg.Reporter.Report(ctx, snap); err != nil {
		// 心跳失败不致命，下一周期重试。
		_ = err
	}
}

// setCapacity 更新容量并触发回调。
func (a *Agent) setCapacity(newBytes uint64) {
	a.mu.Lock()
	old := a.capacity
	a.capacity = newBytes
	a.mu.Unlock()
	if old != newBytes {
		a.cfg.CapacityChangeCb(old, newBytes)
	}
}

func (a *Agent) setStatus(s heartbeat.Status) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
}

// Capacity 返回当前可贡献容量（字节）。
func (a *Agent) Capacity() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.capacity
}

// Status 返回当前状态。
func (a *Agent) Status() heartbeat.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Store 返回 KV 引擎句柄，供外壳程序提供 KV 服务。
func (a *Agent) Store() *kvstore.Store {
	return a.store
}

// Governor 返回贡献治理器（可查询状态等）。
func (a *Agent) Governor() *governor.Governor {
	return a.gov
}

// SetUsedBytes 由外壳程序上报 KV 引擎实际占用字节数，随下次心跳发出。
func (a *Agent) SetUsedBytes(n uint64) {
	a.mu.Lock()
	a.usedBytes = n
	a.mu.Unlock()
}
