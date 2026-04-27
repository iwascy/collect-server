package api

import (
	"log/slog"
	"net/http"

	"infohub/internal/collector"
	"infohub/internal/scheduler"
	"infohub/internal/store"
)

func NewRouter(dataStore store.Store, registry *collector.Registry, scheduler *scheduler.Scheduler, logger *slog.Logger, authToken string, dashboardToken string, dashboardMockEnabled bool, dashboardSources DashboardSources) http.Handler {
	handler := NewHandlerWithOptions(dataStore, registry, scheduler, HandlerOptions{
		DashboardMockEnabled: dashboardMockEnabled,
		DashboardSources:     dashboardSources,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/summary", handler.Summary)
	mux.HandleFunc("GET /api/v1/source/{name}", handler.Source)
	mux.HandleFunc("GET /api/v1/health", handler.Health)
	mux.HandleFunc("POST /api/v1/collect/{name}", handler.Collect)
	mux.Handle("GET /dashboard/eink", withDashboardAccess(http.HandlerFunc(handler.EInkDashboard), authToken, dashboardToken))
	mux.Handle("GET /dashboard/eink.json", withDashboardAccess(http.HandlerFunc(handler.EInkDashboardData), authToken, dashboardToken))
	mux.Handle("GET /dashboard/eink/device.json", withDashboardAccess(http.HandlerFunc(handler.EInkDeviceData), authToken, dashboardToken))

	var root http.Handler = mux
	root = withAuth(root, authToken)
	root = withLogging(root, logger)
	return root
}
