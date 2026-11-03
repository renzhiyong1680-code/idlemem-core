//go:build darwin

package meminfo

import (
	"os/exec"
	"strconv"
	"strings"
)

// Snapshot 通过 sysctl 获取总内存，通过 vm_stat 计算可用内存。
// 可用内存按 macOS 惯例取 free + inactive 页面。
func (SystemProvider) Snapshot() (Info, error) {
	total, err := sysctlUint64("hw.memsize")
	if err != nil {
		return Info{}, err
	}
	pageSize, err := sysctlUint64("hw.pagesize")
	if err != nil {
		return Info{}, err
	}

	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return Info{}, err
	}

	var freePages, inactivePages uint64
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Pages free:") {
			freePages = parseVMPage(line)
		} else if strings.HasPrefix(line, "Pages inactive:") {
			inactivePages = parseVMPage(line)
		}
	}

	available := (freePages + inactivePages) * pageSize
	info := Info{
		Total:     total,
		Available: available,
	}
	info.Used = info.Total - info.Available
	if info.Total > 0 {
		info.UsedPct = float64(info.Used) / float64(info.Total) * 100
	}
	return info, nil
}

// sysctlUint64 通过 /usr/sbin/sysctl 读取无符号 64 位系统参数。
// 不使用 syscall.SysctlUint64：新版 Go 的 syscall 包已不在 darwin 上提供该 API。
func sysctlUint64(name string) (uint64, error) {
	out, err := exec.Command("/usr/sbin/sysctl", "-n", name).Output()
	if err != nil {
		return 0, err
	}
	raw := strings.TrimSpace(string(out))
	return strconv.ParseUint(raw, 10, 64)
}

// parseVMPage 从形如 "Pages free:  12345." 的行中提取页面数。
func parseVMPage(line string) uint64 {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return 0
	}
	raw := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[idx+1:]), "."))
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
