# 本地 Claude Code / Codex 用量采集与墨水屏显示方案

> 目标：在**没有任何服务器**的前提下，仅凭一台已安装 Claude Code 与 Codex CLI 的电脑，
> 实现两类工具的用量、额度统计，并复用现有 InfoHub + ESPHome 墨水屏链路展示。

> **当前实现状态（2026-04-26）**：已落地本地用量采集 MVP，并已同步本文档。
> `claude_local` / `codex_local` 已接入 collector 注册、配置、调度、SQLite schema、测试与
> `/dashboard/eink` / `/dashboard/eink.json` 展示。SQLite 模式已接入基于
> `local_parse_state` + `local_usage_events` 的增量续扫；memory store 仍保持全量扫描。
> Claude `mode=ccusage` 已支持外部进程解析，失败时自动回退 builtin。

## 0. 已完成实现记录

本次已经完成的代码落点如下：

| 模块 | 文件 | 完成内容 |
|------|------|----------|
| 本地采集器 | `internal/collector/local_usage.go` | 新增 `claude_local` / `codex_local` builtin JSONL 扫描、宽容解析、窗口聚合、quota / usage DataItem 输出 |
| 采集器测试 | `internal/collector/local_usage_test.go` | 覆盖 Claude JSONL、Codex JSONL 与路径缺失错误 |
| 配置模型 | `internal/config/config.go` | 增加 `LocalCollectorConfig` / `LocalQuotaConfig`，并为本地源设置默认路径、cron、mode |
| 配置文件 | `config.yaml`、`dist/config.yaml` | 增加 `claude_local` / `codex_local` 配置块与 quota 环境变量 |
| 入口注册 | `cmd/infohub/main.go` | 本地 collector 按 enabled 配置注册进 scheduler |
| 存储 schema | `internal/store/sqlite.go` | 新增 `local_parse_state` 与 `local_usage_events`，支持 SQLite 增量续扫与窗口回放聚合 |
| 调度注入 | `internal/scheduler/scheduler.go` | collector 支持时注入 store，使本地采集器可访问 SQLite 增量状态 |
| 墨水屏 API | `internal/api/dashboard.go` | `/dashboard/eink` / `/dashboard/eink.json` 优先展示本地源，缺失时回退远端源 |

当前行为边界：

- builtin + SQLite store：按 `local_parse_state` 从上次 byte offset 续扫，并从 `local_usage_events` 回放当前窗口事件聚合
- builtin + memory store：每轮全量递归扫描配置路径下的 `*.jsonl`
- 解析器只读取 usage / model / timestamp 等统计字段，不读取或输出 prompt 内容
- 文件截断或 mtime 倒退时会重置该文件的已解析事件并从头重扫
- `mode=ccusage` 当前只适用于 `claude_local`；`codex_local` 没有 ccusage 等价物，会走 builtin

## 1. 背景与动机

现有 InfoHub 数据源依赖 CRS（Claude Relay Service）与 sub2api 等"中转网关"。
这条链路存在以下局限：

- **覆盖不全**：本地直连官方订阅的 Claude Code / Codex 用量根本不会经过中转层
- **数据失真**：网关侧聚合粒度粗，5h 窗口、模型分布、缓存命中率等都拿不到
- **可用性绑定**：网关挂掉、Token 失效，仪表盘整片空白
- **隐私**：把全部对话流量绕到第三方网关，不是所有人都能接受

而 Claude Code 与 Codex CLI 的所有调用记录都已落在**本机磁盘**，是天然的、最权威、
最完整的数据源。把"统计"和"转发"解耦后，即便不挂任何网关，也能拿到比当前更精确
的用量视图。

## 2. 设计目标

| # | 目标 | 验收标准 |
|---|------|----------|
| G1 | 零服务器运行 | 仅依赖一台 Mac/Linux 桌面，端口走 LAN |
| G2 | 不动现有架构 | 通过新增 collector 接入，复用 store / scheduler / dashboard |
| G3 | 离线可用 | 即便公网不通，本地数据照常聚合 |
| G4 | 与墨水屏直连 | ESPHome 设备只需更换一个 URL 即可工作 |
| G5 | 隐私可控 | 仅读取本机 jsonl，不上传任何对话内容 |
| G6 | 渐进式落地 | MVP 当天可用，正式版 collector 可后续替换 |

## 3. 数据源调研

### 3.1 Claude Code

- **路径**：`~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl`
- **格式**：每行一个 JSON event，关键事件类型：
  - `user` / `assistant`：含 `message.usage`，给出 `input_tokens`、`output_tokens`、
    `cache_creation_input_tokens`、`cache_read_input_tokens`
  - `summary`：会话总结
  - `system`：模型切换、工具加载等
- **时间戳**：每条 event 含 `timestamp`（ISO 8601, UTC）
- **模型**：`message.model`（如 `claude-opus-4-7`、`claude-sonnet-4-6`）
- **5h 计费窗口**：Claude Code 客户端在 `~/.claude/statsig/`、`~/.claude/__store.db`
  里缓存本地估算；更稳妥的做法是**自己按时间戳重算**

### 3.2 Codex CLI

- **路径**：`~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl`
- **格式**：rollout JSONL，包含 `prompt`、`response`、`usage`（含 `input_tokens`、
  `output_tokens`、`reasoning_tokens`、`total_tokens`）
- **模型**：`response.model` 字段
- **时间戳**：rollout 内含 `created_at`（Unix epoch 秒）
- **额度**：ChatGPT 订阅（Plus/Pro/Team）的 Codex 配额官方未开放查询，
  本方案仅统计"已用"，"剩余"通过经验上限估算并打标"approx"

### 3.3 第三方助力工具：ccusage

> npm 包 `ccusage`（GitHub: ryoppippi/ccusage），社区维护，离线解析
> `~/.claude/projects` 并输出聚合 JSON

- 提供 `daily` / `monthly` / `blocks` 子命令
- 支持 `--json` 输出，字段稳定
- 可作为后续增强引入；当前已实现版本默认使用 builtin 解析器，避免 MVP 依赖 Node/npm

ccusage 当前只覆盖 Claude Code，没有 Codex 等价物。当前已实现版本中，Claude 与 Codex
都先走轻量自研解析（递归扫描 JSONL 并按窗口累加 usage）。

## 4. 总体架构

```
┌──────────────────── 用户 Mac（常开） ────────────────────┐
│                                                            │
│  数据源（只读）                                            │
│   ├─ ~/.claude/projects/**/*.jsonl                         │
│   ├─ ~/.claude/__store.db                                  │
│   └─ ~/.codex/sessions/**/*.jsonl                          │
│                                                            │
│             │              │                               │
│             ▼              ▼                               │
│  ┌──────────────────┐  ┌──────────────────┐                │
│  │ claude_local     │  │ codex_local      │  新增采集器   │
│  │  collector       │  │  collector       │                │
│  └──────────────────┘  └──────────────────┘                │
│             │              │                               │
│             ▼              ▼                               │
│  ┌────────────── InfoHub Core ──────────────┐              │
│  │  scheduler  →  store(SQLite)  →  api     │              │
│  └──────────────────────────────────────────┘              │
│                       │                                    │
│                       ▼                                    │
│       :8080  /dashboard/eink  /dashboard/eink.json         │
│                       │                                    │
└───────────────────────┼────────────────────────────────────┘
                        │ LAN (mDNS: mac.local 或静态 IP)
                        ▼
              ┌───────────────────┐
              │ ESPHome E-Paper   │  reTerminal + 7.5"
              │ online_image      │  按 N 分钟拉一次
              └───────────────────┘
```

关键点：

- **CRS / sub2api 等远端 collector 与本方案并存**，按需启用
- **ESPHome 端零代码改动**：只把 `online_image.url` 从云端 InfoHub 换到 `http://mac.local:8080`
- 数据源全部只读，永不写回原文件

## 5. 模块设计

### 5.1 新增采集器：`claude_local`

#### 5.1.1 配置项

```yaml
collectors:
  claude_local:
    enabled: ${CLAUDE_LOCAL_ENABLED}
    cron: "*/5 * * * *"
    timeout_seconds: 15
    paths:
      - "${HOME}/.config/claude/projects" # Claude Code 新版本默认路径
      - "${HOME}/.claude/projects"        # 旧版本 / 兼容路径
    mode: "builtin"                        # builtin | ccusage
    online:
      enabled: true                         # 只读 OAuth usage API，不修改 Claude Code 配置
      # auth_path: "${HOME}/.claude/.credentials.json"
      # base_url: "https://api.anthropic.com"
      # timeout_seconds: 8
      # stale_after_seconds: 60
    ccusage_bin: "npx"                     # ccusage 模式可改成绝对路径
    ccusage_args: ["ccusage@latest", "--json"]
    windows:
      - name: "5h"
        seconds: 18000
      - name: "today"
        boundary: "local-midnight"
      - name: "weekly"
        seconds: 604800
    quota:                                 # 经验上限，仅用于进度条；不存在则不显示
      plan: "max-200"                      # 给前端做标签
      five_hour_msg_cap: 800               # 5h 内消息估算上限
      weekly_msg_cap: null                 # 不设上限则隐藏；monthly_msg_cap 仍兼容旧配置
```

#### 5.1.2 行为

1. `Collect()` 触发后：
   - 已实现：`mode=builtin`，扫 `paths` 下的 `*.jsonl`，按 `timestamp` 与 `usage` 聚合；
     SQLite store 下会增量续扫并回放事件表，memory store 下全量扫描
  - 已实现：`mode=ccusage`，`exec.CommandContext(ctx, ccusage_bin, ccusage_args...)`
     拿 stdout，宽容解析输出 JSON 中的 usage rows
   - 已实现：`online.enabled=true` 时只读 Claude Code OAuth 凭据，调用 Anthropic
     OAuth usage API，提取真实 5H / 7D 剩余额度
2. 输出多个 `DataItem`，遵循现有 `Source/Category/Title/Value/Extra` 约定：

| Category | Title | Value 示例 | Extra |
|----------|-------|-----------|-------|
| `quota` | `5h_window` | `42.3%` | `used_msgs`、`cap`、`window_end_at`、`models{}` |
| `quota` | `today` | `123,456 tokens` | `input`、`output`、`cache_read`、`cache_creation` |
| `quota` | `weekly` | `3.2M tokens` | `messages`、`models` |
| `usage` | `model_top1` | `opus-4` | `share_percent`、`tokens` |
| `usage` | `cache_hit` | `78%` | `cache_read`、`total_input` |

3. 失败语义：
   - 路径不存在 → `SourceSnapshot.Status=error`，`Error="claude path missing"`
   - ccusage 不可用 → 自动降级到 `builtin` 模式（一次告警日志）
   - OAuth 凭据不存在、过期或 API 不可达 → 只展示本地估算额度；记录 warn 后继续采集

#### 5.1.3 Claude Code OAuth 额度接入

`claude_local.online.enabled=true` 时，InfoHub 参考 cc-switch 的做法，只读
Claude Code 现有 OAuth 凭据，不修改 `~/.claude/settings.json`：

1. macOS 优先读取 Keychain：`Claude Code-credentials`
2. 兜底读取 `${HOME}/.claude/.credentials.json`
3. 调用 `GET https://api.anthropic.com/api/oauth/usage`
4. 解析 `five_hour.utilization` / `resets_at` 与 `seven_day.utilization` / `resets_at`

成功后 `quota_source=claude_oauth_usage`。InfoHub 不实现 Claude 登录、不刷新 token、
不写回 Claude Code 凭据或配置文件。

#### 5.1.4 builtin 解析最小算法

```
for each *.jsonl in paths (递归):
  for each line:
    decode -> event
    if event.type in {assistant, user} and event.message.usage:
      bucket_key = (window, model)
      bucket.input += usage.input_tokens
      bucket.output += usage.output_tokens
      bucket.cache_read += usage.cache_read_input_tokens
      bucket.cache_creation += usage.cache_creation_input_tokens
      bucket.msg_count += 1
emit DataItem per (window, aggregate)
```

当前实现会在 SQLite store 下记录每个文件上次解析的字节偏移到 `local_parse_state`，
并把已解析 usage 事件写入 `local_usage_events`。后续采集只读新增行，再从事件表按当前
窗口重新聚合，避免只读增量行后丢失窗口内旧数据。

### 5.2 新增采集器：`codex_local`

结构与 `claude_local` 同构，差异只在路径与字段映射：

| Codex 字段 | 用途 |
|-----------|------|
| `payload.usage.input_tokens` | input |
| `payload.usage.output_tokens` | output |
| `payload.usage.reasoning_tokens` | reasoning（独立 Extra 项） |
| `payload.model` | 模型名 |
| `created_at` | 时间戳，归入对应窗口 |

输出 DataItem：

| Category | Title | 备注 |
|----------|-------|------|
| `quota` | `weekly` | Codex 订阅以"周"为参考周期 |
| `quota` | `today` | 同 Claude |
| `usage` | `model_top1` | `gpt-5-codex` 等 |
| `usage` | `reasoning_share` | reasoning_tokens / output_tokens |

当前实现的 Codex 解析器支持多种 rollout 形态：

- usage：`payload.usage`、`response.usage`、`usage`、`payload.response.usage`
- model：`payload.model`、`response.model`、`model`、`payload.response.model`
- 时间：`created_at`、`payload.created_at`、`response.created_at`、`timestamp`

#### 5.2.1 在线额度兜底

Codex CLI v0.125.x 起，`rollout-*.jsonl` 里的 `payload.rate_limits.primary` /
`secondary` 可能长期为 `null`。这时 `codex_local` 可以显式开启在线兜底：

```yaml
collectors:
  codex_local:
    online:
      enabled: true
      # auth_path: "${HOME}/.codex/auth.json"
      # base_url: "https://chatgpt.com"
      # timeout_seconds: 8
      # stale_after_seconds: 60
```

开启后，InfoHub 只读取 Codex CLI 已存在的 `${CODEX_HOME:-$HOME/.codex}/auth.json`，
用其中的 `tokens.access_token` 与 `tokens.account_id` 请求 ChatGPT 官方域名的
`/backend-api/wham/usage`。InfoHub 不保存 token、不刷新 token、不写回 Codex 文件；
token 失效或端点不可用时会静默降级到本地估算。

Quota DataItem 会通过 `extra.quota_source` 标识来源：

| 值 | 含义 |
|----|------|
| `codex_rate_limits` | 从 rollout JSONL 直读到的 5H / Week 百分比 |
| `codex_wham_usage` | 在线兜底从 ChatGPT 后端读取的真实百分比 |
| `estimated_cap` | 在线与本地观测都不可用时，按 config 中的经验 cap 估算 |

排错可看 `extra.online_quota_status`：`disabled`、`token_missing`、`unauthorized`、
`rate_limited`、`endpoint_404`、`transport_error` 或 `ok`。

### 5.3 注册流程

`cmd/infohub/main.go` 的 collector 注册位置追加两个分支，
保持与现有 `claude_relay`、`sub2api` 一致的 enabled / config 校验姿态。

### 5.4 仪表盘渲染

`internal/api/dashboard.go` 中：

- 新增两个 source key：`claude_local`、`codex_local`
- 复用现有进度条组件，5h 窗口可用 `extra.window_end_at` 渲染倒计时
- 当前实现：墨水屏面板优先展示 `claude_local` / `codex_local`，不存在本地源时回退到
  `claude_relay` / `sub2api`，并保持远端 collector 可并存
- 后续如要在同一屏同时展示本地源与远端源，应按 source 分卡片展示，不要做"自动合并"——
  两个源对"额度"的定义不同，合并易误导

## 6. 部署形态

### 6.1 进程常驻（macOS）

`~/Library/LaunchAgents/com.infohub.local.plist`：

```xml
<plist>
  <dict>
    <key>Label</key><string>com.infohub.local</string>
    <key>ProgramArguments</key>
    <array>
      <string>/usr/local/bin/caffeinate</string>
      <string>-i</string>
      <string>/Users/hcy/code/InfoHub/bin/infohub</string>
      <string>-config</string>
      <string>/Users/hcy/code/InfoHub/config.yaml</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardOutPath</key><string>/tmp/infohub.log</string>
    <key>StandardErrorPath</key><string>/tmp/infohub.err</string>
  </dict>
</plist>
```

`launchctl load -w ~/Library/LaunchAgents/com.infohub.local.plist`

### 6.2 进程常驻（Linux）

systemd user unit：`~/.config/systemd/user/infohub.service`，
`Restart=always`，`systemctl --user enable --now infohub`。

### 6.3 网络发布

- 监听 `0.0.0.0:8080`（避免只绑 `127.0.0.1`）
- mDNS：macOS 自动 `mac.local`，Linux 装 `avahi-daemon` 即可
- 静态 IP（路由器 DHCP 绑定）作为 fallback
- 防火墙：只放 LAN 段，例如 `192.168.0.0/16`

### 6.4 墨水屏侧

在 ESPHome YAML 里把 `online_image` 的 URL 改成：

```yaml
online_image:
  url: "http://mac.local:8080/dashboard/eink?token=${INFOHUB_DASHBOARD_TOKEN}"
  format: PNG
  update_interval: 5min
```

如果 mDNS 不稳定，换成静态 IP `http://192.168.1.50:8080/...`。

## 7. 存储变更

新增表（仅当 `store.type=sqlite` 时启用）：

```sql
CREATE TABLE IF NOT EXISTS local_parse_state (
  source       TEXT NOT NULL,         -- claude_local / codex_local
  file_path    TEXT NOT NULL,
  byte_offset  INTEGER NOT NULL,
  mtime_unix   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  PRIMARY KEY (source, file_path)
);
```

另有事件表用于回放窗口聚合：

```sql
CREATE TABLE IF NOT EXISTS local_usage_events (
  source         TEXT NOT NULL,
  file_path      TEXT NOT NULL,
  byte_offset    INTEGER NOT NULL,
  at_unix        INTEGER NOT NULL,
  model          TEXT NOT NULL,
  input          REAL NOT NULL,
  output         REAL NOT NULL,
  cache_read     REAL NOT NULL,
  cache_creation REAL NOT NULL,
  reasoning      REAL NOT NULL,
  PRIMARY KEY (source, file_path, byte_offset)
);
```

读流程：

1. 启动时 `SELECT byte_offset, mtime_unix FROM local_parse_state WHERE source=?`
2. 若 `os.Stat(file).Size() == byte_offset` 且文件未重置，跳过该文件
3. 若文件变大，从 `byte_offset` 续读新增 JSONL 行并写入 `local_usage_events`
4. 解析完更新 `byte_offset`、`mtime_unix`
5. 采集输出前，从 `local_usage_events` 查询覆盖当前窗口范围的事件并重新聚合

mtime 倒退或文件变小（如文件被覆盖、轮转）→ 删除该文件旧事件，重置 offset 为 0。

`store.type=memory` 时跳过持久化，每次全量扫描，作为 dev 路径。

## 8. 兼容性与边界

| 场景 | 对策 |
|------|------|
| Mac 合盖外出 | 接受"墨水屏停在最后快照"。需要持续更新就把 InfoHub 跑在 NAS / 树莓派，用 Syncthing 同步 jsonl 目录 |
| 多机协同（家+公司 Mac） | 一台跑 InfoHub，另一台 Syncthing 同步 `~/.claude/projects` 到本地副本，作为第二个 path |
| Claude Code 升级改 jsonl 格式 | builtin 解析器写为"宽容模式"，缺字段降级；ccusage 升级跟随上游 |
| Codex 订阅额度未知 | 仅展示"已用"+"经验上限"，UI 注明"approx"，避免误判 |
| 隐私 | 解析过程**只读 usage 字段**；明确 .gitignore 防止误把 jsonl 拷进仓库；config 不要写入 prompt 内容 |
| 墨水屏鉴权 | 用 `dashboard_token`（已有）+ 查询参数，LAN 内足够；不引入 HTTPS |

## 9. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| ccusage 上游字段变更 | 解析失败 | 已自动 fallback 到 builtin；生产环境可版本锁到 `ccusage@<X.Y>` |
| jsonl 文件巨大（数 GB） | 首次全量扫慢 | SQLite store 首次后走增量 offset；后续可增加 `--max-history-days` 限制首次回填 |
| Mac 频繁休眠 | 数据断点 | launchd plist 示例包含 `caffeinate -i`；或迁移 NAS |
| 5h / week 窗口估算偏差 | 进度条不准 | quota.cap 暴露在 config，用户可校准；UI 标注"estimated" |
| 墨水屏在断网时全白 | 体验差 | ESPHome 侧加 `cache_image: true`，离线显示上一帧 |

## 10. 落地计划

> **不给时间估算**，只列依赖与里程碑顺序。

### M0 — MVP（端到端打通）

- [x] 引入 ccusage 执行路径：`mode=ccusage` 时执行 `ccusage_bin + ccusage_args`，失败自动 fallback builtin
- [x] 在 `internal/collector/` 新增本地用量采集器（`local_usage.go`），已实现 `claude_local` builtin 解析
- [x] `cmd/infohub/main.go` 注册 collector，`config.yaml` / `dist/config.yaml` 增加配置块
- [x] 仪表盘加上 `claude_local` 展示，优先展示本地源并保留远端源回退
- [ ] launchd plist 上线
- [x] 本地浏览器验收：`/dashboard/eink` 能看到本地 5h 窗口
- [x] 本文档同步当前 builtin MVP 实现状态

### M1 — Codex 接入

- [x] 新增 `codex_local` builtin 解析（与 `claude_local` 共享 `local_usage.go`）
- [x] config 增加 `quota.weekly_msg_cap` / `quota.weekly_token_cap` 字段，collector Extra 增加 `approx`
- [x] 验收：屏上同时显示 Claude Local 与 Codex Local 卡片
- [x] 文档记录 Codex rollout 字段兼容策略

### M2 — 增量解析与稳定性

- [x] 新增 `local_parse_state` 表 schema、store 接口与 collector 增量续扫
- [x] 新增 `local_usage_events` 表，避免增量续扫覆盖窗口内旧数据
- [x] builtin 模式接管为默认路径，ccusage 变成后续可选加速器
- [x] 测试覆盖：SQLite 增量追加、文件截断重置、ccusage JSON 解析
- [ ] 测试覆盖：大文件、轮转文件、跨时区时间戳
- [ ] 验收：1GB 历史 jsonl 下，单次采集 < 2s

### M3 — 多机同步（可选）

- [ ] 文档化 Syncthing 接入步骤（`docs/zh/infohub-local-multi-host.md`）
- [x] `paths:` 已支持多入口配置，并会跳过重复的规范化路径
- [x] SQLite 增量状态以 `source+file_path` 为键保存
- [ ] 跨入口同步产生的同内容不同路径文件去重

## 11. 验收 Checklist

- [x] 本地无外部网关依赖 → InfoHub 正常出 `claude_local` / `codex_local` 数据
- [x] 本地浏览器访问 `/dashboard/eink` 渲染正确（已用 `127.0.0.1:18081` 验证）
- [ ] launchctl 重启后服务自动拉起
- [x] 进程对 `~/.claude/projects` / `~/.codex/sessions` 只读扫描，不写回原始 JSONL
- [x] `go test ./...` 通过；新增采集器有 table-driven 测试
- [ ] README 增加"本地模式"章节并互链本文档
- [x] 本文档已补齐已完成实现记录、当前边界与后续项
- [x] SQLite 增量续扫与 ccusage fallback 已实现并通过 `go test ./...`

### 已验证输出（2026-04-26，本机）

- `claude_local`: `status=ok`，今日 `1,097,023` tokens，5H 剩余 `97%`
- `codex_local`: `status=ok`，今日暂无本日用量，5H / Week 剩余按未配置 cap 显示 `100%`
- `/dashboard/eink.json`: 同时返回 Claude Local / Codex Local / 合计卡片，`alerts=[]`

## 12. 关联资料

- 现有架构：`README.md` §项目结构、`internal/collector/collector.go`
- 已有墨水屏链路：`docs/zh/infohub-eink-direct-api-panel.md`
- ccusage 上游：<https://github.com/ryoppippi/ccusage>
- Claude Code OTel 监控（备选数据源）：官方 `docs.claude.com/en/docs/claude-code/monitoring-usage`

---

> **状态**：MVP 已实现并通过本机验证；SQLite 增量续扫与 Claude ccusage 模式已实现；
> launchd 上线、README 互链、Syncthing 独立文档与大文件压测待补。
> **作者**：本文档由 Claude Code 协助起草，并随实现进展更新。
