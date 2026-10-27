// Package cadsdb is the CADS Data interface backed by PostgreSQL/TimescaleDB (D3.5 ARCH-DATA-007,
// ARCH-DATA-009, ARCH-DATA-010, ARCH-COMP-007). Each demonstrator site owns one PostgreSQL schema
// named by its site id holding ingested measurements and the results written back by runs.
//
// The schema created by EnsureSiteSchema is shared with the ingestion scripts and must stay
// exactly in sync with them.
package cadsdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// EnvURL is the environment variable holding the database connection string.
const EnvURL = "CADS_DB_URL"

// ErrRunNotFound is returned by ReadRun when the run has no row in <site>.runs.
var ErrRunNotFound = errors.New("run not found in database")

var siteIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// ValidSite reports whether site can be used verbatim as a PostgreSQL schema name.
func ValidSite(site string) bool {
	return siteIdentifier.MatchString(site)
}

// CheckSite returns an error when site is not a usable schema identifier.
func CheckSite(site string) error {
	if !ValidSite(site) {
		return fmt.Errorf("site %q must be a lowercase identifier ([a-z][a-z0-9_]*)", site)
	}
	return nil
}

// URLFromEnv returns the trimmed CADS_DB_URL (empty when unset).
func URLFromEnv() string {
	return strings.TrimSpace(os.Getenv(EnvURL))
}

// Row is one measurement sample.
type Row struct {
	Time    time.Time
	Channel string
	Value   float64
}

// ChannelInfo summarises one ingested channel of a site.
type ChannelInfo struct {
	Channel           string     `json:"channel"`
	Unit              string     `json:"unit"`
	Source            string     `json:"source"`
	SamplingIntervalS float64    `json:"samplingIntervalS"`
	Points            int64      `json:"points"`
	From              *time.Time `json:"from"`
	To                *time.Time `json:"to"`
	IngestedAt        time.Time  `json:"ingestedAt"`
	Batch             string     `json:"batch"`
}

// WriteSummary reports what WriteRun stored.
type WriteSummary struct {
	Site         string `json:"site"`
	RunID        string `json:"run_id"`
	Outputs      int    `json:"outputs"`
	SeriesPoints int    `json:"series_points"`
}

// SeriesReader is the read side used by the workflow executor for `input_series.db`.
type SeriesReader interface {
	ReadSeries(ctx context.Context, site string, channels []string, from, to time.Time) ([]Row, error)
}

// StoredRun is one <site>.runs row as listed by ListRuns.
type StoredRun struct {
	RunID            string
	WorkflowPath     string
	Status           string
	StartedAt        *time.Time
	FinishedAt       *time.Time
	WallSeconds      *float64
	SimulatedSeconds *float64
	// Provenance is the stored `_run` record (decoded jsonb).
	Provenance map[string]any
}

// PreviousRun is the newest succeeded run of a workflow that recorded a data window, as used
// to continue assessments from where the previous one stopped.
type PreviousRun struct {
	RunID      string
	Mode       string
	From, To   time.Time
	FinishedAt *time.Time
	// Outputs has the runner result shape: step -> flattened output name -> value (scalar
	// outputs only; no traces, no `_run`).
	Outputs map[string]map[string]any
}

// WindowReader is the read side the executor uses to resolve `data_window` assessment windows
// and `carry_over` state.
type WindowReader interface {
	SeriesReader
	// LatestSamples returns the newest sample time per channel. Channels without samples are
	// absent from the map; a site without measurements yields an empty map.
	LatestSamples(ctx context.Context, site string, channels []string) (map[string]time.Time, error)
	// LatestSucceededRun returns the succeeded run of workflowPath whose data window (of the
	// given mode) ends last, or nil, nil when there is none.
	LatestSucceededRun(ctx context.Context, site, workflowPath, mode string) (*PreviousRun, error)
}

// Store is the full CADS Data interface.
type Store interface {
	WindowReader
	ReadSeriesBinned(ctx context.Context, site string, channels []string, from, to time.Time, binSeconds float64) ([]Row, error)
	ListRuns(ctx context.Context, site string, limit int) ([]StoredRun, error)
	ReadRunOutputs(ctx context.Context, site string, runIDs []string) ([]OutputRow, error)
	ReadRunSeries(ctx context.Context, site string, runIDs, steps, signals []string) ([]RunSeriesRow, error)
	EnsureSiteSchema(ctx context.Context, site string) error
	ChannelStatus(ctx context.Context, site string) ([]ChannelInfo, error)
	WriteRun(ctx context.Context, site, runID string, results map[string]map[string]any) (*WriteSummary, error)
	ReadRun(ctx context.Context, site, runID string) (map[string]map[string]any, error)
	Close()
}
