package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"infohub/internal/collector"
	"infohub/internal/model"
	"infohub/internal/scheduler"
	"infohub/internal/store"
)

type Handler struct {
	store                store.Store
	registry             *collector.Registry
	scheduler            *scheduler.Scheduler
	dashboardMockEnabled bool
	dashboardSources     DashboardSources
}

func NewHandler(store store.Store, registry *collector.Registry, scheduler *scheduler.Scheduler) *Handler {
	return NewHandlerWithOptions(store, registry, scheduler, HandlerOptions{})
}

type HandlerOptions struct {
	DashboardMockEnabled bool
	DashboardSources     DashboardSources
}

type DashboardSources struct {
	Claude string
	Codex  string
}

func NewHandlerWithOptions(store store.Store, registry *collector.Registry, scheduler *scheduler.Scheduler, options HandlerOptions) *Handler {
	return &Handler{
		store:                store,
		registry:             registry,
		scheduler:            scheduler,
		dashboardMockEnabled: options.DashboardMockEnabled,
		dashboardSources:     options.DashboardSources,
	}
}

func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	sources, err := h.store.GetAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	response := model.SummaryResponse{
		UpdatedAt: maxLastFetch(sources),
		Sources:   sources,
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) Source(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	snapshot, err := h.store.GetBySource(name)
	if err != nil {
		if errors.Is(err, store.ErrSourceNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "source not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	sources, err := h.store.GetAll()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	collectors := make(map[string]model.CollectorStatus, len(sources))
	for source, snapshot := range sources {
		collectors[source] = model.CollectorStatus{
			Status:    snapshot.Status,
			LastFetch: snapshot.LastFetch,
			Error:     snapshot.Error,
		}
	}

	for _, registered := range h.registry.All() {
		if _, exists := collectors[registered.Name()]; exists {
			continue
		}
		collectors[registered.Name()] = model.CollectorStatus{
			Status:    "unknown",
			LastFetch: 0,
		}
	}

	writeJSON(w, http.StatusOK, model.HealthResponse{
		Status:     "ok",
		Collectors: collectors,
	})
}

func (h *Handler) Collect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := h.registry.Get(name); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "collector not found"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := h.scheduler.TriggerNow(ctx, name); err != nil {
		status := http.StatusBadGateway
		payload := map[string]any{
			"status": "error",
			"error":  err.Error(),
		}
		if snapshot, snapshotErr := h.store.GetBySource(name); snapshotErr == nil {
			payload["snapshot"] = snapshot
		}
		writeJSON(w, status, payload)
		return
	}

	snapshot, err := h.store.GetBySource(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"source":   name,
		"snapshot": snapshot,
	})
}

func maxLastFetch(sources map[string]model.SourceSnapshot) int64 {
	var updatedAt int64
	for _, snapshot := range sources {
		if snapshot.LastFetch > updatedAt {
			updatedAt = snapshot.LastFetch
		}
	}
	return updatedAt
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
