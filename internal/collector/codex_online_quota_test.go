package collector

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"infohub/internal/config"
)

func TestCodexOnlineQuotaClientHappyPath(t *testing.T) {
	var hitCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		if r.URL.Path != "/backend-api/wham/usage" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "account-123" {
			t.Fatalf("unexpected account header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"rate_limits": {
				"primary": {"used_percent": 32.5, "window_minutes": 300, "resets_at": 1777215600},
				"secondary": {"used_percent": 78, "window_minutes": 10080, "resets_at": "2026-04-27T00:00:00Z"}
			}
		}`))
	}))
	defer server.Close()

	client := newTestCodexOnlineQuotaClient(t, server.URL, writeTestCodexAuth(t, t.TempDir()))
	limits, ok, err := client.FetchRateLimits(context.Background())
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if !ok {
		t.Fatal("expected rate limits")
	}
	if limits.FiveHour.UsedPercent != 32.5 {
		t.Fatalf("unexpected 5H used percent: %v", limits.FiveHour.UsedPercent)
	}
	if limits.Week.UsedPercent != 78 {
		t.Fatalf("unexpected weekly used percent: %v", limits.Week.UsedPercent)
	}
	if got := atomic.LoadInt32(&hitCount); got != 1 {
		t.Fatalf("unexpected hit count: %d", got)
	}
}

func TestCodexOnlineQuotaClientWhamSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"user_id": "user-x",
			"plan_type": "plus",
			"rate_limit": {
				"allowed": true,
				"limit_reached": false,
				"primary_window": {
					"used_percent": 34,
					"limit_window_seconds": 18000,
					"reset_after_seconds": 15757,
					"reset_at": 1777238471
				},
				"secondary_window": {
					"used_percent": 82,
					"limit_window_seconds": 604800,
					"reset_after_seconds": 186826,
					"reset_at": 1777409541
				}
			},
			"credits": {"has_credits": false}
		}`))
	}))
	defer server.Close()

	client := newTestCodexOnlineQuotaClient(t, server.URL, writeTestCodexAuth(t, t.TempDir()))
	limits, ok, err := client.FetchRateLimits(context.Background())
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if !ok {
		t.Fatal("expected rate limits parsed from wham schema")
	}
	if limits.FiveHour.UsedPercent != 34 {
		t.Fatalf("unexpected 5H used percent: %v", limits.FiveHour.UsedPercent)
	}
	if limits.Week.UsedPercent != 82 {
		t.Fatalf("unexpected weekly used percent: %v", limits.Week.UsedPercent)
	}
	if limits.FiveHour.ResetAt == "" {
		t.Fatal("expected 5H reset_at to be parsed from wham schema")
	}
	if limits.Week.ResetAt == "" {
		t.Fatal("expected weekly reset_at to be parsed from wham schema")
	}
}

func TestCodexOnlineQuotaClientPathFallback(t *testing.T) {
	var firstPathHits int32
	var fallbackHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/wham/usage":
			atomic.AddInt32(&firstPathHits, 1)
			http.NotFound(w, r)
		case "/backend-api/wham/api/codex/usage":
			atomic.AddInt32(&fallbackHits, 1)
			_, _ = w.Write([]byte(`{"rate_limits":{"primary":{"used_percent":10},"secondary":{"used_percent":20}}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestCodexOnlineQuotaClient(t, server.URL, writeTestCodexAuth(t, t.TempDir()))
	client.staleAfter = 0

	if _, ok, err := client.FetchRateLimits(context.Background()); err != nil || !ok {
		t.Fatalf("first fetch ok=%v err=%v", ok, err)
	}
	if _, ok, err := client.FetchRateLimits(context.Background()); err != nil || !ok {
		t.Fatalf("second fetch ok=%v err=%v", ok, err)
	}
	if got := atomic.LoadInt32(&firstPathHits); got != 1 {
		t.Fatalf("expected first path to be skipped after fallback success, got %d hits", got)
	}
	if got := atomic.LoadInt32(&fallbackHits); got != 2 {
		t.Fatalf("unexpected fallback hit count: %d", got)
	}
	if client.successPath != "/backend-api/wham/api/codex/usage" {
		t.Fatalf("unexpected success path: %q", client.successPath)
	}
}

func TestCodexOnlineQuotaClientUnauthorized(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewCodexOnlineQuotaClient(config.LocalCodexOnlineConfig{
		AuthPath:       writeTestCodexAuth(t, t.TempDir()),
		BaseURL:        server.URL,
		TimeoutSeconds: 1,
		StaleAfterSec:  60,
	}, logger)

	_, ok, err := client.FetchRateLimits(context.Background())
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if ok {
		t.Fatal("expected unauthorized fetch to return ok=false")
	}
	if got := client.LastStatus(); got != codexOnlineQuotaStatusUnauthorized {
		t.Fatalf("unexpected status: %s", got)
	}
	if !strings.Contains(logs.String(), "last_refresh=2026-04-26T10:00:00Z") {
		t.Fatalf("expected last_refresh in logs, got: %s", logs.String())
	}
	if strings.Contains(logs.String(), "access-token") || strings.Contains(logs.String(), "account-123") {
		t.Fatalf("logs leaked credentials: %s", logs.String())
	}
}

func TestCodexOnlineQuotaClientRateLimitShortCircuit(t *testing.T) {
	var hitCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := newTestCodexOnlineQuotaClient(t, server.URL, writeTestCodexAuth(t, t.TempDir()))
	client.staleAfter = 0

	for i := 0; i < 2; i++ {
		if _, ok, err := client.FetchRateLimits(context.Background()); err != nil || ok {
			t.Fatalf("fetch %d ok=%v err=%v", i, ok, err)
		}
	}
	if got := atomic.LoadInt32(&hitCount); got != 1 {
		t.Fatalf("expected short circuit to avoid second request, got %d hits", got)
	}
}

func TestCodexOnlineQuotaClientCacheHit(t *testing.T) {
	var hitCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		_, _ = w.Write([]byte(`{"rate_limits":{"primary":{"used_percent":11},"secondary":{"used_percent":22}}}`))
	}))
	defer server.Close()

	client := newTestCodexOnlineQuotaClient(t, server.URL, writeTestCodexAuth(t, t.TempDir()))
	for i := 0; i < 3; i++ {
		if _, ok, err := client.FetchRateLimits(context.Background()); err != nil || !ok {
			t.Fatalf("fetch %d ok=%v err=%v", i, ok, err)
		}
	}
	if got := atomic.LoadInt32(&hitCount); got != 1 {
		t.Fatalf("expected cache hit, got %d requests", got)
	}
}

func TestCodexOnlineQuotaClientMissingAuth(t *testing.T) {
	var hitCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
	}))
	defer server.Close()

	client := newTestCodexOnlineQuotaClient(t, server.URL, filepath.Join(t.TempDir(), "missing-auth.json"))
	if _, ok, err := client.FetchRateLimits(context.Background()); err != nil || ok {
		t.Fatalf("fetch ok=%v err=%v", ok, err)
	}
	if got := atomic.LoadInt32(&hitCount); got != 0 {
		t.Fatalf("expected no request without auth, got %d", got)
	}
	if got := client.LastStatus(); got != codexOnlineQuotaStatusTokenMissing {
		t.Fatalf("unexpected status: %s", got)
	}
}

func TestCodexOnlineQuotaClientMissingTokens(t *testing.T) {
	var hitCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
	}))
	defer server.Close()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write auth failed: %v", err)
	}

	client := newTestCodexOnlineQuotaClient(t, server.URL, authPath)
	if _, ok, err := client.FetchRateLimits(context.Background()); err != nil || ok {
		t.Fatalf("fetch ok=%v err=%v", ok, err)
	}
	if got := atomic.LoadInt32(&hitCount); got != 0 {
		t.Fatalf("expected no request without tokens, got %d", got)
	}
	if got := client.LastStatus(); got != codexOnlineQuotaStatusTokenMissing {
		t.Fatalf("unexpected status: %s", got)
	}
}

func newTestCodexOnlineQuotaClient(t *testing.T, baseURL string, authPath string) *CodexOnlineQuotaClient {
	t.Helper()
	return NewCodexOnlineQuotaClient(config.LocalCodexOnlineConfig{
		AuthPath:       authPath,
		BaseURL:        baseURL,
		TimeoutSeconds: 1,
		StaleAfterSec:  60,
	}, nil)
}

func writeTestCodexAuth(t *testing.T, dir string) string {
	t.Helper()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{
		"auth_mode": "chatgpt",
		"tokens": {
			"access_token": "access-token",
			"id_token": "id-token",
			"refresh_token": "refresh-token",
			"account_id": "account-123"
		},
		"last_refresh": "2026-04-26T10:00:00Z"
	}`), 0o600); err != nil {
		t.Fatalf("write auth failed: %v", err)
	}
	return authPath
}
