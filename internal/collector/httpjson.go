package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"infohub/internal/config"
	"infohub/internal/model"
)

const defaultEndpointKey = "default"

type httpJSONCollector struct {
	name        string
	service     *serviceJSONClient
	endpointKey string
}

func (c *httpJSONCollector) fetch(ctx context.Context) (any, error) {
	if c.service == nil {
		return nil, fmt.Errorf("%s service client is nil", c.name)
	}

	session, err := c.service.newSession(ctx)
	if err != nil {
		return nil, err
	}

	endpointKey := c.endpointKey
	if endpointKey == "" {
		endpointKey = defaultEndpointKey
	}

	return session.fetchJSON(ctx, http.MethodGet, endpointKey, nil, nil)
}

type serviceJSONClient struct {
	name      string
	client    *http.Client
	logger    *slog.Logger
	baseURL   string
	headers   map[string]string
	endpoints map[string]string
	auth      config.HTTPAuthConfig
	authCache *loginTokenCache
}

type serviceSession struct {
	name      string
	client    *http.Client
	logger    *slog.Logger
	baseURL   string
	headers   map[string]string
	endpoints map[string]string
	auth      config.HTTPAuthConfig
	authCache *loginTokenCache
}

type loginTokenCache struct {
	mu    sync.Mutex
	token string
}

type httpStatusError struct {
	StatusCode int
	Body       string
}

func (e *httpStatusError) Error() string {
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("upstream status %d", e.StatusCode)
	}
	return fmt.Sprintf("upstream status %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

func newServiceJSONClient(name string, cfg config.HTTPCollectorConfig, logger *slog.Logger) *serviceJSONClient {
	return &serviceJSONClient{
		name:      name,
		client:    newHTTPClient(cfg.Timeout()),
		logger:    logger,
		baseURL:   strings.TrimSpace(cfg.Service.BaseURL),
		headers:   cloneHeaders(cfg.Service.Headers),
		endpoints: cloneHeaders(cfg.Service.Endpoints),
		auth:      cfg.Auth,
		authCache: newLoginTokenCache(cfg.Auth),
	}
}

func (c *serviceJSONClient) hasEndpoint(key string) bool {
	if c == nil {
		return false
	}

	endpointKey := strings.TrimSpace(key)
	if endpointKey == "" {
		endpointKey = defaultEndpointKey
	}

	endpoint, ok := c.endpoints[endpointKey]
	return ok && strings.TrimSpace(endpoint) != ""
}

func (c *serviceJSONClient) newSession(ctx context.Context) (*serviceSession, error) {
	if c == nil {
		return nil, fmt.Errorf("service client is nil")
	}
	if strings.TrimSpace(c.baseURL) == "" {
		return nil, fmt.Errorf("%s service.base_url is empty", c.name)
	}

	session := &serviceSession{
		name:      c.name,
		client:    c.client,
		logger:    c.logger,
		baseURL:   c.baseURL,
		headers:   cloneHeaders(c.headers),
		endpoints: cloneHeaders(c.endpoints),
		auth:      c.auth,
		authCache: c.authCache,
	}

	if err := session.applyAuth(ctx, false); err != nil {
		return nil, err
	}

	return session, nil
}

func (s *serviceSession) fetchJSON(ctx context.Context, method, endpointKey string, query url.Values, body any) (any, error) {
	payload, err := s.fetchJSONOnce(ctx, method, endpointKey, query, body)
	if err == nil {
		return payload, nil
	}
	if !s.canRefreshAuth(err) {
		return nil, err
	}
	if err := s.applyAuth(ctx, true); err != nil {
		return nil, err
	}
	return s.fetchJSONOnce(ctx, method, endpointKey, query, body)
}

func (s *serviceSession) fetchJSONOnce(ctx context.Context, method, endpointKey string, query url.Values, body any) (any, error) {
	endpoint, err := s.resolveEndpoint(endpointKey)
	if err != nil {
		return nil, err
	}

	targetURL, err := joinURL(s.baseURL, endpoint)
	if err != nil {
		return nil, err
	}
	if len(query) > 0 {
		parsedURL, err := url.Parse(targetURL)
		if err != nil {
			return nil, fmt.Errorf("parse target url: %w", err)
		}
		values := parsedURL.Query()
		for key, entries := range query {
			for _, value := range entries {
				values.Add(key, value)
			}
		}
		parsedURL.RawQuery = values.Encode()
		targetURL = parsedURL.String()
	}

	reader, contentType, err := jsonRequestBody(body)
	if err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(method)), targetURL, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range s.headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request upstream: %w", err)
	}
	defer resp.Body.Close()

	payload, err := readJSONResponse(resp)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func (s *serviceSession) canRefreshAuth(err error) bool {
	if !strings.EqualFold(strings.TrimSpace(s.auth.Type), "login_json") {
		return false
	}

	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.StatusCode == http.StatusUnauthorized || statusErr.StatusCode == http.StatusForbidden
}

func (s *serviceSession) resolveEndpoint(endpointKey string) (string, error) {
	key := strings.TrimSpace(endpointKey)
	if key == "" {
		key = defaultEndpointKey
	}

	endpoint, ok := s.endpoints[key]
	if !ok || strings.TrimSpace(endpoint) == "" {
		return "", fmt.Errorf("%s endpoint %q is empty", s.name, key)
	}
	return endpoint, nil
}

func (s *serviceSession) applyAuth(ctx context.Context, force bool) error {
	auth := s.auth

	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "", "none":
		return nil
	case "bearer":
		token := strings.TrimSpace(auth.Token)
		if token == "" {
			return fmt.Errorf("%s auth token is empty", s.name)
		}
		s.headers[strings.TrimSpace(auth.HeaderName)] = composeTokenValue(auth.TokenPrefix, token)
		return nil
	case "login_json":
		return s.applyLoginJSONAuth(ctx, auth, force)
	default:
		return fmt.Errorf("%s unsupported auth.type %q", s.name, auth.Type)
	}
}

func (s *serviceSession) applyLoginJSONAuth(ctx context.Context, auth config.HTTPAuthConfig, force bool) error {
	if s.authCache == nil {
		token, err := s.loginJSONToken(ctx, auth)
		if err != nil {
			return err
		}
		s.headers[strings.TrimSpace(auth.HeaderName)] = composeTokenValue(auth.TokenPrefix, token)
		return nil
	}

	token, err := s.authCache.getOrRefresh(force, func() (string, error) {
		return s.loginJSONToken(ctx, auth)
	})
	if err != nil {
		return err
	}

	s.headers[strings.TrimSpace(auth.HeaderName)] = composeTokenValue(auth.TokenPrefix, token)
	return nil
}

func (s *serviceSession) loginJSONToken(ctx context.Context, auth config.HTTPAuthConfig) (string, error) {
	loginEndpoint := strings.TrimSpace(auth.LoginEndpoint)
	if loginEndpoint == "" {
		if endpoint, ok := s.endpoints["login"]; ok {
			loginEndpoint = endpoint
		}
	}
	if loginEndpoint == "" {
		return "", fmt.Errorf("%s auth.login_endpoint is empty", s.name)
	}

	targetURL, err := joinURL(s.baseURL, loginEndpoint)
	if err != nil {
		return "", err
	}

	reader, contentType, err := jsonRequestBody(auth.Credentials)
	if err != nil {
		return "", fmt.Errorf("encode auth credentials: %w", err)
	}

	method := strings.ToUpper(strings.TrimSpace(auth.Method))
	if method == "" {
		method = http.MethodPost
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, reader)
	if err != nil {
		return "", fmt.Errorf("create auth request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range s.headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	for key, value := range auth.Headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request auth endpoint: %w", err)
	}
	defer resp.Body.Close()

	payload, err := readJSONResponse(resp)
	if err != nil {
		return "", fmt.Errorf("login auth failed: %w", err)
	}

	tokenValue, ok := nestedValue(payload, auth.TokenPath)
	if !ok {
		return "", fmt.Errorf("auth token not found at %q", auth.TokenPath)
	}
	token := strings.TrimSpace(stringify(tokenValue))
	if token == "" {
		return "", fmt.Errorf("auth token at %q is empty", auth.TokenPath)
	}

	return token, nil
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func joinURL(baseURL, endpoint string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	relative, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	return base.ResolveReference(relative).String(), nil
}

func parseEnvelopeItems(source string, payload any, defaultCategory string) ([]model.DataItem, bool, error) {
	switch typed := payload.(type) {
	case []any:
		items, err := decodeItems(source, typed, defaultCategory)
		return items, true, err
	case map[string]any:
		for _, key := range []string{"items", "data", "result"} {
			raw, ok := typed[key]
			if !ok {
				continue
			}
			if key == "data" || key == "result" {
				if nested, ok := raw.(map[string]any); ok {
					for _, nestedKey := range []string{"items", "list", "records"} {
						if itemsRaw, ok := nested[nestedKey]; ok {
							items, err := decodeRawItems(source, itemsRaw, defaultCategory)
							return items, true, err
						}
					}
				}
			}

			items, err := decodeRawItems(source, raw, defaultCategory)
			return items, true, err
		}
	}

	return nil, false, nil
}

func decodeRawItems(source string, raw any, defaultCategory string) ([]model.DataItem, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("items is not an array")
	}
	return decodeItems(source, items, defaultCategory)
}

func decodeItems(source string, rawItems []any, defaultCategory string) ([]model.DataItem, error) {
	items := make([]model.DataItem, 0, len(rawItems))
	for _, raw := range rawItems {
		record, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("item is not an object")
		}

		item := model.DataItem{
			Source:    source,
			Category:  firstString(record, "category"),
			Title:     firstString(record, "title", "name"),
			Value:     firstValueString(record, "value", "amount", "status"),
			FetchedAt: firstInt64(record, "fetched_at", "fetchedAt"),
		}
		if item.Category == "" {
			item.Category = defaultCategory
		}
		if item.Title == "" {
			item.Title = item.Category
		}
		if item.Value == "" {
			item.Value = "-"
		}
		if item.FetchedAt == 0 {
			item.FetchedAt = time.Now().Unix()
		}
		if extra, ok := record["extra"].(map[string]any); ok && len(extra) > 0 {
			item.Extra = extra
		}

		items = append(items, item)
	}

	return items, nil
}

func findValue(payload any, keys ...string) (any, bool) {
	expected := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		expected[normalizeKey(key)] = struct{}{}
	}
	return searchValue(payload, expected)
}

func searchValue(payload any, expected map[string]struct{}) (any, bool) {
	switch typed := payload.(type) {
	case map[string]any:
		for key, value := range typed {
			if _, ok := expected[normalizeKey(key)]; ok {
				return value, true
			}
			if nested, ok := searchValue(value, expected); ok {
				return nested, true
			}
		}
	case []any:
		for _, value := range typed {
			if nested, ok := searchValue(value, expected); ok {
				return nested, true
			}
		}
	}
	return nil, false
}

func nestedValue(payload any, path string) (any, bool) {
	if strings.TrimSpace(path) == "" {
		return nil, false
	}

	current := payload
	for _, part := range strings.Split(path, ".") {
		key := strings.TrimSpace(part)
		if key == "" {
			return nil, false
		}

		record, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		next, ok := record[key]
		if !ok {
			return nil, false
		}
		current = next
	}

	return current, true
}

func normalizeKey(key string) string {
	replacer := strings.NewReplacer("-", "_", ".", "_", " ", "_")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(key)))
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', 2, 64)
	case float32:
		if typed == float32(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(float64(typed), 'f', 2, 32)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(raw)
	}
}

func firstString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := record[key]; ok {
			if text := stringify(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func firstValueString(record map[string]any, keys ...string) string {
	return firstString(record, keys...)
}

func firstInt64(record map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, ok := record[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int64:
			return typed
		case int:
			return int64(typed)
		case float64:
			return int64(typed)
		case string:
			parsed, err := strconv.ParseInt(typed, 10, 64)
			if err == nil {
				return parsed
			}
		}
	}
	return 0
}

func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}

func withFetchedAt(items []model.DataItem) []model.DataItem {
	now := time.Now().Unix()
	for index := range items {
		if items[index].FetchedAt == 0 {
			items[index].FetchedAt = now
		}
	}
	return items
}

func readJSONResponse(resp *http.Response) (any, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &httpStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}

	var payload any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, fmt.Errorf("decode upstream response: %w", err)
	}
	return payload, nil
}

func jsonRequestBody(body any) (io.Reader, string, error) {
	if body == nil {
		return nil, "", nil
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}
	return bytes.NewReader(payload), "application/json", nil
}

func composeTokenValue(prefix, token string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return token
	}
	return prefix + " " + token
}

func newLoginTokenCache(auth config.HTTPAuthConfig) *loginTokenCache {
	if !strings.EqualFold(strings.TrimSpace(auth.Type), "login_json") {
		return nil
	}
	return &loginTokenCache{}
}

func (c *loginTokenCache) getOrRefresh(force bool, fetch func() (string, error)) (string, error) {
	if c == nil {
		return fetch()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if force {
		c.token = ""
	}
	if token := strings.TrimSpace(c.token); token != "" {
		return token, nil
	}

	token, err := fetch()
	if err != nil {
		return "", err
	}

	c.token = strings.TrimSpace(token)
	return c.token, nil
}

func cloneHeaders(source map[string]string) map[string]string {
	if len(source) == 0 {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func formatFloat(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func formatPercent(value float64) string {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return formatFloat(value) + "%"
}

func remainingPercent(used float64) float64 {
	switch {
	case used <= 0:
		return 100
	case used >= 100:
		return 0
	default:
		return 100 - used
	}
}
