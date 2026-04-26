# Codex 在线额度查询 实现规格

> 目标读者：Codex（实现执行者）
> 仓库：`infohub`（Go）
> 关联代码：`internal/collector/local_usage.go`、`internal/config/config.go`
> 关联现有文档：`docs/zh/infohub-local-claude-codex-usage.md`

---

## 1. 背景与问题

`internal/collector/local_usage.go` 通过解析 `~/.codex/sessions/**/rollout-*.jsonl` 中
`type=event_msg` 且 `payload.type=token_count` 的事件，读取
`payload.rate_limits.primary` (5H) 与 `payload.rate_limits.secondary` (Week)
的 `used_percent`，作为 Codex 配额仪表盘的"观测值"。

自 Codex CLI v0.125.x 起，`rollout-*.jsonl` 的 `rate_limits` 字段长期为 `null`：

```json
"rate_limits": {
  "limit_id": "codex",
  "primary": null,
  "secondary": null,
  "credits": null,
  "plan_type": null,
  "rate_limit_reached_type": null
}
```

`extractCodexRateLimits` (`local_usage.go:564`) 因此始终返回 `OK=false`，
`quotaItem` (`local_usage.go:762`) 退化为基于 `WeeklyCap` 的本地估算，
导致 Codex 周配额恒显示 ~100% 剩余，与真实情况严重偏离（实测仅剩 20%+）。

Codex CLI 自身、cc-switch、codex-quota、codex-ratelimit 等工具均通过调用
ChatGPT 后端 `wham/usage` 端点拿到真实百分比。本规格要求把同样的数据通路
集成到 InfoHub，作为本地解析失效时的兜底数据源。

---

## 2. 目标与非目标

### 目标

1. 当 `payload.rate_limits.{primary,secondary}` 为 `null` 时，从 ChatGPT 后端
   拉取真实的 5H / Week 配额，回填进 `localRateLimits.FiveHour/Week`。
2. 配额项 `quota_source` 字段能区分三种来源：
   `codex_rate_limits`（rollout 直读）、`codex_wham_usage`（在线兜底）、
   `estimated_cap`（最后的本地估算）。
3. 复用 Codex CLI 已经写好的 `~/.codex/auth.json`，**不要求用户额外登录**。
4. 与现有 `LocalUsageCollector.Collect` 流程对齐：失败必须降级，绝不阻塞采集。
5. 全程仅访问 ChatGPT 官方域名，不引入新的第三方依赖。

### 非目标

- 不实现 OAuth Refresh Token 全流程。token 过期时静默降级，由用户自己跑 `codex` 触发刷新。
- 不替换现有 builtin / ccusage 模式；只在 `localCodexSource` 路径增加可选的在线层。
- 不改变现有 `model.DataItem` 的对外字段语义，只新增 `quota_source` 取值。
- 不做仪表盘前端改造（前端按现有 `extra.used_percent` / `extra.remaining_percent` 渲染即可）。

---

## 3. 数据通路

### 3.1 端点

```
GET https://chatgpt.com/backend-api/wham/usage
Host: chatgpt.com
Authorization: Bearer <tokens.access_token>
ChatGPT-Account-Id: <tokens.account_id>
Accept: application/json
User-Agent: infohub-codex-quota/1.0 (+https://github.com/<owner>/InfoHub)
```

> 端点路径以 Codex CLI 二进制中的实际值为准（`backend-client/src/client.rs::get_rate_limits`）。
> 若 `/wham/usage` 返回 404，再尝试 `/wham/api/codex/usage`、`/api/codex/usage` 两个回退路径，
> 取首个 200 响应。命中后把成功路径写入 store，下次直接命中。

### 3.2 凭据来源

文件路径：`${CODEX_HOME:-${HOME}/.codex}/auth.json`

实测 schema（不要把真实值写进日志）：

```jsonc
{
  "auth_mode": "chatgpt",
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "...",
    "access_token": "...",   // 取这个做 Bearer
    "refresh_token": "...",
    "account_id": "..."      // 取这个做 ChatGPT-Account-Id
  },
  "last_refresh": "2026-..."
}
```

读取规则：
- 文件不存在 / 解析失败 / `tokens` 缺失 → 在线层禁用，降级到本地估算，记录 `slog.Warn`。
- 文件读取走 `os.ReadFile`，权限要求 `0600`（仅作日志提示，不强制拒绝）。
- 不缓存 token 到 InfoHub 自己的 store，每次采集前重新读文件，避免 Codex CLI 刷新后我们用旧 token。

### 3.3 响应结构（按 Codex CLI 内部模型推断）

```jsonc
{
  "rate_limits": {
    "primary": {
      "used_percent": 32.5,
      "window_minutes": 300,
      "resets_at": 1777215600
    },
    "secondary": {
      "used_percent": 78.0,
      "window_minutes": 10080,
      "resets_at": 1777392000
    }
  }
}
```

字段映射：
- `primary` → `localRateLimits.FiveHour`
- `secondary` → `localRateLimits.Week`
- `resets_at` 兼容秒级 unix 时间戳与 RFC3339 字符串两种类型；`parseEventTime` 已能处理。

若响应包裹在 `data`、`payload`、`result` 之类的键下，按 `firstNestedMap` 的方式逐级尝试，
保证健壮。

---

## 4. 模块设计

### 4.1 新增文件 `internal/collector/codex_online_quota.go`

```go
package collector

// CodexOnlineQuotaClient 负责调用 ChatGPT 后端 wham/usage 端点。
// 该客户端不会自行重试鉴权，token 失败由调用方决定如何降级。
type CodexOnlineQuotaClient struct {
    httpClient   *http.Client
    authPath     string         // 默认 ${CODEX_HOME}/auth.json，测试可注入
    baseURL      string         // 默认 https://chatgpt.com
    pathOrder    []string       // 按顺序尝试的 endpoint 路径
    successPath  string         // 命中后记忆，下次先试
    successPathM sync.RWMutex
    now          func() time.Time
    logger       *slog.Logger
}

// FetchRateLimits 返回最新的 5H / Week 观测；若禁用、失败、token 无效则返回 (zero, false, nil)。
// 仅当遇到不可预期的传输/编码错误时返回 error。
func (c *CodexOnlineQuotaClient) FetchRateLimits(ctx context.Context) (localRateLimits, bool, error)
```

实现要点：
- HTTP 客户端使用 `http.Client{ Timeout: cfg.TimeoutSeconds }`，再叠 `ctx`。
- 严格按 `Authorization` + `ChatGPT-Account-Id` 设置 header，**不要**自动跟随 30x（避免被引导到登录页）。
- 状态码处理：
  - `200` → 解析 JSON。
  - `401 / 403` → 视为 token 失效，记 `Warn`（含 `last_refresh`），返回 `(zero, false, nil)`。
  - `404` → 把当前路径标记失败，下次尝试下一条 `pathOrder`。全部 404 后日志 `Error` 一次，长记忆失败 30 分钟。
  - `429` → 视为上游限流，返回 `(zero, false, nil)`，并把整个客户端短路 60 秒。
  - 其它 5xx → `Warn` + 短路 30 秒。
- 不要在日志中打印 `access_token` / `id_token` / `refresh_token` / `account_id`；可打印这些字段的长度作为可观测性指标。

### 4.2 修改 `internal/collector/local_usage.go`

只在 `c.source == localCodexSource` 时启用在线兜底。位置见 `buildItems` 中
`latestQuota` 的取值流程（`local_usage.go:671-684`）：

1. 在 `LocalUsageCollector` 上加字段：
   ```go
   onlineCodexQuota *CodexOnlineQuotaClient // 可空，nil 表示不启用
   ```
   通过新方法 `SetCodexOnlineQuotaClient(client *CodexOnlineQuotaClient)` 注入。
2. `Collect` 仍走原有 `collectEvents` → `buildItems`。
3. 在 `buildItems` 内、构造 `latestQuota` 之前：
   - 若 `c.source == localCodexSource && c.onlineCodexQuota != nil && (latestQuota 缺 5H 或 Week)`，
     调用 `FetchRateLimits(ctx)`：
     - 仅替换缺失的窗口（rollout 文件中如果还能拿到 primary，优先信任 rollout 的较新值）。
     - 把命中标记落到一个本地变量 `quotaSourceForWindow map[string]string{"5H": "...", "Week": "..."}`。
4. `quotaItem` 渲染时按这个 `quotaSourceForWindow` 写 `extra.quota_source`：
   - rollout 命中 → `codex_rate_limits`
   - 在线命中 → `codex_wham_usage`
   - 都没有 → `estimated_cap`（保持现状）。
5. 注意：`Collect` 的 `ctx` 必须传到 `buildItems`，目前 `buildItems` 没有 ctx 参数。
   修改其签名为 `buildItems(ctx context.Context, events []localUsageEvent)`，所有调用点同步更新。

### 4.3 配置：`internal/config/config.go`

`LocalQuotaConfig` 不动，新增 `LocalCodexOnlineConfig`：

```go
type LocalCollectorConfig struct {
    // ... 既有字段保持不变
    Online LocalCodexOnlineConfig `yaml:"online"` // 仅 codex_local 使用
}

type LocalCodexOnlineConfig struct {
    Enabled         bool   `yaml:"enabled"`           // 默认 false（向后兼容）
    AuthPath        string `yaml:"auth_path"`         // 默认 ${HOME}/.codex/auth.json
    BaseURL         string `yaml:"base_url"`          // 默认 https://chatgpt.com
    TimeoutSeconds  int    `yaml:"timeout_seconds"`   // 默认 8
    UserAgent       string `yaml:"user_agent"`        // 默认 infohub-codex-quota/1.0
    StaleAfterSec   int    `yaml:"stale_after_seconds"` // 缓存过期秒数，默认 60
}
```

`applyDefaults("codex_local")` 中：

- 仅当 `source == "codex_local"` 时填默认值。
- `Enabled` 默认值取 `false`。**只有用户在 `config.yaml` 显式打开后才生效**，
  避免老用户升级后突然出网。

`config.yaml` 示例（同步更新 `dist/config.yaml`）：

```yaml
collectors:
  codex_local:
    enabled: true
    online:
      enabled: true
      # auth_path: ~/.codex/auth.json
      # base_url: https://chatgpt.com
      # timeout_seconds: 8
      # stale_after_seconds: 60
```

### 4.4 装配：`cmd/infohub/main.go`

在创建 `NewCodexLocalCollector` 后：

```go
codexCollector := collector.NewCodexLocalCollector(cfg.Collectors.CodexLocal, logger)
codexCollector.SetStore(store)
if cfg.Collectors.CodexLocal.Online.Enabled {
    online := collector.NewCodexOnlineQuotaClient(cfg.Collectors.CodexLocal.Online, logger)
    codexCollector.SetCodexOnlineQuotaClient(online)
}
```

`NewCodexOnlineQuotaClient` 内部完成路径展开（`~` 与 `${CODEX_HOME}` / `${HOME}`），
默认值兜底，`http.Client` 实例化。

### 4.5 缓存与节流

- 在 `CodexOnlineQuotaClient` 内部维护一个 `lastResult struct{ at time.Time; limits localRateLimits }`。
- 每次 `FetchRateLimits` 先看 `time.Since(lastResult.at) < cfg.StaleAfter`，命中直接返回缓存。
- `409/429/5xx` 触发的"短路"用单独的 `breakerUntil time.Time` 字段控制，不污染正常缓存。
- 不要把这份缓存写进 SQLite；进程重启后重新拿一次即可。

---

## 5. 错误处理与可观测性

### 日志级别约定

- `Debug`：每次发起请求、命中缓存、命中短路。
- `Info`：在线层首次成功 / 首次失败（带原因短语）。
- `Warn`：401/403/路径全部 404 / token 文件不存在或字段缺失。
- `Error`：JSON 解码失败、连接被重置等不可预期异常。

### 字段可见性

- 日志只允许出现 `auth_path`、`base_url`、HTTP 状态码、字段长度、`last_refresh`。
- 严禁打印 token 任何前缀/后缀。

### 失败语义

- 在线层从不让 `Collect` 返回 error。
- 在线失败 → `quota_source` 退到 `estimated_cap`，同时在 `extra` 中加：
  ```go
  "online_quota_status": "disabled" | "token_missing" | "unauthorized" | "rate_limited" | "endpoint_404" | "transport_error" | "ok"
  ```
  方便仪表盘排错。

---

## 6. 测试计划

新增 `internal/collector/codex_online_quota_test.go`，覆盖：

1. **happy path**：用 `httptest.NewServer` 模拟成功响应，断言
   `localRateLimits.FiveHour/Week.UsedPercent` 与期望相等，`OK=true`。
2. **path fallback**：第一次 `/wham/usage` 返回 404，第二次 `/wham/api/codex/usage` 200。
   断言 `successPath` 被记忆，第二次调用直接命中第二条路径。
3. **401**：返回 401 + 任意 body，断言 `(zero, false, nil)` 且日志包含 `last_refresh`。
4. **429 短路**：连续两次 `FetchRateLimits`，第二次必须直接返回 false 而不发请求
   （断言 server hit count == 1）。
5. **缓存命中**：`StaleAfter=60`，连续三次 `FetchRateLimits`，server hit count == 1。
6. **auth.json 缺失**：临时改 `authPath` 到不存在的路径，返回 false 且不发起 HTTP。
7. **auth.json 缺 tokens**：写一个空 `{}`，返回 false。

修改 `internal/collector/local_usage_test.go`：

8. 新增 `TestCodexLocalCollector_OnlineQuotaFallback`：
   - 构造一份 `rate_limits` 全为 null 的 rollout JSONL。
   - 用 fake online client（实现一个测试桩接口或暴露 `FetchRateLimits` 注入点）
     返回 `FiveHour=10%, Week=78%`。
   - 断言：
     - `账号 Codex Local Week 额度` 的 `Value == "22%"`（`100 - 78`）。
     - `extra.quota_source == "codex_wham_usage"`。
     - `extra.online_quota_status == "ok"`。
9. 新增 `TestCodexLocalCollector_OnlinePrefersRolloutWhenAvailable`：
   - rollout 中 `primary` 有真值、`secondary` 为 null。
   - online 同时返回 5H 与 Week。
   - 断言 5H 用 rollout 来源（`codex_rate_limits`），Week 用在线来源
     （`codex_wham_usage`）。

`make test` 等价命令：`go test ./internal/collector/...`

---

## 7. 验收标准（DoD）

- [ ] 新增文件 `internal/collector/codex_online_quota.go`，含 `CodexOnlineQuotaClient`
      与构造函数 `NewCodexOnlineQuotaClient`。
- [ ] `LocalUsageCollector` 暴露 `SetCodexOnlineQuotaClient`，`buildItems` 接受 `ctx`。
- [ ] `LocalCollectorConfig` 增加 `Online`，默认禁用，配置文件不写时行为完全等于现状。
- [ ] `dist/config.yaml` 给出 `online: enabled: true` 的示例（保持 `enabled: false` 默认值，
      但加注释说明启用方式）。
- [ ] `cmd/infohub/main.go` 在 online enabled 时注入客户端。
- [ ] `quotaItem` 在 online 命中时输出 `quota_source=codex_wham_usage`。
- [ ] 新增的 8、9 两个集成测试通过；既有 `TestCodexLocalCollectorCollectsBuiltinJSONL`
      仍然通过。
- [ ] 实测：在我本机（有 `~/.codex/auth.json`）启动 InfoHub 后，
      `/api/dashboard` 返回的 Codex Week 额度与 `codex` TUI `/status` 一致（误差 ±1%）。
- [ ] README 或 `docs/zh/infohub-local-claude-codex-usage.md` 增补一节"在线兜底"，
      解释如何启用与隐私边界。

---

## 8. 风险与对策

| 风险 | 对策 |
| --- | --- |
| 端点路径未来再次变化 | `pathOrder` 顺序可配置；并保留 `successPath` 记忆。 |
| Token 在采集期间被 CLI 刷新 | 每次采集都重新 `os.ReadFile`，不进程内长缓存。 |
| 高频访问被 ChatGPT 风控 | `StaleAfter=60s` + 5xx/429 自动短路。Cron 默认 5 分钟。 |
| 用户没注意默认 enabled | 默认 `Online.Enabled = false`，必须显式开启。 |
| 上游响应字段名变更 | 用 `firstNestedMap` / `firstNumber` 兼容多种命名；解析失败走降级。 |
| 跨设备同步污染 token 文件 | `auth.json` 不复制到 InfoHub 持久化目录，每次按需读取。 |

---

## 9. 实施顺序建议

1. 先开 PR 框架：新增空的 `codex_online_quota.go` + 单元测试桩，跑通 `go build`。
2. 加 `LocalCodexOnlineConfig` + 默认值 + 配置示例。
3. 实现 `CodexOnlineQuotaClient`（含路径回退、缓存、短路）+ 单元测试 1-7。
4. 改 `local_usage.go`：`buildItems(ctx, events)` 签名变更 + 在线注入点。
5. 加 `quota_source` / `online_quota_status` 字段，调整 `quotaItem`。
6. 加 `local_usage_test.go` 集成测试 8、9。
7. 在 `cmd/infohub/main.go` 装配。
8. 实测仪表盘，更新文档。

---

## 10. 提交规范

- Commit 全部英文，遵循 Conventional Commits。建议拆分：
  - `feat(collector): add online codex quota client`
  - `feat(collector): wire online quota into codex local collector`
  - `feat(config): add codex_local.online config block`
  - `docs(zh): describe codex online quota fallback`
  - `test(collector): cover online quota fallback paths`
- 不修改与本特性无关的文件。
- 不在日志中输出任何 token；提交前 `git diff` 自查。

---

## 附录 A：参考实现来源

- Codex CLI：`tui/src/chatwidget.rs::ChatWidget::prefetch_rate_limits` →
  `backend-client/src/client.rs::get_rate_limits`（每 ~60s 调用 wham 端点）
- cc-switch v3.14.1：托盘菜单的 Codex 用量展示（React Query 缓存 + 节流）
- xiangz19/codex-ratelimit：用 Python 解析 `~/.codex/sessions/*.jsonl` 的旧通路（已失效，仅参考字段命名）
- knightli.com `codex-quota` 系列文章：从 `auth.json` 读 token + 调 `wham/usage`

> 以上工具均使用同一对字段：`primary.used_percent` / `secondary.used_percent` /
> `resets_at`。本特性沿用同一份语义，并把它对接进 `localRateLimits`。
