// This file implements the pre-flight self check (xiancun-agent -check).
//
// 供给端最常见的失败不是程序起不来，而是起起来了却没人能连上：
// 端口被占、控制面地址写错、节点令牌没配、NAT 没做映射。这些都在
// 启动后才暴露，而且日志里不容易看出来，所以做成一条能单独跑的命令，
// 部署脚本和运维巡检都可以直接调用（退出码 0 = 全部通过）。
package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/xiancuncloud/idlemem-core/governor"
	"github.com/xiancuncloud/idlemem-core/heartbeat"
	"github.com/xiancuncloud/idlemem-core/meminfo"
)

// checkItem 一条自检结论。
type checkItem struct {
	name   string
	ok     bool
	detail string
	hint   string
}

// runCheck 执行自检并按结果返回进程退出码。
func runCheck(cfg config, id string) int {
	fmt.Printf("xiancun-agent %s 自检\n", version)
	fmt.Printf("节点 ID：%s\n", id)
	fmt.Printf("控制面：%s\nKV 端口：%s\n\n", cfg.Server, cfg.KVAddr)

	info, share, memItem := checkMemory()
	items := []checkItem{
		memItem,
		checkKVPort(cfg.KVAddr),
		checkControlPlane(cfg, id, info, share),
	}

	failed := 0
	for _, it := range items {
		mark := "[ OK ]"
		if !it.ok {
			mark = "[FAIL]"
			failed++
		}
		fmt.Printf("%s %s\n       %s\n", mark, it.name, it.detail)
		if it.hint != "" {
			fmt.Printf("       建议：%s\n", it.hint)
		}
	}

	if cfg.NodeToken == "" {
		fmt.Print("\n注意：未配置 node_token。若控制面开启了节点鉴权，心跳会被拒绝（401）。\n")
	}

	fmt.Print("\n公网可达性无法在本机自证：网关是从公网回连 kv_addr 的。\n")
	fmt.Print("请在另一台机器（或手机热点网络）上运行 xiancun-client -addr <公网地址>:<端口> ping，\n")
	fmt.Print("能返回 PONG 才说明 NAT/端口映射真的通。\n")

	if failed > 0 {
		fmt.Printf("\n结论：%d 项未通过，先按上面的建议处理再启动服务。\n", failed)
		return 1
	}
	fmt.Print("\n结论：全部通过，可以启动贡献节点。\n")
	return 0
}

// checkMemory 采集内存并计算当前可贡献容量。
func checkMemory() (meminfo.Info, uint64, checkItem) {
	item := checkItem{name: "内存与可贡献容量"}
	info, err := meminfo.SystemProvider{}.Snapshot()
	if err != nil {
		item.detail = err.Error()
		item.hint = "读取系统内存失败，节点无法计算贡献量"
		return info, 0, item
	}
	gov, err := governor.New(governor.DefaultConfig())
	if err != nil {
		item.detail = err.Error()
		item.hint = "贡献治理参数无效"
		return info, 0, item
	}
	share := gov.Share(info)
	item.ok = true
	item.detail = fmt.Sprintf("总内存 %s，可用 %s，使用率 %.1f%%；可贡献 %s（状态 %s）",
		humanBytes(info.Total), humanBytes(info.Available), info.UsedPct,
		humanBytes(share), gov.StateOf())
	if share == 0 {
		item.hint = "当前可用内存低于保底预留，节点暂时没有可贡献容量；不影响启动，空闲后会自动恢复"
	}
	return info, share, item
}

// checkKVPort 验证 KV 端口是否真的可用。
//
// 只做 Listen 不够：Windows 上 Listen(":16380") 会绑到 [::]:16380，
// 与别人已经占用的 IPv4 0.0.0.0:16380 可以共存，"绑定成功"是假的。
// 所以先用一次 dial 探活，能连上就说明端口已经有服务。
func checkKVPort(addr string) checkItem {
	item := checkItem{name: "KV 端口可监听"}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		item.detail = "地址格式不对：" + err.Error()
		item.hint = "写成 :16380 或 0.0.0.0:16380"
		return item
	}
	probe := net.JoinHostPort("127.0.0.1", port)
	if c, derr := net.DialTimeout("tcp", probe, time.Second); derr == nil {
		_ = c.Close()
		item.detail = probe + " 上已有服务在监听"
		item.hint = "端口被占用：先停掉旧进程，或换一个 -kv 端口"
		return item
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		item.detail = err.Error()
		item.hint = "端口可能已被占用，或地址写法不对，例如 :16380"
		return item
	}
	bound := ln.Addr().String()
	_ = ln.Close()
	item.ok = true
	item.detail = "已成功绑定 " + bound + "，本机可服务；公网能否回连见文末提示"
	return item
}

// checkControlPlane 真发一次心跳：同时验证控制面可达、令牌有效、节点被登记。
func checkControlPlane(cfg config, id string, info meminfo.Info, share uint64) checkItem {
	item := checkItem{name: "控制面心跳上报"}
	opts := []heartbeat.HTTPOption{heartbeat.WithTimeout(8 * time.Second)}
	if cfg.NodeToken != "" {
		opts = append(opts, heartbeat.WithHeader("X-Node-Token", cfg.NodeToken))
	}
	rep := heartbeat.NewHTTPReporter(cfg.Server+"/api/v1/nodes/heartbeat", opts...)
	defer rep.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	snap := heartbeat.Snapshot{
		NodeID:        id,
		Version:       version,
		Status:        heartbeat.StatusOnline,
		CapacityBytes: share,
		TotalBytes:    info.Total,
		ReportedAt:    time.Now(),
		KVAddr:        cfg.KVAddr,
	}
	if err := rep.Report(ctx, snap); err != nil {
		item.detail = err.Error()
		switch {
		case strings.Contains(item.detail, "401"), strings.Contains(item.detail, "403"):
			item.hint = "控制面已开启节点鉴权：用服务端 -gen-token 生成令牌，再用 -node-token 传入"
		case strings.Contains(item.detail, "connection refused"),
			strings.Contains(item.detail, "actively refused"),
			strings.Contains(item.detail, "connectex"),
			strings.Contains(item.detail, "no such host"),
			strings.Contains(item.detail, "i/o timeout"):
			item.hint = "控制面不可达：检查 -server 地址、服务是否已启动、防火墙是否放行"
		default:
			item.hint = "上报失败，查看控制面日志与网络"
		}
		return item
	}
	item.ok = true
	item.detail = "心跳已被接受，节点已在控制面登记为 online"
	return item
}

// humanBytes 以 1024 进制格式化字节数，便于在终端里读。
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v := float64(n)
	i := -1
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
