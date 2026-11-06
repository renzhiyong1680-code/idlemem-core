// Command basic 演示如何嵌入 idlemem-core 搭建一个"闲置内存贡献节点"。
//
// 运行：go run ./examples/basic
// 行为：每 2 秒采样一次系统内存，计算可贡献容量，打印心跳快照；
//
//	并按 Ctrl+C 优雅退出。真实接入平台时只需替换 NoopReporter
//	为 heartbeat.NewHTTPReporter(平台端点)。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/renzhiyong1680-code/idlemem-core/agent"
	"github.com/renzhiyong1680-code/idlemem-core/governor"
	"github.com/renzhiyong1680-code/idlemem-core/heartbeat"
	"github.com/renzhiyong1680-code/idlemem-core/meminfo"
	"github.com/renzhiyong1680-code/idlemem-core/nodeid"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	id, err := nodeid.Generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate node id:", err)
		os.Exit(1)
	}

	// 阶段1：构造一个"心跳观察者"，真实环境替换为 HTTPReporter。
	reporter := &printReporter{}

	// 阶段2：组装 Agent（核心库唯一入口）。
	a, err := agent.New(agent.Config{
		NodeID:         id,
		MemProvider:    meminfo.SystemProvider{},
		GovernorCfg:    governor.DefaultConfig(),
		Reporter:       reporter,
		ReportInterval: 2 * time.Second,
		CapacityChangeCb: func(old, new uint64) {
			fmt.Printf("[capacity] %d -> %d bytes\n", old, new)
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "create agent:", err)
		os.Exit(1)
	}

	fmt.Printf("idlemem-core demo, node=%s, version=%s\n", id, agent.Version)
	if err := a.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "start agent:", err)
		os.Exit(1)
	}

	<-ctx.Done()
	a.Stop()
	fmt.Println("bye.")
}

// printReporter 把心跳快照打印到终端，方便本地调试。
type printReporter struct{}

func (printReporter) Report(_ context.Context, s heartbeat.Snapshot) error {
	fmt.Printf(
		"[heartbeat] status=%s capacity=%d bytes used=%d bytes sys_used=%.1f%% uptime=%ds\n",
		s.Status, s.CapacityBytes, s.UsedBytes, s.SystemUsedPct, s.UptimeSeconds,
	)
	return nil
}

func (printReporter) Close() error { return nil }
