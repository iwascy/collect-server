package config

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultPort                   = 8080
	defaultReadTimeoutSeconds     = 10
	defaultWriteTimeoutSeconds    = 10
	defaultShutdownTimeoutSeconds = 10
	defaultStoreType              = "sqlite"
	defaultSQLitePath             = "./data/infohub.db"
	defaultLogLevel               = "info"
	defaultCollectorTimeout       = 15
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Collectors CollectorsConfig `yaml:"collectors"`
	Store      StoreConfig      `yaml:"store"`
	Log        LogConfig        `yaml:"log"`
}

type ServerConfig struct {
	Port                   int    `yaml:"port"`
	AuthToken              string `yaml:"auth_token"`
	DashboardToken         string `yaml:"dashboard_token"`
	ReadTimeoutSeconds     int    `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds    int    `yaml:"write_timeout_seconds"`
	ShutdownTimeoutSeconds int    `yaml:"shutdown_timeout_seconds"`
}

type CollectorsConfig struct {
	ClaudeRelay HTTPCollectorConfig   `yaml:"claude_relay"`
	Sub2API     HTTPCollectorConfig   `yaml:"sub2api"`
	Feishu      FeishuCollectorConfig `yaml:"feishu"`
}

type HTTPCollectorConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Cron           string            `yaml:"cron"`
	Service        HTTPServiceConfig `yaml:"service"`
	Auth           HTTPAuthConfig    `yaml:"auth"`
	BaseURL        string            `yaml:"base_url"`
	Endpoint       string            `yaml:"endpoint"`
	APIKey         string            `yaml:"api_key"`
	TimeoutSeconds int               `yaml:"timeout_seconds"`
	Headers        map[string]string `yaml:"headers"`
}

type HTTPServiceConfig struct {
	BaseURL   string            `yaml:"base_url"`
	Headers   map[string]string `yaml:"headers"`
	Endpoints map[string]string `yaml:"endpoints"`
}

type HTTPAuthConfig struct {
	Type          string            `yaml:"type"`
	HeaderName    string            `yaml:"header_name"`
	TokenPrefix   string            `yaml:"token_prefix"`
	Token         string            `yaml:"token"`
	Headers       map[string]string `yaml:"headers"`
	LoginEndpoint string            `yaml:"login_endpoint"`
	Method        string            `yaml:"method"`
	TokenPath     string            `yaml:"token_path"`
	Credentials   map[string]string `yaml:"credentials"`
}

type FeishuCollectorConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Cron           string            `yaml:"cron"`
	BaseURL        string            `yaml:"base_url"`
	Endpoint       string            `yaml:"endpoint"`
	AppID          string            `yaml:"app_id"`
	AppSecret      string            `yaml:"app_secret"`
	ProjectKey     string            `yaml:"project_key"`
	TimeoutSeconds int               `yaml:"timeout_seconds"`
	Headers        map[string]string `yaml:"headers"`
}

type StoreConfig struct {
	Type       string `yaml:"type"`
	SQLitePath string `yaml:"sqlite_path"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

type CollectorSchedule struct {
	Enabled bool
	Cron    string
	Timeout time.Duration
}

type ScheduleConfig map[string]CollectorSchedule

func Load(path string) (Config, error) {
	var cfg Config

	if err := loadDotEnv(path); err != nil {
		return cfg, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config file: %w", err)
	}
	expanded := os.ExpandEnv(string(content))
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return cfg, fmt.Errorf("parse config yaml: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func (c Config) ScheduleConfig() ScheduleConfig {
	return ScheduleConfig{
		"claude_relay": {
			Enabled: c.Collectors.ClaudeRelay.Enabled,
			Cron:    c.Collectors.ClaudeRelay.Cron,
			Timeout: c.Collectors.ClaudeRelay.Timeout(),
		},
		"sub2api": {
			Enabled: c.Collectors.Sub2API.Enabled,
			Cron:    c.Collectors.Sub2API.Cron,
			Timeout: c.Collectors.Sub2API.Timeout(),
		},
		"feishu": {
			Enabled: c.Collectors.Feishu.Enabled,
			Cron:    c.Collectors.Feishu.Cron,
			Timeout: c.Collectors.Feishu.Timeout(),
		},
	}
}

func (s ServerConfig) Address() string {
	return fmt.Sprintf(":%d", s.Port)
}

func (s ServerConfig) ReadTimeout() time.Duration {
	return time.Duration(s.ReadTimeoutSeconds) * time.Second
}

func (s ServerConfig) WriteTimeout() time.Duration {
	return time.Duration(s.WriteTimeoutSeconds) * time.Second
}

func (s ServerConfig) ShutdownTimeout() time.Duration {
	return time.Duration(s.ShutdownTimeoutSeconds) * time.Second
}

func (c HTTPCollectorConfig) Timeout() time.Duration {
	return time.Duration(c.TimeoutSeconds) * time.Second
}

func (c FeishuCollectorConfig) Timeout() time.Duration {
	return time.Duration(c.TimeoutSeconds) * time.Second
}

func (c *Config) applyDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = defaultPort
	}
	if c.Server.ReadTimeoutSeconds == 0 {
		c.Server.ReadTimeoutSeconds = defaultReadTimeoutSeconds
	}
	if c.Server.WriteTimeoutSeconds == 0 {
		c.Server.WriteTimeoutSeconds = defaultWriteTimeoutSeconds
	}
	if c.Server.ShutdownTimeoutSeconds == 0 {
		c.Server.ShutdownTimeoutSeconds = defaultShutdownTimeoutSeconds
	}
	if c.Store.Type == "" {
		c.Store.Type = defaultStoreType
	}
	if c.Store.SQLitePath == "" {
		c.Store.SQLitePath = defaultSQLitePath
	}
	if c.Log.Level == "" {
		c.Log.Level = defaultLogLevel
	}

	c.Collectors.ClaudeRelay.applyDefaults()
	c.Collectors.Sub2API.applyDefaults()
	c.Collectors.Feishu.applyDefaults()
}

func (c Config) validate() error {
	switch strings.ToLower(c.Store.Type) {
	case "memory", "sqlite":
	default:
		return fmt.Errorf("unsupported store type %q", c.Store.Type)
	}

	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port %d", c.Server.Port)
	}

	return nil
}

func (c *HTTPCollectorConfig) applyDefaults() {
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = defaultCollectorTimeout
	}
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}

	if c.Service.BaseURL == "" {
		c.Service.BaseURL = strings.TrimSpace(c.BaseURL)
	}
	if c.Service.Headers == nil {
		if len(c.Headers) > 0 {
			c.Service.Headers = cloneStringMap(c.Headers)
		} else {
			c.Service.Headers = map[string]string{}
		}
	}
	if c.Service.Endpoints == nil {
		c.Service.Endpoints = map[string]string{}
	}
	if len(c.Service.Endpoints) == 0 && strings.TrimSpace(c.Endpoint) != "" {
		c.Service.Endpoints["default"] = strings.TrimSpace(c.Endpoint)
	}

	if c.Auth.Headers == nil {
		c.Auth.Headers = map[string]string{}
	}
	if c.Auth.Credentials == nil {
		c.Auth.Credentials = map[string]string{}
	}
	if c.Auth.Token == "" && strings.TrimSpace(c.APIKey) != "" {
		c.Auth.Token = strings.TrimSpace(c.APIKey)
	}

	switch strings.ToLower(strings.TrimSpace(c.Auth.Type)) {
	case "":
		if strings.TrimSpace(c.Auth.Token) != "" {
			c.Auth.Type = "bearer"
		} else {
			c.Auth.Type = "none"
		}
	case "none", "bearer", "login_json":
	default:
		c.Auth.Type = strings.TrimSpace(c.Auth.Type)
	}

	if c.Auth.Type != "none" && c.Auth.HeaderName == "" {
		c.Auth.HeaderName = "Authorization"
	}
	if c.Auth.Type != "none" && c.Auth.TokenPrefix == "" {
		c.Auth.TokenPrefix = "Bearer "
	}
	if strings.EqualFold(c.Auth.Type, "login_json") {
		if c.Auth.Method == "" {
			c.Auth.Method = "POST"
		}
		if c.Auth.TokenPath == "" {
			c.Auth.TokenPath = "token"
		}
	}
}

func (c *FeishuCollectorConfig) applyDefaults() {
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = defaultCollectorTimeout
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://open.feishu.cn"
	}
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
}

func ParseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func loadDotEnv(configPath string) error {
	dotEnvPath := filepath.Join(filepath.Dir(configPath), ".env")
	content, err := os.ReadFile(dotEnvPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read .env file: %w", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		if key == "" || os.Getenv(key) != "" {
			continue
		}

		value = strings.TrimSpace(value)
		if unquoted, ok := unquoteEnvValue(value); ok {
			value = unquoted
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set env %s: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan .env file: %w", err)
	}

	return nil
}

func unquoteEnvValue(value string) (string, bool) {
	if len(value) < 2 {
		return value, false
	}
	if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
		(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
		return value[1 : len(value)-1], true
	}
	return value, false
}
