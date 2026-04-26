package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"infohub/internal/model"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db     *sql.DB
	memory *MemoryStore
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	store := &SQLiteStore{
		db:     db,
		memory: NewMemoryStore(),
	}

	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.loadSnapshots(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *SQLiteStore) Save(source string, items []model.DataItem) error {
	lastFetch := resolveLastFetch(items)
	if lastFetch == 0 {
		lastFetch = time.Now().Unix()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO source_state(source, status, last_fetch, error)
		VALUES (?, 'ok', ?, '')
		ON CONFLICT(source) DO UPDATE SET status='ok', last_fetch=excluded.last_fetch, error=''
	`, source, lastFetch); err != nil {
		return fmt.Errorf("upsert source state: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM source_items WHERE source = ?`, source); err != nil {
		return fmt.Errorf("delete old items: %w", err)
	}

	for _, item := range items {
		extraJSON, err := json.Marshal(item.Extra)
		if err != nil {
			return fmt.Errorf("marshal extra: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO source_items(source, category, title, value, extra_json, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, source, item.Category, item.Title, item.Value, string(extraJSON), item.FetchedAt); err != nil {
			return fmt.Errorf("insert source item: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite tx: %w", err)
	}

	return s.memory.Save(source, items)
}

func (s *SQLiteStore) SaveFailure(source string, err error, fetchedAt time.Time) error {
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	message := ""
	if err != nil {
		message = err.Error()
	}

	if _, execErr := s.db.Exec(`
		INSERT INTO source_state(source, status, last_fetch, error)
		VALUES (?, 'error', ?, ?)
		ON CONFLICT(source) DO UPDATE SET status='error', last_fetch=excluded.last_fetch, error=excluded.error
	`, source, fetchedAt.Unix(), message); execErr != nil {
		return fmt.Errorf("persist source failure: %w", execErr)
	}

	return s.memory.SaveFailure(source, err, fetchedAt)
}

func (s *SQLiteStore) GetBySource(source string) (model.SourceSnapshot, error) {
	return s.memory.GetBySource(source)
}

func (s *SQLiteStore) GetAll() (map[string]model.SourceSnapshot, error) {
	return s.memory.GetAll()
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) initSchema() error {
	const schema = `
CREATE TABLE IF NOT EXISTS source_state (
	source TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	last_fetch INTEGER NOT NULL,
	error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS source_items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	source TEXT NOT NULL,
	category TEXT NOT NULL,
	title TEXT NOT NULL,
	value TEXT NOT NULL,
	extra_json TEXT NOT NULL DEFAULT '{}',
	fetched_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS local_parse_state (
	source TEXT NOT NULL,
	file_path TEXT NOT NULL,
	byte_offset INTEGER NOT NULL,
	mtime_unix INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	parser_version INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (source, file_path)
);

CREATE TABLE IF NOT EXISTS local_usage_events (
	source TEXT NOT NULL,
	file_path TEXT NOT NULL,
	byte_offset INTEGER NOT NULL,
	at_unix INTEGER NOT NULL,
	model TEXT NOT NULL,
	input REAL NOT NULL,
	output REAL NOT NULL,
	cache_read REAL NOT NULL,
	cache_creation REAL NOT NULL,
	reasoning REAL NOT NULL,
	total REAL NOT NULL DEFAULT 0,
	quota_5h_used_percent REAL NOT NULL DEFAULT -1,
	quota_5h_reset_at TEXT NOT NULL DEFAULT '',
	quota_week_used_percent REAL NOT NULL DEFAULT -1,
	quota_week_reset_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (source, file_path, byte_offset)
);

CREATE INDEX IF NOT EXISTS idx_local_usage_events_source_at
	ON local_usage_events(source, at_unix);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("init sqlite schema: %w", err)
	}
	if err := s.ensureLocalParseStateColumns(); err != nil {
		return err
	}
	if err := s.ensureLocalUsageColumns(); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) ensureLocalUsageColumns() error {
	columns, err := s.localUsageColumns()
	if err != nil {
		return err
	}
	ddl := map[string]string{
		"quota_5h_used_percent":   `ALTER TABLE local_usage_events ADD COLUMN quota_5h_used_percent REAL NOT NULL DEFAULT -1`,
		"quota_5h_reset_at":       `ALTER TABLE local_usage_events ADD COLUMN quota_5h_reset_at TEXT NOT NULL DEFAULT ''`,
		"quota_week_used_percent": `ALTER TABLE local_usage_events ADD COLUMN quota_week_used_percent REAL NOT NULL DEFAULT -1`,
		"quota_week_reset_at":     `ALTER TABLE local_usage_events ADD COLUMN quota_week_reset_at TEXT NOT NULL DEFAULT ''`,
		"total":                   `ALTER TABLE local_usage_events ADD COLUMN total REAL NOT NULL DEFAULT 0`,
	}
	for column, statement := range ddl {
		if columns[column] {
			continue
		}
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("add local usage column %s: %w", column, err)
		}
	}
	return nil
}

func (s *SQLiteStore) ensureLocalParseStateColumns() error {
	columns, err := s.tableColumns("local_parse_state")
	if err != nil {
		return err
	}
	if columns["parser_version"] {
		return nil
	}
	if _, err := s.db.Exec(`ALTER TABLE local_parse_state ADD COLUMN parser_version INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add local parse state parser_version column: %w", err)
	}
	return nil
}

func (s *SQLiteStore) localUsageColumns() (map[string]bool, error) {
	return s.tableColumns("local_usage_events")
}

func (s *SQLiteStore) tableColumns(table string) (map[string]bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal any
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return nil, fmt.Errorf("scan %s column: %w", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s columns: %w", table, err)
	}
	return columns, nil
}

func (s *SQLiteStore) loadSnapshots() error {
	states, err := s.loadStates()
	if err != nil {
		return err
	}

	rows, err := s.db.Query(`
		SELECT source, category, title, value, extra_json, fetched_at
		FROM source_items
		ORDER BY id ASC
	`)
	if err != nil {
		return fmt.Errorf("query source items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			source    string
			category  string
			title     string
			value     string
			extraJSON string
			fetchedAt int64
		)
		if err := rows.Scan(&source, &category, &title, &value, &extraJSON, &fetchedAt); err != nil {
			return fmt.Errorf("scan source item: %w", err)
		}

		item := model.DataItem{
			Source:    source,
			Category:  category,
			Title:     title,
			Value:     value,
			FetchedAt: fetchedAt,
		}
		if extraJSON != "" && extraJSON != "null" {
			var extra map[string]any
			if err := json.Unmarshal([]byte(extraJSON), &extra); err == nil && len(extra) > 0 {
				item.Extra = extra
			}
		}

		snapshot := states[source]
		snapshot.Items = append(snapshot.Items, item)
		states[source] = snapshot
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate source items: %w", err)
	}

	s.memory.mu.Lock()
	defer s.memory.mu.Unlock()
	s.memory.sources = states
	return nil
}

func (s *SQLiteStore) loadStates() (map[string]model.SourceSnapshot, error) {
	rows, err := s.db.Query(`SELECT source, status, last_fetch, error FROM source_state`)
	if err != nil {
		return nil, fmt.Errorf("query source states: %w", err)
	}
	defer rows.Close()

	states := make(map[string]model.SourceSnapshot)
	for rows.Next() {
		var (
			source    string
			status    string
			lastFetch int64
			errorText string
		)
		if err := rows.Scan(&source, &status, &lastFetch, &errorText); err != nil {
			return nil, fmt.Errorf("scan source state: %w", err)
		}

		states[source] = model.SourceSnapshot{
			Status:    status,
			LastFetch: lastFetch,
			Error:     errorText,
			Items:     []model.DataItem{},
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source states: %w", err)
	}

	return states, nil
}

func (s *SQLiteStore) LoadLocalParseStates(source string) (map[string]LocalParseState, error) {
	rows, err := s.db.Query(`
		SELECT source, file_path, byte_offset, mtime_unix, updated_at, parser_version
		FROM local_parse_state
		WHERE source = ?
	`, source)
	if err != nil {
		return nil, fmt.Errorf("query local parse states: %w", err)
	}
	defer rows.Close()

	states := map[string]LocalParseState{}
	for rows.Next() {
		var state LocalParseState
		if err := rows.Scan(&state.Source, &state.FilePath, &state.ByteOffset, &state.MTimeUnix, &state.UpdatedAt, &state.ParserVersion); err != nil {
			return nil, fmt.Errorf("scan local parse state: %w", err)
		}
		states[state.FilePath] = state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate local parse states: %w", err)
	}
	return states, nil
}

func (s *SQLiteStore) SaveLocalUsageBatch(source string, states []LocalParseState, records []LocalUsageRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin local usage tx: %w", err)
	}
	defer tx.Rollback()

	for _, state := range states {
		if state.Source == "" {
			state.Source = source
		}
		if state.Reset {
			if _, err := tx.Exec(`DELETE FROM local_usage_events WHERE source = ? AND file_path = ?`, state.Source, state.FilePath); err != nil {
				return fmt.Errorf("delete reset local usage events: %w", err)
			}
		}
		if _, err := tx.Exec(`
			INSERT INTO local_parse_state(source, file_path, byte_offset, mtime_unix, updated_at, parser_version)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(source, file_path) DO UPDATE SET
				byte_offset=excluded.byte_offset,
				mtime_unix=excluded.mtime_unix,
				updated_at=excluded.updated_at,
				parser_version=excluded.parser_version
		`, state.Source, state.FilePath, state.ByteOffset, state.MTimeUnix, state.UpdatedAt, state.ParserVersion); err != nil {
			return fmt.Errorf("upsert local parse state: %w", err)
		}
	}

	for _, record := range records {
		if record.Source == "" {
			record.Source = source
		}
		if _, err := tx.Exec(`
			INSERT INTO local_usage_events(
				source, file_path, byte_offset, at_unix, model,
				input, output, cache_read, cache_creation, reasoning,
				total,
				quota_5h_used_percent, quota_5h_reset_at,
				quota_week_used_percent, quota_week_reset_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source, file_path, byte_offset) DO UPDATE SET
				at_unix=excluded.at_unix,
				model=excluded.model,
				input=excluded.input,
				output=excluded.output,
				cache_read=excluded.cache_read,
				cache_creation=excluded.cache_creation,
				reasoning=excluded.reasoning,
				total=excluded.total,
				quota_5h_used_percent=excluded.quota_5h_used_percent,
				quota_5h_reset_at=excluded.quota_5h_reset_at,
				quota_week_used_percent=excluded.quota_week_used_percent,
				quota_week_reset_at=excluded.quota_week_reset_at
		`, record.Source, record.FilePath, record.ByteOffset, record.At.Unix(), record.Model,
			record.Input, record.Output, record.CacheRead, record.CacheCreation, record.Reasoning,
			record.Total, record.Quota5hUsed, record.Quota5hReset, record.QuotaWeekUsed, record.QuotaWeekReset); err != nil {
			return fmt.Errorf("upsert local usage event: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit local usage tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListLocalUsageRecords(source string, start time.Time, end time.Time) ([]LocalUsageRecord, error) {
	rows, err := s.db.Query(`
		SELECT source, file_path, byte_offset, at_unix, model,
			input, output, cache_read, cache_creation, reasoning,
			total,
			quota_5h_used_percent, quota_5h_reset_at,
			quota_week_used_percent, quota_week_reset_at
		FROM local_usage_events
		WHERE source = ? AND at_unix >= ? AND at_unix < ?
		ORDER BY at_unix ASC, file_path ASC, byte_offset ASC
	`, source, start.Unix(), end.Unix())
	if err != nil {
		return nil, fmt.Errorf("query local usage records: %w", err)
	}
	defer rows.Close()

	var records []LocalUsageRecord
	for rows.Next() {
		var (
			record LocalUsageRecord
			atUnix int64
		)
		if err := rows.Scan(
			&record.Source,
			&record.FilePath,
			&record.ByteOffset,
			&atUnix,
			&record.Model,
			&record.Input,
			&record.Output,
			&record.CacheRead,
			&record.CacheCreation,
			&record.Reasoning,
			&record.Total,
			&record.Quota5hUsed,
			&record.Quota5hReset,
			&record.QuotaWeekUsed,
			&record.QuotaWeekReset,
		); err != nil {
			return nil, fmt.Errorf("scan local usage record: %w", err)
		}
		record.At = time.Unix(atUnix, 0)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate local usage records: %w", err)
	}
	return records, nil
}
