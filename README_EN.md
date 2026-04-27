# InfoHub

[中文文档](README_CN.md)

API quota aggregation service with e-ink dashboard. Collects usage metrics from multiple AI service providers on a schedule, persists snapshots, and exposes REST APIs plus a visual dashboard optimized for e-paper displays.

<<<<<<< HEAD
=======
![InfoHub Dashboard](docs/mockups/dashboard.png)

>>>>>>> 0ac6b18c8ae66348f109ff80dfc51e02211b39be
## Features

- **Multi-source collection** -- pluggable collectors for Claude Relay, Sub2API, Feishu, and generic HTTP/JSON sources, with a simple interface to add more
- **Scheduled & on-demand** -- cron-based periodic collection with manual trigger support
- **Dual storage** -- SQLite for production, in-memory for development and testing
- **E-ink dashboard** -- responsive HTML dashboard with progress bars and alerts, designed for 7.5" e-paper displays
- **Device integration** -- JSON endpoints for ESPHome devices and Home Assistant embedding
- **Flexible auth** -- Bearer token, login/JWT flow, or no-auth per collector

## Quick Start

### Prerequisites

- Go 1.24+
- (Optional) Docker for containerized deployment

### Build & Run

```bash
# Build
make build

# Run with config
make run

# Or directly
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

## Configuration

InfoHub uses YAML configuration with environment variable interpolation (`${VAR_NAME}`).

```yaml
server:
  port: ${INFOHUB_PORT}               # default: 8080
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
  type: "${INFOHUB_STORE_TYPE}"        # "sqlite" or "memory"
  sqlite_path: "./data/infohub.db"

log:
  level: "info"                        # debug | info | warn | error
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `INFOHUB_PORT` | HTTP server port | `8080` |
| `INFOHUB_AUTH_TOKEN` | API authentication token | -- |
| `INFOHUB_DASHBOARD_TOKEN` | Dashboard access token (separate from API auth) | -- |
| `INFOHUB_STORE_TYPE` | Storage backend: `sqlite` or `memory` | `memory` |
| `INFOHUB_SQLITE_PATH` | SQLite database file path | `./data/infohub.db` |
| `INFOHUB_LOG_LEVEL` | Log verbosity | `info` |

## API Reference

All API endpoints require `Authorization: Bearer <auth_token>` header.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/summary` | All source snapshots with latest data items |
| `GET` | `/api/v1/source/{name}` | Specific source snapshot |
| `GET` | `/api/v1/health` | Collector status and last fetch timestamps |
| `POST` | `/api/v1/collect/{name}` | Manually trigger a collector |
| `GET` | `/dashboard/eink` | HTML e-ink dashboard |
| `GET` | `/dashboard/eink.json` | Dashboard data as JSON |
| `GET` | `/dashboard/eink/device.json` | Device-optimized JSON payload |

Dashboard endpoints accept the token via query parameter (`?token=xxx`) or header.

### Response Examples

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

## Architecture

```
cmd/infohub/          Entry point, server bootstrap
internal/
  api/                HTTP handlers, middleware, dashboard rendering
  collector/          Collector interface + implementations
  config/             YAML config with env var expansion
  model/              Data models (DataItem, SourceSnapshot)
  scheduler/          Cron-based task scheduling
  store/              Storage interface (SQLite, in-memory)
deploy/
  esphome/            ESPHome device configs for e-ink displays
  homeassistant/      Home Assistant integration configs
```

### Adding a Collector

Implement the `Collector` interface and register it in `cmd/infohub/main.go`:

```go
type Collector interface {
    Name() string
    Collect(ctx context.Context) ([]model.DataItem, error)
}
```

## E-ink Display Integration

InfoHub includes first-class support for e-paper displays via ESPHome:

- **Target device**: reTerminal E1001 + Waveshare 7.5" e-paper
- **Display modes**: HTML dashboard (via screenshot) or device JSON endpoint
- **Refresh**: Automatic on schedule + GPIO button for manual refresh

See [`docs/en/`](docs/en/) for detailed setup guides:

- [First Flash Runbook](docs/en/infohub-eink-first-flash-runbook.md)
- [Direct API Panel](docs/en/infohub-eink-direct-api-panel.md)
- [Deploy & Display Tuning](docs/en/infohub-eink-deploy-and-display-tuning.md)
- [ESPHome Docker on macOS](docs/en/infohub-eink-esphome-docker-mac.md)
- [Partial Refresh Probe](docs/en/infohub-eink-partial-refresh-probe.md)

### ESPHome Commands

```bash
make esphome-up         # Start ESPHome container
make esphome-compile-stage1   # Compile first-flash firmware
make esphome-compile-stage2   # Compile production firmware
make esphome-logs       # Stream ESPHome logs
```

## Development

```bash
make fmt      # Format code
make test     # Run tests
make tidy     # Tidy dependencies
```

### Testing

Tests use the standard `testing` package with `httptest` for HTTP mocking and in-memory storage for isolation.

```bash
go test ./...
go test ./internal/collector/... -v   # Verbose collector tests
```

## License

[MIT](LICENSE)
