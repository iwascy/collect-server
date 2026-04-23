# InfoHub EInk（reTerminal E1001）部署与显示调优全记录

这份文档把本次 `reTerminal E1001 + ESPHome + Home Assistant + InfoHub API` 的实际落地过程完整串起来，重点记录：

- 最终采用的主线方案
- 目录结构和可执行命令
- 第一次 USB 首刷和后续 OTA 更新流程
- 墨水屏白屏、401、错误接口地址等关键坑位
- 当前已经验证可用的显示参数和交互行为

如果后续要重搭环境，优先看这一份，再按文中链接跳转到细分 runbook。

## 一页结论

这次最终跑通的主线不是 `UTM + HAOS + ESPHome add-on`，而是：

1. `Mac Studio` 运行 `collect-server`
2. `OrbStack Docker` 独立运行 `ESPHome Dashboard`
3. 用浏览器 `web.esphome.io` 做第一次 USB 首刷
4. 首刷成功后，切到业务固件并通过 OTA 更新
5. 设备直接请求 `collect-server` 的 `/dashboard/eink/device.json`
6. `Home Assistant` 负责设备发现和可选的 iframe 看板，不再承担主编译链路

这条链路已经验证可用，当前设备能正常显示业务面板。

## 最终架构

当前落地形态如下：

```text
Mac Studio
├── collect-server
│   ├── HTML dashboard: /dashboard/eink
│   ├── debug JSON:     /dashboard/eink.json
│   └── device JSON:    /dashboard/eink/device.json
├── OrbStack Docker
│   └── ESPHome Dashboard (http://localhost:6052)
├── web.esphome.io
│   └── 首次 USB 刷入 factory 固件
└── reTerminal E1001
    ├── Stage 1: 首刷诊断固件
    └── Stage 2: InfoHub API 面板固件

Home Assistant
├── Web: http://10.30.5.227:8123/
└── 可选：挂 iframe 版 InfoHub dashboard
```

主机侧当前已知地址：

- `collect-server`：`http://10.30.5.172:8080`
- `Home Assistant`：`http://10.30.5.227:8123`
- `ESPHome Dashboard`：`http://localhost:6052`

## 为什么改成这条主线

前期阻塞主要集中在两类问题：

- `HAOS + add-on` 编译链过长，排障面太大
- `utmctl exec + docker exec` 在长时间编译场景下不稳定

改成 `Mac + OrbStack + 独立 ESPHome Docker` 后，有几个明显好处：

- 编译和 dashboard 管理由宿主机直接掌控
- `factory` 固件可以稳定手动下载
- 首刷和 OTA 可以分成两阶段排障
- 即使 `Home Assistant` 暂时有问题，也不影响 ESPHome 编译和设备刷写

## 仓库内关键文件

本次过程最终落到这些文件上：

- `Makefile`
- `deploy/esphome/docker/compose.yaml`
- `deploy/esphome/secrets.example.yaml`
- `deploy/esphome/reterminal_e1001_first_flash_alt.yaml`
- `deploy/esphome/reterminal_e1001_infohub_api.yaml`
- `docs/infohub-eink-esphome-docker-mac.md`
- `docs/infohub-eink-first-flash-runbook.md`
- `docs/infohub-eink-direct-api-panel.md`
- `docs/mockups/reterminal-e1001-ui-v3.svg`
- `docs/mockups/reterminal-e1001-ui-v3.png`

其中两份最关键的固件配置是：

- Stage 1 首刷：`deploy/esphome/reterminal_e1001_first_flash_alt.yaml`
- Stage 2 业务固件：`deploy/esphome/reterminal_e1001_infohub_api.yaml`

## 目录结构

```text
/Users/cyan/code/collect-server/
├── Makefile
├── deploy/
│   └── esphome/
│       ├── docker/
│       │   ├── compose.yaml
│       │   └── .env.example
│       ├── secrets.example.yaml
│       ├── reterminal_e1001_first_flash.yaml
│       ├── reterminal_e1001_first_flash_alt.yaml
│       ├── reterminal_e1001_infohub.yaml
│       └── reterminal_e1001_infohub_api.yaml
└── docs/
    ├── infohub-eink-esphome-docker-mac.md
    ├── infohub-eink-first-flash-runbook.md
    ├── infohub-eink-direct-api-panel.md
    └── mockups/
        ├── reterminal-e1001-ui-v2.svg
        ├── reterminal-e1001-ui-v2.png
        ├── reterminal-e1001-ui-v3.svg
        └── reterminal-e1001-ui-v3.png
```

## 完整部署流程

### 1. 选择 Docker 路线，而不是继续把 HA add-on 当主链路

当前推荐做法：

- 编译、管理 YAML、下载固件都走宿主机上的 `ESPHome Dashboard`
- `Home Assistant` 只做接入和展示
- 第一次刷机依赖浏览器 Web Serial

不再建议把“HA add-on 是否能稳定长时间 compile”作为主阻塞点。

### 2. 准备 secrets 和环境变量

在仓库根目录执行：

```bash
cd /Users/cyan/code/collect-server
cp deploy/esphome/secrets.example.yaml deploy/esphome/secrets.yaml
cp deploy/esphome/docker/.env.example deploy/esphome/docker/.env
```

然后按实际情况填写 `deploy/esphome/secrets.yaml`。

各字段来源如下：

| 字段 | 来源 |
| --- | --- |
| `wifi_ssid` | 设备要连接的 `2.4GHz Wi-Fi` 名称 |
| `wifi_password` | 对应 Wi-Fi 密码 |
| `wifi_fallback_password` | 设备 fallback AP 自定义密码 |
| `esphome_api_encryption_key` | 本机执行 `openssl rand -base64 32` 生成 |
| `esphome_ota_password` | 本机执行 `openssl rand -hex 16` 生成 |
| `infohub_eink_device_url` | `collect-server` 的设备直连接口地址，格式见下文 |

生成密钥命令：

```bash
openssl rand -base64 32
openssl rand -hex 16
```

Stage 2 使用的设备接口地址格式：

```text
http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300
```

如果使用线上域名，也应该是 `device.json` 地址，而不是 HTML 看板地址：

```text
https://summary.cccy.fun/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300
```

### 3. 启动 OrbStack 上的 ESPHome Dashboard

本次环境里，如果直接执行 `docker compose`，很容易遇到：

```text
Cannot connect to the Docker daemon at unix:///Users/cyan/.docker/run/docker.sock
```

原因是当前实际使用的是 `OrbStack`，不是默认 Docker context。

所以建议统一显式带上：

```bash
DOCKER_CONTEXT=orbstack
```

常用命令如下：

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-config
make DOCKER_CONTEXT=orbstack esphome-pull
make DOCKER_CONTEXT=orbstack esphome-up
make DOCKER_CONTEXT=orbstack esphome-logs
make DOCKER_CONTEXT=orbstack esphome-ps
```

启动后打开：

```text
http://localhost:6052
```

### 4. 处理 PlatformIO / ESP-IDF 在 macOS 共享目录下的编译问题

中途实际遇到过 `PlatformIO` 安装 `framework-espidf` 时复制失败的问题，典型报错是大量：

- `No such file or directory`
- `shutil.Error`
- 安装包位于 `/config/.esphome/platformio/...`

根因不是 YAML 语法，而是 `ESP-IDF` 大量文件在 macOS 共享目录里复制时不稳定。

最终稳定方案是：

- `/config` 仍映射仓库里的 `deploy/esphome`
- `PlatformIO` 缓存走 Docker volume：`/cache`
- 编译产物走 Docker volume：`/build`
- CLI 编译直接复用已运行的 `infohub-esphome` 容器

对应实现已经写进：

- `deploy/esphome/docker/compose.yaml`
- `Makefile`

现在推荐使用：

```bash
make DOCKER_CONTEXT=orbstack esphome-compile-stage1-alt
make DOCKER_CONTEXT=orbstack esphome-compile-stage2
```

### 5. Stage 1：先让屏幕亮起来

第一次刷机不要直接上业务固件，先走最小首刷配置：

```text
deploy/esphome/reterminal_e1001_first_flash_alt.yaml
```

先编译：

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-compile-stage1-alt
```

也可以直接在 `ESPHome Dashboard` 中选择安装并手动下载 `factory` 固件。

### 6. 用 `web.esphome.io` 做第一次 USB 首刷

第一次刷机的稳定做法是：

1. 在 `ESPHome Dashboard` 里拿到 `factory` 固件
2. 打开 [ESPHome Web](https://web.esphome.io/)
3. 点击 `Connect`
4. 选择串口设备
5. 选择 `Install`
6. 选择刚才手动下载的 `factory` 固件

这一步不需要先点 `Prepare for first use`。

只要已经拿到了 `factory` 固件，直接走手动安装就可以。

### 7. 白屏问题的真实根因和修复

第一次按常规 `7.50inv2` 配置刷入后，屏幕表现是：

- 固件刷进去了
- 设备能启动
- 但电子纸屏幕全白

最终确认根因是当前这台 `reTerminal E1001` 的显示初始化参数与常见官方示例不一致。

当前设备实际可用的稳定参数是：

```yaml
model: 7.50inv2alt
reset_duration: 2ms
```

因此首刷必须改用：

```text
deploy/esphome/reterminal_e1001_first_flash_alt.yaml
```

这一步刷完后，屏幕能正常显示 `ALT PROFILE`，说明白屏问题已经解决。

### 8. Stage 1 固件闪烁是否正常

首刷诊断固件刷进去后，设备会隔一段时间闪一下，这属于正常现象。

原因是 Stage 1 固件配置了：

- `update_interval`
- 周期性重绘测试内容

它的目标是确认屏幕能稳定刷新，不是最终业务显示效果。

### 9. Stage 2：切到业务固件

确认 Stage 1 正常后，再切换到：

```text
deploy/esphome/reterminal_e1001_infohub_api.yaml
```

编译命令：

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-compile-stage2
```

然后通过 OTA 更新设备。

这份固件的核心逻辑是：

- 设备直接拉取 `infohub_eink_device_url`
- 请求返回 body 后在设备端解析 JSON
- 如果 payload 和上一次完全一致，不刷新屏幕
- 只有内容变化或错误状态变化时才触发显示更新

## 业务固件联调过程中踩过的坑

### 1. “数据解析失败，check device.json payload”

这个报错的常见根因不是 JSON 真损坏，而是地址填错了。

错误写法：

```text
https://summary.cccy.fun/dashboard/eink?token=...&refresh=600
```

这是 HTML 页面，不是给设备用的接口。

正确写法：

```text
https://summary.cccy.fun/dashboard/eink/device.json?token=...&refresh=300
```

设备端必须指向 `device.json`。

### 2. `HTTP 401`

后来又出现过：

```text
HTTP 401
```

这说明设备已经连通接口，但鉴权没有通过。

本次最终修复路径是：

1. 到服务端确认 dashboard 鉴权方式
2. 更新代码并部署
3. 同步修改 `deploy/esphome/secrets.yaml` 中的 `infohub_eink_device_url`
4. 重新刷入或 OTA 更新后恢复正常

这也说明：

- 同一个 token 不一定能同时用于 HTML 页面和设备接口
- 即使路径看起来相似，也要以服务端当前鉴权逻辑为准

### 3. 编译错误不是只有“语法问题”

Stage 2 过程中，除了接口问题，还处理过几类配置问题：

- fallback AP 名称过长
- `font.glyphs` 有重复字符
- 使用 `json::parse_json(...)` 但缺少根级 `json:`
- UI 调整时 lambda 内容改坏，导致 C++ 生成后编译失败

这些问题已经在当前 `deploy/esphome/reterminal_e1001_infohub_api.yaml` 中修复，现版本可以正常编译。

## 当前设备交互行为

当前业务固件里，相关按键和按钮的实际情况如下：

| 按键 | 当前行为 |
| --- | --- |
| `GPIO3` | 触发一次主动拉取 `fetch_dashboard_payload` |
| HA 按钮 `Force Sync` | 触发一次主动拉取 `fetch_dashboard_payload` |
| `GPIO4` | 当前未绑定 |
| `GPIO5` | 当前未绑定 |

注意：

- `Force Sync` 和 `GPIO3` 的含义是“主动去拉最新 payload”
- 如果拉到的 body 与上一次完全一致，屏幕不会强制重刷
- 所以“按了按钮不一定看到整屏重绘”是当前设计使然，不是按钮失效

## 墨水屏显示优化过程

后续在业务固件基础上又做了一轮版式优化，方向包括：

- Token 统一用 `M（百万）` 作为展示单位
- 去掉“请求数”
- 去掉“启动数”
- 去掉配额卡片里的账户名
- `Sub2API` 多账户按“合并统计”展示
- 字体整体做大，减少小号中文锯齿感

这一轮先做了效果图，没有直接把最终版全量落进固件。

当前确认的视觉稿路径：

- `docs/mockups/reterminal-e1001-ui-v3.svg`
- `docs/mockups/reterminal-e1001-ui-v3.png`

这版视觉稿体现的方向包括：

- 顶部保留刷新时间和整体状态
- 三张总览卡片统一大数字
- `Sub2API` 明确标注“合并统计”
- 左侧为 Claude / Sub2API 配额区域
- 右侧统一做“告警与系统”信息栏

需要注意：

- 当前 mockup `v3` 是“下一版 UI 方向”
- 不应误认为已经全部上线到当前设备固件
- 当前设备已经能正常显示，但业务固件上的 UI 仍是较早一版大字号优化稿

## 常见问题速查

| 现象 | 原因 | 处理方式 |
| --- | --- | --- |
| `Cannot connect to the Docker daemon ... docker.sock` | 当前实际使用 `OrbStack`，但命令没切对 Docker context | 所有命令显式加 `DOCKER_CONTEXT=orbstack` |
| Stage 1 刷完白屏 | `7.50inv2` 初始化参数不适配当前批次屏 | 改刷 `reterminal_e1001_first_flash_alt.yaml` |
| Stage 1 周期性闪烁 | 首刷诊断固件本来就会周期性刷新 | 正常现象 |
| 中间显示“数据解析失败” | 指向了 HTML 页，不是 `device.json` | 改成 `/dashboard/eink/device.json` |
| 显示 `HTTP 401` | token 或设备接口鉴权不匹配 | 检查服务端鉴权并更新 `secrets.yaml` |
| PlatformIO 安装过程中大量 `No such file or directory` | macOS 共享目录下的包复制不稳定 | 缓存和构建目录改走 Docker volume |
| 按下按钮没看到明显重绘 | payload 没变化，设计上不刷屏 | 属于当前逻辑，不是按钮损坏 |

## 当前稳定配置结论

到目前为止，已经确认的稳定结论有这些：

- 主线路线应固定为 `Mac + OrbStack Docker + Web Serial + OTA`
- 当前这台 `reTerminal E1001` 应统一使用：

  ```yaml
  model: 7.50inv2alt
  reset_duration: 2ms
  ```

- 业务固件应请求：

  ```text
  /dashboard/eink/device.json
  ```

- `Home Assistant` 适合作为设备接入和辅助展示层，而不是主编译层
- `Force Sync` / `GPIO3` 是主动拉取，不是无条件整屏重绘

## 现在的状态

当前状态已经进入“主链路打通”阶段：

- `ESPHome Docker` 方案已经跑通
- `web.esphome.io` 首刷已经成功
- `Stage 1 alt` 已确认能正常显示
- `Stage 2` 业务固件已编译并刷入
- 服务端鉴权已经修正
- `secrets.yaml` 已更新为正确设备接口
- 设备已经恢复正常显示

## 后续可继续做的优化

接下来如果继续迭代，优先顺序建议是：

1. 把 `docs/mockups/reterminal-e1001-ui-v3.*` 的视觉稿正式落进业务固件
2. 如果需要硬件按键扩展，再决定是否绑定 `GPIO4`、`GPIO5`
3. 增加更多 HA 侧实体，方便在 HA 中看设备状态和手动触发同步
4. 根据实际刷新体验，再微调轮询周期和文案密度

## 相关文档

- 细化的 Docker 方案：`docs/infohub-eink-esphome-docker-mac.md`
- 细化的首刷 runbook：`docs/infohub-eink-first-flash-runbook.md`
- 细化的 API 直连说明：`docs/infohub-eink-direct-api-panel.md`
- 2026-04-22 状态记录：`docs/infohub-eink-ha-status-2026-04-22.md`
