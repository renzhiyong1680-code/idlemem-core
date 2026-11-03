//go:build windows

package meminfo

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx 对应 Windows 的 MEMORYSTATUSEX 结构体。
// 结构体布局必须与 kernel32.GlobalMemoryStatusEx 一致。
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")
)

// Snapshot 通过 GlobalMemoryStatusEx 获取系统物理内存信息。
func (SystemProvider) Snapshot() (Info, error) {
	var ms memoryStatusEx
	ms.length = uint32(unsafe.Sizeof(ms))

	r1, _, err := procGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&ms)))
	if r1 == 0 {
		return Info{}, err
	}

	info := Info{
		Total:     ms.totalPhys,
		Available: ms.availPhys,
	}
	info.Used = info.Total - info.Available
	if info.Total > 0 {
		info.UsedPct = float64(info.Used) / float64(info.Total) * 100
	}
	return info, nil
}
