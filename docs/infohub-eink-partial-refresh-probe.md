# reTerminal E1001 局部刷新验证方案

这份文档对应一份独立实验固件：
[reterminal_e1001_partial_refresh_probe.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_partial_refresh_probe.yaml)

目标原本不是替换当前业务面板，而是用最小风险确认这台 `reTerminal E1001` 是否能在 ESPHome 下走硬件级 partial refresh。

截至 2026-04-23，这个 probe 已完成验证，正式业务面板已切到 `7.50inV2p`。

## 适用前提

开始前先默认你已经确认过这些事情：

- 这台设备用 `7.50inv2alt + reset_duration: 2ms` 能稳定亮屏
- 设备已经具备 OTA 能力
- 你愿意接受“这份实验 YAML 可能仍然白屏、花屏、整屏闪或者残影明显”

如果你还没完成这些前提，请先走：
[reTerminal E1001 首刷 Runbook](/Users/cyan/code/collect-server/docs/infohub-eink-first-flash-runbook.md)

## 这个探针固件做了什么

探针固件和当前业务面板最大的区别有两点：

1. 显示驱动型号切到 `7.50inV2p`
2. 启用 `full_update_every: 15`

按照 ESPHome 官方文档，`7.50inV2p` 是支持 partial refresh 的 7.5 寸 V2 型号变体；因此这份固件的目的就是验证你这块实际屏幕是否能稳定工作在这条路径上。

## 画面怎么读

屏幕上会分成两块：

- 左侧 `STATIC REFERENCE`
  这里是边框和棋盘格，原则上应该长期不变
- 右侧 `DYNAMIC BOX`
  这里的两位数字会按固定周期递增，下面两个小条会交替翻转

底部还会显示：

- 当前触发来源：`BOOT` / `AUTO` / `MANUAL` / `GPIO3` / `RESET`
- 当前 tick 计数
- 手动触发次数

## 怎么验证

刷入后盯住这三件事：

1. 动态框是否按周期更新。
2. 左侧静态区域是否保持稳定，不会每次都整屏明显闪白。
3. 第 15 次、30 次这类整除点，是否只偶发一次全刷。

如果现象是下面这样，可以认为“局部刷新基本成立”：

- 右侧动态框变化明显
- 左侧棋盘格和外框大多数更新中基本不闪
- 只有每隔若干次出现一次全屏刷新
- 残影可接受，没有快速失真

如果现象是下面这样，就说明这条路不适合直接上业务：

- 每次计数变化都整屏闪
- 静态区域明显反复重绘
- 很快出现严重残影、脏刷、黑边
- 直接白屏或初始化失败

## 触发方式

你可以用三种方式观察：

1. 等自动更新
默认每 `12s` 更新一次。

2. 按硬件按键
`GPIO3` 会手动推进一步。

3. 在 ESPHome / HA 里点按钮
有两个模板按钮：
- `Step`
- `Reset Counter`

## 推荐验证顺序

1. 先 OTA 刷入这份 probe 固件。
2. 观察连续 5 到 10 次自动更新。
3. 再手动按几次 `GPIO3`，看是否仍然只动右侧区域。
4. 至少等到第 15 次更新，确认是否按预期发生一次全刷。
5. 如果整体验证通过，再考虑把业务面板迁到同一显示模型。

## 风险边界

这份 probe 固件是实验用途，不建议直接替换业务屏长期运行。

原因有两个：

- 当前仓库里实际已验证稳定的型号仍然是 `7.50inv2alt`
- 即使 `7.50inV2p` 能跑起来，也还需要继续验证长时间运行的残影、偶发错刷和恢复能力

如果 probe 失败，直接刷回：
[reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml)

## 参考资料

- ESPHome Waveshare E-Paper Display:
  [https://esphome.io/components/display/waveshare_epaper/](https://esphome.io/components/display/waveshare_epaper/)
- Seeed reTerminal E Series with ESPHome:
  [https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome/](https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome/)
