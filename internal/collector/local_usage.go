package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"infohub/internal/config"
	"infohub/internal/model"
	"infohub/internal/store"
)

const (
	localClaudeSource = "claude_local"
	localCodexSource  = "codex_local"

	localClaudeParserVersion = 1
	localCodexParserVersion  = 1
)

type LocalUsageCollector struct {
	source            string
	cfg               config.LocalCollectorConfig
	logger            *slog.Logger
	now               func() time.Time
	store             store.LocalUsageStateStore
	onlineClaudeQuota onlineQuotaFetcher
	onlineCodexQuota  onlineQuotaFetcher
}

type onlineQuotaFetcher interface {
	FetchRateLimits(context.Context) (localRateLimits, bool, error)
	LastStatus() string
}

type localUsageEvent struct {
	At            time.Time
	Model         string
	Input         float64
	Output        float64
	CacheRead     float64
	CacheCreation float64
	Reasoning     float64
	Total         float64
	Quota         localRateLimits
}

type localUsageBucket struct {
	Tokens        float64
	Input         float64
	Output        float64
	CacheRead     float64
	CacheCreation float64
	Reasoning     float64
	Messages      float64
	Models        map[string]float64
}

type localQuotaObservation struct {
	OK          bool
	UsedPercent float64
	ResetAt     string
}

type localRateLimits struct {
	FiveHour localQuotaObservation
	Week     localQuotaObservation
}

type localWindow struct {
	Key       string
	Label     string
	Title     string
	Start     time.Time
	End       time.Time
	QuotaCap  float64
	QuotaUnit string
}

func NewClaudeLocalCollector(cfg config.LocalCollectorConfig, logger *slog.Logger) *LocalUsageCollector {
	return &LocalUsageCollector{source: localClaudeSource, cfg: cfg, logger: logger, now: time.Now}
}

func NewCodexLocalCollector(cfg config.LocalCollectorConfig, logger *slog.Logger) *LocalUsageCollector {
	return &LocalUsageCollector{source: localCodexSource, cfg: cfg, logger: logger, now: time.Now}
}

func (c *LocalUsageCollector) Name() string {
	return c.source
}

func (c *LocalUsageCollector) SetStore(dataStore store.Store) {
	if usageStore, ok := dataStore.(store.LocalUsageStateStore); ok {
		c.store = usageStore
	}
}

func (c *LocalUsageCollector) SetCodexOnlineQuotaClient(client *CodexOnlineQuotaClient) {
	c.onlineCodexQuota = client
}

func (c *LocalUsageCollector) SetClaudeOnlineQuotaClient(client *ClaudeOnlineQuotaClient) {
	c.onlineClaudeQuota = client
}

func (c *LocalUsageCollector) Collect(ctx context.Context) ([]model.DataItem, error) {
	events, err := c.collectEvents(ctx)
	if err != nil {
		return nil, err
	}

	items := c.buildItems(ctx, events)
	return withFetchedAt(items), nil
}

func (c *LocalUsageCollector) collectEvents(ctx context.Context) ([]localUsageEvent, error) {
	switch strings.ToLower(strings.TrimSpace(c.cfg.Mode)) {
	case "", "builtin":
		return c.scanBuiltin(ctx)
	case "ccusage":
		if c.source != localClaudeSource {
			return c.scanBuiltin(ctx)
		}
		events, err := c.scanCCUsage(ctx)
		if err == nil {
			return events, nil
		}
		if c.logger != nil {
			c.logger.Warn("ccusage local collector failed; fallback to builtin", "source", c.source, "error", err)
		}
		return c.scanBuiltin(ctx)
	default:
		return nil, fmt.Errorf("unsupported %s mode %q", c.source, c.cfg.Mode)
	}
}

func (c *LocalUsageCollector) scanBuiltin(ctx context.Context) ([]localUsageEvent, error) {
	if c.store != nil {
		return c.scanBuiltinIncremental(ctx)
	}

	paths := c.expandedPaths()
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s path missing", strings.TrimSuffix(c.source, "_local"))
	}

	var (
		events   []localUsageEvent
		foundDir bool
	)
	for _, root := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		info, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat %s: %w", root, err)
		}
		if !info.IsDir() {
			continue
		}
		foundDir = true

		walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}

			fileEvents, err := c.parseJSONL(path)
			if err != nil && c.logger != nil {
				c.logger.Debug("skip local usage file", "source", c.source, "path", path, "error", err)
			}
			events = append(events, fileEvents...)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	if !foundDir {
		return nil, fmt.Errorf("%s path missing", strings.TrimSuffix(c.source, "_local"))
	}

	return events, nil
}

func (c *LocalUsageCollector) scanBuiltinIncremental(ctx context.Context) ([]localUsageEvent, error) {
	paths := c.expandedPaths()
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s path missing", strings.TrimSuffix(c.source, "_local"))
	}

	states, err := c.store.LoadLocalParseStates(c.source)
	if err != nil {
		return nil, err
	}

	var (
		nextStates []store.LocalParseState
		records    []store.LocalUsageRecord
		foundDir   bool
	)
	for _, root := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		info, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat %s: %w", root, err)
		}
		if !info.IsDir() {
			continue
		}
		foundDir = true

		walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return nil
			}
			offset := int64(0)
			reset := false
			if state, ok := states[path]; ok {
				offset = state.ByteOffset
				if info.Size() < offset || info.ModTime().UnixNano() < state.MTimeUnix || state.ParserVersion != c.parserVersion() {
					offset = 0
					reset = true
				}
			}
			if info.Size() == offset && !reset {
				return nil
			}

			fileRecords, nextOffset, err := c.parseJSONLRecords(path, offset)
			if err != nil {
				if c.logger != nil {
					c.logger.Debug("skip local usage file", "source", c.source, "path", path, "error", err)
				}
				return nil
			}
			records = append(records, fileRecords...)
			nextStates = append(nextStates, store.LocalParseState{
				Source:        c.source,
				FilePath:      path,
				ByteOffset:    nextOffset,
				MTimeUnix:     info.ModTime().UnixNano(),
				UpdatedAt:     c.now().Unix(),
				ParserVersion: c.parserVersion(),
				Reset:         reset,
			})
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	if !foundDir {
		return nil, fmt.Errorf("%s path missing", strings.TrimSuffix(c.source, "_local"))
	}

	if len(nextStates) > 0 || len(records) > 0 {
		if err := c.store.SaveLocalUsageBatch(c.source, nextStates, records); err != nil {
			return nil, err
		}
	}

	start, end := c.recordQueryRange()
	storedRecords, err := c.store.ListLocalUsageRecords(c.source, start, end)
	if err != nil {
		return nil, err
	}
	events := make([]localUsageEvent, 0, len(storedRecords))
	for _, record := range storedRecords {
		events = append(events, localUsageEvent{
			At:            record.At,
			Model:         record.Model,
			Input:         record.Input,
			Output:        record.Output,
			CacheRead:     record.CacheRead,
			CacheCreation: record.CacheCreation,
			Reasoning:     record.Reasoning,
			Total:         record.Total,
			Quota: localRateLimits{
				FiveHour: localQuotaObservation{
					OK:          record.Quota5hUsed >= 0,
					UsedPercent: record.Quota5hUsed,
					ResetAt:     record.Quota5hReset,
				},
				Week: localQuotaObservation{
					OK:          record.QuotaWeekUsed >= 0,
					UsedPercent: record.QuotaWeekUsed,
					ResetAt:     record.QuotaWeekReset,
				},
			},
		})
	}
	return events, nil
}

func (c *LocalUsageCollector) recordQueryRange() (time.Time, time.Time) {
	windows := c.windows(c.now())
	if len(windows) == 0 {
		now := c.now()
		return now, now
	}
	start := windows[0].Start
	end := windows[0].End
	for _, window := range windows[1:] {
		if window.Start.Before(start) {
			start = window.Start
		}
		if window.End.After(end) {
			end = window.End
		}
	}
	return start, end
}

func (c *LocalUsageCollector) parseJSONL(path string) ([]localUsageEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var events []localUsageEvent
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var payload any
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			continue
		}

		event, ok := c.extractEvent(payload)
		if ok {
			events = append(events, event)
		}
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

func (c *LocalUsageCollector) parseJSONLRecords(path string, offset int64) ([]store.LocalUsageRecord, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer file.Close()

	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, offset, err
		}
	}

	reader := bufio.NewReaderSize(file, 256*1024)
	currentOffset := offset
	var records []store.LocalUsageRecord
	for {
		lineOffset := currentOffset
		line, err := reader.ReadString('\n')
		currentOffset += int64(len(line))
		if len(strings.TrimSpace(line)) > 0 {
			var payload any
			decoder := json.NewDecoder(strings.NewReader(line))
			decoder.UseNumber()
			if err := decoder.Decode(&payload); err == nil {
				if event, ok := c.extractEvent(payload); ok {
					records = append(records, store.LocalUsageRecord{
						Source:         c.source,
						FilePath:       path,
						ByteOffset:     lineOffset,
						At:             event.At,
						Model:          event.Model,
						Input:          event.Input,
						Output:         event.Output,
						CacheRead:      event.CacheRead,
						CacheCreation:  event.CacheCreation,
						Reasoning:      event.Reasoning,
						Total:          event.Total,
						Quota5hUsed:    quotaUsedOrMissing(event.Quota.FiveHour),
						Quota5hReset:   event.Quota.FiveHour.ResetAt,
						QuotaWeekUsed:  quotaUsedOrMissing(event.Quota.Week),
						QuotaWeekReset: event.Quota.Week.ResetAt,
					})
				}
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return records, currentOffset, err
	}
	return records, currentOffset, nil
}

func (c *LocalUsageCollector) scanCCUsage(ctx context.Context) ([]localUsageEvent, error) {
	bin := strings.TrimSpace(c.cfg.CCUsageBin)
	if bin == "" {
		bin = "npx"
	}
	args := c.cfg.CCUsageArgs
	if len(args) == 0 {
		args = []string{"ccusage@latest", "--json"}
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run ccusage: %w", err)
	}
	return parseCCUsageEvents(output, c.now())
}

func (c *LocalUsageCollector) extractEvent(payload any) (localUsageEvent, bool) {
	switch c.source {
	case localClaudeSource:
		return extractClaudeLocalEvent(payload)
	case localCodexSource:
		return extractCodexLocalEvent(payload)
	default:
		return localUsageEvent{}, false
	}
}

func extractClaudeLocalEvent(payload any) (localUsageEvent, bool) {
	record, ok := payload.(map[string]any)
	if !ok {
		return localUsageEvent{}, false
	}

	eventType := strings.TrimSpace(stringify(record["type"]))
	if eventType != "" && eventType != "assistant" && eventType != "user" {
		return localUsageEvent{}, false
	}

	usage, ok := nestedMap(record, "message.usage")
	if !ok {
		return localUsageEvent{}, false
	}

	at, ok := firstTime(record, "timestamp", "created_at")
	if !ok {
		return localUsageEvent{}, false
	}

	return localUsageEvent{
		At:            at,
		Model:         firstNestedString(record, "message.model", "model"),
		Input:         numberAt(usage, "input_tokens"),
		Output:        numberAt(usage, "output_tokens"),
		CacheRead:     numberAt(usage, "cache_read_input_tokens"),
		CacheCreation: numberAt(usage, "cache_creation_input_tokens"),
		Total:         firstNumber(usage, "total_tokens", "totalTokens", "total"),
	}, true
}

func extractCodexLocalEvent(payload any) (localUsageEvent, bool) {
	record, ok := payload.(map[string]any)
	if !ok {
		return localUsageEvent{}, false
	}

	if event, ok := extractCodexTokenCountEvent(record); ok {
		return event, true
	}

	usage, ok := firstNestedMap(record,
		"payload.usage",
		"response.usage",
		"usage",
		"payload.response.usage",
	)
	if !ok {
		return localUsageEvent{}, false
	}

	at, ok := firstTime(record, "created_at", "payload.created_at", "response.created_at", "timestamp")
	if !ok {
		return localUsageEvent{}, false
	}

	return localUsageEvent{
		At:        at,
		Model:     firstNestedString(record, "payload.model", "response.model", "model", "payload.response.model"),
		Input:     numberAt(usage, "input_tokens"),
		Output:    numberAt(usage, "output_tokens"),
		Reasoning: numberAt(usage, "reasoning_tokens"),
		Total:     firstNumber(usage, "total_tokens", "totalTokens", "total"),
	}, true
}

func extractCodexTokenCountEvent(record map[string]any) (localUsageEvent, bool) {
	eventType := strings.TrimSpace(stringify(record["type"]))
	payloadType := strings.TrimSpace(firstNestedString(record, "payload.type"))
	if eventType != "event_msg" || payloadType != "token_count" {
		return localUsageEvent{}, false
	}

	usage, ok := firstNestedMap(record,
		"payload.info.last_token_usage",
		"payload.info.total_token_usage",
	)
	rateLimits := extractCodexRateLimits(record)
	if !ok && !rateLimits.hasAny() {
		return localUsageEvent{}, false
	}

	at, ok := firstTime(record, "timestamp", "created_at", "payload.created_at")
	if !ok {
		return localUsageEvent{}, false
	}

	event := localUsageEvent{
		At:    at,
		Model: firstNestedString(record, "payload.model", "response.model", "model", "payload.response.model"),
		Quota: rateLimits,
	}
	if usage != nil {
		event.Input = firstNumber(usage, "input_tokens", "inputTokens", "input")
		event.Output = firstNumber(usage, "output_tokens", "outputTokens", "output")
		event.Reasoning = firstNumber(usage, "reasoning_tokens", "reasoning_output_tokens", "reasoningOutputTokens", "reasoning")
		event.Total = firstNumber(usage, "total_tokens", "totalTokens", "total")
	}
	return event, event.TotalTokens() > 0 || event.Quota.hasAny()
}

func extractCodexRateLimits(record map[string]any) localRateLimits {
	rateLimits, ok := nestedMap(record, "payload.rate_limits")
	if !ok {
		return localRateLimits{}
	}
	return localRateLimits{
		FiveHour: extractCodexRateLimit(rateLimits, "primary"),
		Week:     extractCodexRateLimit(rateLimits, "secondary"),
	}
}

func extractCodexRateLimit(rateLimits map[string]any, key string) localQuotaObservation {
	limit, ok := nestedMap(rateLimits, key)
	if !ok {
		return localQuotaObservation{}
	}
	used, ok := floatValue(limit["used_percent"])
	if !ok {
		return localQuotaObservation{}
	}
	observation := localQuotaObservation{
		OK:          true,
		UsedPercent: used,
	}
	var resetVal any
	if v, present := limit["resets_at"]; present && v != nil {
		resetVal = v
	} else if v, present := limit["reset_at"]; present && v != nil {
		resetVal = v
	}
	if reset, ok := parseEventTime(resetVal); ok {
		observation.ResetAt = reset.Format(time.RFC3339)
	}
	return observation
}

func parseCCUsageEvents(payload []byte, fallbackTime time.Time) ([]localUsageEvent, error) {
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode ccusage json: %w", err)
	}

	var events []localUsageEvent
	visitCCUsageValue(decoded, fallbackTime, &events)
	if len(events) == 0 {
		return nil, fmt.Errorf("ccusage payload contains no usage rows")
	}
	return events, nil
}

func visitCCUsageValue(value any, inheritedAt time.Time, events *[]localUsageEvent) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			visitCCUsageValue(item, inheritedAt, events)
		}
	case map[string]any:
		at := inheritedAt
		if parsed, ok := firstTime(typed,
			"timestamp",
			"date",
			"day",
			"startTime",
			"start_time",
			"endTime",
			"end_time",
			"from",
			"since",
		); ok {
			at = parsed
		}
		if event, ok := extractCCUsageEvent(typed, at); ok {
			*events = append(*events, event)
			return
		}
		for key, nested := range typed {
			if key == "usage" || key == "tokenCounts" || key == "tokens" {
				continue
			}
			visitCCUsageValue(nested, at, events)
		}
	}
}

func extractCCUsageEvent(record map[string]any, at time.Time) (localUsageEvent, bool) {
	usage := record
	if nested, ok := firstNestedMap(record, "usage", "tokenCounts", "tokens"); ok {
		usage = nested
	}
	event := localUsageEvent{
		At:            at,
		Model:         firstNestedString(record, "model", "modelName", "model_name"),
		Input:         firstNumber(usage, "input_tokens", "inputTokens", "input"),
		Output:        firstNumber(usage, "output_tokens", "outputTokens", "output"),
		CacheRead:     firstNumber(usage, "cache_read_input_tokens", "cacheReadInputTokens", "cacheReadTokens", "cache_read"),
		CacheCreation: firstNumber(usage, "cache_creation_input_tokens", "cacheCreationInputTokens", "cacheCreationTokens", "cache_creation"),
	}
	if event.TotalTokens() == 0 {
		event.Input = firstNumber(usage, "total_tokens", "totalTokens", "total")
	}
	return event, event.TotalTokens() > 0
}

func (c *LocalUsageCollector) buildItems(ctx context.Context, events []localUsageEvent) []model.DataItem {
	now := c.now()
	windows := c.windows(now)
	buckets := make(map[string]*localUsageBucket, len(windows))
	for _, window := range windows {
		buckets[window.Key] = &localUsageBucket{Models: map[string]float64{}}
	}

	var latestQuota localRateLimits
	var latestQuotaAt time.Time
	for _, event := range events {
		if event.Quota.hasAny() && (latestQuotaAt.IsZero() || event.At.After(latestQuotaAt)) {
			latestQuota = event.Quota
			latestQuotaAt = event.At
		}
		for _, window := range windows {
			if event.At.Before(window.Start) || !event.At.Before(window.End) {
				continue
			}
			buckets[window.Key].add(event)
		}
	}
	latestQuota = discardExpiredRateLimits(latestQuota, now)

	quotaSourceForWindow := map[string]string{}
	if latestQuota.FiveHour.OK {
		quotaSourceForWindow["5H"] = "codex_rate_limits"
	}
	if latestQuota.Week.OK {
		quotaSourceForWindow["Week"] = "codex_rate_limits"
	}
	if c.source == localClaudeSource {
		if c.onlineClaudeQuota != nil {
			onlineLimits, ok, err := c.onlineClaudeQuota.FetchRateLimits(ctx)
			if err != nil && c.logger != nil {
				c.logger.Warn("claude online quota unavailable", "error", err)
			}
			if ok {
				onlineLimits = discardExpiredRateLimits(onlineLimits, now)
				if onlineLimits.FiveHour.OK {
					latestQuota.FiveHour = onlineLimits.FiveHour
					quotaSourceForWindow["5H"] = "claude_oauth_usage"
				}
				if onlineLimits.Week.OK {
					latestQuota.Week = onlineLimits.Week
					quotaSourceForWindow["Week"] = "claude_oauth_usage"
				}
			}
		}
	}
	onlineQuotaStatus := ""
	if c.source == localCodexSource {
		onlineQuotaStatus = codexOnlineQuotaStatusDisabled
		if c.onlineCodexQuota != nil {
			onlineQuotaStatus = codexOnlineQuotaStatusOK
			if !latestQuota.FiveHour.OK || !latestQuota.Week.OK {
				onlineLimits, ok, err := c.onlineCodexQuota.FetchRateLimits(ctx)
				onlineQuotaStatus = normalizeCodexOnlineQuotaStatus(c.onlineCodexQuota.LastStatus())
				if err != nil {
					onlineQuotaStatus = codexOnlineQuotaStatusTransportError
					if c.logger != nil {
						c.logger.Warn("codex online quota fallback failed", "error", err)
					}
				}
				if ok {
					onlineQuotaStatus = codexOnlineQuotaStatusOK
					onlineLimits = discardExpiredRateLimits(onlineLimits, now)
					if !latestQuota.FiveHour.OK && onlineLimits.FiveHour.OK {
						latestQuota.FiveHour = onlineLimits.FiveHour
						quotaSourceForWindow["5H"] = "codex_wham_usage"
					}
					if !latestQuota.Week.OK && onlineLimits.Week.OK {
						latestQuota.Week = onlineLimits.Week
						quotaSourceForWindow["Week"] = "codex_wham_usage"
					}
				}
			}
		}
	}

	items := make([]model.DataItem, 0, 8)
	today := buckets["today"]
	if today == nil {
		today = &localUsageBucket{Models: map[string]float64{}}
	}
	items = append(items, model.DataItem{
		Source:   c.source,
		Category: "token_usage",
		Title:    "今日 Token 用量",
		Value:    formatFloat(today.TotalTokens()),
		Extra: map[string]any{
			"daily_requests":        today.Messages,
			"daily_cost":            0,
			"enabled_accounts":      1,
			"enabled_account_names": []string{c.displayName()},
			"input":                 today.Input,
			"output":                today.Output,
			"cache_read":            today.CacheRead,
			"cache_creation":        today.CacheCreation,
			"reasoning":             today.Reasoning,
		},
	})

	for _, window := range windows {
		bucket := buckets[window.Key]
		if bucket == nil {
			continue
		}
		if window.Label == "5H" || window.Label == "Week" {
			items = append(items, c.quotaItem(window, bucket, latestQuota.forWindow(window.Label), quotaSourceForWindow[window.Label], onlineQuotaStatus))
		}
		if window.Key == "today" || window.Key == "month" || window.Key == "weekly" {
			items = append(items, c.windowUsageItem(window, bucket))
		}
	}

	if modelName, tokens, ok := topModel(today.Models); ok {
		items = append(items, model.DataItem{
			Source:   c.source,
			Category: "usage",
			Title:    "model_top1",
			Value:    modelName,
			Extra: map[string]any{
				"tokens":        tokens,
				"share_percent": percentOf(tokens, today.TotalTokens()),
			},
		})
	}

	if c.source == localClaudeSource {
		items = append(items, model.DataItem{
			Source:   c.source,
			Category: "usage",
			Title:    "cache_hit",
			Value:    formatPercent(percentOf(today.CacheRead, today.Input+today.CacheRead)),
			Extra: map[string]any{
				"cache_read":  today.CacheRead,
				"total_input": today.Input + today.CacheRead,
			},
		})
	} else {
		items = append(items, model.DataItem{
			Source:   c.source,
			Category: "usage",
			Title:    "reasoning_share",
			Value:    formatPercent(percentOf(today.Reasoning, today.Output)),
			Extra: map[string]any{
				"reasoning": today.Reasoning,
				"output":    today.Output,
			},
		})
	}

	return items
}

func (c *LocalUsageCollector) quotaItem(window localWindow, bucket *localUsageBucket, observed localQuotaObservation, observedSource string, onlineQuotaStatus string) model.DataItem {
	used := bucket.Messages
	if window.QuotaUnit == "tokens" {
		used = bucket.TotalTokens()
	}
	usedPercent := 0.0
	if window.QuotaCap > 0 {
		usedPercent = percentOf(used, window.QuotaCap)
	}
	resetAt := window.End.Format(time.RFC3339)
	quotaSource := "estimated_cap"
	if observed.OK {
		usedPercent = observed.UsedPercent
		quotaSource = strings.TrimSpace(observedSource)
		if quotaSource == "" {
			if c.source == localClaudeSource {
				quotaSource = "claude_oauth_usage"
			} else {
				quotaSource = "codex_rate_limits"
			}
		}
		if strings.TrimSpace(observed.ResetAt) != "" {
			resetAt = observed.ResetAt
		}
	}
	extra := map[string]any{
		"account_id":        c.source,
		"used_percent":      usedPercent,
		"remaining_percent": remainingPercent(usedPercent),
		"window":            window.Label,
		"used":              used,
		"cap":               window.QuotaCap,
		"quota_unit":        window.QuotaUnit,
		"window_start_at":   window.Start.Format(time.RFC3339),
		"window_end_at":     window.End.Format(time.RFC3339),
		"reset_at":          resetAt,
		"models":            bucket.Models,
		"approx":            !observed.OK,
		"quota_source":      quotaSource,
	}
	if c.source == localCodexSource {
		extra["online_quota_status"] = normalizeCodexOnlineQuotaStatus(onlineQuotaStatus)
	}
	if strings.TrimSpace(c.cfg.Quota.Plan) != "" {
		extra["plan"] = strings.TrimSpace(c.cfg.Quota.Plan)
	}

	return model.DataItem{
		Source:   c.source,
		Category: "quota",
		Title:    fmt.Sprintf("账号 %s %s 额度", c.displayName(), window.Label),
		Value:    formatPercent(remainingPercent(usedPercent)),
		Extra:    extra,
	}
}

func (c *LocalUsageCollector) windowUsageItem(window localWindow, bucket *localUsageBucket) model.DataItem {
	return model.DataItem{
		Source:   c.source,
		Category: "quota",
		Title:    window.Title,
		Value:    formatFloat(bucket.TotalTokens()) + " tokens",
		Extra: map[string]any{
			"window":         window.Key,
			"input":          bucket.Input,
			"output":         bucket.Output,
			"cache_read":     bucket.CacheRead,
			"cache_creation": bucket.CacheCreation,
			"reasoning":      bucket.Reasoning,
			"messages":       bucket.Messages,
			"models":         bucket.Models,
		},
	}
}

func (c *LocalUsageCollector) windows(now time.Time) []localWindow {
	localNow := now.In(time.Local)
	todayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.Local)

	if c.source == localCodexSource {
		weekday := int(localNow.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		weekStart := todayStart.AddDate(0, 0, -(weekday - 1))
		weeklyCap := c.cfg.Quota.WeeklyCap
		quotaUnit := "messages"
		if weeklyCap <= 0 && c.cfg.Quota.WeeklyTokenCap > 0 {
			weeklyCap = c.cfg.Quota.WeeklyTokenCap
			quotaUnit = "tokens"
		}
		return []localWindow{
			{Key: "5h", Label: "5H", Title: "5H 用量", Start: now.Add(-5 * time.Hour), End: now, QuotaCap: c.cfg.Quota.FiveHourCap, QuotaUnit: "messages"},
			{Key: "today", Label: "Today", Title: "今日用量", Start: todayStart, End: todayStart.AddDate(0, 0, 1), QuotaUnit: "tokens"},
			{Key: "weekly", Label: "Week", Title: "本周用量", Start: weekStart, End: weekStart.AddDate(0, 0, 7), QuotaCap: weeklyCap, QuotaUnit: quotaUnit},
		}
	}

	weeklyCap := c.cfg.Quota.WeeklyCap
	if weeklyCap <= 0 {
		weeklyCap = c.cfg.Quota.MonthlyCap
	}
	return []localWindow{
		{Key: "5h", Label: "5H", Title: "5H 用量", Start: now.Add(-5 * time.Hour), End: now, QuotaCap: c.cfg.Quota.FiveHourCap, QuotaUnit: "messages"},
		{Key: "today", Label: "Today", Title: "今日用量", Start: todayStart, End: todayStart.AddDate(0, 0, 1), QuotaUnit: "tokens"},
		{Key: "weekly", Label: "Week", Title: "7D 用量", Start: now.AddDate(0, 0, -7), End: now, QuotaCap: weeklyCap, QuotaUnit: "messages"},
	}
}

func (c *LocalUsageCollector) expandedPaths() []string {
	return expandedLocalPaths(c.cfg.Paths)
}

func expandedLocalPaths(rawPaths []string) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		path := strings.TrimSpace(os.ExpandEnv(raw))
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
			}
		}
		if path == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		paths = append(paths, cleaned)
	}
	return paths
}

func (c *LocalUsageCollector) displayName() string {
	if c.source == localCodexSource {
		return "Codex Local"
	}
	return "Claude Local"
}

func (c *LocalUsageCollector) parserVersion() int {
	if c.source == localCodexSource {
		return localCodexParserVersion
	}
	return localClaudeParserVersion
}

func (b *localUsageBucket) add(event localUsageEvent) {
	b.Tokens += event.TotalTokens()
	b.Input += event.Input
	b.Output += event.Output
	b.CacheRead += event.CacheRead
	b.CacheCreation += event.CacheCreation
	b.Reasoning += event.Reasoning
	b.Messages++
	modelName := strings.TrimSpace(event.Model)
	if modelName == "" {
		modelName = "unknown"
	}
	b.Models[modelName] += event.TotalTokens()
}

func (b localUsageBucket) TotalTokens() float64 {
	return b.Tokens
}

func (e localUsageEvent) TotalTokens() float64 {
	if e.Total > 0 {
		return e.Total
	}
	return e.Input + e.Output + e.CacheRead + e.CacheCreation + e.Reasoning
}

func (q localRateLimits) hasAny() bool {
	return q.FiveHour.OK || q.Week.OK
}

func (q localRateLimits) forWindow(window string) localQuotaObservation {
	switch window {
	case "5H":
		return q.FiveHour
	case "Week":
		return q.Week
	default:
		return localQuotaObservation{}
	}
}

func discardExpiredRateLimits(limits localRateLimits, now time.Time) localRateLimits {
	if quotaObservationExpired(limits.FiveHour, now) {
		limits.FiveHour = localQuotaObservation{}
	}
	if quotaObservationExpired(limits.Week, now) {
		limits.Week = localQuotaObservation{}
	}
	return limits
}

func quotaObservationExpired(observation localQuotaObservation, now time.Time) bool {
	if !observation.OK || strings.TrimSpace(observation.ResetAt) == "" {
		return false
	}
	resetAt, ok := parseEventTime(observation.ResetAt)
	return ok && !resetAt.After(now)
}

func quotaUsedOrMissing(observation localQuotaObservation) float64 {
	if !observation.OK {
		return -1
	}
	return observation.UsedPercent
}

func normalizeCodexOnlineQuotaStatus(status string) string {
	switch strings.TrimSpace(status) {
	case codexOnlineQuotaStatusTokenMissing,
		codexOnlineQuotaStatusUnauthorized,
		codexOnlineQuotaStatusRateLimited,
		codexOnlineQuotaStatusEndpoint404,
		codexOnlineQuotaStatusTransportError,
		codexOnlineQuotaStatusOK:
		return strings.TrimSpace(status)
	default:
		return codexOnlineQuotaStatusDisabled
	}
}

func firstNestedMap(record map[string]any, paths ...string) (map[string]any, bool) {
	for _, path := range paths {
		if value, ok := nestedMap(record, path); ok {
			return value, true
		}
	}
	return nil, false
}

func nestedMap(record map[string]any, path string) (map[string]any, bool) {
	value, ok := nestedValue(record, path)
	if !ok {
		return nil, false
	}
	typed, ok := value.(map[string]any)
	return typed, ok
}

func firstNestedString(record map[string]any, paths ...string) string {
	for _, path := range paths {
		value, ok := nestedValue(record, path)
		if !ok {
			continue
		}
		if text := strings.TrimSpace(stringify(value)); text != "" {
			return text
		}
	}
	return ""
}

func firstTime(record map[string]any, paths ...string) (time.Time, bool) {
	for _, path := range paths {
		value, ok := nestedValue(record, path)
		if !ok {
			continue
		}
		if parsed, ok := parseEventTime(value); ok {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func parseEventTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return time.Unix(parsed, 0), true
		}
		if parsed, err := typed.Float64(); err == nil {
			seconds := int64(parsed)
			nanos := int64((parsed - float64(seconds)) * 1e9)
			return time.Unix(seconds, nanos), true
		}
	case float64:
		seconds := int64(typed)
		nanos := int64((typed - float64(seconds)) * 1e9)
		return time.Unix(seconds, nanos), true
	case int64:
		return time.Unix(typed, 0), true
	case int:
		return time.Unix(int64(typed), 0), true
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return time.Time{}, false
		}
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return parsed, true
		}
		if parsed, err := time.ParseInLocation("2006-01-02", text, time.Local); err == nil {
			return parsed, true
		}
		if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
			return time.Unix(parsed, 0), true
		}
		if parsed, err := strconv.ParseFloat(text, 64); err == nil {
			seconds := int64(parsed)
			nanos := int64((parsed - float64(seconds)) * 1e9)
			return time.Unix(seconds, nanos), true
		}
	}
	return time.Time{}, false
}

func numberAt(record map[string]any, key string) float64 {
	value, ok := record[key]
	if !ok {
		return 0
	}
	number, ok := floatValue(value)
	if !ok {
		return 0
	}
	return number
}

func firstNumber(record map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if number := numberAt(record, key); number != 0 {
			return number
		}
	}
	return 0
}

func topModel(models map[string]float64) (string, float64, bool) {
	if len(models) == 0 {
		return "", 0, false
	}
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)

	topName := names[0]
	topTokens := models[topName]
	for _, name := range names[1:] {
		if models[name] > topTokens {
			topName = name
			topTokens = models[name]
		}
	}
	return topName, topTokens, true
}

func percentOf(value, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return value / total * 100
}
