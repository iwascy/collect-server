package api

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
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
  <title>AI 额度监控面板</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f5f5f5;
      --panel: #ffffff;
      --ink: #111111;
      --muted: #5e5e5e;
      --line: #111111;
      --soft-line: #c8c8c8;
      --radius: 16px;
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
    }
    .page {
      width: 100vw;
      height: 100vh;
      padding: 1.8vh 1.7vw;
      display: grid;
      grid-template-rows: auto auto minmax(0, 1fr);
      gap: 1.6vh;
    }
    .header {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      gap: 2vw;
    }
    .title {
      font-size: clamp(26px, 3.4vw, 54px);
      font-weight: 900;
      line-height: 1.06;
      letter-spacing: 0.02em;
    }
    .subtitle {
      margin-top: 0.55vh;
      font-size: clamp(14px, 1.5vw, 24px);
      color: #222222;
    }
    .refresh {
      padding-top: 0.8vh;
      font-size: clamp(13px, 1.4vw, 24px);
      white-space: nowrap;
    }
    .overview-grid {
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 1.2vw;
      min-height: 0;
    }
    .card, .panel {
      background: var(--panel);
      border: 1.5px solid rgba(17, 17, 17, 0.45);
      border-radius: var(--radius);
    }
    .card {
      padding: 1.4vh 1.1vw 1vh;
      display: flex;
      flex-direction: column;
      min-height: 0;
    }
    .card-top {
      display: grid;
      grid-template-columns: auto minmax(0, 1fr);
      gap: 1vw;
      align-items: start;
    }
    .icon-box {
      width: clamp(52px, 5.5vw, 82px);
      height: clamp(52px, 5.5vw, 82px);
      border: 1.5px solid var(--line);
      border-radius: 12px;
      display: flex;
      align-items: center;
      justify-content: center;
      flex-shrink: 0;
    }
    .icon-box svg {
      width: 70%;
      height: 70%;
      stroke: #111111;
      fill: none;
      stroke-width: 1.9;
      stroke-linecap: round;
      stroke-linejoin: round;
    }
    .card-title {
      font-size: clamp(14px, 1.5vw, 24px);
      line-height: 1.25;
      margin-top: 0.2vh;
    }
    .card-value {
      margin-top: 0.35vh;
      font-size: clamp(30px, 4vw, 66px);
      font-weight: 900;
      line-height: 1.02;
      letter-spacing: -0.04em;
      white-space: nowrap;
    }
    .card-label {
      margin-top: 0.4vh;
      font-size: clamp(13px, 1.35vw, 22px);
      color: #222222;
    }
    .card-stats {
      margin-top: auto;
      padding-top: 0.9vh;
      border-top: 1px dotted rgba(17, 17, 17, 0.45);
      display: flex;
      justify-content: space-between;
      gap: 0.8vw;
      font-size: clamp(11px, 1.2vw, 20px);
      flex-wrap: nowrap;
    }
    .card-stats span {
      position: relative;
      white-space: nowrap;
      flex: 1 1 0;
      text-align: center;
    }
    .card-stats span + span::before {
      content: "";
      position: absolute;
      left: -0.4vw;
      top: 0.15em;
      width: 1px;
      height: 1.1em;
      background: rgba(17, 17, 17, 0.32);
    }
    .content-grid {
      display: grid;
      grid-template-columns: minmax(0, 1fr) minmax(0, 1.55fr) minmax(220px, 0.62fr);
      gap: 1vw;
      min-height: 0;
    }
    .panel {
      padding: 1.1vh 0.9vw 0.8vh;
      display: flex;
      flex-direction: column;
      min-height: 0;
    }
    .panel-header {
      display: flex;
      align-items: center;
      gap: 0.7vw;
      font-size: clamp(16px, 1.65vw, 27px);
      margin-bottom: 0.8vh;
    }
    .panel-header svg {
      width: clamp(20px, 2vw, 32px);
      height: clamp(20px, 2vw, 32px);
      stroke: #111111;
      fill: none;
      stroke-width: 1.9;
      stroke-linecap: round;
      stroke-linejoin: round;
      flex-shrink: 0;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      table-layout: fixed;
      font-size: clamp(11px, 1.2vw, 18px);
    }
    th, td {
      border: 1px solid rgba(17, 17, 17, 0.22);
      padding: 0.7vh 0.55vw;
      vertical-align: middle;
      text-align: left;
    }
    th {
      font-weight: 700;
      background: rgba(17, 17, 17, 0.02);
      white-space: nowrap;
    }
    .table-wrap {
      min-height: 0;
      flex: 1 1 auto;
      display: flex;
      flex-direction: column;
    }
    .table-wrap table {
      height: 100%;
    }
    .account-cell {
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .progress-cell {
      display: flex;
      align-items: center;
      gap: 0.5vw;
      min-width: 0;
    }
    .progress-text {
      width: 2.8em;
      flex-shrink: 0;
      white-space: nowrap;
    }
    .progress-track {
      position: relative;
      flex: 1 1 auto;
      min-width: 0;
      height: clamp(14px, 1.35vw, 20px);
      border: 1.5px solid rgba(17, 17, 17, 0.55);
      border-radius: 2px;
      background: #ffffff;
      overflow: hidden;
    }
    .progress-fill {
      height: 100%;
      background-image: repeating-linear-gradient(-45deg, #111111 0, #111111 2px, #ffffff 2px, #ffffff 4px);
      background-size: 8px 8px;
      background-color: #111111;
    }
    .status-badge {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-width: 4.6em;
      padding: 0.34em 0.58em;
      border-radius: 7px;
      border: 1.5px solid #111111;
      font-size: 0.94em;
      white-space: nowrap;
      background: #ffffff;
    }
    .status-badge.solid {
      background: #111111;
      color: #ffffff;
    }
    .table-footer {
      margin-top: 0.65vh;
      padding: 0 0.15vw;
      font-size: clamp(10px, 1.05vw, 16px);
      color: #2b2b2b;
      display: flex;
      justify-content: space-between;
      gap: 0.8vw;
      white-space: nowrap;
    }
    .alerts {
      padding-left: 1.1em;
      margin: 0.2vh 0 0;
      font-size: clamp(12px, 1.25vw, 18px);
      line-height: 1.55;
    }
    .alerts li + li {
      margin-top: 0.7vh;
    }
    .empty-note {
      color: var(--muted);
      font-size: clamp(12px, 1.15vw, 18px);
      padding: 1.4vh 0.2vw;
      line-height: 1.5;
    }
    .error-note {
      color: #111111;
      font-size: clamp(12px, 1.12vw, 17px);
      line-height: 1.45;
      padding: 1.2vh 0.2vw;
    }
  </style>
</head>
<body>
  <main class="page">
    <header class="header">
      <div>
        <div class="title">AI 额度监控面板</div>
        <div class="subtitle">Claude Code · Sub2API</div>
      </div>
      <div class="refresh">刷新时间 {{.UpdatedAt}}</div>
    </header>

    <section class="overview-grid">
      {{range .Overview}}
      <article class="card">
        <div class="card-top">
          <div class="icon-box">{{.Icon}}</div>
          <div>
            <div class="card-title">{{.Title}}</div>
            <div class="card-value">{{.Value}}</div>
            <div class="card-label">{{.Label}}</div>
          </div>
        </div>
        <div class="card-stats">
          {{range .Stats}}<span>{{.}}</span>{{end}}
        </div>
      </article>
      {{end}}
    </section>

    <section class="content-grid">
      <section class="panel">
        <div class="panel-header">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M17 20H7a2 2 0 0 1-2-2c0-3.3 3.1-6 7-6s7 2.7 7 6a2 2 0 0 1-2 2Z"/><circle cx="12" cy="7" r="4"/></svg>
          <span>{{.ClaudeTable.Title}}</span>
        </div>
        {{if .ClaudeTable.ErrorText}}
        <div class="error-note">{{.ClaudeTable.ErrorText}}</div>
        {{else if .ClaudeTable.HasRows}}
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th style="width: 19%;">账号</th>
                <th style="width: 33%;">5H</th>
                <th style="width: 30%;">Week</th>
                <th style="width: 18%;">状态</th>
              </tr>
            </thead>
            <tbody>
              {{range .ClaudeTable.Rows}}
              <tr>
                <td class="account-cell" title="{{.Account}}">{{.Account}}</td>
                <td>
                  <div class="progress-cell">
                    <span class="progress-text">{{.FiveHour.Text}}</span>
                    <div class="progress-track"><div class="progress-fill" style="width: {{.FiveHour.Percent}}%"></div></div>
                  </div>
                </td>
                <td>
                  <div class="progress-cell">
                    <span class="progress-text">{{.Week.Text}}</span>
                    <div class="progress-track"><div class="progress-fill" style="width: {{.Week.Percent}}%"></div></div>
                  </div>
                </td>
                <td><span class="status-badge {{.StatusClass}}">{{.Status}}</span></td>
              </tr>
              {{end}}
            </tbody>
          </table>
        </div>
        <div class="table-footer">
          <span>{{.ClaudeTable.FiveHourReset}}</span>
          <span>{{.ClaudeTable.WeekReset}}</span>
        </div>
        {{else}}
        <div class="empty-note">当前还没有 Claude Relay 额度数据。</div>
        {{end}}
      </section>

      <section class="panel">
        <div class="panel-header">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 18a4 4 0 0 1 0-8c.2 0 .5 0 .7.1A5.5 5.5 0 1 1 18 12h-1"/><path d="M8.5 18.5v2"/><path d="M12 16.5v4"/><path d="M15.5 18.5v2"/><circle cx="8.5" cy="20.5" r=".7" fill="#111111"/><circle cx="12" cy="20.5" r=".7" fill="#111111"/><circle cx="15.5" cy="20.5" r=".7" fill="#111111"/></svg>
          <span>{{.Sub2APITable.Title}}</span>
        </div>
        {{if .Sub2APITable.ErrorText}}
        <div class="error-note">{{.Sub2APITable.ErrorText}}</div>
        {{else if .Sub2APITable.HasRows}}
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th style="width: 31%;">账号</th>
                <th style="width: 29%;">5H</th>
                <th style="width: 29%;">Week</th>
                <th style="width: 11%;">状态</th>
              </tr>
            </thead>
            <tbody>
              {{range .Sub2APITable.Rows}}
              <tr>
                <td class="account-cell" title="{{.Account}}">{{.Account}}</td>
                <td>
                  <div class="progress-cell">
                    <span class="progress-text">{{.FiveHour.Text}}</span>
                    <div class="progress-track"><div class="progress-fill" style="width: {{.FiveHour.Percent}}%"></div></div>
                  </div>
                </td>
                <td>
                  <div class="progress-cell">
                    <span class="progress-text">{{.Week.Text}}</span>
                    <div class="progress-track"><div class="progress-fill" style="width: {{.Week.Percent}}%"></div></div>
                  </div>
                </td>
                <td><span class="status-badge {{.StatusClass}}">{{.Status}}</span></td>
              </tr>
              {{end}}
            </tbody>
          </table>
        </div>
        {{else}}
        <div class="empty-note">当前还没有 Sub2API 额度数据。</div>
        {{end}}
      </section>

      <aside class="panel">
        <div class="panel-header">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M15 17H5.5a1.5 1.5 0 0 1-1.2-2.4l1.4-1.8V10a5.3 5.3 0 1 1 10.6 0v2.8l1.4 1.8a1.5 1.5 0 0 1-1.2 2.4H15"/><path d="M9.5 18.5a2 2 0 0 0 4 0"/></svg>
          <span>重点提醒</span>
        </div>
        {{if .Alerts}}
        <ul class="alerts">
          {{range .Alerts}}<li>{{.}}</li>{{end}}
        </ul>
        {{else}}
        <div class="empty-note">暂无异常提醒，当前额度状态稳定。</div>
        {{end}}
      </aside>
    </section>
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
	Sub2API        einkDeviceOverview `json:"sub2api"`
	Total          einkDeviceOverview `json:"total"`
	ClaudeRows     []einkQuotaRow     `json:"claude_rows"`
	Sub2APIRows    []einkQuotaRow     `json:"sub2api_rows"`
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
}

func (h *Handler) EInkDashboard(w http.ResponseWriter, r *http.Request) {
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
	snapshots, err := h.store.GetAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, buildEInkDashboardPage(snapshots, dashboardRefreshSeconds(r)))
}

func (h *Handler) EInkDeviceData(w http.ResponseWriter, r *http.Request) {
	snapshots, err := h.store.GetAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, buildEInkDevicePayload(snapshots, dashboardRefreshSeconds(r)))
}

func buildEInkDashboardPage(snapshots map[string]model.SourceSnapshot, refreshSeconds int) einkDashboardPage {
	updatedAtUnix := maxSnapshotDataUpdatedAt(snapshots)
	claudeOverview, claudeTable, claudeCritical, claudeAlerts := buildSourceDashboard("claude_relay", "Claude Relay 今日概览", snapshots["claude_relay"])
	subOverview, subTable, subCritical, subAlerts := buildSourceDashboard("sub2api", "Sub2API 今日概览", snapshots["sub2api"])

	totalOverview := buildTotalOverview(claudeOverview, subOverview, claudeCritical+subCritical)
	alerts := append([]string{}, subAlerts...)
	alerts = append(alerts, claudeAlerts...)

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
	}
}

func buildEInkDevicePayload(snapshots map[string]model.SourceSnapshot, refreshSeconds int) einkDevicePayload {
	updatedAtUnix := maxSnapshotDataUpdatedAt(snapshots)
	claudeOverview, claudeTable, _, claudeAlerts := buildSourceDashboard("claude_relay", "Claude Relay 今日概览", snapshots["claude_relay"])
	subOverview, subTable, _, subAlerts := buildSourceDashboard("sub2api", "Sub2API 今日概览", snapshots["sub2api"])

	totalToken := claudeOverview.TokenValue + subOverview.TokenValue
	totalRequests := int(math.Round(claudeOverview.Requests + subOverview.Requests))
	totalCost := claudeOverview.Cost + subOverview.Cost

	alerts := append([]string{}, subAlerts...)
	alerts = append(alerts, claudeAlerts...)

	return einkDevicePayload{
		UpdatedAt:      formatHeaderTime(updatedAtUnix),
		UpdatedAtUnix:  updatedAtUnix,
		RefreshSeconds: refreshSeconds,
		Claude: einkDeviceOverview{
			Title:        claudeOverview.Card.Title,
			Value:        claudeOverview.Card.Value,
			Label:        claudeOverview.Card.Label,
			Requests:     int(math.Round(claudeOverview.Requests)),
			Cost:         fmt.Sprintf("%.2f", claudeOverview.Cost),
			Enabled:      int(math.Round(claudeOverview.EnabledAccount)),
			ValueNumeric: int64(math.Round(claudeOverview.TokenValue)),
		},
		Sub2API: einkDeviceOverview{
			Title:        subOverview.Card.Title,
			Value:        subOverview.Card.Value,
			Label:        subOverview.Card.Label,
			Requests:     int(math.Round(subOverview.Requests)),
			Cost:         fmt.Sprintf("%.2f", subOverview.Cost),
			Enabled:      int(math.Round(subOverview.EnabledAccount)),
			ValueNumeric: int64(math.Round(subOverview.TokenValue)),
		},
		Total: einkDeviceOverview{
			Title:        "今日合计",
			Value:        formatPrimaryNumber(totalToken),
			Label:        "总 Token",
			Requests:     totalRequests,
			Cost:         fmt.Sprintf("%.2f", totalCost),
			Alerts:       len(alerts),
			ValueNumeric: int64(math.Round(totalToken)),
		},
		ClaudeRows:  claudeTable.Rows,
		Sub2APIRows: subTable.Rows,
		Alerts:      alerts,
		ResetHints: map[string]string{
			"claude_five_hour":  claudeTable.FiveHourReset,
			"claude_week":       claudeTable.WeekReset,
			"sub2api_five_hour": subTable.FiveHourReset,
			"sub2api_week":      subTable.WeekReset,
		},
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
	case "claude_relay":
		return "Claude Relay 配额"
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
		Account: account.Name,
		FiveHour: einkPercentCell{
			Percent: account.FiveHour.RemainingPercent,
			Text:    formatPercentText(account.FiveHour.RemainingPercent),
		},
		Week: einkPercentCell{
			Percent: account.Week.RemainingPercent,
			Text:    formatPercentText(account.Week.RemainingPercent),
		},
		Status:      status,
		StatusClass: className,
	}
}

func quotaStatus(account quotaAccount) (string, string) {
	switch {
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
	if sourceKey == "claude_relay" {
		displayName = "Claude " + displayName
	}

	switch {
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
	case "claude_relay":
		return template.HTML(`<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/><rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M9.3 14.4v-4.8h2.2c1.6 0 2.6 1 2.6 2.4s-1 2.4-2.6 2.4H9.3Z"/><path d="M15.2 9.6v4.8"/></svg>`)
	case "sub2api":
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
