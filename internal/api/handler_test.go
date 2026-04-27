package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"infohub/internal/collector"
	"infohub/internal/model"
	"infohub/internal/store"
)

type fakeCollector struct {
	name string
}

func (f fakeCollector) Name() string { return f.name }

func (f fakeCollector) Collect(_ context.Context) ([]model.DataItem, error) {
	return nil, nil
}

func newTestDashboardHandler(dataStore store.Store) *Handler {
	return NewHandlerWithOptions(dataStore, collector.NewRegistry(), nil, HandlerOptions{
		DashboardSources: DashboardSources{
			Claude: "claude_relay",
			Codex:  "sub2api",
		},
	})
}

func TestSummaryHandler(t *testing.T) {
	dataStore := store.NewMemoryStore()
	if err := dataStore.Save("claude_relay", []model.DataItem{{
		Source:    "claude_relay",
		Category:  "token_usage",
		Title:     "今日 Token 用量",
		Value:     "123",
		FetchedAt: 1713600000,
	}}); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	registry := collector.NewRegistry()
	handler := NewHandler(dataStore, registry, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/summary", nil)
	rec := httptest.NewRecorder()
	handler.Summary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload model.SummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.UpdatedAt != 1713600000 {
		t.Fatalf("unexpected updated_at: %d", payload.UpdatedAt)
	}
	if got := payload.Sources["claude_relay"].Items[0].Value; got != "123" {
		t.Fatalf("unexpected source item value: %s", got)
	}
}

func TestHealthHandlerIncludesUnknownRegisteredCollector(t *testing.T) {
	dataStore := store.NewMemoryStore()
	registry := collector.NewRegistry()
	registry.Register(fakeCollector{name: "feishu"})

	handler := NewHandler(dataStore, registry, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload model.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.Collectors["feishu"].Status != "unknown" {
		t.Fatalf("unexpected collector status: %s", payload.Collectors["feishu"].Status)
	}
}

func TestEInkDashboardRendersCustomLayout(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "claude_relay", []model.DataItem{
		{
			Source:    "claude_relay",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "1058870",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"daily_cost":       1.62,
				"daily_requests":   14,
				"enabled_accounts": 1,
			},
		},
		{
			Source:    "claude_relay",
			Category:  "quota",
			Title:     "账号 cycyzg 5H 额度",
			Value:     "71%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 71,
				"window":            "5H",
				"reset_at":          "2026-04-21T22:00:00+08:00",
			},
		},
		{
			Source:    "claude_relay",
			Category:  "quota",
			Title:     "账号 cycyzg Week 额度",
			Value:     "77%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 77,
				"window":            "Week",
				"reset_at":          "2026-04-26T00:00:00+08:00",
			},
		},
	})
	mustSaveSnapshot(t, dataStore, "sub2api", []model.DataItem{
		{
			Source:    "sub2api",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "24854435",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"daily_cost":       13.55,
				"daily_requests":   394,
				"enabled_accounts": 5,
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 2 5H 额度",
			Value:     "77%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 77,
				"window":            "5H",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 2 Week 额度",
			Value:     "93%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 93,
				"window":            "Week",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 5H 额度",
			Value:     "56%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 56,
				"window":            "5H",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 Week 额度",
			Value:     "92%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 92,
				"window":            "Week",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 kr2vv1nh@test1.susususu.fun 5H 额度",
			Value:     "100%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 100,
				"window":            "5H",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 kr2vv1nh@test1.susususu.fun Week 额度",
			Value:     "64%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 64,
				"window":            "Week",
			},
		},
	})

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink?refresh=600", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	body := rec.Body.String()
	for _, expected := range []string{
		"InfoHub 墨水屏面板",
		"InfoHub",
		"Claude 配额",
		"Codex 配额",
		">5H<",
		">Week<",
		"1.1M",
		"24.9M",
		"25.9M",
		"刷新周期 600s",
		"71%",
		"77%",
		"56%",
		"92%",
		"admin10010",
		"5H 余量仅 56%",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("dashboard body missing %q", expected)
		}
	}
}

func TestDashboardRouteUsesDedicatedToken(t *testing.T) {
	dataStore := store.NewMemoryStore()
	registry := collector.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(dataStore, registry, nil, logger, "api-token", "view-token", false, DashboardSources{})

	t.Run("rejects missing dashboard token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/eink", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected status code: %d", rec.Code)
		}
	})

	t.Run("accepts dedicated dashboard token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/eink?token=view-token", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status code: %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("accepts dedicated dashboard token for json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/eink.json?token=view-token", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status code: %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("accepts dedicated dashboard token for device json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json?token=view-token", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status code: %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("keeps api auth unchanged", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/summary?token=view-token", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected status code: %d", rec.Code)
		}
	})
}

func TestEInkDashboardDataReturnsStructuredPayload(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "claude_relay", []model.DataItem{
		{
			Source:    "claude_relay",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "1058870",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"daily_cost":       1.62,
				"daily_requests":   14,
				"enabled_accounts": 1,
			},
		},
		{
			Source:    "claude_relay",
			Category:  "quota",
			Title:     "账号 cycyzg 5H 额度",
			Value:     "71%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 71,
				"window":            "5H",
				"reset_at":          "2026-04-21T22:00:00+08:00",
			},
		},
		{
			Source:    "claude_relay",
			Category:  "quota",
			Title:     "账号 cycyzg Week 额度",
			Value:     "77%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 77,
				"window":            "Week",
				"reset_at":          "2026-04-26T00:00:00+08:00",
			},
		},
	})

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink.json?refresh=300", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboardData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		UpdatedAtUnix  int64 `json:"updated_at_unix"`
		RefreshSeconds int   `json:"refresh_seconds"`
		Overview       []struct {
			Kind  string   `json:"kind"`
			Title string   `json:"title"`
			Value string   `json:"value"`
			Stats []string `json:"stats"`
		} `json:"overview"`
		ClaudeTable struct {
			HasRows bool `json:"has_rows"`
			Rows    []struct {
				Account  string `json:"account"`
				Status   string `json:"status"`
				FiveHour struct {
					Percent int    `json:"percent"`
					Text    string `json:"text"`
				} `json:"five_hour"`
				Week struct {
					Percent int    `json:"percent"`
					Text    string `json:"text"`
				} `json:"week"`
			} `json:"rows"`
		} `json:"claude_table"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if payload.UpdatedAtUnix != 1776766339 {
		t.Fatalf("unexpected updated_at_unix: %d", payload.UpdatedAtUnix)
	}
	if payload.RefreshSeconds != 300 {
		t.Fatalf("unexpected refresh_seconds: %d", payload.RefreshSeconds)
	}
	if len(payload.Overview) == 0 || payload.Overview[0].Kind != "claude_relay" {
		t.Fatalf("unexpected overview payload: %+v", payload.Overview)
	}
	if !payload.ClaudeTable.HasRows || len(payload.ClaudeTable.Rows) != 1 {
		t.Fatalf("unexpected claude table rows: %+v", payload.ClaudeTable.Rows)
	}
	row := payload.ClaudeTable.Rows[0]
	if row.FiveHour.Percent != 71 || row.FiveHour.Text != "71%" {
		t.Fatalf("unexpected 5H payload: %+v", row.FiveHour)
	}
	if row.Week.Percent != 77 || row.Week.Text != "77%" {
		t.Fatalf("unexpected week payload: %+v", row.Week)
	}
	if row.Status != "正常" {
		t.Fatalf("unexpected row status: %s", row.Status)
	}
}

func TestEInkDeviceDataReturnsCompactPayload(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "claude_relay", []model.DataItem{
		{
			Source:    "claude_relay",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "1058870",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"daily_cost":       1.62,
				"daily_requests":   14,
				"enabled_accounts": 1,
			},
		},
		{
			Source:    "claude_relay",
			Category:  "quota",
			Title:     "账号 cycyzg 5H 额度",
			Value:     "71%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 71,
				"window":            "5H",
				"reset_at":          "2026-04-21T22:00:00+08:00",
			},
		},
		{
			Source:    "claude_relay",
			Category:  "quota",
			Title:     "账号 cycyzg Week 额度",
			Value:     "77%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 77,
				"window":            "Week",
				"reset_at":          "2026-04-26T00:00:00+08:00",
			},
		},
	})
	mustSaveSnapshot(t, dataStore, "sub2api", []model.DataItem{
		{
			Source:    "sub2api",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "24854435",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"daily_cost":       13.55,
				"daily_requests":   394,
				"enabled_accounts": 5,
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 5H 额度",
			Value:     "56%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 56,
				"window":            "5H",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 Week 额度",
			Value:     "92%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 92,
				"window":            "Week",
			},
		},
	})

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json?refresh=180", nil)
	rec := httptest.NewRecorder()
	handler.EInkDeviceData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		UpdatedAtUnix  int64 `json:"updated_at_unix"`
		RefreshSeconds int   `json:"refresh_seconds"`
		Claude         struct {
			Value        string `json:"value"`
			Requests     int    `json:"requests"`
			Cost         string `json:"cost"`
			Enabled      int    `json:"enabled"`
			ValueNumeric int64  `json:"value_numeric"`
		} `json:"claude"`
		Total struct {
			Value    string `json:"value"`
			Requests int    `json:"requests"`
			Cost     string `json:"cost"`
			Alerts   int    `json:"alerts"`
		} `json:"total"`
		Codex struct {
			Value        string `json:"value"`
			Requests     int    `json:"requests"`
			Cost         string `json:"cost"`
			Enabled      int    `json:"enabled"`
			ValueNumeric int64  `json:"value_numeric"`
		} `json:"codex"`
		CodexRows []struct {
			Account  string `json:"account"`
			FiveHour struct {
				Percent int `json:"percent"`
			} `json:"five_hour"`
			Status string `json:"status"`
		} `json:"codex_rows"`
		Alerts []string `json:"alerts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if payload.UpdatedAtUnix != 1776766339 {
		t.Fatalf("unexpected updated_at_unix: %d", payload.UpdatedAtUnix)
	}
	if payload.RefreshSeconds != 180 {
		t.Fatalf("unexpected refresh_seconds: %d", payload.RefreshSeconds)
	}
	if payload.Claude.Value != "1,058,870" || payload.Claude.Requests != 14 || payload.Claude.Cost != "1.62" || payload.Claude.Enabled != 1 {
		t.Fatalf("unexpected claude payload: %+v", payload.Claude)
	}
	if payload.Claude.ValueNumeric != 1058870 {
		t.Fatalf("unexpected claude numeric payload: %+v", payload.Claude)
	}
	if payload.Codex.Value != "24,854,435" || payload.Codex.Requests != 394 || payload.Codex.Cost != "13.55" || payload.Codex.Enabled != 5 {
		t.Fatalf("unexpected codex payload: %+v", payload.Codex)
	}
	if payload.Codex.ValueNumeric != 24854435 {
		t.Fatalf("unexpected codex numeric payload: %+v", payload.Codex)
	}
	if payload.Total.Value != "25,913,305" || payload.Total.Requests != 408 || payload.Total.Cost != "15.17" || payload.Total.Alerts != 1 {
		t.Fatalf("unexpected total payload: %+v", payload.Total)
	}
	if len(payload.CodexRows) != 1 || payload.CodexRows[0].Account != "admin10010" || payload.CodexRows[0].FiveHour.Percent != 56 || payload.CodexRows[0].Status != "关注" {
		t.Fatalf("unexpected codex rows: %+v", payload.CodexRows)
	}
	if len(payload.Alerts) != 1 || payload.Alerts[0] != "Codex admin10010：5H 余量仅 56%" {
		t.Fatalf("unexpected alerts: %+v", payload.Alerts)
	}
}

func TestEInkDashboardUsesConfiguredRemoteSourcesOverLocalFailures(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "claude_relay", []model.DataItem{
		{
			Source:    "claude_relay",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "1058870",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"daily_cost":       1.62,
				"daily_requests":   14,
				"enabled_accounts": 1,
			},
		},
	})
	mustSaveSnapshot(t, dataStore, "sub2api", []model.DataItem{
		{
			Source:    "sub2api",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "24854435",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"daily_cost":       13.55,
				"daily_requests":   394,
				"enabled_accounts": 5,
			},
		},
	})
	if err := dataStore.SaveFailure("claude_local", context.DeadlineExceeded, time.Unix(1776767000, 0)); err != nil {
		t.Fatalf("save claude local failure failed: %v", err)
	}
	if err := dataStore.SaveFailure("codex_local", context.DeadlineExceeded, time.Unix(1776767000, 0)); err != nil {
		t.Fatalf("save codex local failure failed: %v", err)
	}

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink.json", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboardData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var dashboardPayload struct {
		UpdatedAtUnix int64 `json:"updated_at_unix"`
		Overview      []struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
			Value string `json:"value"`
		} `json:"overview"`
		Device struct {
			UpdatedAtUnix int64 `json:"updated_at_unix"`
			Claude        struct {
				Title string `json:"title"`
				Value string `json:"value"`
			} `json:"claude"`
			Codex struct {
				Title string `json:"title"`
				Value string `json:"value"`
			} `json:"codex"`
		} `json:"device"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dashboardPayload); err != nil {
		t.Fatalf("decode dashboard response failed: %v", err)
	}

	if dashboardPayload.UpdatedAtUnix != 1776766339 || dashboardPayload.Device.UpdatedAtUnix != 1776766339 {
		t.Fatalf("dashboard should use remote source timestamps: %+v", dashboardPayload)
	}
	if len(dashboardPayload.Overview) < 2 || dashboardPayload.Overview[0].Kind != "claude_relay" || dashboardPayload.Overview[1].Kind != "sub2api" {
		t.Fatalf("dashboard should prefer remote sources: %+v", dashboardPayload.Overview)
	}
	if dashboardPayload.Device.Claude.Title != "Claude Relay 今日概览" || dashboardPayload.Device.Claude.Value != "1,058,870" {
		t.Fatalf("device should use Claude Relay data: %+v", dashboardPayload.Device.Claude)
	}
	if dashboardPayload.Device.Codex.Title != "Codex 今日概览" || dashboardPayload.Device.Codex.Value != "24,854,435" {
		t.Fatalf("device should use Sub2API data for Codex panel: %+v", dashboardPayload.Device.Codex)
	}
}

func TestEInkDashboardDoesNotReadSourcesWithoutConfiguration(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "claude_relay", []model.DataItem{
		{
			Source:    "claude_relay",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "1058870",
			FetchedAt: 1776766339,
		},
	})
	mustSaveSnapshot(t, dataStore, "sub2api", []model.DataItem{
		{
			Source:    "sub2api",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "24854435",
			FetchedAt: 1776766339,
		},
	})

	handler := NewHandler(dataStore, collector.NewRegistry(), nil)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json", nil)
	rec := httptest.NewRecorder()
	handler.EInkDeviceData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		UpdatedAtUnix int64 `json:"updated_at_unix"`
		Claude        struct {
			Title string `json:"title"`
			Value string `json:"value"`
		} `json:"claude"`
		Codex struct {
			Title string `json:"title"`
			Value string `json:"value"`
		} `json:"codex"`
		Total struct {
			Value string `json:"value"`
		} `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if payload.UpdatedAtUnix != 0 {
		t.Fatalf("dashboard should not read unconfigured source timestamps: %+v", payload)
	}
	if payload.Claude.Value != "--" || payload.Codex.Value != "--" || payload.Total.Value != "--" {
		t.Fatalf("dashboard should not read unconfigured source values: %+v", payload)
	}
	if payload.Claude.Title != "未配置数据源" || payload.Codex.Title != "未配置数据源" {
		t.Fatalf("dashboard should expose unconfigured source titles: %+v", payload)
	}
}

func TestEInkDashboardDataPrioritizesLowestRemainingAlert(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "claude_relay", []model.DataItem{
		{
			Source:    "claude_relay",
			Category:  "quota",
			Title:     "账号 cycyzg 5H 额度",
			Value:     "6%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 6,
				"window":            "5H",
			},
		},
		{
			Source:    "claude_relay",
			Category:  "quota",
			Title:     "账号 cycyzg Week 额度",
			Value:     "83%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 83,
				"window":            "Week",
			},
		},
	})
	mustSaveSnapshot(t, dataStore, "sub2api", []model.DataItem{
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 5H 额度",
			Value:     "58%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 58,
				"window":            "5H",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 Week 额度",
			Value:     "18%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 18,
				"window":            "Week",
			},
		},
	})

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/data.json?refresh=300", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboardData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		Alerts      []string `json:"alerts"`
		AlertTitle  string   `json:"alert_title"`
		AlertDetail string   `json:"alert_detail"`
		Device      struct {
			Alerts []string `json:"alerts"`
		} `json:"device"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if len(payload.Alerts) != 2 || payload.Alerts[0] != "Claude cycyzg：5H 余量仅 6%" || payload.Alerts[1] != "admin10010：5H 余量仅 58%" {
		t.Fatalf("unexpected dashboard alerts: %+v", payload.Alerts)
	}
	if payload.AlertTitle != "Claude cycyzg" || payload.AlertDetail != "5H 余量仅 6%" {
		t.Fatalf("unexpected dashboard alert summary: %q / %q", payload.AlertTitle, payload.AlertDetail)
	}
	if len(payload.Device.Alerts) != 2 || payload.Device.Alerts[0] != "Claude cycyzg：5H 余量仅 6%" || payload.Device.Alerts[1] != "Codex admin10010：5H 余量仅 58%" {
		t.Fatalf("unexpected device alerts: %+v", payload.Device.Alerts)
	}
}

func TestEInkDashboardDataPrioritizesClaudeAlertWhenRemainingTies(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "claude_relay", []model.DataItem{
		{
			Source:    "claude_relay",
			Category:  "quota",
			Title:     "账号 cycyzg 5H 额度",
			Value:     "58%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 58,
				"window":            "5H",
			},
		},
		{
			Source:    "claude_relay",
			Category:  "quota",
			Title:     "账号 cycyzg Week 额度",
			Value:     "83%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 83,
				"window":            "Week",
			},
		},
	})
	mustSaveSnapshot(t, dataStore, "sub2api", []model.DataItem{
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 5H 额度",
			Value:     "58%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 58,
				"window":            "5H",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 Week 额度",
			Value:     "18%",
			FetchedAt: 1777027500,
			Extra: map[string]any{
				"remaining_percent": 18,
				"window":            "Week",
			},
		},
	})

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink.json?refresh=300", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboardData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		Alerts      []string `json:"alerts"`
		AlertTitle  string   `json:"alert_title"`
		AlertDetail string   `json:"alert_detail"`
		Device      struct {
			Alerts []string `json:"alerts"`
		} `json:"device"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if len(payload.Alerts) != 2 || payload.Alerts[0] != "Claude cycyzg：5H 余量仅 58%" || payload.Alerts[1] != "admin10010：5H 余量仅 58%" {
		t.Fatalf("unexpected dashboard alerts: %+v", payload.Alerts)
	}
	if payload.AlertTitle != "Claude cycyzg" || payload.AlertDetail != "5H 余量仅 58%" {
		t.Fatalf("unexpected dashboard alert summary: %q / %q", payload.AlertTitle, payload.AlertDetail)
	}
	if len(payload.Device.Alerts) != 2 || payload.Device.Alerts[0] != "Claude cycyzg：5H 余量仅 58%" || payload.Device.Alerts[1] != "Codex admin10010：5H 余量仅 58%" {
		t.Fatalf("unexpected device alerts: %+v", payload.Device.Alerts)
	}
}

func TestEInkDashboardMarksLocalQuotaWithoutCapUnknown(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "claude_local", []model.DataItem{
		{
			Source:    "claude_local",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "10120138",
			FetchedAt: 1777265700,
			Extra: map[string]any{
				"daily_cost":       0,
				"daily_requests":   160,
				"enabled_accounts": 1,
			},
		},
		{
			Source:    "claude_local",
			Category:  "quota",
			Title:     "账号 Claude Local 5H 额度",
			Value:     "100%",
			FetchedAt: 1777265700,
			Extra: map[string]any{
				"cap":               0,
				"quota_source":      "estimated_cap",
				"remaining_percent": 100,
				"window":            "5H",
			},
		},
		{
			Source:    "claude_local",
			Category:  "quota",
			Title:     "账号 Claude Local Week 额度",
			Value:     "100%",
			FetchedAt: 1777265700,
			Extra: map[string]any{
				"cap":               0,
				"quota_source":      "estimated_cap",
				"remaining_percent": 100,
				"window":            "Week",
			},
		},
	})

	handler := NewHandlerWithOptions(dataStore, collector.NewRegistry(), nil, HandlerOptions{
		DashboardSources: DashboardSources{
			Claude: "claude_local",
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink.json", nil)
	rec := httptest.NewRecorder()
	handler.EInkDashboardData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		Alerts      []string `json:"alerts"`
		ClaudeTable struct {
			Rows []struct {
				FiveHour struct {
					Text string `json:"text"`
				} `json:"five_hour"`
				Week struct {
					Text string `json:"text"`
				} `json:"week"`
				Status string `json:"status"`
			} `json:"rows"`
		} `json:"claude_table"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if len(payload.ClaudeTable.Rows) != 1 {
		t.Fatalf("unexpected claude rows: %+v", payload.ClaudeTable.Rows)
	}
	row := payload.ClaudeTable.Rows[0]
	if row.FiveHour.Text != "--" || row.Week.Text != "--" || row.Status != "额度未知" {
		t.Fatalf("unexpected unknown local quota row: %+v", row)
	}
	if len(payload.Alerts) != 1 || payload.Alerts[0] != "Claude Local：额度未知" {
		t.Fatalf("unexpected alerts: %+v", payload.Alerts)
	}
}

func TestEInkDeviceDataReturnsMockPayloadWhenEnabled(t *testing.T) {
	dataStore := store.NewMemoryStore()
	if err := dataStore.Save("sub2api", []model.DataItem{
		{
			Source:    "sub2api",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "1",
			FetchedAt: 1,
		},
	}); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	handler := NewHandlerWithOptions(dataStore, collector.NewRegistry(), nil, HandlerOptions{DashboardMockEnabled: true})
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json?refresh=180", nil)
	rec := httptest.NewRecorder()
	handler.EInkDeviceData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		UpdatedAtUnix  int64    `json:"updated_at_unix"`
		RefreshSeconds int      `json:"refresh_seconds"`
		Alerts         []string `json:"alerts"`
		Codex          struct {
			Value        string `json:"value"`
			ValueNumeric int64  `json:"value_numeric"`
		} `json:"codex"`
		Total struct {
			Alerts int `json:"alerts"`
		} `json:"total"`
		CodexRows []struct {
			Account string `json:"account"`
			Status  string `json:"status"`
		} `json:"codex_rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if payload.UpdatedAtUnix != 1776997800 || payload.RefreshSeconds != 180 {
		t.Fatalf("unexpected mock metadata: %+v", payload)
	}
	if payload.Codex.Value != "24,854,435" || payload.Codex.ValueNumeric != 24854435 {
		t.Fatalf("unexpected mock codex payload: %+v", payload.Codex)
	}
	if payload.Total.Alerts != 0 || len(payload.Alerts) != 0 {
		t.Fatalf("mock payload should not include alerts: %+v", payload)
	}
	if len(payload.CodexRows) != 2 || payload.CodexRows[1].Account != "admin10086" || payload.CodexRows[1].Status != "正常" {
		t.Fatalf("unexpected mock codex rows: %+v", payload.CodexRows)
	}
}

func TestEInkDeviceDataKeepsLastSuccessfulSnapshotOnCollectorFailure(t *testing.T) {
	dataStore := store.NewMemoryStore()
	mustSaveSnapshot(t, dataStore, "sub2api", []model.DataItem{
		{
			Source:    "sub2api",
			Category:  "token_usage",
			Title:     "今日 Token 用量",
			Value:     "24854435",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"daily_cost":       13.55,
				"daily_requests":   394,
				"enabled_accounts": 5,
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 5H 额度",
			Value:     "56%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 56,
				"window":            "5H",
			},
		},
		{
			Source:    "sub2api",
			Category:  "quota",
			Title:     "账号 admin10010 Week 额度",
			Value:     "92%",
			FetchedAt: 1776766339,
			Extra: map[string]any{
				"remaining_percent": 92,
				"window":            "Week",
			},
		},
	})
	if err := dataStore.SaveFailure("sub2api", context.DeadlineExceeded, time.Unix(1776767000, 0)); err != nil {
		t.Fatalf("save failure failed: %v", err)
	}

	handler := newTestDashboardHandler(dataStore)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/eink/device.json?refresh=180", nil)
	rec := httptest.NewRecorder()
	handler.EInkDeviceData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	var payload struct {
		UpdatedAtUnix int64 `json:"updated_at_unix"`
		Codex         struct {
			Value    string `json:"value"`
			Requests int    `json:"requests"`
			Cost     string `json:"cost"`
			Enabled  int    `json:"enabled"`
		} `json:"codex"`
		CodexRows []struct {
			Account string `json:"account"`
			Status  string `json:"status"`
		} `json:"codex_rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if payload.UpdatedAtUnix != 1776766339 {
		t.Fatalf("unexpected updated_at_unix: %d", payload.UpdatedAtUnix)
	}
	if payload.Codex.Value != "24,854,435" || payload.Codex.Requests != 394 || payload.Codex.Cost != "13.55" || payload.Codex.Enabled != 5 {
		t.Fatalf("unexpected cached codex payload: %+v", payload.Codex)
	}
	if len(payload.CodexRows) != 1 || payload.CodexRows[0].Account != "admin10010" || payload.CodexRows[0].Status != "关注" {
		t.Fatalf("unexpected cached codex rows: %+v", payload.CodexRows)
	}
}

func mustSaveSnapshot(t *testing.T, dataStore store.Store, source string, items []model.DataItem) {
	t.Helper()
	if err := dataStore.Save(source, items); err != nil {
		t.Fatalf("save failed: %v", err)
	}
}
