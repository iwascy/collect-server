# InfoHub EInk 直连 API 面板说明

> 当前文档对应第 2 阶段业务面板。
> 第一次 USB 首刷请先走 [reTerminal E1001 首刷 Runbook](/Users/cyan/code/collect-server/docs/infohub-eink-first-flash-runbook.md)，确认设备、屏幕和 OTA 基础链路没问题后，再切到这里。
> 当前更推荐的编译/管理入口是 [Mac 上独立 ESPHome Docker 方案](/Users/cyan/code/collect-server/docs/infohub-eink-esphome-docker-mac.md)。

这份方案是当前推荐路径的第 2 阶段：`reTerminal E1001 + ESPHome` 直接请求当前项目提供的设备接口，不再依赖 HA 截图链路。

核心数据接口：

- HTML 看板：`/dashboard/eink?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=600`
- 调试 JSON：`/dashboard/eink.json?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=600`
- 设备直连 JSON：`/dashboard/eink/device.json?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=300`

## 为什么改成直连 API

这条链路更适合你现在的要求：

1. 不走截图，不需要 `Puppet`
2. 面板直接消费当前项目 API，设备端不依赖浏览器渲染
3. ESPHome 只在 payload 变化时触发一次电子纸刷新，避免无意义的反复刷屏
4. 看板页面仍然可以继续接入 Home Assistant 的 iframe dashboard，方便在 HA 里看同一份数据
5. 设备侧版式按当前 HTML 看板做高保真复刻，保持三张概览卡片、双表格和右侧提醒栏的同一视觉结构

## 当前仓库里对应的文件

- 设备接口：`GET /dashboard/eink/device.json`
- ESPHome 模板：[reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml)
- HA iframe 看板注册：
  [infohub_dashboard_registration.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/configuration/infohub_dashboard_registration.yaml)
  [infohub_eink.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/dashboards/infohub_eink.yaml)

## 1. 先确认项目接口

项目启动后，先验证三个入口：

```bash
curl "http://10.30.5.172:8080/dashboard/eink?token=YOUR_DASHBOARD_TOKEN&refresh=600"
curl "http://10.30.5.172:8080/dashboard/eink.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
curl "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

设备接口返回的是更适合 ESPHome 解析的紧凑结构，包含：

- `updated_at`
- `claude`
- `sub2api`
- `total`
- `claude_rows`
- `sub2api_rows`
- `alerts`
- `reset_hints`

## 2. 在 Home Assistant 里保留一个 iframe 看板

如果你希望在 HA 里也能看同一份内容，可以继续保留 HTML dashboard：

1. 把 [infohub_dashboard_registration.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/configuration/infohub_dashboard_registration.yaml) 合并进 HA 的 `configuration.yaml`
2. 把 [infohub_eink.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/dashboards/infohub_eink.yaml) 放到 `/config/dashboards/infohub_eink.yaml`
3. 在 HA 的 `secrets.yaml` 里加入：

```yaml
infohub_eink_source_url: "http://10.30.5.172:8080/dashboard/eink?token=YOUR_DASHBOARD_TOKEN&refresh=600"
```

这样 HA 里会有一个 `InfoHub EInk` dashboard，但这只是辅助查看，不再参与设备渲染。

## 3. ESPHome 设备改走直连接口

在你已经完成 Stage 1 首刷之后，再使用 [reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml) 作为设备 YAML。

这个模板的关键点：

- `http_request.get` 直接拉 `device.json`
- `capture_response: true`，在设备端拿到完整 JSON body
- `max_response_buffer_size: 16384`，避免 1KB 默认缓冲过小
- `update_interval: never`，显示器不做固定周期刷新
- 只要 HTTP 返回 body 和上次完全一致，就不触发 `component.update`
- `GPIO3` 保留为实体手动刷新按钮
- `GPIO4` 同时作为夜间 deep sleep 的唤醒键
- 还额外暴露了一个 HA 里的 `Force Sync` 按钮
- 新增了“插电高实时 / 电池省电 / 电池夜间静默”三种运行状态

ESPHome 的 `secrets.yaml` 至少要补这些值：

```yaml
wifi_ssid: "YOUR_WIFI"
wifi_password: "YOUR_WIFI_PASSWORD"
wifi_fallback_password: "YOUR_FALLBACK_PASSWORD"
esphome_api_encryption_key: "YOUR_ESPHOME_API_KEY"
esphome_ota_password: "YOUR_OTA_PASSWORD"
infohub_eink_device_url: "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

你也可以直接从 [deploy/esphome/secrets.example.yaml](/Users/cyan/code/collect-server/deploy/esphome/secrets.example.yaml) 复制示例，再填入真实值。

### 省电版轮询策略

当前仓库里的 API 模板已经内置一套偏稳妥的省电策略：

- 插电模式：每 `2min` 请求一次
- 电池模式：每 `5min` 请求一次
- 电池夜间静默：`22:00` 到次日 `10:00` 不请求业务接口，并直接进入 deep sleep，等到 `10:00` 自动唤醒
- 电子纸刷新仍然保留“只有 payload 变化才刷新”的逻辑，所以插电模式虽然请求更频繁，但不会因为同一份内容反复刷屏
- 如果电量低于阈值，顶部状态栏会额外显示 `低电量` 标识

另外，模板还会额外暴露这些实体，方便在 Home Assistant 里确认策略是否按预期切换：

- `Battery Voltage`
- `Battery Level`
- `Power Profile`

### 供电模式如何判断

这版默认使用 Seeed 官方公开的电池测量方式：

- `GPIO21` 打开电池电压测量
- `GPIO1` 读取电池电压

再通过两个电压阈值做近似判断：

- `>= 4.15V` 视为插电高实时
- `<= 4.05V` 视为电池省电
- `<= 20%` 触发顶部 `低电量` 标识

这是一个“够实用、但不是绝对精确”的默认方案。原因是当前公开资料里比较明确的是电池电压采样能力，而不是一个现成的、已在当前仓库验证过的 USB/VBUS 供电脚位。

如果你后面实机发现：

- 满电拔电后一小段时间里仍被判成“插电”
- 或者边充边用但电压还没抬到阈值时，切换不够快

可以直接微调 [reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml) 顶部这两个 substitution：

- `plugged_voltage_threshold`
- `battery_voltage_threshold`
- `low_battery_level_threshold`

### 已验证的配置注意事项

这两点是 2026-04-22 在真实 HA / ESPHome 环境里已经踩到并确认过的问题：

- fallback AP 的 `ssid` 不能超过 32 个字符，所以不要继续用 `"${friendly_name} Fallback"` 这种长名字，当前模板已经改成 `InfoHub Fallback`
- `font.glyphs` 在 ESPHome 2026.4.1 下会严格校验重复字符，重复的空格、换行或汉字都会让 `esphome config` 直接失败；当前模板里的字形集合已经去重

基础 API 模板此前已经在当前仓库里跑通过 `esphome config`。这次新增的省电版改动，建议你在本机或 ESPHome Dashboard 里再补跑一次 `esphome config reterminal_e1001_infohub_api.yaml` 做最终确认。

另外，2026-04-22 在当前这台 `reTerminal E1001` 上已经实机确认：

- 首刷标准 `7.50inv2` 会出现全白屏
- 改成 `7.50inv2alt`
- 并加上 `reset_duration: 2ms`

之后屏幕即可正常显示。

所以当前仓库里的 API 模板已经同步切到这套显示参数，避免 Stage 1 能亮、Stage 2 又回退成白屏。

## 4. 关于“局部刷新”的当前结论

这里要如实区分两层含义：

1. 逻辑层面
   现在这份配置已经做到“局部数据更新”：
   只有 API payload 真的变了，设备才会再次刷新屏幕。

2. 物理显示层面
   `reTerminal E1001` 常见官方示例仍然是 `waveshare_epaper` + `model: 7.50inv2`。但当前这台设备实测需要 `7.50inv2alt + reset_duration: 2ms` 才能稳定显示。而 ESPHome 官方把支持 partial refresh 的 7.5 寸型号单独列成 `7.50inV2p`。

截至 2026-04-23，当前这台设备已经用独立 probe 固件完成验证，因此正式业务面板可以收敛为：

- 这套方案能做到“API 直连 + 仅变化时刷新”
- 当前这台已确认可切到 `7.50inV2p`
- 正式业务面板推荐保留 `reset_duration: 2ms`
- 建议同时设置 `full_update_every`，避免长时间纯局刷积累残影

## 5. 推荐的部署顺序

1. 先完成 [reTerminal E1001 首刷 Runbook](/Users/cyan/code/collect-server/docs/infohub-eink-first-flash-runbook.md)，确认最小固件已 USB 刷入并且屏幕能亮字
2. 保持现在的 HAOS 空间状态，先不要急着装更多 add-on
3. 启动并确认 `collect-server` 的 `device.json` 可以访问
4. 在 HA 里先把 iframe dashboard 挂好，方便直接验证 token 和页面
5. 再把设备 YAML 切换成 API 直连版
6. 通过 OTA 更新设备，而不是重新走 USB 刷机
7. 验证只有 JSON 内容变化时才会重新刷屏
8. 如果你启用了省电版模板，建议顺手观察一下 HA 里新增的 `Power Profile` / `Battery Voltage` / `Battery Level` 三个实体，确认插电和电池切换是否符合这台机器的实际电压表现
9. 如果 `esphome config` 失败，先优先检查 Wi-Fi fallback 名称长度、`font.glyphs` 是否有重复字符，以及是否缺少根级 `json:` 组件

如果你准备进一步验证硬件级 partial refresh，不要直接拿业务面板硬切显示型号，先走独立探针固件：
[reTerminal E1001 局部刷新验证方案](/Users/cyan/code/collect-server/docs/infohub-eink-partial-refresh-probe.md)

## 6. 如果还要继续省电

当前这版已经把“夜间不请求 + 夜间 deep sleep”做进模板了。如果你后面还想继续压榨续航，可以继续往下做：

- 把白天电池模式也改成“定时唤醒后请求一次，再次 deep sleep”，省电幅度会比常驻 Wi‑Fi 再大一截
- 如果后面确认到稳定可用的 USB/VBUS 检测脚位，可以把现在的电压近似判断改成真正的外部供电检测，切换会更准
- 如果你确定后端采集本身不是分钟级变化，可以把 `battery_poll_interval` 从 `5min` 再拉长到 `15min`、`30min` 或更长
- 如果夜间只需要保留画面、不需要联机，可以进一步评估在进入静默前主动关 Wi‑Fi 或更早进入 deep sleep

## 参考资料

- Seeed 官方的 E1001 + ESPHome 基础接线和 `waveshare_epaper` 示例：
  [reTerminal E Series with ESPHome](https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome/)
- ESPHome 官方 `waveshare_epaper` 组件文档：
  [Waveshare E-Paper Display](https://esphome.io/components/display/waveshare_epaper.html)
- ESPHome 官方 `http_request` 组件文档：
  [HTTP Request Component](https://esphome.io/components/http_request.html)
