package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"infohub/internal/config"
	"infohub/internal/model"
)

var ErrSourceNotFound = errors.New("source not found")

type Store interface {
	Save(source string, items []model.DataItem) error
	SaveFailure(source string, err error, fetchedAt time.Time) error
	GetBySource(source string) (model.SourceSnapshot, error)
	GetAll() (map[string]model.SourceSnapshot, error)
	Close() error
}

type LocalParseState struct {
	Source        string
	FilePath      string
	ByteOffset    int64
	MTimeUnix     int64
	UpdatedAt     int64
	ParserVersion int
	Reset         bool
}

type LocalUsageRecord struct {
	Source         string
	FilePath       string
	ByteOffset     int64
	At             time.Time
	Model          string
	Input          float64
	Output         float64
	CacheRead      float64
	CacheCreation  float64
	Reasoning      float64
	Total          float64
	Quota5hUsed    float64
	Quota5hReset   string
	QuotaWeekUsed  float64
	QuotaWeekReset string
}

type LocalUsageStateStore interface {
	LoadLocalParseStates(source string) (map[string]LocalParseState, error)
	SaveLocalUsageBatch(source string, states []LocalParseState, records []LocalUsageRecord) error
	ListLocalUsageRecords(source string, start time.Time, end time.Time) ([]LocalUsageRecord, error)
}

func New(cfg config.StoreConfig) (Store, error) {
	switch strings.ToLower(cfg.Type) {
	case "memory":
		return NewMemoryStore(), nil
	case "sqlite":
		return NewSQLiteStore(cfg.SQLitePath)
	default:
		return nil, fmt.Errorf("unsupported store type %q", cfg.Type)
	}
}
