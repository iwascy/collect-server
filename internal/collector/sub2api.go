package collector

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"infohub/internal/config"
	"infohub/internal/model"
)

type Sub2APICollector struct {
	service *serviceJSONClient
	targets []config.Sub2APITarget
}

type sub2apiAccountQuota struct {
	ID           string
	RawID        any
	Name         string
	Used5h       float64
	Reset5h      string
	UsedWeek     float64
	ResetWeek    string
	IncludeUsage bool
}

type sub2apiUserStat struct {
	UserID   string
	UserName string
	Email    string
	Tokens   float64
	Requests float64
	Cost     float64
}

func NewSub2APICollector(cfg config.HTTPCollectorConfig, logger *slog.Logger) *Sub2APICollector {
	return &Sub2APICollector{
		service: newServiceJSONClient("sub2api", cfg, logger),
		targets: normalizeSub2APITargets(cfg.Targets),
	}
}

func (c *Sub2APICollector) Name() string {
	return "sub2api"
}

func (c *Sub2APICollector) Collect(ctx context.Context) ([]model.DataItem, error) {
	session, err := c.service.newSession(ctx)
	if err != nil {
		return nil, err
	}

	allAccounts, err := c.fetchAccounts(ctx, session)
	if err != nil {
		return nil, err
	}

	userTargets := c.userTargets()
	selectedAccounts := selectSub2APIAccounts(allAccounts, c.accountTargets())
	usageAccounts := selectedAccounts
	if len(c.targets) > 0 {
		usageAccounts = accountsWithUsageTarget(selectedAccounts)
	}

	accountIDs := make([]any, 0, len(usageAccounts))
	for _, account := range usageAccounts {
		if account.RawID != nil && stringify(account.RawID) != "" {
			accountIDs = append(accountIDs, account.RawID)
		}
	}

	statsByAccount, err := c.fetchTodayStats(ctx, session, accountIDs)
	if err != nil {
		return nil, err
	}

	var (
		totalTokens   float64
		totalRequests float64
		totalCost     float64
		items         []model.DataItem
		names         []string
	)

	for _, target := range userTargets {
		userStat, err := c.fetchUserTodayStats(ctx, session, target)
		if err != nil {
			return nil, err
		}
		names = append(names, userStat.UserName)
		totalTokens += userStat.Tokens
		totalRequests += userStat.Requests
		totalCost += userStat.Cost
		items = append(items, sub2apiUserTokenItem(c.Name(), userStat))
	}

	for _, account := range selectedAccounts {
		names = append(names, account.Name)
		if stats, ok := statsByAccount[account.ID]; ok {
			totalTokens += floatPath(stats, "tokens")
			totalRequests += floatPath(stats, "requests")
			totalCost += floatPath(stats, "cost")
		}

		items = append(items,
			sub2apiQuotaItem(c.Name(), account.ID, account.Name, "5H", account.Used5h, account.Reset5h),
			sub2apiQuotaItem(c.Name(), account.ID, account.Name, "Week", account.UsedWeek, account.ResetWeek),
		)
	}

	items = append([]model.DataItem{{
		Source:   c.Name(),
		Category: "token_usage",
		Title:    "今日 Token 用量",
		Value:    formatFloat(totalTokens),
		Extra: map[string]any{
			"enabled_accounts":      len(selectedAccounts),
			"enabled_account_names": names,
			"daily_requests":        totalRequests,
			"daily_cost":            totalCost,
			"target_mode":           len(c.targets) > 0,
			"matched_targets":       len(userTargets) + len(selectedAccounts),
		},
		FetchedAt: 0,
	}}, items...)

	return withFetchedAt(items), nil
}

func (c *Sub2APICollector) fetchAccounts(ctx context.Context, session *serviceSession) ([]sub2apiAccountQuota, error) {
	query := url.Values{
		"page":      {"1"},
		"page_size": {"1000"},
		"platform":  {"openai"},
		"type":      {"oauth"},
		"status":    {"active"},
	}
	accountsPayload, err := session.fetchJSON(ctx, http.MethodGet, "accounts", query, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch sub2api accounts: %w", err)
	}

	rawAccounts, ok := nestedValue(accountsPayload, "data.items")
	if !ok {
		return nil, fmt.Errorf("sub2api accounts payload missing data.items")
	}
	accountList, ok := rawAccounts.([]any)
	if !ok {
		return nil, fmt.Errorf("sub2api accounts list is not an array")
	}

	accounts := make([]sub2apiAccountQuota, 0, len(accountList))
	for _, rawAccount := range accountList {
		account, ok := rawAccount.(map[string]any)
		if !ok || !sub2apiAccountEnabled(account) {
			continue
		}
		accounts = append(accounts, newSub2APIAccountQuota(account))
	}
	return accounts, nil
}

func newSub2APIAccountQuota(account map[string]any) sub2apiAccountQuota {
	accountID := firstString(account, "id", "accountId", "account_id")
	rawID, hasRawID := account["id"]
	if !hasRawID {
		rawID = accountID
	}
	name := firstString(account, "name", "email", "accountName")
	if name == "" {
		name = accountID
	}
	if name == "" {
		name = "未命名账号"
	}

	return sub2apiAccountQuota{
		ID:        accountID,
		RawID:     rawID,
		Name:      name,
		Used5h:    floatPath(account, "extra.codex_5h_used_percent"),
		Reset5h:   firstStringValue(stringCandidate(account, "extra.codex_5h_reset_at")),
		UsedWeek:  floatPath(account, "extra.codex_7d_used_percent"),
		ResetWeek: firstStringValue(stringCandidate(account, "extra.codex_7d_reset_at")),
	}
}

func normalizeSub2APITargets(targets []config.Sub2APITarget) []config.Sub2APITarget {
	normalized := make([]config.Sub2APITarget, 0, len(targets))
	for _, target := range targets {
		target.Type = strings.ToLower(strings.TrimSpace(target.Type))
		target.ID = strings.TrimSpace(target.ID)
		target.Name = strings.TrimSpace(target.Name)
		target.Match = strings.TrimSpace(target.Match)
		target.Email = strings.TrimSpace(target.Email)
		if target.Type == "" {
			if target.Email != "" {
				target.Type = "user"
			} else {
				target.Type = "account"
			}
		}
		if target.Type == "user" && target.Email == "" && strings.Contains(target.ID, "@") {
			target.Email = target.ID
		}
		if target.Match == "" {
			switch {
			case target.Type == "user" && target.Email != "":
				target.Match = target.Email
			case target.Name != "":
				target.Match = target.Name
			case target.ID != "":
				target.Match = target.ID
			}
		}
		if target.Type == "user" {
			target.IncludeUsage = true
		}
		normalized = append(normalized, target)
	}
	return normalized
}

func (c *Sub2APICollector) accountTargets() []config.Sub2APITarget {
	if len(c.targets) == 0 {
		return nil
	}
	targets := make([]config.Sub2APITarget, 0)
	for _, target := range c.targets {
		switch target.Type {
		case "account", "gpt_account", "gpt":
			targets = append(targets, target)
		}
	}
	return targets
}

func (c *Sub2APICollector) userTargets() []config.Sub2APITarget {
	targets := make([]config.Sub2APITarget, 0)
	for _, target := range c.targets {
		if target.Type == "user" {
			targets = append(targets, target)
		}
	}
	return targets
}

func selectSub2APIAccounts(accounts []sub2apiAccountQuota, targets []config.Sub2APITarget) []sub2apiAccountQuota {
	if len(targets) == 0 {
		return accounts
	}

	selected := make([]sub2apiAccountQuota, 0, len(targets))
	seen := map[string]struct{}{}
	for _, target := range targets {
		for _, account := range accounts {
			if !sub2apiAccountMatchesTarget(account, target) {
				continue
			}
			if _, ok := seen[account.ID]; ok {
				continue
			}
			account.IncludeUsage = target.IncludeUsage
			selected = append(selected, account)
			seen[account.ID] = struct{}{}
		}
	}
	return selected
}

func sub2apiAccountMatchesTarget(account sub2apiAccountQuota, target config.Sub2APITarget) bool {
	if target.ID != "" && (stringsEqualFold(account.ID, target.ID) || stringsEqualFold(stringify(account.RawID), target.ID)) {
		return true
	}
	if target.Name != "" && stringsEqualFold(account.Name, target.Name) {
		return true
	}
	if target.Match != "" && strings.Contains(strings.ToLower(account.Name), strings.ToLower(target.Match)) {
		return true
	}
	return false
}

func accountsWithUsageTarget(accounts []sub2apiAccountQuota) []sub2apiAccountQuota {
	filtered := make([]sub2apiAccountQuota, 0, len(accounts))
	for _, account := range accounts {
		if account.IncludeUsage {
			filtered = append(filtered, account)
		}
	}
	return filtered
}

func (c *Sub2APICollector) fetchUserTodayStats(ctx context.Context, session *serviceSession, target config.Sub2APITarget) (sub2apiUserStat, error) {
	userID := strings.TrimSpace(target.ID)
	email := strings.TrimSpace(target.Email)
	name := strings.TrimSpace(target.Name)

	if (userID == "" || strings.Contains(userID, "@")) && email == "" {
		email = strings.TrimSpace(target.Match)
	}
	if userID == "" || strings.Contains(userID, "@") {
		resolvedID, resolvedEmail, err := c.resolveUser(ctx, session, firstNonEmpty(email, target.Match, name))
		if err != nil {
			return sub2apiUserStat{}, err
		}
		userID = resolvedID
		if email == "" {
			email = resolvedEmail
		}
	}
	if userID == "" {
		return sub2apiUserStat{}, fmt.Errorf("sub2api user target %q has no id/email", target.Name)
	}
	if name == "" {
		name = firstNonEmpty(email, target.Match, userID)
	}

	today := time.Now().Format("2006-01-02")
	query := url.Values{
		"user_id":    {userID},
		"period":     {"today"},
		"timezone":   {"Asia/Shanghai"},
		"start_date": {today},
		"end_date":   {today},
	}
	payload, err := session.fetchJSON(ctx, http.MethodGet, "usage_stats", query, nil)
	if err != nil {
		return sub2apiUserStat{}, fmt.Errorf("fetch sub2api user usage stats: %w", err)
	}

	return sub2apiUserStat{
		UserID:   userID,
		UserName: name,
		Email:    email,
		Tokens:   firstPathFloat(payload, "data.total_tokens", "total_tokens"),
		Requests: firstPathFloat(payload, "data.total_requests", "total_requests"),
		Cost:     firstPathFloat(payload, "data.total_actual_cost", "data.total_cost", "total_actual_cost", "total_cost"),
	}, nil
}

func (c *Sub2APICollector) resolveUser(ctx context.Context, session *serviceSession, keyword string) (string, string, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return "", "", fmt.Errorf("sub2api user search keyword is empty")
	}
	query := url.Values{"q": {keyword}}
	payload, err := session.fetchJSON(ctx, http.MethodGet, "search_users", query, nil)
	if err != nil {
		return "", "", fmt.Errorf("search sub2api users: %w", err)
	}

	rawUsers := payload
	if data, ok := nestedValue(payload, "data"); ok {
		rawUsers = data
	}
	users, ok := rawUsers.([]any)
	if !ok || len(users) == 0 {
		return "", "", fmt.Errorf("sub2api user %q not found", keyword)
	}

	var fallbackID, fallbackEmail string
	for _, rawUser := range users {
		user, ok := rawUser.(map[string]any)
		if !ok {
			continue
		}
		id := firstString(user, "id", "user_id", "userId")
		email := firstString(user, "email")
		if fallbackID == "" {
			fallbackID = id
			fallbackEmail = email
		}
		if stringsEqualFold(email, keyword) {
			return id, email, nil
		}
	}
	if fallbackID == "" {
		return "", "", fmt.Errorf("sub2api user %q not found", keyword)
	}
	return fallbackID, fallbackEmail, nil
}

func (c *Sub2APICollector) fetchTodayStats(ctx context.Context, session *serviceSession, accountIDs []any) (map[string]map[string]any, error) {
	if len(accountIDs) == 0 {
		return map[string]map[string]any{}, nil
	}

	requestBodies := []map[string]any{
		{"account_ids": accountIDs},
		{"accountIds": accountIDs},
		{"ids": accountIDs},
	}

	var lastErr error
	for _, body := range requestBodies {
		payload, err := session.fetchJSON(ctx, http.MethodPost, "today_stats", nil, body)
		if err != nil {
			lastErr = err
			continue
		}

		statsByAccount := map[string]map[string]any{}
		rawStats, ok := nestedValue(payload, "data.stats")
		if !ok {
			return statsByAccount, fmt.Errorf("sub2api today stats payload missing data.stats")
		}

		statsMap, ok := rawStats.(map[string]any)
		if !ok {
			return statsByAccount, fmt.Errorf("sub2api today stats is not an object")
		}

		for accountID, rawStat := range statsMap {
			stat, ok := rawStat.(map[string]any)
			if !ok {
				continue
			}
			statsByAccount[accountID] = stat
		}

		return statsByAccount, nil
	}

	return nil, fmt.Errorf("fetch sub2api today stats: %w", lastErr)
}

func sub2apiAccountEnabled(account map[string]any) bool {
	status := firstString(account, "status")
	if status != "" && !stringsEqualFold(status, "active") {
		return false
	}

	if schedulable, ok := account["schedulable"]; ok {
		if enabled, ok := boolValue(schedulable); ok && !enabled {
			return false
		}
	}

	return true
}

func sub2apiUserTokenItem(source string, stat sub2apiUserStat) model.DataItem {
	return model.DataItem{
		Source:   source,
		Category: "token_usage_user",
		Title:    fmt.Sprintf("%s 今日 Token 用量", stat.UserName),
		Value:    formatFloat(stat.Tokens),
		Extra: map[string]any{
			"scope":          "user",
			"user_id":        stat.UserID,
			"email":          stat.Email,
			"daily_requests": stat.Requests,
			"daily_cost":     stat.Cost,
		},
		FetchedAt: 0,
	}
}

func sub2apiQuotaItem(source, accountID, name, window string, usedPercent float64, resetAt string) model.DataItem {
	extra := map[string]any{
		"account_id":        accountID,
		"used_percent":      usedPercent,
		"remaining_percent": remainingPercent(usedPercent),
		"window":            window,
	}
	if resetAt != "" {
		extra["reset_at"] = resetAt
	}

	return model.DataItem{
		Source:    source,
		Category:  "quota",
		Title:     fmt.Sprintf("账号 %s %s 额度", name, window),
		Value:     formatPercent(remainingPercent(usedPercent)),
		Extra:     extra,
		FetchedAt: 0,
	}
}

func firstPathFloat(payload any, paths ...string) float64 {
	for _, path := range paths {
		if value, ok := nestedFloat(payload, path); ok {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
