# InfoHub

[中文文档](README_CN.md)

API quota aggregation service with an e-ink dashboard. InfoHub collects usage metrics from Claude Relay, Sub2API, Feishu, local Claude Code, local Codex CLI, and other sources on a schedule, persists the latest snapshots, and exposes REST APIs plus a dashboard optimized for e-paper displays.

![InfoHub Dashboard](docs/mockups/dashboard.png)

## Features

- **Multi-source collection** -- Built-in collectors for Claude Relay, Sub2API, Feishu, local Claude Code, and local Codex CLI
- **Scheduled and on-demand** -- Cron-based collection with an API for manually triggering a collector
- **Dual storage backends** -- SQLite by default for snapshots and local usage events; memory storage for tests
- **Local usage tracking** -- Read-only scans of Claude / Codex JSONL files, with optional online quota fallback
- **E-ink dashboard** -- Responsive HTML dashboard and device JSON for reTerminal E1001 + 7.5" e-paper displays
- **Flexible authentication** -- API Bearer token, separate dashboard token, collector Bearer auth, or login JSON auth

## Quick Start

### Prerequisites

- Go 1.24+
- Optional: Docker for containerized deployment

### Build and Run

```bash
make build
make run

# Or run the binary directly
./bin/infohub -config config.yaml
```

The config loader reads a `.env` file next to the config file before expanding `${VAR_NAME}` placeholders. For a minimal local run, set `INFOHUB_AUTH_TOKEN`, `INFOHUB_DASHBOARD_TOKEN`, and `INFOHUB_SQLITE_PATH` as needed.

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

The default config enables `claude_local` and `codex_local`. To use local collectors inside a container, also mount the host Claude / Codex data directories. If you only collect from remote gateways, disable the local collectors in the container config.

## Configuration

Use [config.yaml](config.yaml) and [dist/config.yaml](dist/config.yaml) as the source of truth. This is the main shape:

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

To show local Claude Code / Codex CLI data on the dashboard, set `dashboard.sources.claude` to `claude_local` and `dashboard.sources.codex` to `codex_local`.

### Key Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `INFOHUB_PORT` | HTTP server port | `8080` |
| `INFOHUB_AUTH_TOKEN` | API Bearer token; empty means API auth is disabled | empty |
| `INFOHUB_DASHBOARD_TOKEN` | Dashboard query token; empty means dashboard uses API token or no auth | empty |
| `INFOHUB_MOCK_ENABLED` | Return mock dashboard data | `false` |
| `INFOHUB_STORE_TYPE` | Storage backend: `sqlite` or `memory` | `sqlite` |
| `INFOHUB_SQLITE_PATH` | SQLite database file path | `./data/infohub.db` |
| `INFOHUB_LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | `info` |
| `CLAUDE_RELAY_ENABLED` | Register the Claude Relay collector | `false` |
| `SUB2API_ENABLED` | Register the Sub2API collector | `false` |

## API Reference

When `INFOHUB_AUTH_TOKEN` is set, regular API endpoints require `Authorization: Bearer <token>`. Dashboard endpoints accept the same Bearer token or a separate `?token=<INFOHUB_DASHBOARD_TOKEN>`. If both tokens are empty, access authentication is disabled.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/summary` | Latest snapshots and items for all sources |
| `GET` | `/api/v1/source/{name}` | Snapshot for one source |
| `GET` | `/api/v1/health` | Collector status and last fetch timestamps |
| `POST` | `/api/v1/collect/{name}` | Manually trigger one collector |
| `GET` | `/dashboard/eink` | HTML e-ink dashboard |
| `GET` | `/dashboard/eink.json` | Dashboard debug JSON |
| `GET` | `/dashboard/eink/device.json` | ESPHome device JSON |

Dashboard endpoints support a `refresh` query parameter from 60 to 3600 seconds, for example `/dashboard/eink?token=xxx&refresh=600`.

### Response Examples

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

## Project Structure

```
cmd/infohub/          Entry point and server bootstrap
internal/
  api/                HTTP handlers, middleware, dashboard rendering
  collector/          Collector interface and implementations
  config/             YAML config, .env loading, environment expansion
  model/              Data models (DataItem, SourceSnapshot)
  scheduler/          Cron-based task scheduling
  store/              Storage interface (SQLite, memory)
deploy/
  esphome/            ESPHome device configs for e-ink displays
docs/
  zh/                 Chinese topic docs
  en/                 English topic docs
```

### Adding a Collector

Implement the `Collector` interface and register it in `cmd/infohub/main.go`:

```go
type Collector interface {
    Name() string
    Collect(ctx context.Context) ([]model.DataItem, error)
}
```

Local collectors that need incremental parse state can use the scheduler-injected store and the SQLite `local_parse_state` / `local_usage_events` tables.

## E-ink Display Integration

InfoHub includes first-class ESPHome support for e-paper displays:

- **Target device**: reTerminal E1001 + Waveshare 7.5" e-paper
- **Display modes**: HTML dashboard screenshot or direct rendering from device JSON
- **Refresh**: Automatic schedule plus GPIO button refresh

See [`docs/en/`](docs/en/) for setup guides:

- [First Flash Runbook](docs/en/infohub-eink-first-flash-runbook.md)
- [Direct API Panel](docs/en/infohub-eink-direct-api-panel.md)
- [Deploy and Display Tuning](docs/en/infohub-eink-deploy-and-display-tuning.md)
- [ESPHome Docker on macOS](docs/en/infohub-eink-esphome-docker-mac.md)
- [Partial Refresh Probe](docs/en/infohub-eink-partial-refresh-probe.md)

### ESPHome Commands

```bash
make esphome-up
make esphome-compile-stage1
make esphome-compile-stage1-alt
make esphome-compile-stage2
make esphome-compile-partial-probe
make esphome-logs
```

## Development

```bash
make fmt
make test
make tidy
```

Tests use the standard `testing` package with `httptest` for HTTP mocks and memory storage for isolation.

```bash
go test ./...
go test ./internal/collector/... -v
```

## License

[MIT](LICENSE)
