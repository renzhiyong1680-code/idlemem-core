// Package governor 负责计算本机可以安全贡献的空闲内存容量。
//
// 核心职责：
//   - 预留保底内存，保证本机用户正常使用不受影响；
//   - 本机内存高水位时自动缩容，低水位时恢复贡献（带迟滞避免抖动）；
//   - 输出"当前可贡献字节数"，供上层调度使用。
package governor

import (
	"sync"

	"github.com/xiancuncloud/idlemem-core/meminfo"
)

// Config 定义贡献治理参数。
type Config struct {
	// ReservedBytes 始终为本机保留的内存字节数，这部分永不贡献。
	ReservedBytes uint64
	// MaxShareBytes 单机最大可贡献字节数上限；0 表示不设上限。
	MaxShareBytes uint64
	// HighUsedPct 本机内存使用率达到该百分比时开始缩容（进入挂起状态）。
	// 取值范围 0-100。
	HighUsedPct float64
	// LowUsedPct 本机内存使用率回落至该百分比以下时恢复贡献。
	// 必须小于 HighUsedPct，形成迟滞区间，防止频繁抖动。
	LowUsedPct float64
}

// Validate 校验配置并返回错误；零值配置视为合法（仅做防御性校验）。
func (c Config) Validate() error {
	if c.HighUsedPct > 100 || c.LowUsedPct > 100 {
		return ErrInvalidPercent
	}
	if c.LowUsedPct >= c.HighUsedPct && c.HighUsedPct > 0 {
		return ErrInvalidHysteresis
	}
	return nil
}

// DefaultConfig 返回适合普通家用/工作室主机的默认配置。
// 默认保底 4GiB，使用率 85% 缩容、70% 恢复。
func DefaultConfig() Config {
	const (
		gib = 1 << 30
	)
	return Config{
		ReservedBytes: 4 * gib,
		HighUsedPct:   85,
		LowUsedPct:    70,
	}
}

// State 描述贡献状态。
type State int

const (
	// StateContributing 当前正在贡献内存。
	StateContributing State = iota
	// StateSuspended 本机内存高负载，已暂停贡献。
	StateSuspended
)

func (s State) String() string {
	switch s {
	case StateContributing:
		return "contributing"
	case StateSuspended:
		return "suspended"
	default:
		return "unknown"
	}
}

// Governor 计算可贡献容量，并跟踪贡献状态。
type Governor struct {
	mu       sync.Mutex
	cfg      Config
	state    State
	last     uint64
	disabled bool // 手动暂停（节点主人通过托盘/菜单栏控制，优先于内存压力判定）
}

// New 创建一个 Governor。
func New(cfg Config) (*Governor, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.HighUsedPct == 0 {
		cfg = DefaultConfig()
	}
	return &Governor{cfg: cfg}, nil
}

// Share 根据当前内存快照计算可贡献字节数，并更新内部状态。
// 返回 0 表示当前不应贡献任何内存。
func (g *Governor) Share(info meminfo.Info) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	cap := g.compute(info)
	g.last = cap
	return cap
}

// StateOf 返回当前贡献状态（不触发计算）。
func (g *Governor) StateOf() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// SetEnabled 手动开启/暂停贡献（节点主人控制）。
// 暂停时强制贡献 0 并进入 suspended 状态；恢复后回到内存压力判定。
func (g *Governor) SetEnabled(enabled bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.disabled = !enabled
	if !enabled {
		g.state = StateSuspended
		g.last = 0
	}
}

// Enabled 返回当前是否允许贡献（手动开关 && 内存压力未挂起）。
func (g *Governor) Enabled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.disabled
}

// compute 在锁内执行实际计算。
func (g *Governor) compute(info meminfo.Info) uint64 {
	// 手动暂停优先于内存压力判定。
	if g.disabled {
		return 0
	}
	// 进入/退出挂起状态的判定。
	if info.UsedPct >= g.cfg.HighUsedPct {
		g.state = StateSuspended
	} else if info.UsedPct < g.cfg.LowUsedPct {
		g.state = StateContributing
	}
	// 迟滞区间内保持原状态。

	if g.state == StateSuspended {
		return 0
	}

	// 可贡献 = 可用内存 - 保底预留；不足则贡献 0。
	var cap uint64
	if info.Available > g.cfg.ReservedBytes {
		cap = info.Available - g.cfg.ReservedBytes
	}
	if g.cfg.MaxShareBytes > 0 && cap > g.cfg.MaxShareBytes {
		cap = g.cfg.MaxShareBytes
	}
	return cap
}
