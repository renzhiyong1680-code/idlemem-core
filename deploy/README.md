# 闲存云 Agent 部署（Linux / systemd / Windows）

把 `xiancun-agent` 部署成一台"闲置内存贡献节点"：开机自启、崩溃自动重启、心跳自动注册到控制面。

## 文件

| 文件 | 说明 |
|---|---|
| `xiancun-agent.service` | systemd 单元（`/etc/systemd/system/xiancun-agent.service`） |
| `install-agent.sh` | 一键部署脚本（root/sudo 执行） |
| `agent-config.json.example` | 生产配置模板（`/etc/xiancun/agent-config.json`） |

## Linux 快速开始

```bash
# 1. 构建（本机或 CI 产物）
go build -o bin/xiancun-agent ./cmd/xiancun-agent

# 2. 上传到节点后一键部署（需给出控制面地址）
sudo ./deploy/install-agent.sh --binary ./bin/xiancun-agent \
     --server http://server.example.com:8080

# 3. 确认
systemctl status xiancun-agent
journalctl -u xiancun-agent -f   # 应看到 node id / 心跳 / KV 监听日志
```

也可以直接从 URL 拉取二进制：

```bash
sudo ./deploy/install-agent.sh --binary https://example.com/xiancun-agent-linux-amd64 \
     --server http://server.example.com:8080
```

使用自定义配置（跳过模板）：

```bash
sudo ./deploy/install-agent.sh --binary ./bin/xiancun-agent --config ./my-agent-config.json
```

## Windows 快速启动（本机贡献节点）

```powershell
# 构建
go build -o xiancun-agent.exe ./cmd/xiancun-agent

# 启动（控制面地址 + KV 端口；Ctrl+C 退出）
.\xiancun-agent.exe -server http://127.0.0.1:8080 -kv :16380 -interval 10s
```

Windows 上如需开机自启，可用"任务计划程序"（schtasks）注册任务，或在托盘/服务包装器中托管。

## 默认布局

| 路径 | 内容 |
|---|---|
| `/usr/local/bin/xiancun-agent` | Agent 二进制 |
| `/etc/xiancun/agent-config.json` | 配置文件 |
| 运行用户 | `xiancun`（脚本自动创建） |

## 配置项速查

| 字段 | 默认 | 说明 |
|---|---|---|
| `server` | `http://127.0.0.1:8080` | 控制面 REST API 地址 |
| `kv_addr` | `0.0.0.0:16380` | 节点 KV 服务监听地址（网关按此转发） |
| `node_id` | 自动 | 节点 ID；留空自动生成稳定 ID |
| `interval` | `10s` | 心跳上报间隔（纳秒） |
| `kv_max_bytes` | `0` | KV 容量上限；0=由治理器动态决定 |

> **NAT 节点注意**：`kv_addr` 是监听地址。若节点在 NAT/防火墙后，网关无法直接拨通该地址，
> 需要在控制面侧把节点 IP 配置为公网可拨通地址（或做端口映射），否则转发会失败（Best-Effort，仅记日志）。

## 常用运维命令

```bash
systemctl status xiancun-agent
journalctl -u xiancun-agent -f
systemctl restart xiancun-agent
systemctl disable --now xiancun-agent
```

## 卸载

```bash
sudo systemctl disable --now xiancun-agent
sudo rm /etc/systemd/system/xiancun-agent.service /usr/local/bin/xiancun-agent
sudo rm -rf /etc/xiancun /opt/xiancun
sudo systemctl daemon-reload
```
