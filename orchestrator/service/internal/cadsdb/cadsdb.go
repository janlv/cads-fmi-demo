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

// Store is the full CADS Data interface.
type Store interface {
	SeriesReader
	EnsureSiteSchema(ctx context.Context, site string) error
	ChannelStatus(ctx context.Context, site string) ([]ChannelInfo, error)
	WriteRun(ctx context.Context, site, runID string, results map[string]map[string]any) (*WriteSummary, error)
	ReadRun(ctx context.Context, site, runID string) (map[string]map[string]any, error)
	Close()
}
