# InfoHub Design

## 设计目标

InfoHub 的目标是把多个 AI 服务或本机工具的用量信息收敛到一个轻量服务里，并为电子墨水屏提供稳定、低刷新成本的展示接口。服务需要能在个人电脑、局域网设备或容器环境中运行，默认使用 SQLite 保留最新快照和本地用量增量状态。

核心诉求：

- 采集器可插拔，远端网关和本机文件采集可以并存
- API、调度、存储、仪表盘之间保持清晰边界
- 本地 Claude Code / Codex CLI 采集只读用户数据，不写回原工具配置
- ESPHome 设备可通过简单 JSON 端点直连，不依赖浏览器能力

## 方案选择

项目采用单 Go 进程架构：

- `cmd/infohub` 负责加载配置、注册采集器、创建存储和启动 HTTP 服务
- `internal/collector` 定义采集器接口，并实现 Claude Relay、Sub2API、飞书、本地 Claude / Codex 采集器
- `internal/scheduler` 使用 cron 周期触发采集，也支持 API 手动触发
- `internal/store` 提供 SQLite 与 memory 两种实现
- `internal/api` 输出普通 REST API、HTML 仪表盘和设备 JSON

这个方案比拆成多个服务更容易部署，也更适合个人局域网和桌面常驻场景。SQLite 作为默认存储，避免额外数据库依赖，同时足够支撑快照、错误状态、本地解析状态和事件表。

## 关键决策

### Collector 作为扩展边界

所有采集器实现同一个 `Collector` 接口，统一输出 `[]model.DataItem`。这样 API 与仪表盘不需要理解每个上游的请求细节，只消费规范化后的快照。

### SQLite 默认持久化

默认存储选择 SQLite，而不是 memory。memory 适合测试，但进程重启会丢状态；本地用量采集需要 `local_parse_state` 和 `local_usage_events` 支持增量续扫与窗口回放，因此 SQLite 更符合生产默认值。

### 本地采集只读

`claude_local` 和 `codex_local` 只读取本机 JSONL 和已有认证文件，用于统计 usage 与可选在线额度。实现不修改 Claude Code / Codex CLI 配置，不写回 token，也不输出 prompt 内容。

### 仪表盘与 API 分开鉴权

普通 API 使用 `INFOHUB_AUTH_TOKEN` 的 Bearer 鉴权；电子墨水屏设备可以使用独立的 `INFOHUB_DASHBOARD_TOKEN` 查询参数，便于 ESPHome 这类设备配置 URL。两个 token 都为空时，服务按本地可信环境处理，不启用访问鉴权。

### 设备 JSON 独立于 HTML

`/dashboard/eink` 服务于浏览器截图或人工查看，`/dashboard/eink/device.json` 服务于 ESPHome 直连绘制。两者共享同一份聚合逻辑，但输出形态分离，避免设备端解析 HTML。

## 已知限制

- 当前仪表盘主要围绕 Claude 与 Codex 两个展示槽位设计，更多来源需要扩展 dashboard source 配置和布局。
- 本地 JSONL 格式来自 CLI 工具内部记录，未来工具格式变化时需要更新解析器。
- Docker 默认配置启用本地采集器，但容器天然看不到宿主机 `~/.claude` 和 `~/.codex`，部署时需要挂载目录或关闭本地采集器。
- OAuth 在线额度查询依赖上游接口和本机已有凭据；失败时只能展示本地估算或未知状态。
- memory store 不保留重启后的快照，也不适合本地采集的增量解析生产使用。

## 变更历史

- 2026-04-27：补充 README 与设计文档，明确 SQLite 默认存储、鉴权规则、本地 Claude/Codex 采集器和 Docker 注意事项。
- 2026-04-26：接入 `claude_local` / `codex_local`，新增 SQLite 本地解析状态和事件表，仪表盘支持本地源展示。
- 初始版本：实现 Claude Relay、Sub2API、飞书采集，REST API，SQLite/memory store 和电子墨水屏仪表盘。
