package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"infohub/internal/config"
)

func TestClaudeOnlineQuotaClientHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/usage" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer claude-token" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Fatalf("unexpected beta header: %q", got)
		}
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 12.5, "resets_at": "2026-04-26T13:00:00Z"},
			"seven_day": {"utilization": 45, "resets_at": "2026-04-28T00:00:00Z"},
			"extra_usage": {"is_enabled": false}
		}`))
	}))
	defer server.Close()

	client := NewClaudeOnlineQuotaClient(config.LocalCodexOnlineConfig{
		AuthPath:       writeTestClaudeCredentials(t, t.TempDir()),
		BaseURL:        server.URL,
		TimeoutSeconds: 1,
		StaleAfterSec:  60,
	}, nil)
	client.keychainReader = nil

	limits, ok, err := client.FetchRateLimits(context.Background())
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if !ok {
		t.Fatal("expected rate limits")
	}
	if limits.FiveHour.UsedPercent != 12.5 {
		t.Fatalf("unexpected 5H used percent: %v", limits.FiveHour.UsedPercent)
	}
	if limits.Week.UsedPercent != 45 {
		t.Fatalf("unexpected weekly used percent: %v", limits.Week.UsedPercent)
	}
	if limits.FiveHour.ResetAt != "2026-04-26T13:00:00Z" {
		t.Fatalf("unexpected 5H reset_at: %s", limits.FiveHour.ResetAt)
	}
}

func TestClaudeOnlineQuotaClientCredentialsKeyVariants(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(path, []byte(`{
		"claude.ai_oauth": {"accessToken": "dot-token"}
	}`), 0o600); err != nil {
		t.Fatalf("write credentials failed: %v", err)
	}

	token, ok, err := readClaudeAccessTokenFromFile(path)
	if err != nil {
		t.Fatalf("read token failed: %v", err)
	}
	if !ok || token != "dot-token" {
		t.Fatalf("unexpected token ok=%v token=%q", ok, token)
	}
}

func writeTestClaudeCredentials(t *testing.T, dir string) string {
	t.Helper()
	authPath := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(authPath, []byte(`{
		"claudeAiOauth": {
			"accessToken": "claude-token",
			"expiresAt": "2099-01-01T00:00:00Z"
		}
	}`), 0o600); err != nil {
		t.Fatalf("write credentials failed: %v", err)
	}
	return authPath
}
