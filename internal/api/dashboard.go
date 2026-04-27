package api

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"infohub/internal/model"
)

const (
	einkDashboardDefaultRefreshSeconds = 600
	einkDashboardMinRefreshSeconds     = 60
	einkDashboardMaxRefreshSeconds     = 3600
)

var einkDashboardTmpl = template.Must(template.New("eink-dashboard").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="refresh" content="{{.RefreshSeconds}}">
  <title>InfoHub 墨水屏面板</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f0ede6;
      --panel: #f5f3ec;
      --ink: #111111;
      --muted: #555555;
      --line: #222222;
      --soft-line: rgba(34, 34, 34, 0.5);
    }
    * { box-sizing: border-box; }
    html, body {
      width: 100%;
      height: 100%;
      margin: 0;
      padding: 0;
      background: var(--bg);
      color: var(--ink);
      font-family: "Noto Sans SC", "PingFang SC", "Microsoft YaHei", sans-serif;
    }
    body {
      overflow: hidden;
      display: grid;
      place-items: center;
    }
    .page {
      container-type: size;
      position: relative;
      width: min(100vw, calc(100vh * 1.6666667));
      height: min(100vh, calc(100vw * 0.6));
      aspect-ratio: 800 / 480;
      background: var(--bg);
      overflow: hidden;
    }
    .frame, .header, .overview-card, .quota-card, .system-panel {
      position: absolute;
      background: var(--panel);
      border: 0.1875vmin solid var(--line);
    }
    .frame { left: 1.125%; top: 1.875%; width: 97.75%; height: 96.25%; border-radius: 1.042%; }
    .header {
      left: 2.25%; top: 3.542%; width: 95.5%; height: 7.5%;
      border-radius: 0.833%;
      display: flex;
      align-items: center;
      padding: 0 1.25%;
    }
    .title { font-size: 5vmin; font-weight: 700; line-height: 1; }
    .refresh {
      position: absolute;
      left: 50%;
      transform: translateX(-50%);
      font-size: 3.333vmin;
      font-weight: 600;
      color: #444444;
      white-space: nowrap;
    }
    .badge {
      margin-left: auto;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-width: 11.25%;
      height: 5vmin;
      padding: 0 1.5%;
      border-radius: 999px;
      border: 0.125vmin solid var(--line);
      font-size: 2.708vmin;
      font-weight: 700;
      white-space: nowrap;
    }
    .badge.solid { background: var(--ink); color: var(--panel); }
    .overview-card { top: 12.083%; width: 31%; height: 27.083%; padding: 2.292% 1.5% 0; }
    .overview-card.claude { left: 2.25%; }
    .overview-card.codex { left: 34.5%; }
    .overview-card.total { left: 66.75%; }
    .overview-title, .quota-title, .system-title {
      font-size: 3.542vmin;
      font-weight: 700;
      line-height: 1;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .overview-value {
      margin-top: 2.083%;
      font-size: 11.25vmin;
      font-weight: 700;
      line-height: 1.08;
      letter-spacing: -0.04em;
      white-space: nowrap;
      overflow: hidden;
    }
    .overview-divider {
      position: absolute;
      left: 4.839%; right: 4.839%; bottom: 6.458vmin;
      border-top: 0.125vmin solid var(--soft-line);
    }
    .overview-cost {
      position: absolute;
      left: 4.839%; bottom: 2.5vmin;
      font-size: 2.917vmin;
      font-weight: 700;
      color: var(--muted);
      white-space: nowrap;
    }
    .quota-card { left: 2.25%; width: 63.25%; height: 28.125%; padding: 2.083% 1.5% 0; }
    .quota-card.claude { top: 40.208%; }
    .quota-card.codex { top: 69.375%; height: 28.75%; }
    .quota-reset {
      margin-top: 3.125%;
      font-size: 2.708vmin;
      font-weight: 700;
      color: var(--muted);
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .metric-row {
      position: absolute;
      left: 2.372%; right: 2.372%; top: 46.667%;
      display: grid;
      grid-template-columns: 190fr 25fr 230fr;
      column-gap: 25px;
      align-items: start;
    }
    .codex .metric-row { top: 45.652%; }
    .metric { min-width: 0; }
    .metric-head {
      display: flex;
      justify-content: space-between;
      font-size: 3.75vmin;
      font-weight: 700;
      line-height: 1;
    }
    .progress-cell { margin-top: 2.292vmin; }
    .progress-track {
      width: 100%;
      height: 1.875vmin;
      border: 0.125vmin solid #bbbbbb;
      border-radius: 999px;
      overflow: hidden;
    }
    .progress-fill {
      height: calc(100% - 0.416vmin);
      margin: 0.208vmin;
      border-radius: 999px;
      background: #333333;
    }
    .metric-divider {
      width: 0;
      height: 8.958vmin;
      border-left: 0.125vmin solid var(--soft-line);
      margin: 0 auto;
    }
    .system-panel { left: 66.75%; top: 40.208%; width: 31%; height: 57.917%; padding: 3.333% 2% 0; }
    .system-alert-title, .system-alert-detail {
      font-size: 3.125vmin;
      font-weight: 700;
      line-height: 1.55;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .system-alert-title { margin-top: 5.833%; }
    .system-divider {
      position: absolute;
      left: 6.452%; right: 6.452%; bottom: 13.542%;
      border-top: 0.125vmin solid var(--soft-line);
    }
    .refresh-dot {
      position: absolute;
      left: 8.065%; bottom: 9.375%;
      width: 2.083vmin;
      height: 2.083vmin;
      border: 0.104vmin solid var(--muted);
      border-radius: 50%;
    }
    .system-refresh {
      position: absolute;
      left: 16.129%; bottom: 8.958%;
      font-size: 2.708vmin;
      font-weight: 700;
      color: var(--muted);
      white-space: nowrap;
    }
  </style>
</head>
<body>
  <main class="page">
    <div class="frame"></div>
    <header class="header">
      <div class="title">InfoHub</div>
      <div class="refresh">{{.UpdatedAt}}</div>
      <div class="badge solid">状态 正常</div>
    </header>

    <article class="overview-card claude">
      <div class="overview-title">Claude</div>
      <div class="overview-value">{{.Device.Claude.DisplayValue}}</div>
      <div class="overview-divider"></div>
      <div class="overview-cost">${{.Device.Claude.Cost}}</div>
    </article>
    <article class="overview-card codex">
      <div class="overview-title">Codex</div>
      <div class="overview-value">{{.Device.Codex.DisplayValue}}</div>
      <div class="overview-divider"></div>
      <div class="overview-cost">${{.Device.Codex.Cost}}</div>
    </article>
    <article class="overview-card total">
      <div class="overview-title">合计</div>
      <div class="overview-value">{{.Device.Total.DisplayValue}}</div>
      <div class="overview-divider"></div>
      <div class="overview-cost">${{.Device.Total.Cost}}</div>
    </article>

    <article class="quota-card claude">
      <div class="quota-title">Claude 配额</div>
      <div class="quota-reset">{{.ClaudeTable.FiveHourReset}}</div>
      <div class="metric-row">
        <div class="metric">
          <div class="metric-head"><span>5H</span><span>{{.ClaudeTable.FocusRow.FiveHour.Text}}</span></div>
          <div class="progress-cell"><div class="progress-track"><div class="progress-fill" style="width: {{.ClaudeTable.FocusRow.FiveHour.Percent}}%"></div></div></div>
        </div>
        <div class="metric-divider"></div>
        <div class="metric">
          <div class="metric-head"><span>Week</span><span>{{.ClaudeTable.FocusRow.Week.Text}}</span></div>
          <div class="progress-cell"><div class="progress-track"><div class="progress-fill" style="width: {{.ClaudeTable.FocusRow.Week.Percent}}%"></div></div></div>
        </div>
      </div>
    </article>

    <article class="quota-card codex">
      <div class="quota-title">Codex 配额</div>
      <div class="quota-reset">{{.Sub2APITable.FiveHourReset}}</div>
      <div class="metric-row">
        <div class="metric">
          <div class="metric-head"><span>5H</span><span>{{.Sub2APITable.FocusRow.FiveHour.Text}}</span></div>
          <div class="progress-cell"><div class="progress-track"><div class="progress-fill" style="width: {{.Sub2APITable.FocusRow.FiveHour.Percent}}%"></div></div></div>
        </div>
        <div class="metric-divider"></div>
        <div class="metric">
          <div class="metric-head"><span>Week</span><span>{{.Sub2APITable.FocusRow.Week.Text}}</span></div>
          <div class="progress-cell"><div class="progress-track"><div class="progress-fill" style="width: {{.Sub2APITable.FocusRow.Week.Percent}}%"></div></div></div>
        </div>
      </div>
    </article>

    <aside class="system-panel">
      <div class="system-title">告警</div>
      {{if .AlertTitle}}
      <div class="system-alert-title">{{.AlertTitle}}</div>
      <div class="system-alert-detail">{{.AlertDetail}}</div>
      {{else}}
      <div class="system-alert-title">暂无告警</div>
      {{end}}
      <div class="system-divider"></div>
      <div class="refresh-dot"></div>
      <div class="system-refresh">刷新周期 {{.RefreshSeconds}}s</div>
    </aside>
  </main>
</body>
</html>`))

type einkDashboardPage struct {
	UpdatedAt      string             `json:"updated_at"`
	UpdatedAtUnix  int64              `json:"updated_at_unix"`
	RefreshSeconds int                `json:"refresh_seconds"`
	Overview       []einkOverviewCard `json:"overview"`
	ClaudeTable    einkQuotaTable     `json:"claude_table"`
	Sub2APITable   einkQuotaTable     `json:"sub2api_table"`
	Alerts         []string           `json:"alerts"`
	AlertTitle     string             `json:"alert_title"`
	AlertDetail    string             `json:"alert_detail"`
	Device         einkDevicePayload  `json:"device"`
}

type einkOverviewCard struct {
	Kind  string        `json:"kind"`
	Title string        `json:"title"`
	Value string        `json:"value"`
	Label string        `json:"label"`
	Stats []string      `json:"stats"`
	Icon  template.HTML `json:"-"`
}

type einkQuotaTable struct {
	Title         string         `json:"title"`
	Rows          []einkQuotaRow `json:"rows"`
	FocusRow      einkQuotaRow   `json:"focus_row"`
	HasRows       bool           `json:"has_rows"`
	ErrorText     string         `json:"error_text,omitempty"`
	FiveHourReset string         `json:"five_hour_reset"`
	WeekReset     string         `json:"week_reset"`
}

type einkQuotaRow struct {
	Account     string          `json:"account"`
	FiveHour    einkPercentCell `json:"five_hour"`
	Week        einkPercentCell `json:"week"`
	Status      string          `json:"status"`
	StatusClass string          `json:"status_class"`
}

type einkPercentCell struct {
	Percent int    `json:"percent"`
	Text    string `json:"text"`
}

type einkDeviceOverview struct {
	Title        string `json:"title"`
	Value        string `json:"value"`
	DisplayValue string `json:"display_value"`
	Label        string `json:"label"`
	Requests     int    `json:"requests"`
	Cost         string `json:"cost"`
	Enabled      int    `json:"enabled,omitempty"`
	Alerts       int    `json:"alerts,omitempty"`
	ValueNumeric int64  `json:"value_numeric"`
}

type einkDevicePayload struct {
	UpdatedAt      string             `json:"updated_at"`
	UpdatedAtUnix  int64              `json:"updated_at_unix"`
	RefreshSeconds int                `json:"refresh_seconds"`
	Claude         einkDeviceOverview `json:"claude"`
	Codex          einkDeviceOverview `json:"codex"`
	Total          einkDeviceOverview `json:"total"`
	ClaudeRows     []einkQuotaRow     `json:"claude_rows"`
	CodexRows      []einkQuotaRow     `json:"codex_rows"`
	Alerts         []string           `json:"alerts"`
	ResetHints     map[string]string  `json:"reset_hints"`
}

type quotaAccount struct {
	Name      string
	FiveHour  quotaMetric
	Week      quotaMetric
	SourceKey string
}

type quotaMetric struct {
	UsedPercent      int
	RemainingPercent int
	ResetAt          string
	QuotaSource      string
	OnlineStatus     string
	Unknown          bool
}

func (h *Handler) EInkDashboard(w http.ResponseWriter, r *http.Request) {
	if h.dashboardMockEnabled {
		page := buildMockEInkDashboardPage(dashboardRefreshSeconds(r))

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := einkDashboardTmpl.Execute(w, page); err != nil {
			writeDashboardError(w, http.StatusInternalServerError, err)
			return
		}
		return
	}

	snapshots, err := h.store.GetAll()
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, err)
		return
	}

	page := buildEInkDashboardPage(snapshots, dashboardRefreshSeconds(r))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := einkDashboardTmpl.Execute(w, page); err != nil {
		writeDashboardError(w, http.StatusInternalServerError, err)
		return
	}
}

func (h *Handler) EInkDashboardData(w http.ResponseWriter, r *http.Request) {
	if h.dashboardMockEnabled {
		writeJSON(w, http.StatusOK, buildMockEInkDashboardPage(dashboardRefreshSeconds(r)))
		return
	}

	snapshots, err := h.store.GetAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, buildEInkDashboardPage(snapshots, dashboardRefreshSeconds(r)))
}

func (h *Handler) EInkDeviceData(w http.ResponseWriter, r *http.Request) {
	if h.dashboardMockEnabled {
		writeJSON(w, http.StatusOK, buildMockEInkDevicePayload(dashboardRefreshSeconds(r)))
		return
	}

	snapshots, err := h.store.GetAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, buildEInkDevicePayload(snapshots, dashboardRefreshSeconds(r)))
}

func buildEInkDashboardPage(snapshots map[string]model.SourceSnapshot, refreshSeconds int) einkDashboardPage {
	updatedAtUnix := maxSnapshotDataUpdatedAt(snapshots)
	claudeKey, claudeSnapshot := preferredSnapshot(snapshots, "claude_local", "claude_relay")
	codexKey, codexSnapshot := preferredSnapshot(snapshots, "codex_local", "sub2api")
	claudeOverview, claudeTable, claudeCritical, claudeAlerts := buildSourceDashboard(claudeKey, dashboardOverviewTitle(claudeKey), claudeSnapshot)
	subOverview, subTable, subCritical, subAlerts := buildSourceDashboard(codexKey, dashboardOverviewTitle(codexKey), codexSnapshot)

	totalOverview := buildTotalOverview(claudeOverview, subOverview, claudeCritical+subCritical)
	alerts := mergeDashboardAlerts(claudeAlerts, subAlerts)
	alertTitle, alertDetail := splitDashboardAlert(alerts)
	device := buildEInkDevicePayload(snapshots, refreshSeconds)

	return einkDashboardPage{
		UpdatedAt:      formatHeaderTime(updatedAtUnix),
		UpdatedAtUnix:  updatedAtUnix,
		RefreshSeconds: refreshSeconds,
		Overview: []einkOverviewCard{
			claudeOverview.Card,
			subOverview.Card,
			totalOverview,
		},
		ClaudeTable:  claudeTable,
		Sub2APITable: subTable,
		Alerts:       alerts,
		AlertTitle:   alertTitle,
		AlertDetail:  alertDetail,
		Device:       device,
	}
}

func buildEInkDevicePayload(snapshots map[string]model.SourceSnapshot, refreshSeconds int) einkDevicePayload {
	updatedAtUnix := maxSnapshotDataUpdatedAt(snapshots)
	claudeKey, claudeSnapshot := preferredSnapshot(snapshots, "claude_local", "claude_relay")
	codexKey, codexSnapshot := preferredSnapshot(snapshots, "codex_local", "sub2api")
	claudeOverview, claudeTable, _, claudeAlerts := buildSourceDashboard(claudeKey, dashboardOverviewTitle(claudeKey), claudeSnapshot)
	codexDeviceKey := codexKey
	if codexDeviceKey == "sub2api" {
		codexDeviceKey = "codex"
	}
	codexOverview, codexTable, _, codexAlerts := buildSourceDashboard(codexDeviceKey, dashboardOverviewTitle(codexDeviceKey), codexSnapshot)

	totalToken := claudeOverview.TokenValue + codexOverview.TokenValue
	totalRequests := int(math.Round(claudeOverview.Requests + codexOverview.Requests))
	totalCost := claudeOverview.Cost + codexOverview.Cost

	alerts := mergeDashboardAlerts(claudeAlerts, codexAlerts)

	return einkDevicePayload{
		UpdatedAt:      formatHeaderTime(updatedAtUnix),
		UpdatedAtUnix:  updatedAtUnix,
		RefreshSeconds: refreshSeconds,
		Claude: einkDeviceOverview{
			Title:        claudeOverview.Card.Title,
			Value:        claudeOverview.Card.Value,
			DisplayValue: formatTokenMillions(claudeOverview.TokenValue),
			Label:        claudeOverview.Card.Label,
			Requests:     int(math.Round(claudeOverview.Requests)),
			Cost:         fmt.Sprintf("%.2f", claudeOverview.Cost),
			Enabled:      int(math.Round(claudeOverview.EnabledAccount)),
			ValueNumeric: int64(math.Round(claudeOverview.TokenValue)),
		},
		Codex: einkDeviceOverview{
			Title:        codexOverview.Card.Title,
			Value:        codexOverview.Card.Value,
			DisplayValue: formatTokenMillions(codexOverview.TokenValue),
			Label:        codexOverview.Card.Label,
			Requests:     int(math.Round(codexOverview.Requests)),
			Cost:         fmt.Sprintf("%.2f", codexOverview.Cost),
			Enabled:      int(math.Round(codexOverview.EnabledAccount)),
			ValueNumeric: int64(math.Round(codexOverview.TokenValue)),
		},
		Total: einkDeviceOverview{
			Title:        "今日合计",
			Value:        formatPrimaryNumber(totalToken),
			DisplayValue: formatTokenMillions(totalToken),
			Label:        "总 Token",
			Requests:     totalRequests,
			Cost:         fmt.Sprintf("%.2f", totalCost),
			Alerts:       len(alerts),
			ValueNumeric: int64(math.Round(totalToken)),
		},
		ClaudeRows: claudeTable.Rows,
		CodexRows:  codexTable.Rows,
		Alerts:     alerts,
		ResetHints: map[string]string{
			"claude_five_hour": claudeTable.FiveHourReset,
			"claude_week":      claudeTable.WeekReset,
			"codex_five_hour":  codexTable.FiveHourReset,
			"codex_week":       codexTable.WeekReset,
		},
	}
}

func buildMockEInkDashboardPage(refreshSeconds int) einkDashboardPage {
	device := buildMockEInkDevicePayload(refreshSeconds)
	claudeTable := einkQuotaTable{
		Title:         "Claude Relay 配额",
		Rows:          device.ClaudeRows,
		FocusRow:      device.ClaudeRows[0],
		HasRows:       true,
		FiveHourReset: device.ResetHints["claude_five_hour"],
		WeekReset:     device.ResetHints["claude_week"],
	}
	codexTable := einkQuotaTable{
		Title:         "Sub2API 账号额度",
		Rows:          device.CodexRows,
		FocusRow:      device.CodexRows[0],
		HasRows:       true,
		FiveHourReset: device.ResetHints["codex_five_hour"],
		WeekReset:     device.ResetHints["codex_week"],
	}

	return einkDashboardPage{
		UpdatedAt:      device.UpdatedAt,
		UpdatedAtUnix:  device.UpdatedAtUnix,
		RefreshSeconds: refreshSeconds,
		Overview: []einkOverviewCard{
			{
				Kind:  "claude_relay",
				Title: device.Claude.Title,
				Value: device.Claude.Value,
				Label: device.Claude.Label,
				Stats: []string{"请求 14", "成本 $1.62", "启用 1"},
				Icon:  dashboardCardIcon("claude_relay"),
			},
			{
				Kind:  "sub2api",
				Title: "Sub2API 今日概览",
				Value: device.Codex.Value,
				Label: device.Codex.Label,
				Stats: []string{"请求 394", "成本 $13.55", "启用 5"},
				Icon:  dashboardCardIcon("sub2api"),
			},
			{
				Kind:  "total",
				Title: device.Total.Title,
				Value: device.Total.Value,
				Label: device.Total.Label,
				Stats: []string{"总请求 408", "总成本 $15.17", "告警 0"},
				Icon:  dashboardCardIcon("total"),
			},
		},
		ClaudeTable:  claudeTable,
		Sub2APITable: codexTable,
		Alerts:       []string{},
		Device:       device,
	}
}

func buildMockEInkDevicePayload(refreshSeconds int) einkDevicePayload {
	return einkDevicePayload{
		UpdatedAt:      "2026-04-24 10:30",
		UpdatedAtUnix:  1776997800,
		RefreshSeconds: refreshSeconds,
		Claude: einkDeviceOverview{
			Title:        "Claude Relay 今日概览",
			Value:        "1,058,870",
			DisplayValue: "1.1M",
			Label:        "Token 用量",
			Requests:     14,
			Cost:         "1.62",
			Enabled:      1,
			ValueNumeric: 1058870,
		},
		Codex: einkDeviceOverview{
			Title:        "Codex 今日概览",
			Value:        "24,854,435",
			DisplayValue: "24.9M",
			Label:        "Token 用量",
			Requests:     394,
			Cost:         "13.55",
			Enabled:      5,
			ValueNumeric: 24854435,
		},
		Total: einkDeviceOverview{
			Title:        "今日合计",
			Value:        "25,913,305",
			DisplayValue: "25.9M",
			Label:        "总 Token",
			Requests:     408,
			Cost:         "15.17",
			Alerts:       0,
			ValueNumeric: 25913305,
		},
		ClaudeRows: []einkQuotaRow{
			{
				Account:     "cycyzg",
				FiveHour:    einkPercentCell{Percent: 71, Text: "71%"},
				Week:        einkPercentCell{Percent: 77, Text: "77%"},
				Status:      "正常",
				StatusClass: "",
			},
		},
		CodexRows: []einkQuotaRow{
			{
				Account:     "admin10010",
				FiveHour:    einkPercentCell{Percent: 56, Text: "56%"},
				Week:        einkPercentCell{Percent: 92, Text: "92%"},
				Status:      "关注",
				StatusClass: "",
			},
			{
				Account:     "admin10086",
				FiveHour:    einkPercentCell{Percent: 18, Text: "18%"},
				Week:        einkPercentCell{Percent: 64, Text: "64%"},
				Status:      "正常",
				StatusClass: "",
			},
		},
		Alerts: []string{},
		ResetHints: map[string]string{
			"claude_five_hour": "5H 重置: 2026-04-24 15:00",
			"claude_week":      "Week 重置: 2026-04-26 00:00",
			"codex_five_hour":  "5H 重置: 2026-04-24 15:00",
			"codex_week":       "Week 重置: 2026-04-27 08:00",
		},
	}
}

func preferredSnapshot(snapshots map[string]model.SourceSnapshot, keys ...string) (string, model.SourceSnapshot) {
	if len(keys) == 0 {
		return "", model.SourceSnapshot{}
	}
	for _, key := range keys {
		snapshot, ok := snapshots[key]
		if !ok {
			continue
		}
		if len(snapshot.Items) > 0 || snapshot.Status != "" {
			return key, snapshot
		}
	}
	return keys[0], model.SourceSnapshot{}
}

func dashboardOverviewTitle(sourceKey string) string {
	switch sourceKey {
	case "claude_local":
		return "Claude Local 今日概览"
	case "claude_relay":
		return "Claude Relay 今日概览"
	case "codex_local":
		return "Codex Local 今日概览"
	case "codex":
		return "Codex 今日概览"
	case "sub2api":
		return "Sub2API 今日概览"
	default:
		return sourceKey + " 今日概览"
	}
}

type sourceOverview struct {
	Card           einkOverviewCard
	TokenValue     float64
	Requests       float64
	Cost           float64
	EnabledAccount float64
}

func buildSourceDashboard(sourceKey string, title string, snapshot model.SourceSnapshot) (sourceOverview, einkQuotaTable, int, []string) {
	overview := sourceOverview{
		Card: einkOverviewCard{
			Kind:  sourceKey,
			Title: title,
			Value: "--",
			Label: "Token 用量",
			Stats: []string{"请求 0", "成本 $0.00", "启用 0"},
			Icon:  dashboardCardIcon(sourceKey),
		},
	}
	table := einkQuotaTable{
		Title: dashboardTableTitle(sourceKey),
	}

	if strings.EqualFold(snapshot.Status, "error") && len(snapshot.Items) == 0 {
		message := strings.TrimSpace(snapshot.Error)
		if message == "" {
			message = "最近一次采集失败"
		}
		table.ErrorText = message
		return overview, table, 0, nil
	}

	tokenItem, ok := findTokenUsageItem(snapshot.Items)
	if ok {
		overview.TokenValue = numericOrZero(tokenItem.Value)
		overview.Requests = numericExtra(tokenItem.Extra, "daily_requests")
		overview.Cost = numericExtra(tokenItem.Extra, "daily_cost")
		overview.EnabledAccount = numericExtra(tokenItem.Extra, "enabled_accounts")
		overview.Card.Value = formatPrimaryNumber(overview.TokenValue)
		overview.Card.Stats = []string{
			"请求 " + formatCompactWhole(overview.Requests),
			fmt.Sprintf("成本 $%.2f", overview.Cost),
			"启用 " + formatCompactWhole(overview.EnabledAccount),
		}
	}

	accounts, fiveReset, weekReset := buildQuotaAccounts(snapshot.Items, sourceKey)
	table.Rows = make([]einkQuotaRow, 0, len(accounts))
	table.HasRows = len(accounts) > 0
	table.FiveHourReset = "5H 重置: " + nonEmpty(formatResetFooter(fiveReset), "--")
	table.WeekReset = "Week 重置: " + nonEmpty(formatResetFooter(weekReset), "--")

	var (
		criticalCount int
		alerts        []string
	)
	for _, account := range accounts {
		row := buildQuotaRow(account)
		table.Rows = append(table.Rows, row)

		if alert, critical, ok := buildAlert(account, sourceKey); ok {
			alerts = append(alerts, alert)
			if critical {
				criticalCount++
			}
		}
	}
	table.FocusRow = pickFocusQuotaRow(table.Rows)

	return overview, table, criticalCount, alerts
}

func maxSnapshotDataUpdatedAt(snapshots map[string]model.SourceSnapshot) int64 {
	var updatedAt int64
	for _, snapshot := range snapshots {
		candidate := snapshotDataUpdatedAt(snapshot)
		if candidate > updatedAt {
			updatedAt = candidate
		}
	}
	return updatedAt
}

func snapshotDataUpdatedAt(snapshot model.SourceSnapshot) int64 {
	var updatedAt int64
	for _, item := range snapshot.Items {
		if item.FetchedAt > updatedAt {
			updatedAt = item.FetchedAt
		}
	}
	if updatedAt > 0 {
		return updatedAt
	}
	return snapshot.LastFetch
}

func buildTotalOverview(claude sourceOverview, sub sourceOverview, criticalCount int) einkOverviewCard {
	return einkOverviewCard{
		Kind:  "total",
		Title: "今日合计",
		Value: formatPrimaryNumber(claude.TokenValue + sub.TokenValue),
		Label: "总 Token",
		Stats: []string{
			"总请求 " + formatCompactWhole(claude.Requests+sub.Requests),
			fmt.Sprintf("总成本 $%.2f", claude.Cost+sub.Cost),
			"告警 " + formatCompactWhole(float64(criticalCount)),
		},
		Icon: dashboardCardIcon("total"),
	}
}

func dashboardTableTitle(sourceKey string) string {
	switch sourceKey {
	case "claude_local":
		return "Claude Local 配额"
	case "claude_relay":
		return "Claude Relay 配额"
	case "codex_local":
		return "Codex Local 配额"
	case "codex":
		return "Codex 账号额度"
	case "sub2api":
		return "Sub2API 账号额度"
	default:
		return sourceKey + " 配额"
	}
}

func buildQuotaAccounts(items []model.DataItem, sourceKey string) ([]quotaAccount, string, string) {
	order := make([]string, 0)
	byName := make(map[string]*quotaAccount)
	var (
		fiveReset string
		weekReset string
	)

	for _, item := range items {
		if item.Category != "quota" {
			continue
		}

		name, window := extractQuotaNameAndWindow(item)
		if name == "" || window == "" {
			continue
		}
		if window != "5H" && window != "Week" {
			continue
		}

		account, ok := byName[name]
		if !ok {
			account = &quotaAccount{Name: name, SourceKey: sourceKey}
			byName[name] = account
			order = append(order, name)
		}

		metric := quotaMetric{
			UsedPercent:      clampPercent(quotaUsedPercent(item)),
			RemainingPercent: clampPercent(quotaRemainingPercent(item)),
			ResetAt:          stringExtra(item.Extra, "reset_at"),
			QuotaSource:      stringExtra(item.Extra, "quota_source"),
			OnlineStatus:     stringExtra(item.Extra, "online_quota_status"),
		}
		if isLocalQuotaSource(sourceKey) && metric.QuotaSource == "estimated_cap" && numericExtra(item.Extra, "cap") <= 0 {
			metric.Unknown = true
			metric.ResetAt = ""
		}

		switch window {
		case "5H":
			account.FiveHour = metric
			fiveReset = firstResetAt(fiveReset, metric.ResetAt)
		case "Week":
			account.Week = metric
			weekReset = firstResetAt(weekReset, metric.ResetAt)
		}
	}

	result := make([]quotaAccount, 0, len(order))
	for _, name := range order {
		result = append(result, *byName[name])
	}
	return result, fiveReset, weekReset
}

func buildQuotaRow(account quotaAccount) einkQuotaRow {
	status, className := quotaStatus(account)
	return einkQuotaRow{
		Account:     account.Name,
		FiveHour:    quotaPercentCell(account.FiveHour),
		Week:        quotaPercentCell(account.Week),
		Status:      status,
		StatusClass: className,
	}
}

func quotaPercentCell(metric quotaMetric) einkPercentCell {
	if metric.Unknown {
		return einkPercentCell{Percent: 0, Text: "--"}
	}
	return einkPercentCell{
		Percent: metric.RemainingPercent,
		Text:    formatPercentText(metric.RemainingPercent),
	}
}

func pickFocusQuotaRow(rows []einkQuotaRow) einkQuotaRow {
	if len(rows) == 0 {
		return emptyQuotaRow()
	}

	selected := rows[0]
	for _, row := range rows[1:] {
		currentSeverity := quotaRowSeverity(row)
		selectedSeverity := quotaRowSeverity(selected)
		if currentSeverity > selectedSeverity || currentSeverity == selectedSeverity && quotaRowFloor(row) < quotaRowFloor(selected) {
			selected = row
		}
	}
	return selected
}

func emptyQuotaRow() einkQuotaRow {
	return einkQuotaRow{
		Account:  "--",
		FiveHour: einkPercentCell{Percent: 0, Text: "--"},
		Week:     einkPercentCell{Percent: 0, Text: "--"},
		Status:   "--",
	}
}

func quotaRowSeverity(row einkQuotaRow) int {
	switch {
	case row.FiveHour.Text == "--" || row.Week.Text == "--":
		return 4
	case row.Week.Percent == 0:
		return 5
	case row.FiveHour.Percent <= 20 || row.Week.Percent <= 10:
		return 4
	case row.FiveHour.Percent <= 50 || row.Week.Percent <= 50 || strings.Contains(row.Status, "关注"):
		return 3
	default:
		return 1
	}
}

func quotaRowFloor(row einkQuotaRow) int {
	if row.FiveHour.Percent < row.Week.Percent {
		return row.FiveHour.Percent
	}
	return row.Week.Percent
}

func quotaStatus(account quotaAccount) (string, string) {
	switch {
	case account.FiveHour.Unknown || account.Week.Unknown:
		return codexOnlineQuotaStatusLabel(account.FiveHour.OnlineStatus, account.Week.OnlineStatus), "solid"
	case account.Week.RemainingPercent <= 0:
		return "Week 耗尽", "solid"
	case account.FiveHour.RemainingPercent >= 95 && account.Week.RemainingPercent >= 95:
		return "最佳", "solid"
	case account.FiveHour.RemainingPercent >= 75 && account.Week.RemainingPercent >= 75:
		return "充足", ""
	case account.FiveHour.RemainingPercent >= 60 && account.Week.RemainingPercent >= 60:
		return "正常", ""
	case account.FiveHour.RemainingPercent <= 20 || account.Week.RemainingPercent <= 10:
		return "告警", "solid"
	default:
		return "关注", ""
	}
}

func buildAlert(account quotaAccount, sourceKey string) (string, bool, bool) {
	displayName := account.Name
	if (sourceKey == "claude_relay" || sourceKey == "claude_local") && !strings.HasPrefix(displayName, "Claude") {
		displayName = "Claude " + displayName
	} else if (sourceKey == "codex" || sourceKey == "codex_local") && !strings.HasPrefix(displayName, "Codex") {
		displayName = "Codex " + displayName
	}

	switch {
	case account.FiveHour.Unknown || account.Week.Unknown:
		if sourceKey == "claude_local" {
			return fmt.Sprintf("%s：额度未知", displayName), false, true
		}
		return fmt.Sprintf("%s：在线额度%s", displayName, codexOnlineQuotaAlertLabel(account.FiveHour.OnlineStatus, account.Week.OnlineStatus)), false, true
	case account.Week.RemainingPercent <= 0:
		return fmt.Sprintf("%s：Week 余量 %d%%", displayName, account.Week.RemainingPercent), true, true
	case account.Week.RemainingPercent <= 10:
		return fmt.Sprintf("%s：Week 余量仅 %d%%", displayName, account.Week.RemainingPercent), true, true
	case account.FiveHour.RemainingPercent < 60:
		return fmt.Sprintf("%s：5H 余量仅 %d%%", displayName, account.FiveHour.RemainingPercent), false, true
	default:
		return "", false, false
	}
}

func isLocalQuotaSource(sourceKey string) bool {
	return sourceKey == "claude_local" || sourceKey == "codex_local"
}

func codexOnlineQuotaStatusLabel(statuses ...string) string {
	switch firstNonEmptyStatus(statuses...) {
	case "token_missing":
		return "待登录"
	case "unauthorized":
		return "需刷新"
	case "rate_limited":
		return "限流"
	case "endpoint_404":
		return "端点异常"
	case "transport_error":
		return "查询失败"
	default:
		return "额度未知"
	}
}

func codexOnlineQuotaAlertLabel(statuses ...string) string {
	switch firstNonEmptyStatus(statuses...) {
	case "token_missing":
		return "缺少 ChatGPT token"
	case "unauthorized":
		return "登录已失效"
	case "rate_limited":
		return "被上游限流"
	case "endpoint_404":
		return "端点不可用"
	case "transport_error":
		return "查询失败"
	default:
		return "不可用"
	}
}

func firstNonEmptyStatus(statuses ...string) string {
	for _, status := range statuses {
		status = strings.TrimSpace(status)
		if status != "" && status != "disabled" && status != "ok" {
			return status
		}
	}
	for _, status := range statuses {
		status = strings.TrimSpace(status)
		if status != "" {
			return status
		}
	}
	return ""
}

func findTokenUsageItem(items []model.DataItem) (model.DataItem, bool) {
	for _, item := range items {
		if item.Category == "token_usage" {
			return item, true
		}
	}
	return model.DataItem{}, false
}

func extractQuotaNameAndWindow(item model.DataItem) (string, string) {
	window := strings.TrimSpace(stringExtra(item.Extra, "window"))
	title := strings.TrimSpace(item.Title)
	title = strings.TrimPrefix(title, "账号 ")
	title = strings.TrimSuffix(title, " 额度")
	if title == "" {
		return "", window
	}
	if window == "" {
		for _, candidate := range []string{"5H", "Week"} {
			if strings.HasSuffix(title, " "+candidate) {
				window = candidate
				title = strings.TrimSuffix(title, " "+candidate)
				break
			}
		}
	} else {
		title = strings.TrimSuffix(title, " "+window)
	}
	return strings.TrimSpace(title), window
}

func formatHeaderTime(unixSeconds int64) string {
	if unixSeconds <= 0 {
		return time.Now().Format("2006-01-02 15:04")
	}
	return time.Unix(unixSeconds, 0).In(time.Local).Format("2006-01-02 15:04")
}

func formatResetFooter(resetAt string) string {
	resetAt = strings.TrimSpace(resetAt)
	if resetAt == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, resetAt)
	if err != nil {
		return resetAt
	}
	return parsed.In(time.Local).Format("2006-01-02 15:04")
}

func firstResetAt(current string, candidate string) string {
	if strings.TrimSpace(candidate) == "" {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return candidate
	}

	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate)
	if currentErr != nil || candidateErr != nil {
		return current
	}
	if candidateTime.Before(currentTime) {
		return candidate
	}
	return current
}

func dashboardRefreshSeconds(r *http.Request) int {
	refreshSeconds := einkDashboardDefaultRefreshSeconds
	if raw := strings.TrimSpace(r.URL.Query().Get("refresh")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			refreshSeconds = parsed
		}
	}
	if refreshSeconds < einkDashboardMinRefreshSeconds {
		return einkDashboardMinRefreshSeconds
	}
	if refreshSeconds > einkDashboardMaxRefreshSeconds {
		return einkDashboardMaxRefreshSeconds
	}
	return refreshSeconds
}

func formatPrimaryNumber(value float64) string {
	if value <= 0 {
		return "--"
	}
	if math.Abs(value-math.Round(value)) < 0.000001 {
		return formatInt64(int64(math.Round(value)))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), ".")
}

func formatTokenMillions(value float64) string {
	if value <= 0 {
		return "--"
	}
	tenths := int64(math.Round(value / 100000))
	return fmt.Sprintf("%d.%dM", tenths/10, tenths%10)
}

func splitDashboardAlert(alerts []string) (string, string) {
	if len(alerts) == 0 {
		return "", ""
	}
	alert := alerts[0]
	for _, separator := range []string{"：", ":"} {
		if index := strings.Index(alert, separator); index >= 0 {
			return strings.TrimSpace(alert[:index]), strings.TrimSpace(alert[index+len(separator):])
		}
	}
	return alert, ""
}

func mergeDashboardAlerts(groups ...[]string) []string {
	alerts := make([]string, 0)
	for _, group := range groups {
		alerts = append(alerts, group...)
	}
	sort.SliceStable(alerts, func(i, j int) bool {
		leftPercent := alertRemainingPercent(alerts[i])
		rightPercent := alertRemainingPercent(alerts[j])
		if leftPercent != rightPercent {
			return leftPercent < rightPercent
		}
		return alertSourcePriority(alerts[i]) < alertSourcePriority(alerts[j])
	})
	return alerts
}

func alertSourcePriority(alert string) int {
	if strings.HasPrefix(alert, "Claude ") {
		return 0
	}
	if strings.HasPrefix(alert, "Codex ") {
		return 1
	}
	return 2
}

func alertRemainingPercent(alert string) int {
	percentIndex := strings.LastIndex(alert, "%")
	if percentIndex < 0 {
		return 101
	}

	start := percentIndex
	for start > 0 {
		char := alert[start-1]
		if char < '0' || char > '9' {
			break
		}
		start--
	}
	if start == percentIndex {
		return 101
	}

	value, err := strconv.Atoi(alert[start:percentIndex])
	if err != nil {
		return 101
	}
	return value
}

func formatCompactWhole(value float64) string {
	return formatInt64(int64(math.Round(value)))
}

func formatPercentText(value int) string {
	return fmt.Sprintf("%d%%", value)
}

func clampPercent(value float64) int {
	switch {
	case value < 0:
		return 0
	case value > 100:
		return 100
	default:
		return int(math.Round(value))
	}
}

func quotaUsedPercent(item model.DataItem) float64 {
	if value, ok := floatValue(item.Extra["used_percent"]); ok {
		return value
	}
	if value, ok := floatValue(item.Extra["remaining_percent"]); ok {
		return 100 - value
	}
	if number, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(item.Value), "%"), 64); err == nil {
		return 100 - number
	}
	return 0
}

func quotaRemainingPercent(item model.DataItem) float64 {
	if value, ok := floatValue(item.Extra["remaining_percent"]); ok {
		return value
	}
	if value, ok := floatValue(item.Extra["used_percent"]); ok {
		return 100 - value
	}
	if number, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(item.Value), "%"), 64); err == nil {
		return number
	}
	return 0
}

func numericOrZero(value string) float64 {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	if value == "" {
		return 0
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return number
}

func numericExtra(extra map[string]any, key string) float64 {
	if extra == nil {
		return 0
	}
	value, ok := floatValue(extra[key])
	if !ok {
		return 0
	}
	return value
}

func stringExtra(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	return stringValue(extra[key])
}

func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case float32:
		return float64(typed), true
	case float64:
		return typed, true
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return number, true
	default:
		return 0, false
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func formatInt64(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}

	text := strconv.FormatInt(value, 10)
	if len(text) <= 3 {
		if negative {
			return "-" + text
		}
		return text
	}

	var groups []string
	for len(text) > 3 {
		groups = append([]string{text[len(text)-3:]}, groups...)
		text = text[:len(text)-3]
	}
	groups = append([]string{text}, groups...)

	joined := strings.Join(groups, ",")
	if negative {
		return "-" + joined
	}
	return joined
}

func nonEmpty(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func dashboardCardIcon(kind string) template.HTML {
	switch kind {
	case "claude_relay", "claude_local":
		return template.HTML(`<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/><rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M9.3 14.4v-4.8h2.2c1.6 0 2.6 1 2.6 2.4s-1 2.4-2.6 2.4H9.3Z"/><path d="M15.2 9.6v4.8"/></svg>`)
	case "sub2api":
		return template.HTML(`<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 18a4 4 0 0 1 0-8c.2 0 .5 0 .7.1A5.5 5.5 0 1 1 18 12h-1"/><path d="M8.5 18.5v2"/><path d="M12 16.5v4"/><path d="M15.5 18.5v2"/><circle cx="8.5" cy="20.5" r=".7" fill="#111111"/><circle cx="12" cy="20.5" r=".7" fill="#111111"/><circle cx="15.5" cy="20.5" r=".7" fill="#111111"/></svg>`)
	case "codex", "codex_local":
		return template.HTML(`<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 18a4 4 0 0 1 0-8c.2 0 .5 0 .7.1A5.5 5.5 0 1 1 18 12h-1"/><path d="M8.5 18.5v2"/><path d="M12 16.5v4"/><path d="M15.5 18.5v2"/><circle cx="8.5" cy="20.5" r=".7" fill="#111111"/><circle cx="12" cy="20.5" r=".7" fill="#111111"/><circle cx="15.5" cy="20.5" r=".7" fill="#111111"/></svg>`)
	default:
		return template.HTML(`<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 20h16"/><path d="M7 17V9"/><path d="M12 17V5"/><path d="M17 17v-7"/></svg>`)
	}
}

func writeDashboardError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	message := "dashboard render failed"
	if err != nil {
		message = err.Error()
	}
	_, _ = fmt.Fprintf(w, "<!doctype html><html lang=\"zh-CN\"><meta charset=\"utf-8\"><title>Error</title><body style=\"font-family:sans-serif;padding:24px\">%s</body></html>", template.HTMLEscapeString(message))
}
