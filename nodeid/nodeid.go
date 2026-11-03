// Package nodeid 生成稳定的节点标识。
//
// 节点 ID 应在本机保持不变（跨重启、跨 Agent 版本），
// 以便调度控制面对节点进行信誉累计与统计。
// 当前实现基于机器指纹（主机名 + 首块非回环网卡 MAC）哈希生成。
package nodeid

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
)

// Generate 生成稳定的节点 ID（64 位十六进制字符串）。
func Generate() (string, error) {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}
	mac := primaryMAC()
	raw := hostname + "|" + mac
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8]), nil
}

// primaryMAC 返回第一块非回环、状态为 up 的网卡硬件地址；失败返回空串。
func primaryMAC() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		if len(ifc.HardwareAddr) == 0 {
			continue
		}
		return strings.ToLower(ifc.HardwareAddr.String())
	}
	return ""
}

// String 输出可读形式（便于日志展示）。
func String() string {
	id, err := Generate()
	if err != nil {
		return fmt.Sprintf("nodeid-error:%v", err)
	}
	return id
}
