package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"infohub/internal/config"
	"infohub/internal/model"
)

func TestSub2APICollectorCollect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode login body failed: %v", err)
			}
			if payload["email"] != "admin@example.com" || payload["password"] != "sub2api-pass" {
				t.Fatalf("unexpected login payload: %#v", payload)
			}

			writeJSON(t, w, map[string]any{
				"code":    0,
				"message": "success",
				"data": map[string]any{
					"access_token": "sub2api-session",
				},
			})
		case "/api/v1/admin/accounts":
			expectAuthHeader(t, r, "Bearer sub2api-session")
			expectQueryValues(t, r.URL.Query(), map[string]string{
				"page":      "1",
				"page_size": "1000",
				"platform":  "openai",
				"type":      "oauth",
				"status":    "active",
			})
			writeJSON(t, w, map[string]any{
				"code": 0,
				"data": map[string]any{
					"items": []map[string]any{
						{
							"id":          391,
							"name":        "openai-a",
							"status":      "active",
							"schedulable": true,
							"extra": map[string]any{
								"codex_5h_used_percent": 32.5,
								"codex_5h_reset_at":     "2026-04-20T12:00:00Z",
								"codex_7d_used_percent": 80,
								"codex_7d_reset_at":     "2026-04-27T00:00:00Z",
							},
						},
						{
							"id":          392,
							"name":        "openai-disabled",
							"status":      "active",
							"schedulable": false,
							"extra": map[string]any{
								"codex_5h_used_percent": 99,
								"codex_7d_used_percent": 99,
							},
						},
						{
							"id":          393,
							"name":        "openai-b",
							"status":      "active",
							"schedulable": true,
							"extra": map[string]any{
								"codex_5h_used_percent": 10,
								"codex_5h_reset_at":     "2026-04-20T13:00:00Z",
								"codex_7d_used_percent": 20,
								"codex_7d_reset_at":     "2026-04-28T00:00:00Z",
							},
						},
					},
				},
			})
		case "/api/v1/admin/accounts/today-stats/batch":
			expectAuthHeader(t, r, "Bearer sub2api-session")

			var payload map[string][]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode today-stats body failed: %v", err)
			}
			gotIDs := payload["account_ids"]
			if len(gotIDs) != 2 || gotIDs[0] != 391.0 || gotIDs[1] != 393.0 {
				t.Fatalf("unexpected today-stats body: %#v", payload)
			}

			writeJSON(t, w, map[string]any{
				"code": 0,
				"data": map[string]any{
					"stats": map[string]any{
						"391": map[string]any{
							"tokens":   111,
							"requests": 4,
							"cost":     0.12,
						},
						"393": map[string]any{
							"tokens":   222,
							"requests": 6,
							"cost":     0.34,
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	collector := NewSub2APICollector(config.HTTPCollectorConfig{
		TimeoutSeconds: 2,
		Service: config.HTTPServiceConfig{
			BaseURL: server.URL,
			Endpoints: map[string]string{
				"accounts":    "/api/v1/admin/accounts",
				"today_stats": "/api/v1/admin/accounts/today-stats/batch",
			},
		},
		Auth: config.HTTPAuthConfig{
			Type:          "login_json",
			HeaderName:    "Authorization",
			TokenPrefix:   "Bearer",
			LoginEndpoint: "/api/v1/auth/login",
			Method:        http.MethodPost,
			TokenPath:     "data.access_token",
			Credentials: map[string]string{
				"email":    "admin@example.com",
				"password": "sub2api-pass",
			},
		},
	}, nil)

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("unexpected item count: %d", len(items))
	}

	tokenItem := mustFindItem(t, items, "今日 Token 用量")
	if tokenItem.Value != "333" {
		t.Fatalf("unexpected token value: %s", tokenItem.Value)
	}
	if got := tokenItem.Extra["daily_requests"]; got != 10.0 {
		t.Fatalf("unexpected request total: %v", got)
	}
	if got := mustFindItem(t, items, "账号 openai-a 5H 额度").Value; got != "67.50%" {
		t.Fatalf("unexpected openai-a 5H value: %s", got)
	}
	if got := mustFindItem(t, items, "账号 openai-a Week 额度").Value; got != "20%" {
		t.Fatalf("unexpected openai-a week value: %s", got)
	}
	if got := mustFindItem(t, items, "账号 openai-b 5H 额度").Value; got != "90%" {
		t.Fatalf("unexpected openai-b 5H value: %s", got)
	}
	if got := mustFindItem(t, items, "账号 openai-b Week 额度").Value; got != "80%" {
		t.Fatalf("unexpected openai-b week value: %s", got)
	}
}

func TestSub2APICollectorCollectTargetsUserAndAccount(t *testing.T) {
	var todayStatsCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			writeJSON(t, w, map[string]any{
				"code": 0,
				"data": map[string]any{"access_token": "sub2api-session"},
			})
		case "/api/v1/admin/accounts":
			expectAuthHeader(t, r, "Bearer sub2api-session")
			writeJSON(t, w, map[string]any{
				"code": 0,
				"data": map[string]any{
					"items": []map[string]any{
						{
							"id":          391,
							"name":        "Pro 20x",
							"status":      "active",
							"schedulable": true,
							"extra": map[string]any{
								"codex_5h_used_percent": 25,
								"codex_5h_reset_at":     "2026-05-25T12:00:00Z",
								"codex_7d_used_percent": 40,
								"codex_7d_reset_at":     "2026-05-27T00:00:00Z",
							},
						},
						{
							"id":          392,
							"name":        "Other Pro",
							"status":      "active",
							"schedulable": true,
							"extra": map[string]any{
								"codex_5h_used_percent": 99,
								"codex_7d_used_percent": 99,
							},
						},
					},
				},
			})
		case "/api/v1/admin/usage/search-users":
			expectAuthHeader(t, r, "Bearer sub2api-session")
			if got := r.URL.Query().Get("q"); got != "admin@sub2api.cccy.fun" {
				t.Fatalf("unexpected user search query: %q", got)
			}
			writeJSON(t, w, []map[string]any{{
				"id":    88,
				"email": "admin@sub2api.cccy.fun",
			}})
		case "/api/v1/admin/usage/stats":
			expectAuthHeader(t, r, "Bearer sub2api-session")
			expectQueryValues(t, r.URL.Query(), map[string]string{
				"user_id":  "88",
				"period":   "today",
				"timezone": "Asia/Shanghai",
			})
			writeJSON(t, w, map[string]any{
				"total_tokens":       12345,
				"total_requests":     7,
				"total_actual_cost":  1.23,
				"total_account_cost": 2.34,
			})
		case "/api/v1/admin/accounts/today-stats/batch":
			todayStatsCalled = true
			writeJSON(t, w, map[string]any{
				"code": 0,
				"data": map[string]any{"stats": map[string]any{}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	collector := NewSub2APICollector(config.HTTPCollectorConfig{
		TimeoutSeconds: 2,
		Targets: []config.Sub2APITarget{
			{
				Type:  "user",
				Name:  "admin@sub2api.cccy.fun",
				Email: "admin@sub2api.cccy.fun",
			},
			{
				Type:  "account",
				Name:  "Pro 20x",
				Match: "Pro 20x",
			},
		},
		Service: config.HTTPServiceConfig{
			BaseURL: server.URL,
			Endpoints: map[string]string{
				"accounts":     "/api/v1/admin/accounts",
				"today_stats":  "/api/v1/admin/accounts/today-stats/batch",
				"search_users": "/api/v1/admin/usage/search-users",
				"usage_stats":  "/api/v1/admin/usage/stats",
			},
		},
		Auth: config.HTTPAuthConfig{
			Type:          "login_json",
			HeaderName:    "Authorization",
			TokenPrefix:   "Bearer",
			LoginEndpoint: "/api/v1/auth/login",
			Method:        http.MethodPost,
			TokenPath:     "data.access_token",
			Credentials: map[string]string{
				"email":    "admin@example.com",
				"password": "sub2api-pass",
			},
		},
	}, nil)

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}
	if todayStatsCalled {
		t.Fatal("account today stats should not be called when account target does not include usage")
	}

	tokenItem := mustFindItem(t, items, "今日 Token 用量")
	if tokenItem.Value != "12345" {
		t.Fatalf("unexpected token value: %s", tokenItem.Value)
	}
	if got := tokenItem.Extra["enabled_accounts"]; got != 1 {
		t.Fatalf("unexpected enabled account count: %v", got)
	}
	if got := tokenItem.Extra["matched_targets"]; got != 2 {
		t.Fatalf("unexpected matched target count: %v", got)
	}
	if got := mustFindItem(t, items, "admin@sub2api.cccy.fun 今日 Token 用量").Value; got != "12345" {
		t.Fatalf("unexpected user token item: %s", got)
	}
	if got := mustFindItem(t, items, "账号 Pro 20x 5H 额度").Value; got != "75%" {
		t.Fatalf("unexpected Pro 20x 5H value: %s", got)
	}
	if got := mustFindItem(t, items, "账号 Pro 20x Week 额度").Value; got != "60%" {
		t.Fatalf("unexpected Pro 20x Week value: %s", got)
	}
	if hasItem(items, "账号 Other Pro 5H 额度") {
		t.Fatal("unexpected quota item for unmatched account")
	}
}

func hasItem(items []model.DataItem, title string) bool {
	for _, item := range items {
		if item.Title == title {
			return true
		}
	}
	return false
}

func expectQueryValues(t *testing.T, values url.Values, expected map[string]string) {
	t.Helper()

	for key, want := range expected {
		if got := values.Get(key); got != want {
			t.Fatalf("unexpected query %s: %q", key, got)
		}
	}
}
