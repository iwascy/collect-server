package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"infohub/internal/api"
	"infohub/internal/collector"
	"infohub/internal/config"
	"infohub/internal/scheduler"
	"infohub/internal/store"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.Log.Level)

	dataStore, err := store.New(cfg.Store)
	if err != nil {
		logger.Error("create store failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := dataStore.Close(); err != nil {
			logger.Error("close store failed", "error", err)
		}
	}()

	registry := collector.NewRegistry()
	registerCollectors(registry, logger, cfg)

	taskScheduler, err := scheduler.New(registry, dataStore, logger, cfg.ScheduleConfig())
	if err != nil {
		logger.Error("create scheduler failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	initialCtx, cancelInitial := context.WithTimeout(ctx, 30*time.Second)
	taskScheduler.RunAllNow(initialCtx)
	cancelInitial()

	handler := api.NewRouter(dataStore, registry, taskScheduler, logger, cfg.Server.AuthToken, cfg.Server.DashboardToken, cfg.Server.MockEnabled, api.DashboardSources{
		Claude: cfg.Dashboard.Sources.Claude,
		Codex:  cfg.Dashboard.Sources.Codex,
	})
	server := &http.Server{
		Addr:              cfg.Server.Address(),
		Handler:           handler,
		ReadHeaderTimeout: cfg.Server.ReadTimeout(),
		WriteTimeout:      cfg.Server.WriteTimeout(),
	}

	taskScheduler.Start()
	logger.Info("infohub started", "addr", server.Addr)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout())
		defer cancel()

		taskScheduler.Stop()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown failed", "error", err)
		}
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func registerCollectors(registry *collector.Registry, logger *slog.Logger, cfg config.Config) {
	if cfg.Collectors.ClaudeRelay.Enabled {
		registry.Register(collector.NewClaudeRelayCollector(cfg.Collectors.ClaudeRelay, logger))
	}
	if cfg.Collectors.Sub2API.Enabled {
		registry.Register(collector.NewSub2APICollector(cfg.Collectors.Sub2API, logger))
	}
	if cfg.Collectors.Feishu.Enabled {
		registry.Register(collector.NewFeishuCollector(cfg.Collectors.Feishu, logger))
	}
	if cfg.Collectors.ClaudeLocal.Enabled {
		claudeCollector := collector.NewClaudeLocalCollector(cfg.Collectors.ClaudeLocal, logger)
		if cfg.Collectors.ClaudeLocal.Online.Enabled {
			claudeCollector.SetClaudeOnlineQuotaClient(collector.NewClaudeOnlineQuotaClient(cfg.Collectors.ClaudeLocal.Online, logger))
		}
		registry.Register(claudeCollector)
	}
	if cfg.Collectors.CodexLocal.Enabled {
		codexCollector := collector.NewCodexLocalCollector(cfg.Collectors.CodexLocal, logger)
		if cfg.Collectors.CodexLocal.Online.Enabled {
			codexCollector.SetCodexOnlineQuotaClient(collector.NewCodexOnlineQuotaClient(cfg.Collectors.CodexLocal.Online, logger))
		}
		registry.Register(codexCollector)
	}
}

func newLogger(level string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: config.ParseLogLevel(level)}
	handler := slog.NewJSONHandler(os.Stdout, opts)
	return slog.New(handler)
}
