# InfoHub

[English](README_EN.md)

API 配额聚合服务，带有电子墨水屏仪表盘。定时从多个 AI 服务商采集用量指标，持久化快照数据，并提供 REST API 和专为电子墨水屏优化的可视化仪表盘。

![InfoHub 仪表盘](docs/mockups/dashboard.png)

## 功能特性

- **多源采集** -- 内置 Claude Relay、Sub2API、飞书及通用 HTTP/JSON 采集器，可轻松扩展
- **定时与手动** -- 基于 Cron 的定时采集，支持手动触发
- **双存储后端** -- 生产环境使用 SQLite，开发和测试使用内存存储
- **电子墨水屏仪表盘** -- 响应式 HTML 仪表盘，带进度条和告警，专为 7.5 英寸电子纸屏设计
- **设备集成** -- 提供 ESPHome 设备直连的 JSON 端点
- **灵活认证** -- 支持 Bearer Token、登录/JWT 流程，或按采集器配置免认证

## 快速开始

### 前置要求

- Go 1.24+
- （可选）Docker，用于容器化部署

### 构建与运行

```bash
# 构建
make build

# 使用配置文件运行
make run

# 或直接运行
./bin/infohub -config config.yaml
```

### Docker

```bash
docker build -t infohub .
docker run -p 8080:8080 \
  -e INFOHUB_AUTH_TOKEN=your-token \
  -e INFOHUB_STORE_TYPE=sqlite \
  infohub
```

## 配置

InfoHub 使用 YAML 配置文件，支持环境变量插值（`${VAR_NAME}`）。

```yaml
server:
  port: ${INFOHUB_PORT}               # 默认: 8080
  auth_token: "${INFOHUB_AUTH_TOKEN}"
  dashboard_token: "${INFOHUB_DASHBOARD_TOKEN}"
  read_timeout_seconds: 10
  write_timeout_seconds: 10
  shutdown_timeout_seconds: 10

collectors:
  claude_relay:
    enabled: true
    cron: "*/10 * * * *"
    timeout_seconds: 15
    service:
      base_url: "${CLAUDE_RELAY_BASE_URL}"
      endpoints:
        accounts: "/admin/claude-accounts"
        usage: "/admin/claude-accounts/usage"
    auth:
      type: "login_json"
      login_endpoint: "/web/auth/login"
      credentials:
        username: "${CLAUDE_RELAY_USERNAME}"
        password: "${CLAUDE_RELAY_PASSWORD}"

  sub2api:
    enabled: true
    cron: "*/10 * * * *"
    service:
      base_url: "${SUB2API_BASE_URL}"
      endpoints:
        accounts: "/api/v1/admin/accounts"
        today_stats: "/api/v1/admin/accounts/today-stats/batch"
    auth:
      type: "login_json"
      credentials:
        email: "${SUB2API_ADMIN_EMAIL}"
        password: "${SUB2API_ADMIN_PASSWORD}"

store:
  type: "${INFOHUB_STORE_TYPE}"        # "sqlite" 或 "memory"
  sqlite_path: "./data/infohub.db"

log:
  level: "info"                        # debug | info | warn | error
```

### 环境变量

| 变量名 | 说明 | 默认值 |
|--------|------|--------|
| `INFOHUB_PORT` | HTTP 服务端口 | `8080` |
| `INFOHUB_AUTH_TOKEN` | API 认证令牌 | -- |
| `INFOHUB_DASHBOARD_TOKEN` | 仪表盘访问令牌（与 API 认证独立） | -- |
| `INFOHUB_STORE_TYPE` | 存储后端：`sqlite` 或 `memory` | `memory` |
| `INFOHUB_SQLITE_PATH` | SQLite 数据库文件路径 | `./data/infohub.db` |
| `INFOHUB_LOG_LEVEL` | 日志级别 | `info` |

## API 参考

所有 API 端点需要 `Authorization: Bearer <auth_token>` 请求头。

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/summary` | 所有数据源的最新快照和数据项 |
| `GET` | `/api/v1/source/{name}` | 指定数据源的快照 |
| `GET` | `/api/v1/health` | 采集器状态和最近采集时间 |
| `POST` | `/api/v1/collect/{name}` | 手动触发指定采集器 |
| `GET` | `/dashboard/eink` | HTML 电子墨水屏仪表盘 |
| `GET` | `/dashboard/eink.json` | 仪表盘数据（JSON 格式） |
| `GET` | `/dashboard/eink/device.json` | 设备优化的 JSON 数据 |

仪表盘端点支持通过查询参数（`?token=xxx`）或请求头传递令牌。

### 响应示例

**GET /api/v1/summary**

```json
{
  "updated_at": 1713859200,
  "sources": {
    "claude_relay": {
      "status": "ok",
      "last_fetch": 1713859200,
      "items": [
        {
          "source": "claude_relay",
          "category": "quota",
          "title": "5h_limit",
          "value": "75.3%",
          "extra": { "used": 753, "total": 1000 },
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
    "claude_relay": { "status": "ok", "last_fetch": 1713859200 },
    "sub2api": { "status": "ok", "last_fetch": 1713859180 }
  }
}
```

## 项目结构

```
cmd/infohub/          程序入口，服务启动
internal/
  api/                HTTP 处理器、中间件、仪表盘渲染
  collector/          采集器接口及实现
  config/             YAML 配置与环境变量展开
  model/              数据模型（DataItem、SourceSnapshot）
  scheduler/          基于 Cron 的任务调度
  store/              存储接口（SQLite、内存）
deploy/
  esphome/            ESPHome 设备配置（电子墨水屏）
```

### 添加采集器

实现 `Collector` 接口并在 `cmd/infohub/main.go` 中注册：

```go
type Collector interface {
    Name() string
    Collect(ctx context.Context) ([]model.DataItem, error)
}
```

## 电子墨水屏集成

InfoHub 通过 ESPHome 提供完整的电子纸屏支持：

- **目标设备**：reTerminal E1001 + Waveshare 7.5 英寸电子纸屏
- **显示模式**：HTML 仪表盘（截图方式）或设备 JSON 端点
- **刷新方式**：定时自动刷新 + GPIO 按钮手动刷新

详细配置指南请参阅 [`docs/zh/`](docs/zh/) 目录：

- [首次刷机指南](docs/zh/infohub-eink-first-flash-runbook.md)
- [直连 API 面板](docs/zh/infohub-eink-direct-api-panel.md)
- [部署与显示调优](docs/zh/infohub-eink-deploy-and-display-tuning.md)
- [macOS 上的 ESPHome Docker](docs/zh/infohub-eink-esphome-docker-mac.md)
- [局部刷新探测](docs/zh/infohub-eink-partial-refresh-probe.md)

### ESPHome 命令

```bash
make esphome-up         # 启动 ESPHome 容器
make esphome-compile-stage1   # 编译首次刷机固件
make esphome-compile-stage2   # 编译生产固件
make esphome-logs       # 查看 ESPHome 日志
```

## 开发

```bash
make fmt      # 格式化代码
make test     # 运行测试
make tidy     # 整理依赖
```

### 测试

测试使用标准 `testing` 包，通过 `httptest` 进行 HTTP 模拟，使用内存存储实现隔离。

```bash
go test ./...
go test ./internal/collector/... -v   # 详细输出采集器测试
```

## 许可证

[MIT](LICENSE)
