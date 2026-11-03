// Package meminfo 提供跨平台的系统物理内存信息采集。
//
// 该包零第三方依赖，仅使用标准库与系统调用：
//   - Windows: GlobalMemoryStatusEx (kernel32)
//   - Linux:   /proc/meminfo
//   - macOS:   sysctl + vm_stat
package meminfo

// Info 描述系统物理内存的一次快照，单位均为字节。
type Info struct {
	// Total 物理内存总量。
	Total uint64
	// Available 当前可用内存（含缓存可回收部分）。
	Available uint64
	// Used 已使用内存。
	Used uint64
	// UsedPct 使用率百分比，取值范围 0-100。
	UsedPct float64
}

// Provider 抽象内存信息来源，便于测试注入或替换实现。
type Provider interface {
	// Snapshot 获取当前系统内存快照。
	Snapshot() (Info, error)
}

// SystemProvider 读取真实系统内存信息。
type SystemProvider struct{}
