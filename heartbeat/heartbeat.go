// Package heartbeat 定义节点心跳上报的数据结构与接口。
//
// 心跳是 Agent 与调度控制面之间的核心通信：节点周期性地把
// 节点 ID、当前贡献容量、内存负载、运行时长等信息上报到平台。
package heartbeat

import (
	"context"
	"time"
)

// Status 节点在线状态。
type Status string

const (
	// StatusOnline 正常在线并贡献中。
	StatusOnline Status = "online"
	// StatusSuspended 在线但本机高负载，暂停贡献。
	StatusSuspended Status = "suspended"
	// StatusOffline 已离线（一般由控制面根据心跳超时判定，客户端较少主动上报）。
	StatusOffline Status = "offline"
)

// Snapshot 一次心跳上报的快照内容。
type Snapshot struct {
	// NodeID 节点唯一标识。
	NodeID string `json:"node_id"`
	// Version 客户端版本。
	Version string `json:"version"`
	// Status 当前状态。
	Status Status `json:"status"`
	// CapacityBytes 当前可贡献内存字节数。
	CapacityBytes uint64 `json:"capacity_bytes"`
	// UsedBytes 当前 KV 节点实际占用内存字节数。
	UsedBytes uint64 `json:"used_bytes"`
	// SystemUsedPct 本机内存使用率百分比。
	SystemUsedPct float64 `json:"system_used_pct"`
	// TotalBytes 本机物理内存总量。
	TotalBytes uint64 `json:"total_bytes"`
	// UptimeSeconds 本 Agent 已运行秒数。
	UptimeSeconds int64 `json:"uptime_seconds"`
	// ReportedAt 上报时间。
	ReportedAt time.Time `json:"reported_at"`
	// KVAddr 本节点 KV 服务的监听地址（如 "0.0.0.0:16380"），
	// 供控制面网关把数据请求转发到本节点。
	KVAddr string `json:"kv_addr,omitempty"`
}

// Reporter 心跳上报接口。实现可以是 HTTP、gRPC、WebSocket 等。
type Reporter interface {
	// Report 上报一次心跳快照。
	Report(ctx context.Context, snap Snapshot) error
	// Close 释放底层连接等资源。
	Close() error
}
