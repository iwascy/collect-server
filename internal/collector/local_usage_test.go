package collector

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"infohub/internal/config"
	"infohub/internal/store"
)

func TestClaudeLocalCollectorCollectsBuiltinJSONL(t *testing.T) {
	dir := t.TempDir()
	writeJSONLLines(t, filepath.Join(dir, "session.jsonl"),
		map[string]any{
			"type":      "assistant",
			"timestamp": "2026-04-26T10:00:00Z",
			"message": map[string]any{
				"model": "claude-sonnet-4-6",
				"usage": map[string]any{
					"input_tokens":                100,
					"output_tokens":               50,
					"cache_read_input_tokens":     25,
					"cache_creation_input_tokens": 10,
				},
			},
		},
		map[string]any{
			"type":      "summary",
			"timestamp": "2026-04-26T10:05:00Z",
		},
		map[string]any{
			"type":      "user",
			"timestamp": "2026-04-26T11:00:00Z",
			"message": map[string]any{
				"model": "claude-opus-4-7",
				"usage": map[string]any{
					"input_tokens":  20,
					"output_tokens": 5,
				},
			},
		},
	)

	collector := NewClaudeLocalCollector(config.LocalCollectorConfig{
		Paths: []string{dir},
		Quota: config.LocalQuotaConfig{
			Plan:        "max-200",
			FiveHourCap: 10,
		},
	}, nil)
	collector.now = func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) }

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	tokenItem := mustFindItem(t, items, "今日 Token 用量")
	if tokenItem.Value != "210" {
		t.Fatalf("unexpected token total: %s", tokenItem.Value)
	}
	if got := tokenItem.Extra["cache_read"]; got != 25.0 {
		t.Fatalf("unexpected cache read: %v", got)
	}

	quotaItem := mustFindItem(t, items, "账号 Claude Local 5H 额度")
	if quotaItem.Value != "80%" {
		t.Fatalf("unexpected 5H quota value: %s", quotaItem.Value)
	}

	cacheItem := mustFindItem(t, items, "cache_hit")
	if cacheItem.Value != "17.24%" {
		t.Fatalf("unexpected cache hit: %s", cacheItem.Value)
	}
}

func TestCodexLocalCollectorCollectsBuiltinJSONL(t *testing.T) {
	dir := t.TempDir()
	writeJSONLLines(t, filepath.Join(dir, "rollout-1.jsonl"),
		map[string]any{
			"created_at": 1777200000,
			"payload": map[string]any{
				"model": "gpt-5-codex",
				"usage": map[string]any{
					"input_tokens":     1000,
					"output_tokens":    200,
					"reasoning_tokens": 50,
				},
			},
		},
		map[string]any{
			"created_at": 1777203600,
			"response": map[string]any{
				"model": "gpt-5.1-codex",
				"usage": map[string]any{
					"input_tokens":     500,
					"output_tokens":    100,
					"reasoning_tokens": 100,
				},
			},
		},
		map[string]any{
			"timestamp": "2026-04-26T11:30:00Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{
					"last_token_usage": map[string]any{
						"input_tokens":            300,
						"output_tokens":           30,
						"reasoning_output_tokens": 20,
						"total_tokens":            350,
					},
				},
				"rate_limits": map[string]any{
					"primary": map[string]any{
						"used_percent":   32.5,
						"window_minutes": 300,
						"resets_at":      1777215600,
					},
					"secondary": map[string]any{
						"used_percent":   25,
						"window_minutes": 10080,
						"resets_at":      1777392000,
					},
				},
			},
		},
	)

	collector := NewCodexLocalCollector(config.LocalCollectorConfig{
		Paths: []string{dir},
		Quota: config.LocalQuotaConfig{
			Plan:      "pro",
			WeeklyCap: 10,
		},
	}, nil)
	collector.now = func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) }

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	tokenItem := mustFindItem(t, items, "今日 Token 用量")
	if tokenItem.Value != "2300" {
		t.Fatalf("unexpected token total: %s", tokenItem.Value)
	}
	if got := tokenItem.Extra["reasoning"]; got != 170.0 {
		t.Fatalf("unexpected reasoning total: %v", got)
	}

	fiveHourQuotaItem := mustFindItem(t, items, "账号 Codex Local 5H 额度")
	if fiveHourQuotaItem.Value != "67.50%" {
		t.Fatalf("unexpected 5H quota value: %s", fiveHourQuotaItem.Value)
	}
	if got := fiveHourQuotaItem.Extra["quota_source"]; got != "codex_rate_limits" {
		t.Fatalf("unexpected 5H quota source: %v", got)
	}

	quotaItem := mustFindItem(t, items, "账号 Codex Local Week 额度")
	if quotaItem.Value != "75%" {
		t.Fatalf("unexpected weekly quota value: %s", quotaItem.Value)
	}

	reasoningItem := mustFindItem(t, items, "reasoning_share")
	if reasoningItem.Value != "51.52%" {
		t.Fatalf("unexpected reasoning share: %s", reasoningItem.Value)
	}
}

func TestClaudeLocalCollectorReadsStatusLineRateLimits(t *testing.T) {
	dir := t.TempDir()
	writeJSONLLines(t, filepath.Join(dir, "session.jsonl"),
		claudeUsageRecord("2026-04-26T11:00:00Z", "claude-sonnet-4-6", 100, 50),
	)

	rateLimitPath := filepath.Join(t.TempDir(), "infohub-rate-limits.json")
	if err := os.WriteFile(rateLimitPath, []byte(`{
		"session_id": "abc",
		"rate_limits": {
			"five_hour": {
				"used_percentage": 42.5,
				"resets_at": "2026-04-26T13:00:00Z"
			},
			"seven_day": {
				"used_percentage": 70,
				"resets_at": "2026-04-28T00:00:00Z"
			}
		}
	}`), 0o600); err != nil {
		t.Fatalf("write rate limits failed: %v", err)
	}

	collector := NewClaudeLocalCollector(config.LocalCollectorConfig{
		Paths:          []string{dir},
		RateLimitPaths: []string{rateLimitPath},
		Quota: config.LocalQuotaConfig{
			FiveHourCap: 10,
			WeeklyCap:   100,
		},
	}, nil)
	collector.now = func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) }

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	fiveHourQuotaItem := mustFindItem(t, items, "账号 Claude Local 5H 额度")
	if fiveHourQuotaItem.Value != "57.50%" {
		t.Fatalf("unexpected 5H quota value: %s", fiveHourQuotaItem.Value)
	}
	if got := fiveHourQuotaItem.Extra["quota_source"]; got != "claude_statusline" {
		t.Fatalf("unexpected 5H quota source: %v", got)
	}
	if got := fiveHourQuotaItem.Extra["reset_at"]; got != "2026-04-26T13:00:00Z" {
		t.Fatalf("unexpected 5H reset_at: %v", got)
	}

	weeklyQuotaItem := mustFindItem(t, items, "账号 Claude Local Week 额度")
	if weeklyQuotaItem.Value != "30%" {
		t.Fatalf("unexpected weekly quota value: %s", weeklyQuotaItem.Value)
	}
	if got := weeklyQuotaItem.Extra["quota_source"]; got != "claude_statusline" {
		t.Fatalf("unexpected weekly quota source: %v", got)
	}
}

func TestCodexLocalCollectorOnlineQuotaFallback(t *testing.T) {
	dir := t.TempDir()
	writeJSONLLines(t, filepath.Join(dir, "rollout-1.jsonl"),
		map[string]any{
			"timestamp": "2026-04-26T11:30:00Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{
					"last_token_usage": map[string]any{
						"input_tokens":  100,
						"output_tokens": 20,
						"total_tokens":  120,
					},
				},
				"rate_limits": map[string]any{
					"primary":   nil,
					"secondary": nil,
				},
			},
		},
	)

	collector := NewCodexLocalCollector(config.LocalCollectorConfig{
		Paths: []string{dir},
		Quota: config.LocalQuotaConfig{WeeklyCap: 10},
	}, nil)
	collector.now = func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) }
	collector.onlineCodexQuota = &fakeCodexOnlineQuotaFetcher{
		status: codexOnlineQuotaStatusOK,
		ok:     true,
		limits: localRateLimits{
			FiveHour: localQuotaObservation{OK: true, UsedPercent: 10},
			Week:     localQuotaObservation{OK: true, UsedPercent: 78},
		},
	}

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	quotaItem := mustFindItem(t, items, "账号 Codex Local Week 额度")
	if quotaItem.Value != "22%" {
		t.Fatalf("unexpected weekly quota value: %s", quotaItem.Value)
	}
	if got := quotaItem.Extra["quota_source"]; got != "codex_wham_usage" {
		t.Fatalf("unexpected quota source: %v", got)
	}
	if got := quotaItem.Extra["online_quota_status"]; got != "ok" {
		t.Fatalf("unexpected online quota status: %v", got)
	}
}

func TestCodexLocalCollectorOnlinePrefersRolloutWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	writeJSONLLines(t, filepath.Join(dir, "rollout-1.jsonl"),
		map[string]any{
			"timestamp": "2026-04-26T11:30:00Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{
					"last_token_usage": map[string]any{
						"input_tokens":  100,
						"output_tokens": 20,
						"total_tokens":  120,
					},
				},
				"rate_limits": map[string]any{
					"primary": map[string]any{
						"used_percent": 40,
					},
					"secondary": nil,
				},
			},
		},
	)

	collector := NewCodexLocalCollector(config.LocalCollectorConfig{
		Paths: []string{dir},
		Quota: config.LocalQuotaConfig{WeeklyCap: 10},
	}, nil)
	collector.now = func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) }
	collector.onlineCodexQuota = &fakeCodexOnlineQuotaFetcher{
		status: codexOnlineQuotaStatusOK,
		ok:     true,
		limits: localRateLimits{
			FiveHour: localQuotaObservation{OK: true, UsedPercent: 10},
			Week:     localQuotaObservation{OK: true, UsedPercent: 78},
		},
	}

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	fiveHourQuotaItem := mustFindItem(t, items, "账号 Codex Local 5H 额度")
	if fiveHourQuotaItem.Value != "60%" {
		t.Fatalf("unexpected 5H quota value: %s", fiveHourQuotaItem.Value)
	}
	if got := fiveHourQuotaItem.Extra["quota_source"]; got != "codex_rate_limits" {
		t.Fatalf("unexpected 5H quota source: %v", got)
	}

	weeklyQuotaItem := mustFindItem(t, items, "账号 Codex Local Week 额度")
	if weeklyQuotaItem.Value != "22%" {
		t.Fatalf("unexpected weekly quota value: %s", weeklyQuotaItem.Value)
	}
	if got := weeklyQuotaItem.Extra["quota_source"]; got != "codex_wham_usage" {
		t.Fatalf("unexpected weekly quota source: %v", got)
	}
}

func TestLocalCollectorMissingPath(t *testing.T) {
	collector := NewClaudeLocalCollector(config.LocalCollectorConfig{
		Paths: []string{filepath.Join(t.TempDir(), "missing")},
	}, nil)

	if _, err := collector.Collect(context.Background()); err == nil {
		t.Fatal("expected missing path error")
	}
}

func TestLocalCollectorIncrementalSQLiteKeepsPriorEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONLLines(t, path, claudeUsageRecord("2026-04-26T10:00:00Z", "claude-sonnet-4-6", 100, 50))

	dataStore, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "infohub.db"))
	if err != nil {
		t.Fatalf("create sqlite store failed: %v", err)
	}
	defer dataStore.Close()

	collector := NewClaudeLocalCollector(config.LocalCollectorConfig{
		Paths: []string{dir},
	}, nil)
	collector.SetStore(dataStore)
	collector.now = func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) }

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("initial collect failed: %v", err)
	}
	if got := mustFindItem(t, items, "今日 Token 用量").Value; got != "150" {
		t.Fatalf("unexpected initial total: %s", got)
	}

	appendJSONLLines(t, path, claudeUsageRecord("2026-04-26T11:00:00Z", "claude-opus-4-7", 20, 10))
	items, err = collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("incremental collect failed: %v", err)
	}
	if got := mustFindItem(t, items, "今日 Token 用量").Value; got != "180" {
		t.Fatalf("incremental collect lost prior events: %s", got)
	}
}

func TestLocalCollectorIncrementalSQLiteResetsTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONLLines(t, path,
		claudeUsageRecord("2026-04-26T10:00:00Z", "claude-sonnet-4-6", 100, 50),
		claudeUsageRecord("2026-04-26T11:00:00Z", "claude-opus-4-7", 20, 10),
	)

	dataStore, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "infohub.db"))
	if err != nil {
		t.Fatalf("create sqlite store failed: %v", err)
	}
	defer dataStore.Close()

	collector := NewClaudeLocalCollector(config.LocalCollectorConfig{
		Paths: []string{dir},
	}, nil)
	collector.SetStore(dataStore)
	collector.now = func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) }

	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatalf("initial collect failed: %v", err)
	}

	writeJSONLLines(t, path, claudeUsageRecord("2026-04-26T11:30:00Z", "claude-haiku-4-5", 7, 3))
	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("truncated collect failed: %v", err)
	}
	if got := mustFindItem(t, items, "今日 Token 用量").Value; got != "10" {
		t.Fatalf("truncated file should reset old records, got: %s", got)
	}
}

func TestParseCCUsageEvents(t *testing.T) {
	payload := []byte(`{
		"daily": [
			{
				"date": "2026-04-26",
				"model": "claude-sonnet-4-6",
				"inputTokens": 100,
				"outputTokens": 50,
				"cacheReadInputTokens": 25,
				"cacheCreationInputTokens": 10
			}
		]
	}`)

	events, err := parseCCUsageEvents(payload, time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("parse ccusage failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("unexpected event count: %d", len(events))
	}
	if got := events[0].TotalTokens(); got != 185 {
		t.Fatalf("unexpected ccusage token total: %v", got)
	}
}

func claudeUsageRecord(timestamp string, model string, input int, output int) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"timestamp": timestamp,
		"message": map[string]any{
			"model": model,
			"usage": map[string]any{
				"input_tokens":  input,
				"output_tokens": output,
			},
		},
	}
}

func writeJSONLLines(t *testing.T, path string, records ...map[string]any) {
	t.Helper()

	var payload []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal jsonl record failed: %v", err)
		}
		payload = append(payload, line...)
		payload = append(payload, '\n')
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write jsonl failed: %v", err)
	}
}

func appendJSONLLines(t *testing.T, path string, records ...map[string]any) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open jsonl for append failed: %v", err)
	}
	defer file.Close()

	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal jsonl record failed: %v", err)
		}
		if _, err := file.Write(append(line, '\n')); err != nil {
			t.Fatalf("append jsonl failed: %v", err)
		}
	}
}

type fakeCodexOnlineQuotaFetcher struct {
	limits localRateLimits
	ok     bool
	err    error
	status string
}

func (f *fakeCodexOnlineQuotaFetcher) FetchRateLimits(context.Context) (localRateLimits, bool, error) {
	return f.limits, f.ok, f.err
}

func (f *fakeCodexOnlineQuotaFetcher) LastStatus() string {
	if f.status == "" {
		return codexOnlineQuotaStatusOK
	}
	return f.status
}
