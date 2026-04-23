package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"infohub/internal/config"
	"infohub/internal/model"
)

func TestClaudeRelayCollectorCollect(t *testing.T) {
	var loginCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/web/auth/login":
			loginCalls.Add(1)
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected login method: %s", r.Method)
			}

			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode login body failed: %v", err)
			}
			if payload["username"] != "relay-admin" || payload["password"] != "relay-pass" {
				t.Fatalf("unexpected login payload: %#v", payload)
			}

			writeJSON(t, w, map[string]any{"token": "relay-session"})
		case "/admin/claude-accounts":
			expectAuthHeader(t, r, "Bearer relay-session")
			writeJSON(t, w, map[string]any{
				"success": true,
				"data": []map[string]any{
					{
						"id":          "acct-1",
						"name":        "alpha",
						"isActive":    true,
						"status":      "active",
						"schedulable": true,
						"usage": map[string]any{
							"daily": map[string]any{
								"allTokens": 1200,
								"tokens":    900,
								"requests":  12,
								"cost":      1.25,
							},
						},
						"claudeUsage": map[string]any{
							"fiveHour": map[string]any{
								"utilization": 88,
								"resetsAt":    "2026-04-20T10:00:00Z",
							},
							"sevenDay": map[string]any{
								"utilization": 66,
								"resetsAt":    "2026-04-25T00:00:00Z",
							},
						},
					},
					{
						"id":          "acct-2",
						"name":        "beta",
						"isActive":    true,
						"status":      "active",
						"schedulable": true,
						"usage": map[string]any{
							"daily": map[string]any{
								"allTokens": 800,
								"tokens":    500,
								"requests":  8,
								"cost":      0.75,
							},
						},
						"claudeUsage": map[string]any{
							"fiveHour": map[string]any{"utilization": 77},
							"sevenDay": map[string]any{"utilization": 44},
						},
					},
					{
						"id":          "acct-3",
						"name":        "disabled",
						"isActive":    false,
						"status":      "inactive",
						"schedulable": false,
						"usage": map[string]any{
							"daily": map[string]any{
								"allTokens": 9999,
								"tokens":    9999,
							},
						},
					},
				},
			})
		case "/admin/claude-accounts/usage":
			expectAuthHeader(t, r, "Bearer relay-session")
			writeJSON(t, w, map[string]any{
				"success": true,
				"data": map[string]any{
					"acct-1": map[string]any{
						"fiveHour": map[string]any{
							"utilization":      25,
							"resetsAt":         "2026-04-20T11:00:00Z",
							"remainingSeconds": 7200,
						},
						"sevenDay": map[string]any{
							"utilization":      40,
							"resetsAt":         "2026-04-27T00:00:00Z",
							"remainingSeconds": 86400,
						},
					},
					"acct-2": map[string]any{
						"fiveHour": map[string]any{"utilization": 10},
						"sevenDay": map[string]any{"utilization": 20},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	collector := NewClaudeRelayCollector(config.HTTPCollectorConfig{
		TimeoutSeconds: 2,
		Service: config.HTTPServiceConfig{
			BaseURL: server.URL,
			Endpoints: map[string]string{
				"accounts": "/admin/claude-accounts",
				"usage":    "/admin/claude-accounts/usage",
			},
		},
		Auth: config.HTTPAuthConfig{
			Type:          "login_json",
			HeaderName:    "Authorization",
			TokenPrefix:   "Bearer",
			LoginEndpoint: "/web/auth/login",
			Method:        http.MethodPost,
			TokenPath:     "token",
			Credentials: map[string]string{
				"username": "relay-admin",
				"password": "relay-pass",
			},
		},
	}, nil)

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}
	if loginCalls.Load() != 1 {
		t.Fatalf("expected exactly one login call, got %d", loginCalls.Load())
	}
	if len(items) != 5 {
		t.Fatalf("unexpected item count: %d", len(items))
	}

	tokenItem := mustFindItem(t, items, "今日 Token 用量")
	if tokenItem.Value != "2000" {
		t.Fatalf("unexpected token value: %s", tokenItem.Value)
	}
	if got := tokenItem.Extra["enabled_accounts"]; got != 2 {
		t.Fatalf("unexpected enabled account count: %v", got)
	}
	if got := tokenItem.Extra["daily_tokens"]; got != 1400.0 {
		t.Fatalf("unexpected daily tokens extra: %v", got)
	}
	if got := tokenItem.Extra["daily_requests"]; got != 20.0 {
		t.Fatalf("unexpected daily requests extra: %v", got)
	}

	if got := mustFindItem(t, items, "账号 alpha 5H 额度").Value; got != "75%" {
		t.Fatalf("unexpected alpha 5H value: %s", got)
	}
	if got := mustFindItem(t, items, "账号 alpha Week 额度").Value; got != "60%" {
		t.Fatalf("unexpected alpha week value: %s", got)
	}
	if got := mustFindItem(t, items, "账号 beta 5H 额度").Value; got != "90%" {
		t.Fatalf("unexpected beta 5H value: %s", got)
	}
	if got := mustFindItem(t, items, "账号 beta Week 额度").Value; got != "80%" {
		t.Fatalf("unexpected beta week value: %s", got)
	}
}

func TestClaudeRelayCollectorReusesCachedLoginToken(t *testing.T) {
	var loginCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/web/auth/login":
			loginCalls.Add(1)
			writeJSON(t, w, map[string]any{"token": "relay-session"})
		case "/admin/claude-accounts":
			expectAuthHeader(t, r, "Bearer relay-session")
			writeJSON(t, w, map[string]any{
				"data": []map[string]any{
					{
						"id":       "acct-1",
						"name":     "alpha",
						"isActive": true,
						"status":   "active",
						"usage": map[string]any{
							"daily": map[string]any{
								"allTokens": 100,
							},
						},
					},
				},
			})
		case "/admin/claude-accounts/usage":
			expectAuthHeader(t, r, "Bearer relay-session")
			writeJSON(t, w, map[string]any{
				"data": map[string]any{
					"acct-1": map[string]any{
						"fiveHour": map[string]any{"utilization": 10},
						"sevenDay": map[string]any{"utilization": 20},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	collector := NewClaudeRelayCollector(config.HTTPCollectorConfig{
		TimeoutSeconds: 2,
		Service: config.HTTPServiceConfig{
			BaseURL: server.URL,
			Endpoints: map[string]string{
				"accounts": "/admin/claude-accounts",
				"usage":    "/admin/claude-accounts/usage",
			},
		},
		Auth: config.HTTPAuthConfig{
			Type:          "login_json",
			HeaderName:    "Authorization",
			TokenPrefix:   "Bearer",
			LoginEndpoint: "/web/auth/login",
			Method:        http.MethodPost,
			TokenPath:     "token",
			Credentials: map[string]string{
				"username": "relay-admin",
				"password": "relay-pass",
			},
		},
	}, nil)

	for range [2]struct{}{} {
		items, err := collector.Collect(context.Background())
		if err != nil {
			t.Fatalf("collect failed: %v", err)
		}
		if len(items) != 3 {
			t.Fatalf("unexpected item count: %d", len(items))
		}
	}

	if loginCalls.Load() != 1 {
		t.Fatalf("expected cached token to be reused, got %d login calls", loginCalls.Load())
	}
}

func TestClaudeRelayCollectorRefreshesCachedTokenAfterUnauthorized(t *testing.T) {
	var loginCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/web/auth/login":
			call := loginCalls.Add(1)
			writeJSON(t, w, map[string]any{"token": fmt.Sprintf("relay-session-%d", call)})
		case "/admin/claude-accounts", "/admin/claude-accounts/usage":
			auth := r.Header.Get("Authorization")
			if auth == "Bearer relay-session-1" {
				w.WriteHeader(http.StatusUnauthorized)
				writeJSON(t, w, map[string]any{"error": "expired"})
				return
			}
			if auth != "Bearer relay-session-2" {
				t.Fatalf("unexpected authorization header: %q", auth)
			}

			if r.URL.Path == "/admin/claude-accounts" {
				writeJSON(t, w, map[string]any{
					"data": []map[string]any{
						{
							"id":       "acct-1",
							"name":     "alpha",
							"isActive": true,
							"status":   "active",
							"usage": map[string]any{
								"daily": map[string]any{
									"allTokens": 123,
								},
							},
						},
					},
				})
				return
			}

			writeJSON(t, w, map[string]any{
				"data": map[string]any{
					"acct-1": map[string]any{
						"fiveHour": map[string]any{"utilization": 10},
						"sevenDay": map[string]any{"utilization": 20},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	collector := NewClaudeRelayCollector(config.HTTPCollectorConfig{
		TimeoutSeconds: 2,
		Service: config.HTTPServiceConfig{
			BaseURL: server.URL,
			Endpoints: map[string]string{
				"accounts": "/admin/claude-accounts",
				"usage":    "/admin/claude-accounts/usage",
			},
		},
		Auth: config.HTTPAuthConfig{
			Type:          "login_json",
			HeaderName:    "Authorization",
			TokenPrefix:   "Bearer",
			LoginEndpoint: "/web/auth/login",
			Method:        http.MethodPost,
			TokenPath:     "token",
			Credentials: map[string]string{
				"username": "relay-admin",
				"password": "relay-pass",
			},
		},
	}, nil)

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("unexpected item count: %d", len(items))
	}
	if loginCalls.Load() != 2 {
		t.Fatalf("expected initial login plus refresh, got %d login calls", loginCalls.Load())
	}

	items, err = collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("second collect failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("unexpected second item count: %d", len(items))
	}
	if loginCalls.Load() != 2 {
		t.Fatalf("expected refreshed token to be cached, got %d login calls", loginCalls.Load())
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode response failed: %v", err)
	}
}

func expectAuthHeader(t *testing.T, r *http.Request, expected string) {
	t.Helper()

	if got := r.Header.Get("Authorization"); got != expected {
		t.Fatalf("unexpected authorization header: %q", got)
	}
}

func mustFindItem(t *testing.T, items []model.DataItem, title string) model.DataItem {
	t.Helper()

	for _, item := range items {
		if item.Title == title {
			return item
		}
	}
	t.Fatalf("item %q not found", title)
	return model.DataItem{}
}
