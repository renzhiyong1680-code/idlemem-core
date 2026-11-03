# idlemem-core

> 闲存云（XianCun Cloud）Agent 核心库 —— 把闲置电脑内存变成可收益资源的跨平台 Go 库。

`idlemem-core` 是一个**零第三方依赖、跨平台（Windows / Linux / macOS）**的 Go 核心库，用于构建"闲置内存贡献节点"（Agent）。它负责：采集系统内存 → 计算可安全贡献的容量（保底预留 + 高负载缩容 + 迟滞防抖）→ 提供带 TTL 的内存 KV 引擎 → 周期上报心跳到平台控制面。

各端外壳程序（Windows 托盘、macOS 菜单栏、Linux 守护进程）只需嵌入本库，即可获得完整的贡献节点能力。

## 特性

- ✅ **零第三方依赖**：仅使用 Go 标准库与操作系统原生接口，`go mod tidy` 无任何外部包
- ✅ **跨平台**：Windows（`GlobalMemoryStatusEx`）、Linux（`/proc/meminfo`）、macOS（`sysctl` + `vm_stat`）
- ✅ **智能贡献治理**：保底预留内存、使用率阈值缩容、迟滞区间防抖（`governor`）
- ✅ **内存 KV 引擎**：分片锁 + 惰性过期 + 数据拷贝隔离，并发安全（`kvstore`）
- ✅ **心跳上报**：接口化设计，内置 HTTP/JSON 参考实现，可替换 gRPC/WebSocket（`heartbeat`）
- ✅ **稳定节点 ID**：基于主机名 + 网卡 MAC 哈希，跨重启不变（`nodeid`）
- ✅ **完整生命周期**：启动/停止/容量回调，示例程序可直接跑通（`agent` + `examples/basic`）

## 架构

```
┌──────────────────────────────────────────────────────┐
│                 外壳程序（各端各自实现）                │
│   Windows 托盘 · macOS 菜单栏 · Linux systemd 服务     │
├──────────────────────────────────────────────────────┤
│                  idlemem-core（本库）                  │
│                                                      │
│   agent（整合入口：生命周期 / 容量决策 / 心跳调度）      │
│     ├── meminfo    内存采集（跨平台）                  │
│     ├── governor   贡献治理（预留 / 缩容 / 迟滞）       │
│     ├── kvstore    内存 KV 引擎（TTL / 分片锁）        │
│     ├── heartbeat  心跳上报（接口 + HTTP 实现）        │
│     └── nodeid     节点 ID 生成                       │
├──────────────────────────────────────────────────────┤
│               调度控制面（平台侧，另建）                │
└──────────────────────────────────────────────────────┘
```

## 快速开始

```bash
# 运行示例程序（每 2 秒采样内存、计算贡献容量、打印心跳）
go run ./examples/basic
```

示例输出：

```
idlemem-core demo, node=a512c2fde7be0a23, version=0.1.0
[capacity] 0 -> 12011229184 bytes
[heartbeat] status=online capacity=12011229184 bytes used=0 bytes sys_used=52.3% uptime=0s
```

## 嵌入指南（三步接入）

```go
package main

import (
    "context"
    "time"

    "github.com/xiancuncloud/idlemem-core/agent"
    "github.com/xiancuncloud/idlemem-core/governor"
    "github.com/xiancuncloud/idlemem-core/heartbeat"
    "github.com/xiancuncloud/idlemem-core/meminfo"
    "github.com/xiancuncloud/idlemem-core/nodeid"
)

func main() {
    // 1. 生成稳定节点 ID
    id, _ := nodeid.Generate()

    // 2. 组装 Agent：真实内存源 + 默认治理策略 + 心跳上报到平台端点
    a, err := agent.New(agent.Config{
        NodeID:         id,
        MemProvider:    meminfo.SystemProvider{},
        GovernorCfg:    governor.DefaultConfig(),
        Reporter:       heartbeat.NewHTTPReporter("https://platform.example.com/heartbeat"),
        ReportInterval: 10 * time.Second,
        CapacityChangeCb: func(old, new uint64) {
            // 容量变化时通知 KV 服务调整（例如缩容时拒绝新写入）
        },
    })
    if err != nil {
        panic(err)
    }

    // 3. 启动，ctx 取消即优雅停止
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    a.Start(ctx)
    select {}
}
```

## 包说明

| 包 | 职责 |
|---|---|
| `agent` | 整合入口：依赖装配、后台循环（采样→决策→上报）、生命周期、容量回调 |
| `meminfo` | 跨平台物理内存快照（Total / Available / Used / UsedPct） |
| `governor` | 计算可贡献容量：保底预留、高水位缩容、低水位恢复、迟滞防抖 |
| `kvstore` | 纯内存 KV 引擎：分片锁、TTL 惰性过期、值拷贝隔离、容量统计 |
| `heartbeat` | 心跳数据模型 + 上报接口；内置 HTTP/JSON 实现（可替换） |
| `nodeid` | 基于主机指纹的稳定节点 ID |

## 测试

```bash
go vet ./...
go test ./... -count=1          # 单元测试
go test ./... -race -count=1    # 竞态检测（Windows 需安装 gcc 以启用 cgo）
```

覆盖场景：治理迟滞状态机、KV 并发读写、TTL 过期、值隔离、心跳上报（httptest 模拟服务端）、Agent 高负载挂起与恢复。

## 许可证

[MIT](LICENSE)

---

**闲存云 · 把闲置的内存变成可收益的资源**
