# InfoHub

[English](README_EN.md)

API 配额聚合服务，带有电子墨水屏仪表盘。InfoHub 定时从 Claude Relay、Sub2API、飞书、本机 Claude Code / Codex CLI 等数据源采集用量指标，持久化最新快照，并提供 REST API 与面向电子纸屏优化的仪表盘。

![InfoHub 仪表盘](docs/mockups/dashboard.png)

## 功能特性

- **多源采集** -- 内置 Claude Relay、Sub2API、飞书、本地 Claude Code、本地 Codex CLI 采集器
- **定时与手动** -- 基于 Cron 的定时采集，支持通过 API 手动触发单个采集器
- **双存储后端** -- 默认 SQLite 持久化快照和本地用量事件，也支持内存存储用于测试
- **本地用量统计** -- 只读扫描 Claude / Codex 本机 JSONL 记录，可选在线额度兜底
- **电子墨水屏仪表盘** -- 响应式 HTML 仪表盘和设备优化 JSON，适配 reTerminal E1001 + 7.5 英寸电子纸屏
- **灵活认证** -- API Bearer Token、仪表盘独立 token、采集器 Bearer 或 login JSON 认证

## 快速开始

### 前置要求

- Go 1.24+
- 可选：Docker，用于容器化部署

### 构建与运行

```bash
make build
make run

# 或直接运行
./bin/infohub -config config.yaml
```

配置加载时会先读取配置文件同目录下的 `.env`，再展开 `${VAR_NAME}` 环境变量。最小本地运行通常只需要按需设置 `INFOHUB_AUTH_TOKEN`、`INFOHUB_DASHBOARD_TOKEN`、`INFOHUB_SQLITE_PATH`。

### Docker

```bash
docker build -t infohub .
docker run -p 8080:8080 \
  -e INFOHUB_AUTH_TOKEN=your-api-token \
  -e INFOHUB_DASHBOARD_TOKEN=your-dashboard-token \
  -e INFOHUB_STORE_TYPE=sqlite \
  -e INFOHUB_SQLITE_PATH=/data/infohub.db \
  -v "$PWD/data:/data" \
  infohub
```

默认配置会启用 `claude_local` 和 `codex_local`。如果在容器中使用本地采集器，需要额外挂载主机的 Claude / Codex 数据目录；如果只采集远端网关，可以在容器配置文件里关闭本地采集器。

## 配置

完整配置请以 [config.yaml](config.yaml) 和 [dist/config.yaml](dist/config.yaml) 为准。下面是主要结构摘要：

```yaml
server:
  port: ${INFOHUB_PORT}
  auth_token: "${INFOHUB_AUTH_TOKEN}"
  dashboard_token: "${INFOHUB_DASHBOARD_TOKEN}"
  mock_enabled: ${INFOHUB_MOCK_ENABLED}

dashboard:
  sources:
    claude: "claude_relay"
    codex: "sub2api"

collectors:
  claude_relay:
    enabled: ${CLAUDE_RELAY_ENABLED}
    cron: "* * * * *"
    service:
      base_url: "${CLAUDE_RELAY_BASE_URL}"
      endpoints:
        accounts: "/admin/claude-accounts"
        usage: "/admin/claude-accounts/usage"
    auth:
      type: "login_json"
      login_endpoint: "/web/auth/login"
      token_path: "token"

  sub2api:
    enabled: ${SUB2API_ENABLED}
    cron: "* * * * *"
    service:
      base_url: "${SUB2API_BASE_URL}"
      endpoints:
        accounts: "/api/v1/admin/accounts"
        today_stats: "/api/v1/admin/accounts/today-stats/batch"
    auth:
      type: "login_json"
      login_endpoint: "${SUB2API_BASE_URL}/api/v1/auth/login"
      token_path: "data.access_token"

  claude_local:
    enabled: true
    cron: "*/5 * * * *"
    paths:
      - "${HOME}/.config/claude/projects"
      - "${HOME}/.claude/projects"
    mode: "builtin"
    online:
      enabled: true

  codex_local:
    enabled: true
    cron: "*/5 * * * *"
    paths:
      - "${HOME}/.codex/sessions"
    mode: "builtin"
    online:
      enabled: true

store:
  type: "${INFOHUB_STORE_TYPE}"
  sqlite_path: "${INFOHUB_SQLITE_PATH}"

log:
  level: "${INFOHUB_LOG_LEVEL}"
```

如果主要展示本机 Claude Code / Codex CLI 数据，请把 `dashboard.sources.claude` 改成 `claude_local`，把 `dashboard.sources.codex` 改成 `codex_local`。

### 关键环境变量

| 变量名 | 说明 | 默认值 |
|--------|------|--------|
| `INFOHUB_PORT` | HTTP 服务端口 | `8080` |
| `INFOHUB_AUTH_TOKEN` | API Bearer Token；为空时 API 不启用鉴权 | 空 |
| `INFOHUB_DASHBOARD_TOKEN` | 仪表盘查询参数 token；为空时只依赖 API token 或不鉴权 | 空 |
| `INFOHUB_MOCK_ENABLED` | 是否让仪表盘返回模拟数据 | `false` |
| `INFOHUB_STORE_TYPE` | 存储后端：`sqlite` 或 `memory` | `sqlite` |
| `INFOHUB_SQLITE_PATH` | SQLite 数据库文件路径 | `./data/infohub.db` |
| `INFOHUB_LOG_LEVEL` | 日志级别：`debug`、`info`、`warn`、`error` | `info` |
| `CLAUDE_RELAY_ENABLED` | 是否注册 Claude Relay 采集器 | `false` |
| `SUB2API_ENABLED` | 是否注册 Sub2API 采集器 | `false` |

## API 参考

当 `INFOHUB_AUTH_TOKEN` 非空时，普通 API 需要 `Authorization: Bearer <token>`。仪表盘端点可以使用同一个 Bearer Token，也可以使用独立的 `?token=<INFOHUB_DASHBOARD_TOKEN>`。两个 token 都为空时，服务不启用访问鉴权。

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/summary` | 所有数据源的最新快照和数据项 |
| `GET` | `/api/v1/source/{name}` | 指定数据源的快照 |
| `GET` | `/api/v1/health` | 采集器状态和最近采集时间 |
| `POST` | `/api/v1/collect/{name}` | 手动触发指定采集器 |
| `GET` | `/dashboard/eink` | HTML 电子墨水屏仪表盘 |
| `GET` | `/dashboard/eink.json` | 仪表盘调试 JSON |
| `GET` | `/dashboard/eink/device.json` | ESPHome 设备直连 JSON |

仪表盘端点支持 `refresh` 查询参数，范围是 60 到 3600 秒，例如 `/dashboard/eink?token=xxx&refresh=600`。

### 响应示例

**GET /api/v1/summary**

```json
{
  "updated_at": 1713859200,
  "sources": {
    "claude_local": {
      "status": "ok",
      "last_fetch": 1713859200,
      "items": [
        {
          "source": "claude_local",
          "category": "quota",
          "title": "5h_window",
          "value": "76%",
          "extra": { "remaining_percent": 76, "quota_source": "claude_oauth_usage" },
          "fetched_at": 1713859200
        }
      ]
    }
  }
}
```

**GET /api/v1/health**

```json
{
  "status": "ok",
  "collectors": {
    "claude_local": { "status": "ok", "last_fetch": 1713859200 },
    "codex_local": { "status": "ok", "last_fetch": 1713859180 }
  }
}
```

## 项目结构

```
cmd/infohub/          程序入口，服务启动
internal/
  api/                HTTP 处理器、中间件、仪表盘渲染
  collector/          采集器接口及实现
  config/             YAML 配置、.env 读取与环境变量展开
  model/              数据模型（DataItem、SourceSnapshot）
  scheduler/          基于 Cron 的任务调度
  store/              存储接口（SQLite、内存）
deploy/
  esphome/            ESPHome 设备配置（电子墨水屏）
docs/
  zh/                 中文专题文档
  en/                 英文专题文档
```

### 添加采集器

实现 `Collector` 接口并在 `cmd/infohub/main.go` 中注册：

```go
type Collector interface {
    Name() string
    Collect(ctx context.Context) ([]model.DataItem, error)
}
```

本地采集器如需持久化增量解析状态，可以通过 scheduler 注入的 store 使用 SQLite 里的 `local_parse_state` 和 `local_usage_events`。

## 电子墨水屏集成

InfoHub 通过 ESPHome 提供完整的电子纸屏支持：

- **目标设备**：reTerminal E1001 + Waveshare 7.5 英寸电子纸屏
- **显示模式**：HTML 仪表盘截图，或设备 JSON 端点直连绘制
- **刷新方式**：定时自动刷新 + GPIO 按钮手动刷新

详细配置指南请参阅 [`docs/zh/`](docs/zh/) 目录：

- [首次刷机指南](docs/zh/infohub-eink-first-flash-runbook.md)
- [直连 API 面板](docs/zh/infohub-eink-direct-api-panel.md)
- [部署与显示调优](docs/zh/infohub-eink-deploy-and-display-tuning.md)
- [macOS 上的 ESPHome Docker](docs/zh/infohub-eink-esphome-docker-mac.md)
- [局部刷新探测](docs/zh/infohub-eink-partial-refresh-probe.md)
- [本地 Claude/Codex 用量采集](docs/zh/infohub-local-claude-codex-usage.md)

### ESPHome 命令

```bash
make esphome-up
make esphome-compile-stage1
make esphome-compile-stage1-alt
make esphome-compile-stage2
make esphome-compile-partial-probe
make esphome-logs
```

## 开发

```bash
make fmt
make test
make tidy
```

测试使用标准 `testing` 包，通过 `httptest` 进行 HTTP 模拟，使用内存存储实现隔离。

```bash
go test ./...
go test ./internal/collector/... -v
```

## 许可证

[MIT](LICENSE)
