//go:build linux

package meminfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Snapshot 解析 /proc/meminfo 获取系统物理内存信息。
// 优先使用 MemAvailable（内核 3.14+），缺失时回退 MemFree+Buffer+Cached 近似值。
func (SystemProvider) Snapshot() (Info, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return Info{}, err
	}
	defer f.Close()

	var (
		total, free, buffers, cached, available uint64
		haveAvailable                           bool
	)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		val, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		// /proc/meminfo 以 kB 为单位。
		val *= 1024
		switch key {
		case "MemTotal":
			total = val
		case "MemFree":
			free = val
		case "Buffers":
			buffers = val
		case "Cached":
			cached = val
		case "MemAvailable":
			available = val
			haveAvailable = true
		}
	}
	if err := scanner.Err(); err != nil {
		return Info{}, err
	}

	if !haveAvailable {
		available = free + buffers + cached
	}

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
