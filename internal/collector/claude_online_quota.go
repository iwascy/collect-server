package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"infohub/internal/config"
)

const (
	defaultClaudeOnlineQuotaBaseURL    = "https://api.anthropic.com"
	defaultClaudeOnlineQuotaPath       = "/api/oauth/usage"
	defaultClaudeOnlineQuotaTimeout    = 8 * time.Second
	defaultClaudeOnlineQuotaStaleAfter = 60 * time.Second
	defaultClaudeOnlineQuotaUserAgent  = "infohub-claude-quota/1.0"
	claudeCredentialKeychainService    = "Claude Code-credentials"
	claudeOnlineTransportBreakerTTL    = 10 * time.Minute
)

// ClaudeOnlineQuotaClient reads Claude Code OAuth credentials and calls the
// official Anthropic OAuth usage API. It never writes Claude Code config files.
type ClaudeOnlineQuotaClient struct {
	httpClient *http.Client
	authPath   string
	baseURL    string
	path       string
	now        func() time.Time
	logger     *slog.Logger

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

	keychainReader func(context.Context) (string, bool, error)
}

type claudeOnlineCredentialsFile struct {
	ClaudeAiOauth  claudeOnlineOAuthEntry `json:"claudeAiOauth"`
	ClaudeDotOAuth claudeOnlineOAuthEntry `json:"claude.ai_oauth"`
}

type claudeOnlineOAuthEntry struct {
	AccessToken string `json:"accessToken"`
	ExpiresAt   any    `json:"expiresAt"`
}

type claudeOnlineUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

func NewClaudeOnlineQuotaClient(cfg config.LocalCodexOnlineConfig, logger *slog.Logger) *ClaudeOnlineQuotaClient {
	timeout := cfg.Timeout()
	if timeout <= 0 {
		timeout = defaultClaudeOnlineQuotaTimeout
	}
	staleAfter := cfg.StaleAfter()
	if staleAfter <= 0 {
		staleAfter = defaultClaudeOnlineQuotaStaleAfter
	}

	authPath := expandLocalPath(cfg.AuthPath)
	if strings.TrimSpace(authPath) == "" {
		authPath = defaultClaudeOnlineAuthPath()
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultClaudeOnlineQuotaBaseURL
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultClaudeOnlineQuotaUserAgent
	}

	client := &ClaudeOnlineQuotaClient{
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		authPath:   authPath,
		baseURL:    strings.TrimRight(baseURL, "/"),
		path:       defaultClaudeOnlineQuotaPath,
		now:        time.Now,
		logger:     logger,
		userAgent:  userAgent,
		staleAfter: staleAfter,
		lastStatus: codexOnlineQuotaStatusDisabled,
	}
	client.keychainReader = client.readAccessTokenFromKeychain
	return client
}

func (c *ClaudeOnlineQuotaClient) FetchRateLimits(ctx context.Context) (localRateLimits, bool, error) {
	if c == nil {
		return localRateLimits{}, false, nil
	}

	now := c.nowTime()
	if limits, ok := c.cachedResult(now); ok {
		c.setStatus(codexOnlineQuotaStatusOK)
		return limits, true, nil
	}
	if status, until, ok := c.breakerActive(now); ok {
		c.setStatus(status)
		if c.logger != nil {
			c.logger.Debug("claude online quota short-circuited", "status", status, "until", until.Format(time.RFC3339))
		}
		return localRateLimits{}, false, nil
	}

	accessToken, ok := c.readAccessToken(ctx)
	if !ok {
		return localRateLimits{}, false, nil
	}

	endpoint := c.baseURL + c.path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		c.setStatus(codexOnlineQuotaStatusTransportError)
		c.logFirstFailure("request_build")
		return localRateLimits{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.openBreaker(codexOnlineQuotaStatusTransportError, claudeOnlineTransportBreakerTTL)
		c.setStatus(codexOnlineQuotaStatusTransportError)
		c.logFirstFailure("transport_error")
		if c.logger != nil {
			c.logger.Warn("claude online quota request failed", "base_url", c.baseURL, "path", c.path, "error", err)
		}
		return localRateLimits{}, false, nil
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		limits, parsed, err := decodeClaudeOnlineRateLimits(resp.Body)
		if err != nil {
			c.setStatus(codexOnlineQuotaStatusTransportError)
			c.logFirstFailure("decode_error")
			return localRateLimits{}, false, err
		}
		if !parsed {
			c.setStatus(codexOnlineQuotaStatusTransportError)
			c.logFirstFailure("missing_rate_limits")
			return localRateLimits{}, false, fmt.Errorf("claude online quota response missing rate limits")
		}
		c.storeResult(now, limits)
		c.setStatus(codexOnlineQuotaStatusOK)
		c.logFirstSuccess()
		return limits, true, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		c.setStatus(codexOnlineQuotaStatusUnauthorized)
		c.logFirstFailure("unauthorized")
		if c.logger != nil {
			c.logger.Warn("claude online quota token unauthorized", "status", resp.StatusCode, "auth_path", c.authPath)
		}
		return localRateLimits{}, false, nil
	default:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusTooManyRequests {
			c.openBreaker(codexOnlineQuotaStatusRateLimited, time.Minute)
			c.setStatus(codexOnlineQuotaStatusRateLimited)
			c.logFirstFailure("rate_limited")
			return localRateLimits{}, false, nil
		}
		if resp.StatusCode >= 500 {
			c.openBreaker(codexOnlineQuotaStatusTransportError, claudeOnlineTransportBreakerTTL)
		}
		c.setStatus(codexOnlineQuotaStatusTransportError)
		c.logFirstFailure("unexpected_status")
		if c.logger != nil {
			c.logger.Warn("claude online quota returned unexpected status", "status", resp.StatusCode, "base_url", c.baseURL, "path", c.path)
		}
		return localRateLimits{}, false, nil
	}
}

func (c *ClaudeOnlineQuotaClient) LastStatus() string {
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

func (c *ClaudeOnlineQuotaClient) readAccessToken(ctx context.Context) (string, bool) {
	if c.keychainReader != nil {
		if token, ok, err := c.keychainReader(ctx); ok {
			return token, true
		} else if err != nil && c.logger != nil {
			c.logger.Debug("claude keychain credentials unavailable", "error", err)
		}
	}
	token, ok, err := readClaudeAccessTokenFromFile(c.authPath)
	if err != nil {
		c.setStatus(codexOnlineQuotaStatusTokenMissing)
		c.logFirstFailure("auth_unavailable")
		if c.logger != nil {
			c.logger.Warn("claude auth credentials unavailable", "auth_path", c.authPath, "error", err)
		}
		return "", false
	}
	if !ok {
		c.setStatus(codexOnlineQuotaStatusTokenMissing)
		c.logFirstFailure("auth_missing")
		return "", false
	}
	c.setStatus(codexOnlineQuotaStatusOK)
	return token, true
}

func (c *ClaudeOnlineQuotaClient) readAccessTokenFromKeychain(ctx context.Context) (string, bool, error) {
	if runtime.GOOS != "darwin" {
		return "", false, nil
	}
	output, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", claudeCredentialKeychainService, "-w").Output()
	if err != nil {
		return "", false, err
	}
	token, ok, err := parseClaudeAccessTokenJSON(output)
	return token, ok, err
}

func readClaudeAccessTokenFromFile(path string) (string, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return parseClaudeAccessTokenJSON(raw)
}

func parseClaudeAccessTokenJSON(raw []byte) (string, bool, error) {
	var payload claudeOnlineCredentialsFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return "", false, err
	}
	for _, entry := range []claudeOnlineOAuthEntry{payload.ClaudeAiOauth, payload.ClaudeDotOAuth} {
		token := strings.TrimSpace(entry.AccessToken)
		if token == "" {
			continue
		}
		if isClaudeTokenExpired(entry.ExpiresAt) {
			return token, false, fmt.Errorf("claude oauth token expired")
		}
		return token, true, nil
	}
	return "", false, fmt.Errorf("claude oauth accessToken missing")
}

func isClaudeTokenExpired(value any) bool {
	if value == nil {
		return false
	}
	now := time.Now()
	if n, ok := value.(json.Number); ok {
		if ts, err := strconvParseInt64(n.String()); err == nil {
			if ts > 1_000_000_000_000 {
				ts /= 1000
			}
			return time.Unix(ts, 0).Before(now)
		}
	}
	if s, ok := value.(string); ok {
		if ts, err := time.Parse(time.RFC3339, s); err == nil {
			return ts.Before(now)
		}
	}
	return false
}

func decodeClaudeOnlineRateLimits(body io.Reader) (localRateLimits, bool, error) {
	var payload map[string]any
	decoder := json.NewDecoder(io.LimitReader(body, 1<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return localRateLimits{}, false, fmt.Errorf("decode claude usage json: %w", err)
	}

	limits := localRateLimits{
		FiveHour: extractClaudeOnlineRateLimit(payload, "five_hour"),
		Week:     extractClaudeOnlineRateLimit(payload, "seven_day"),
	}
	return limits, limits.hasAny(), nil
}

func extractClaudeOnlineRateLimit(payload map[string]any, key string) localQuotaObservation {
	record, ok := payload[key].(map[string]any)
	if !ok {
		return localQuotaObservation{}
	}
	used, ok := floatValue(record["utilization"])
	if !ok {
		return localQuotaObservation{}
	}
	observation := localQuotaObservation{OK: true, UsedPercent: used}
	if reset, ok := parseEventTime(record["resets_at"]); ok {
		observation.ResetAt = reset.Format(time.RFC3339)
	}
	return observation
}

func (c *ClaudeOnlineQuotaClient) cachedResult(now time.Time) (localRateLimits, bool) {
	c.lastResultM.RLock()
	defer c.lastResultM.RUnlock()
	if c.lastResult.at.IsZero() || !c.lastResult.limits.hasAny() {
		return localRateLimits{}, false
	}
	return c.lastResult.limits, now.Before(c.lastResult.at.Add(c.staleAfter))
}

func (c *ClaudeOnlineQuotaClient) storeResult(at time.Time, limits localRateLimits) {
	c.lastResultM.Lock()
	c.lastResult = codexOnlineQuotaResult{at: at, limits: limits}
	c.lastResultM.Unlock()
}

func (c *ClaudeOnlineQuotaClient) breakerActive(now time.Time) (string, time.Time, bool) {
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

func (c *ClaudeOnlineQuotaClient) openBreaker(status string, duration time.Duration) {
	c.breakerM.Lock()
	c.breakerStatus = status
	c.breakerUntil = c.nowTime().Add(duration)
	c.breakerM.Unlock()
}

func (c *ClaudeOnlineQuotaClient) setStatus(status string) {
	c.lastStatusM.Lock()
	c.lastStatus = status
	c.lastStatusM.Unlock()
}

func (c *ClaudeOnlineQuotaClient) logFirstSuccess() {
	if c.logger == nil {
		return
	}
	c.logStateM.Lock()
	defer c.logStateM.Unlock()
	if c.loggedSuccess {
		return
	}
	c.loggedSuccess = true
	c.logger.Info("claude online quota succeeded", "base_url", c.baseURL, "path", c.path)
}

func (c *ClaudeOnlineQuotaClient) logFirstFailure(reason string) {
	if c.logger == nil {
		return
	}
	c.logStateM.Lock()
	defer c.logStateM.Unlock()
	if c.loggedFailure {
		return
	}
	c.loggedFailure = true
	c.logger.Info("claude online quota unavailable", "reason", reason, "auth_path", c.authPath, "base_url", c.baseURL)
}

func (c *ClaudeOnlineQuotaClient) nowTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func defaultClaudeOnlineAuthPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude", ".credentials.json")
	}
	return filepath.Join(".claude", ".credentials.json")
}

func strconvParseInt64(raw string) (int64, error) {
	var n int64
	_, err := fmt.Sscan(raw, &n)
	return n, err
}
