package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"infohub/internal/config"
)

const (
	codexOnlineQuotaStatusDisabled       = "disabled"
	codexOnlineQuotaStatusTokenMissing   = "token_missing"
	codexOnlineQuotaStatusUnauthorized   = "unauthorized"
	codexOnlineQuotaStatusRateLimited    = "rate_limited"
	codexOnlineQuotaStatusEndpoint404    = "endpoint_404"
	codexOnlineQuotaStatusTransportError = "transport_error"
	codexOnlineQuotaStatusOK             = "ok"

	defaultCodexOnlineQuotaBaseURL    = "https://chatgpt.com"
	defaultCodexOnlineQuotaTimeout    = 8 * time.Second
	defaultCodexOnlineQuotaStaleAfter = 60 * time.Second
	defaultCodexOnlineQuotaUserAgent  = "infohub-codex-quota/1.0"
)

var defaultCodexOnlineQuotaPaths = []string{
	"/backend-api/wham/usage",
	"/backend-api/wham/api/codex/usage",
	"/backend-api/api/codex/usage",
	"/wham/api/codex/usage",
	"/api/codex/usage",
}

// CodexOnlineQuotaClient calls ChatGPT's Codex usage endpoint using Codex CLI auth.
type CodexOnlineQuotaClient struct {
	httpClient   *http.Client
	authPath     string
	baseURL      string
	pathOrder    []string
	successPath  string
	successPathM sync.RWMutex
	now          func() time.Time
	logger       *slog.Logger

	userAgent  string
	staleAfter time.Duration

	lastResult  codexOnlineQuotaResult
	lastResultM sync.RWMutex

	breakerUntil  time.Time
	breakerStatus string
	breakerM      sync.RWMutex

	lastStatus  string
	lastStatusM sync.RWMutex

	logStateM     sync.Mutex
	loggedSuccess bool
	loggedFailure bool
}

type codexOnlineQuotaResult struct {
	at     time.Time
	limits localRateLimits
}

type codexOnlineAuth struct {
	accessToken string
	accountID   string
	lastRefresh string
}

type codexOnlineAuthFile struct {
	AuthMode    string                `json:"auth_mode"`
	Tokens      codexOnlineAuthTokens `json:"tokens"`
	LastRefresh any                   `json:"last_refresh"`
}

type codexOnlineAuthTokens struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

func NewCodexOnlineQuotaClient(cfg config.LocalCodexOnlineConfig, logger *slog.Logger) *CodexOnlineQuotaClient {
	timeout := cfg.Timeout()
	if timeout <= 0 {
		timeout = defaultCodexOnlineQuotaTimeout
	}
	staleAfter := cfg.StaleAfter()
	if staleAfter <= 0 {
		staleAfter = defaultCodexOnlineQuotaStaleAfter
	}

	authPath := expandLocalPath(cfg.AuthPath)
	if strings.TrimSpace(authPath) == "" {
		authPath = defaultCodexOnlineAuthPath()
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultCodexOnlineQuotaBaseURL
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultCodexOnlineQuotaUserAgent
	}

	return &CodexOnlineQuotaClient{
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		authPath:   authPath,
		baseURL:    strings.TrimRight(baseURL, "/"),
		pathOrder:  append([]string(nil), defaultCodexOnlineQuotaPaths...),
		now:        time.Now,
		logger:     logger,
		userAgent:  userAgent,
		staleAfter: staleAfter,
		lastStatus: codexOnlineQuotaStatusDisabled,
	}
}

// FetchRateLimits returns observed Codex 5H and weekly quota usage. Auth failures are
// reported as a disabled result so the caller can fall back to local estimates.
func (c *CodexOnlineQuotaClient) FetchRateLimits(ctx context.Context) (localRateLimits, bool, error) {
	if c == nil {
		return localRateLimits{}, false, nil
	}

	now := c.nowTime()
	if limits, ok := c.cachedResult(now); ok {
		c.setStatus(codexOnlineQuotaStatusOK)
		if c.logger != nil {
			c.logger.Debug("codex online quota cache hit", "auth_path", c.authPath)
		}
		return limits, true, nil
	}

	if status, until, ok := c.breakerActive(now); ok {
		c.setStatus(status)
		if c.logger != nil {
			c.logger.Debug("codex online quota short-circuited", "status", status, "until", until.Format(time.RFC3339))
		}
		return localRateLimits{}, false, nil
	}

	auth, ok := c.readAuth()
	if !ok {
		return localRateLimits{}, false, nil
	}

	paths := c.orderedPaths()
	saw404 := false
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			c.setStatus(codexOnlineQuotaStatusTransportError)
			return localRateLimits{}, false, err
		}

		endpoint, err := c.endpointURL(path)
		if err != nil {
			c.setStatus(codexOnlineQuotaStatusTransportError)
			c.logFirstFailure("endpoint_url")
			return localRateLimits{}, false, err
		}
		if c.logger != nil {
			c.logger.Debug("fetch codex online quota", "base_url", c.baseURL, "path", path)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			c.setStatus(codexOnlineQuotaStatusTransportError)
			c.logFirstFailure("request_build")
			return localRateLimits{}, false, err
		}
		req.Header.Set("Authorization", "Bearer "+auth.accessToken)
		req.Header.Set("ChatGPT-Account-Id", auth.accountID)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.openBreaker(codexOnlineQuotaStatusTransportError, 30*time.Second)
			c.setStatus(codexOnlineQuotaStatusTransportError)
			c.logFirstFailure("transport_error")
			if c.logger != nil {
				c.logger.Error("codex online quota request failed", "base_url", c.baseURL, "path", path, "error", err)
			}
			return localRateLimits{}, false, err
		}

		switch resp.StatusCode {
		case http.StatusOK:
			limits, parsed, err := decodeCodexOnlineRateLimits(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				c.setStatus(codexOnlineQuotaStatusTransportError)
				c.logFirstFailure("decode_error")
				if c.logger != nil {
					c.logger.Error("decode codex online quota response failed", "base_url", c.baseURL, "path", path, "error", err)
				}
				return localRateLimits{}, false, err
			}
			if !parsed {
				err := fmt.Errorf("codex online quota response missing rate limits")
				c.setStatus(codexOnlineQuotaStatusTransportError)
				c.logFirstFailure("missing_rate_limits")
				if c.logger != nil {
					c.logger.Error("codex online quota response missing rate limits", "base_url", c.baseURL, "path", path)
				}
				return localRateLimits{}, false, err
			}
			c.rememberSuccessPath(path)
			c.storeResult(now, limits)
			c.setStatus(codexOnlineQuotaStatusOK)
			c.logFirstSuccess(path)
			return limits, true, nil
		case http.StatusUnauthorized, http.StatusForbidden:
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			c.setStatus(codexOnlineQuotaStatusUnauthorized)
			c.logFirstFailure("unauthorized")
			if c.logger != nil {
				c.logger.Warn("codex online quota token unauthorized",
					"status", resp.StatusCode,
					"auth_path", c.authPath,
					"last_refresh", auth.lastRefresh,
					"access_token_len", len(auth.accessToken),
					"account_id_len", len(auth.accountID),
				)
			}
			return localRateLimits{}, false, nil
		case http.StatusNotFound:
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			saw404 = true
			continue
		case http.StatusTooManyRequests:
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			c.openBreaker(codexOnlineQuotaStatusRateLimited, 60*time.Second)
			c.setStatus(codexOnlineQuotaStatusRateLimited)
			c.logFirstFailure("rate_limited")
			if c.logger != nil {
				c.logger.Warn("codex online quota rate limited", "status", resp.StatusCode, "base_url", c.baseURL, "path", path)
			}
			return localRateLimits{}, false, nil
		default:
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode >= 500 {
				c.openBreaker(codexOnlineQuotaStatusTransportError, 30*time.Second)
			}
			c.setStatus(codexOnlineQuotaStatusTransportError)
			c.logFirstFailure("unexpected_status")
			if c.logger != nil {
				c.logger.Warn("codex online quota returned unexpected status", "status", resp.StatusCode, "base_url", c.baseURL, "path", path)
			}
			return localRateLimits{}, false, nil
		}
	}

	if saw404 {
		c.openBreaker(codexOnlineQuotaStatusEndpoint404, 30*time.Minute)
		c.setStatus(codexOnlineQuotaStatusEndpoint404)
		c.logFirstFailure("endpoint_404")
		if c.logger != nil {
			c.logger.Warn("codex online quota endpoints not found", "base_url", c.baseURL, "paths", paths)
		}
	}
	return localRateLimits{}, false, nil
}

func (c *CodexOnlineQuotaClient) LastStatus() string {
	if c == nil {
		return codexOnlineQuotaStatusDisabled
	}
	c.lastStatusM.RLock()
	defer c.lastStatusM.RUnlock()
	if strings.TrimSpace(c.lastStatus) == "" {
		return codexOnlineQuotaStatusDisabled
	}
	return c.lastStatus
}

func (c *CodexOnlineQuotaClient) readAuth() (codexOnlineAuth, bool) {
	info, err := os.Stat(c.authPath)
	if err != nil {
		c.setStatus(codexOnlineQuotaStatusTokenMissing)
		c.logFirstFailure("auth_missing")
		if c.logger != nil {
			c.logger.Warn("codex auth file unavailable", "auth_path", c.authPath, "error", err)
		}
		return codexOnlineAuth{}, false
	}
	if info.Mode().Perm()&0o077 != 0 && c.logger != nil {
		c.logger.Warn("codex auth file permissions are broader than 0600", "auth_path", c.authPath, "mode", info.Mode().Perm().String())
	}

	raw, err := os.ReadFile(c.authPath)
	if err != nil {
		c.setStatus(codexOnlineQuotaStatusTokenMissing)
		c.logFirstFailure("auth_read_failed")
		if c.logger != nil {
			c.logger.Warn("read codex auth file failed", "auth_path", c.authPath, "error", err)
		}
		return codexOnlineAuth{}, false
	}

	var payload codexOnlineAuthFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		c.setStatus(codexOnlineQuotaStatusTokenMissing)
		c.logFirstFailure("auth_parse_failed")
		if c.logger != nil {
			c.logger.Warn("parse codex auth file failed", "auth_path", c.authPath, "error", err)
		}
		return codexOnlineAuth{}, false
	}

	auth := codexOnlineAuth{
		accessToken: strings.TrimSpace(payload.Tokens.AccessToken),
		accountID:   strings.TrimSpace(payload.Tokens.AccountID),
		lastRefresh: strings.TrimSpace(stringify(payload.LastRefresh)),
	}
	if auth.accessToken == "" || auth.accountID == "" {
		c.setStatus(codexOnlineQuotaStatusTokenMissing)
		c.logFirstFailure("auth_tokens_missing")
		if c.logger != nil {
			c.logger.Warn("codex auth tokens missing",
				"auth_path", c.authPath,
				"last_refresh", auth.lastRefresh,
				"access_token_len", len(auth.accessToken),
				"account_id_len", len(auth.accountID),
			)
		}
		return codexOnlineAuth{}, false
	}

	return auth, true
}

func decodeCodexOnlineRateLimits(body io.Reader) (localRateLimits, bool, error) {
	var payload any
	decoder := json.NewDecoder(io.LimitReader(body, 1<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return localRateLimits{}, false, fmt.Errorf("decode wham usage json: %w", err)
	}
	limits, ok := parseCodexOnlineRateLimits(payload)
	return limits, ok, nil
}

func parseCodexOnlineRateLimits(payload any) (localRateLimits, bool) {
	rateLimits, ok := findCodexOnlineRateLimitsMap(payload, 0)
	if !ok {
		return localRateLimits{}, false
	}
	primaryKey, secondaryKey := "primary", "secondary"
	if _, ok := rateLimits["primary_window"]; ok {
		if _, ok := rateLimits["secondary_window"]; ok {
			primaryKey, secondaryKey = "primary_window", "secondary_window"
		}
	}
	limits := localRateLimits{
		FiveHour: extractCodexRateLimit(rateLimits, primaryKey),
		Week:     extractCodexRateLimit(rateLimits, secondaryKey),
	}
	return limits, limits.hasAny()
}

func findCodexOnlineRateLimitsMap(value any, depth int) (map[string]any, bool) {
	if depth > 6 {
		return nil, false
	}
	record, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	if limits, ok := firstNestedMap(record, "rate_limit", "rate_limits", "rateLimits"); ok {
		return limits, true
	}
	if hasCodexRateLimitWindows(record) {
		return record, true
	}
	for _, key := range []string{"data", "payload", "result", "response"} {
		if nested, ok := record[key]; ok {
			if limits, ok := findCodexOnlineRateLimitsMap(nested, depth+1); ok {
				return limits, true
			}
		}
	}
	return nil, false
}

func hasCodexRateLimitWindows(record map[string]any) bool {
	if _, p := record["primary"]; p {
		if _, s := record["secondary"]; s {
			return true
		}
	}
	if _, p := record["primary_window"]; p {
		if _, s := record["secondary_window"]; s {
			return true
		}
	}
	return false
}

func (c *CodexOnlineQuotaClient) endpointURL(path string) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse codex quota base url: %w", err)
	}
	endpoint, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("parse codex quota endpoint path: %w", err)
	}
	return base.ResolveReference(endpoint).String(), nil
}

func (c *CodexOnlineQuotaClient) orderedPaths() []string {
	paths := make([]string, 0, len(c.pathOrder)+1)
	c.successPathM.RLock()
	successPath := c.successPath
	c.successPathM.RUnlock()
	if strings.TrimSpace(successPath) != "" {
		paths = append(paths, successPath)
	}
	for _, path := range c.pathOrder {
		if strings.TrimSpace(path) == "" || path == successPath {
			continue
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		paths = append(paths, defaultCodexOnlineQuotaPaths...)
	}
	return paths
}

func (c *CodexOnlineQuotaClient) rememberSuccessPath(path string) {
	c.successPathM.Lock()
	c.successPath = path
	c.successPathM.Unlock()
}

func (c *CodexOnlineQuotaClient) cachedResult(now time.Time) (localRateLimits, bool) {
	c.lastResultM.RLock()
	defer c.lastResultM.RUnlock()
	if c.lastResult.at.IsZero() || !c.lastResult.limits.hasAny() {
		return localRateLimits{}, false
	}
	return c.lastResult.limits, now.Before(c.lastResult.at.Add(c.staleAfter))
}

func (c *CodexOnlineQuotaClient) storeResult(at time.Time, limits localRateLimits) {
	c.lastResultM.Lock()
	c.lastResult = codexOnlineQuotaResult{at: at, limits: limits}
	c.lastResultM.Unlock()
}

func (c *CodexOnlineQuotaClient) breakerActive(now time.Time) (string, time.Time, bool) {
	c.breakerM.RLock()
	defer c.breakerM.RUnlock()
	if c.breakerUntil.IsZero() || !now.Before(c.breakerUntil) {
		return "", time.Time{}, false
	}
	status := strings.TrimSpace(c.breakerStatus)
	if status == "" {
		status = codexOnlineQuotaStatusTransportError
	}
	return status, c.breakerUntil, true
}

func (c *CodexOnlineQuotaClient) openBreaker(status string, duration time.Duration) {
	c.breakerM.Lock()
	c.breakerStatus = status
	c.breakerUntil = c.nowTime().Add(duration)
	c.breakerM.Unlock()
}

func (c *CodexOnlineQuotaClient) setStatus(status string) {
	c.lastStatusM.Lock()
	c.lastStatus = status
	c.lastStatusM.Unlock()
}

func (c *CodexOnlineQuotaClient) logFirstSuccess(path string) {
	if c.logger == nil {
		return
	}
	c.logStateM.Lock()
	defer c.logStateM.Unlock()
	if c.loggedSuccess {
		return
	}
	c.loggedSuccess = true
	c.logger.Info("codex online quota succeeded", "base_url", c.baseURL, "path", path)
}

func (c *CodexOnlineQuotaClient) logFirstFailure(reason string) {
	if c.logger == nil {
		return
	}
	c.logStateM.Lock()
	defer c.logStateM.Unlock()
	if c.loggedFailure {
		return
	}
	c.loggedFailure = true
	c.logger.Info("codex online quota unavailable", "reason", reason, "auth_path", c.authPath, "base_url", c.baseURL)
}

func (c *CodexOnlineQuotaClient) nowTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func defaultCodexOnlineAuthPath() string {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, "auth.json")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".codex", "auth.json")
	}
	return filepath.Join(".codex", "auth.json")
}

func expandLocalPath(raw string) string {
	path := strings.TrimSpace(os.ExpandEnv(raw))
	if path == "" {
		return ""
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			path = home
		}
	} else if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return filepath.Clean(path)
}
