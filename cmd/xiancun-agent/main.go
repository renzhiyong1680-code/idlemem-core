// Command xiancun-agent 是闲存云 Agent 的可运行外壳。
//
// 它把 idlemem-core 核心库装配成一个真实运行的"闲置内存贡献节点"：
//   - 周期性采集本机内存、计算可贡献容量、心跳上报控制面；
//   - 通过核心库 kvsrv 组件暴露一个最小 Redis 协议端口，供企业客户端或控制面网关存取缓存；
//   - 全程零第三方依赖（仅标准库），可在 Linux/macOS/Windows 上编译运行。
//
// 用法：
//
//	xiancun-agent -server http://127.0.0.1:8080 -kv :16380
//	xiancun-agent -config agent.json
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xiancuncloud/idlemem-core/agent"
	"github.com/xiancuncloud/idlemem-core/governor"
	"github.com/xiancuncloud/idlemem-core/heartbeat"
	"github.com/xiancuncloud/idlemem-core/kvsrv"
	"github.com/xiancuncloud/idlemem-core/kvstore"
	"github.com/xiancuncloud/idlemem-core/meminfo"
	"github.com/xiancuncloud/idlemem-core/nodeid"
)

// defaultKVAddr 默认 KV 服务监听地址（与生产网关端口区分开）。
const defaultKVAddr = ":16380"

// config Agent 运行配置，可通过 -config <json> 文件或命令行 flag 提供。
type config struct {
	// Server 控制面 REST API 地址，如 http://127.0.0.1:8080。
	Server string `json:"server"`
	// KVAddr KV 服务监听地址。
	KVAddr string `json:"kv_addr"`
	// NodeID 节点 ID；为空时自动生成稳定 ID。
	NodeID string `json:"node_id"`
	// Interval 心跳上报间隔（纳秒）。
	Interval time.Duration `json:"interval"`
	// KVMaxBytes KV 引擎容量上限（0 表示由治理器动态决定）。
	KVMaxBytes uint64 `json:"kv_max_bytes"`
}

func (c *config) applyDefaults() {
	if c.Server == "" {
		c.Server = "http://127.0.0.1:8080"
	}
	if c.KVAddr == "" {
		c.KVAddr = defaultKVAddr
	}
	if c.Interval <= 0 {
		c.Interval = 10 * time.Second
	}
}

func main() {
	logger := log.New(os.Stdout, "[agent] ", log.LstdFlags|log.Lmsgprefix)

	var (
		cfgPath = flag.String("config", "", "config json file path (optional)")
		server  = flag.String("server", "", "control plane API address, e.g. http://127.0.0.1:8080")
		kvAddr  = flag.String("kv", "", "KV service listen address, e.g. :16380")
		nodeID  = flag.String("id", "", "node id (default: auto-generated stable id)")
		interval = flag.Duration("interval", 0, "heartbeat interval (default 10s)")
	)
	flag.Parse()

	cfg := config{}
	// 优先从 JSON 文件加载，flag 覆盖同名项。
	if *cfgPath != "" {
		if err := loadConfig(*cfgPath, &cfg); err != nil {
			logger.Fatalf("load config: %v", err)
		}
	}
	if *server != "" {
		cfg.Server = *server
	}
	if *kvAddr != "" {
		cfg.KVAddr = *kvAddr
	}
	if *nodeID != "" {
		cfg.NodeID = *nodeID
	}
	if *interval > 0 {
		cfg.Interval = *interval
	}
	cfg.applyDefaults()

	// 节点 ID：未指定则自动生成稳定 ID。
	id := cfg.NodeID
	if id == "" {
		id = nodeid.String()
	}
	logger.Printf("node id: %s", id)

	// 装配核心库。
	prov := meminfo.SystemProvider{}
	rep := heartbeat.NewHTTPReporter(cfg.Server + "/api/v1/nodes/heartbeat")
	core, err := agent.New(agent.Config{
		NodeID:         id,
		Version:        agent.Version,
		MemProvider:    prov,
		GovernorCfg:    governor.DefaultConfig(),
		Reporter:       rep,
		StoreOpts:      kvstore.Options{MaxBytes: cfg.KVMaxBytes},
		ReportInterval: cfg.Interval,
		KVAddr:         cfg.KVAddr,
	})
	if err != nil {
		logger.Fatalf("create agent core: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 启动 KV 服务（阻塞在 goroutine，RESP 协议组件来自核心库 kvsrv）。
	kvSrv, err := kvsrv.New(cfg.KVAddr, core, logger)
	if err != nil {
		logger.Fatalf("create kv service: %v", err)
	}
	go kvSrv.Serve()

	// 启动采样与心跳循环。
	if err := core.Start(ctx); err != nil {
		logger.Fatalf("start agent: %v", err)
	}
	logger.Printf("agent started: server=%s kv=%s interval=%s", cfg.Server, cfg.KVAddr, cfg.Interval)

	<-ctx.Done()
	logger.Printf("shutting down...")
	kvSrv.Close()
	core.Stop()
}
