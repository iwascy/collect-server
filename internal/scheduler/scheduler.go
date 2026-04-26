package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"infohub/internal/collector"
	"infohub/internal/config"
	"infohub/internal/store"
)

type Scheduler struct {
	cron     *cron.Cron
	registry *collector.Registry
	store    store.Store
	logger   *slog.Logger
	cfg      config.ScheduleConfig
}

func New(registry *collector.Registry, store store.Store, logger *slog.Logger, cfg config.ScheduleConfig) (*Scheduler, error) {
	s := &Scheduler{
		cron:     cron.New(),
		registry: registry,
		store:    store,
		logger:   logger,
		cfg:      cfg,
	}

	if err := s.registerJobs(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Scheduler) Start() {
	s.cron.Start()
}

func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
}

func (s *Scheduler) TriggerNow(ctx context.Context, name string) error {
	return s.runCollector(ctx, name)
}

func (s *Scheduler) RunAllNow(ctx context.Context) {
	var wg sync.WaitGroup
	for _, c := range s.registry.All() {
		name := c.Name()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.runCollector(ctx, name); err != nil {
				s.logger.Warn("initial collect failed", "collector", name, "error", err)
			}
		}()
	}
	wg.Wait()
}

func (s *Scheduler) registerJobs() error {
	for _, c := range s.registry.All() {
		name := c.Name()
		schedule, ok := s.cfg[name]
		if !ok || !schedule.Enabled {
			continue
		}
		if schedule.Cron == "" {
			return fmt.Errorf("collector %s cron spec is empty", name)
		}

		jobName := name
		if _, err := s.cron.AddFunc(schedule.Cron, func() {
			timeout := schedule.Timeout
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			if err := s.runCollector(ctx, jobName); err != nil {
				s.logger.Error("scheduled collect failed", "collector", jobName, "error", err)
			}
		}); err != nil {
			return fmt.Errorf("register cron job for %s: %w", name, err)
		}
	}
	return nil
}

func (s *Scheduler) runCollector(ctx context.Context, name string) error {
	c, ok := s.registry.Get(name)
	if !ok {
		return fmt.Errorf("collector %s not found", name)
	}
	if setter, ok := c.(interface{ SetStore(store.Store) }); ok {
		setter.SetStore(s.store)
	}

	items, err := c.Collect(ctx)
	if err != nil {
		if saveErr := s.store.SaveFailure(name, err, time.Now()); saveErr != nil {
			s.logger.Error("persist collector failure failed", "collector", name, "error", saveErr)
		}
		return err
	}

	if err := s.store.Save(name, items); err != nil {
		return fmt.Errorf("save collector snapshot: %w", err)
	}

	s.logger.Info("collector finished", "collector", name, "items", len(items))
	return nil
}
