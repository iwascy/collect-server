package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLegacyHTTPCollectorCompatibility(t *testing.T) {
	configPath := writeTempConfig(t, `
collectors:
  claude_relay:
    enabled: true
    cron: "*/10 * * * *"
    base_url: "https://legacy.example.com"
    endpoint: "/api/v1/summary"
    api_key: "legacy-token"
    headers:
      X-Test: "legacy"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	collector := cfg.Collectors.ClaudeRelay
	if collector.Service.BaseURL != "https://legacy.example.com" {
		t.Fatalf("unexpected service base url: %q", collector.Service.BaseURL)
	}
	if got := collector.Service.Endpoints["default"]; got != "/api/v1/summary" {
		t.Fatalf("unexpected default endpoint: %q", got)
	}
	if got := collector.Service.Headers["X-Test"]; got != "legacy" {
		t.Fatalf("unexpected service header: %q", got)
	}
	if collector.Auth.Type != "bearer" {
		t.Fatalf("unexpected auth type: %q", collector.Auth.Type)
	}
	if collector.Auth.Token != "legacy-token" {
		t.Fatalf("unexpected auth token: %q", collector.Auth.Token)
	}
	if collector.Auth.HeaderName != "Authorization" {
		t.Fatalf("unexpected auth header name: %q", collector.Auth.HeaderName)
	}
	if collector.Auth.TokenPrefix != "Bearer " {
		t.Fatalf("unexpected auth token prefix: %q", collector.Auth.TokenPrefix)
	}
}

func TestLoadExpandsEnvVariables(t *testing.T) {
	t.Setenv("TEST_BASE_URL", "http://10.20.0.21:3002")
	t.Setenv("TEST_USERNAME", "relay-admin")
	t.Setenv("TEST_PASSWORD", "relay-pass")

	configPath := writeTempConfig(t, `
collectors:
  claude_relay:
    service:
      base_url: "${TEST_BASE_URL}"
      endpoints:
        accounts: "/admin/claude-accounts"
    auth:
      type: "login_json"
      header_name: "Authorization"
      token_prefix: "Bearer"
      login_endpoint: "/web/auth/login"
      method: "POST"
      token_path: "token"
      credentials:
        username: "${TEST_USERNAME}"
        password: "${TEST_PASSWORD}"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	collector := cfg.Collectors.ClaudeRelay
	if collector.Service.BaseURL != "http://10.20.0.21:3002" {
		t.Fatalf("unexpected service base url: %q", collector.Service.BaseURL)
	}
	if collector.Auth.Credentials["username"] != "relay-admin" {
		t.Fatalf("unexpected username: %q", collector.Auth.Credentials["username"])
	}
	if collector.Auth.Credentials["password"] != "relay-pass" {
		t.Fatalf("unexpected password: %q", collector.Auth.Credentials["password"])
	}
}

func TestLoadReadsDotEnvFromConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	envPath := filepath.Join(dir, ".env")

	if err := os.WriteFile(envPath, []byte("DOTENV_BASE_URL=http://10.20.0.21:3002\nDOTENV_AUTH_TOKEN=local-token\n"), 0o600); err != nil {
		t.Fatalf("write .env failed: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`
server:
  auth_token: "${DOTENV_AUTH_TOKEN}"
collectors:
  claude_relay:
    service:
      base_url: "${DOTENV_BASE_URL}"
      endpoints:
        accounts: "/admin/claude-accounts"
`), 0o600); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	os.Unsetenv("DOTENV_BASE_URL")
	os.Unsetenv("DOTENV_AUTH_TOKEN")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	if cfg.Server.AuthToken != "local-token" {
		t.Fatalf("unexpected auth token: %q", cfg.Server.AuthToken)
	}
	if cfg.Collectors.ClaudeRelay.Service.BaseURL != "http://10.20.0.21:3002" {
		t.Fatalf("unexpected service base url: %q", cfg.Collectors.ClaudeRelay.Service.BaseURL)
	}
}

func TestLoadDefaultsStoreToSQLite(t *testing.T) {
	configPath := writeTempConfig(t, `
collectors:
  claude_relay:
    enabled: false
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	if cfg.Store.Type != "sqlite" {
		t.Fatalf("unexpected store type: %q", cfg.Store.Type)
	}
	if cfg.Store.SQLitePath != "./data/infohub.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.Store.SQLitePath)
	}
}

func TestLoadDefaultsCodexOnlineDisabled(t *testing.T) {
	configPath := writeTempConfig(t, `
collectors:
  codex_local:
    enabled: true
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	online := cfg.Collectors.CodexLocal.Online
	if online.Enabled {
		t.Fatal("codex online quota should default to disabled")
	}
	if online.BaseURL != "https://chatgpt.com" {
		t.Fatalf("unexpected base url: %q", online.BaseURL)
	}
	if online.TimeoutSeconds != 8 {
		t.Fatalf("unexpected timeout: %d", online.TimeoutSeconds)
	}
	if online.StaleAfterSec != 60 {
		t.Fatalf("unexpected stale_after_seconds: %d", online.StaleAfterSec)
	}
	if online.UserAgent != "infohub-codex-quota/1.0" {
		t.Fatalf("unexpected user agent: %q", online.UserAgent)
	}
	if online.AuthPath == "" {
		t.Fatal("expected default auth path")
	}
}

func TestLoadDefaultsClaudeLocalPaths(t *testing.T) {
	configPath := writeTempConfig(t, `
collectors:
  claude_local:
    enabled: true
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	collector := cfg.Collectors.ClaudeLocal
	if len(collector.Paths) != 2 {
		t.Fatalf("unexpected claude local paths: %#v", collector.Paths)
	}
	if collector.Paths[0] != "${HOME}/.config/claude/projects" {
		t.Fatalf("unexpected first claude path: %q", collector.Paths[0])
	}
	if collector.Paths[1] != "${HOME}/.claude/projects" {
		t.Fatalf("unexpected second claude path: %q", collector.Paths[1])
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config failed: %v", err)
	}
	return path
}
