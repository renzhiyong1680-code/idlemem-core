# 更新日志

本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [未发布]

### 新增

- `CLA.md`：贡献者许可协议（中英双语十节），贡献者保留著作权，仅授予项目方在开源与商业授权两种模式下的使用许可
- `CONTRIBUTING.md`、`SECURITY.md`、`.github/ISSUE_TEMPLATE/`：贡献流程、漏洞报告通道与 Issue 模板
- `docs/项目介绍.md`：项目定位、明确不做什么、能力边界、当前进度与关键参数
- README 顶部增加三仓导航与项目介绍入口
- `docs/assets/architecture.svg`：三仓协作架构图，README 新增「三仓如何协作」段并引用该图，含指向 `idlemem-server` 一键起步脚本的入口
- `docs/assets/ai-ran-architecture.svg`：AI 基站（AI-RAN）位置图（1200×790），画已实现能力在边缘服务器 / 本地推理 / MEC 业务上的落位，底部固定四条边界（不持久化、单副本掉线即丢与双副本 ≈34.3s、网关写路径串行、无 TLS/AES 且不绑定 AI-RAN 标准）
- `docs/项目介绍.md` 新增第十一节「和 AI 基站（AI-RAN）是什么关系」：50 字定位、三处落位表（边缘服务器 Agents / 零改造接入 / 计量分账）与边界；README 新增「放到 AI 基站里是什么位置」小节引用该图

### 修复

- `docs/项目介绍.md`：网关命令集由「17 条」更正为实测的 18 条并列出全部命令名；元数据存储由「SQLite」更正为实际的 buntdb 单文件（`idlemem-server.db`）

### 变更

- `ci.yml` 的 `push` 增加 `tags: ["v*"]`：打标签时也执行测试，Release 页面可查 CI 记录

## [v0.1.1] - 2026-11-06

仅许可证变更，代码与 `v0.1.0` 完全一致。

### 变更

- 许可证由 MIT 变更为 AGPL-3.0（GNU Affero General Public License v3.0），保留后续商业授权选项；
  `v0.1.0` 及更早版本仍按 MIT 发布，该授权不可撤销。
- 新增 `COMMERCIAL.md`：商业授权的适用场景、授权形式与联系方式

## [v0.1.0] - 2026-11-06

首个公开版本：闲存云 Agent 的跨平台核心库，以及可直接运行的 Agent 外壳。

### 核心库

- `kvstore`：内存 KV 引擎，支持 TTL、容量上限、`CanFit` 预判；覆盖写按净增量计算占用，
  同一 key 反复改写不会翻倍占容量
- `nodeid`：基于机器信息生成稳定节点 ID（跨重启不变），附确定性测试
- `meminfo`：跨平台内存探测（总量 / 可用 / 系统占用比例）
- `governor`：内存压力判定，决定当前可贡献容量；支持手动暂停/恢复，手动开关优先于压力判定
- `heartbeat`：心跳快照与上报，附带 `kv_addr` 供控制面网关回源到节点
- `kvsrv`：零依赖 RESP KV 服务器（`PING/AUTH/SET[EX]/SETEX/GETSET/APPEND/STRLEN/TYPE/GET/DEL/EXISTS/MGET/MSET/INCR/EXPIRE/TTL/INFO`）

### Agent 外壳 `xiancun-agent`

- 一条命令跑起来：心跳上报 + 本地 KV 服务（默认 `:16380`）
- `-node-token`：带鉴权的心跳，控制面可要求节点令牌
- `-version`：版本由构建时注入，便于运维核对线上版本
- `-check`：启动前自检（可贡献容量 / KV 端口可监听 / 控制面可达），退出码 0 通过、1 有失败

### 部署

- `deploy/` 提供 systemd 单元、一键部署脚本与配置模板

### 工程

- `.gitattributes` 固定 LF 行尾，保证部署脚本与 systemd 单元在 Linux 上可用
- CI：每次提交 `go vet` + `go test`，并做五个平台交叉编译，守住"核心库不含平台相关代码"

### 已知限制

- KV 数据是**缓存语义**：Best-Effort、可丢、不承诺持久化，不适合存放敏感数据
- 公网家用节点只做分布式 KV 缓存，不做远程聚合内存（详见 README 的定位说明）
