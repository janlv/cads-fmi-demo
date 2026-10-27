package cadsdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PG is the PostgreSQL/TimescaleDB implementation of Store.
type PG struct {
	pool    *pgxpool.Pool
	mu      sync.Mutex
	ensured map[string]bool
}

var (
	_ Store        = (*PG)(nil)
	_ WindowReader = (*PG)(nil)
	_ SeriesReader = (*PG)(nil)
)

// Open connects to url (a libpq URL or DSN) and verifies the connection.
func Open(ctx context.Context, url string) (*PG, error) {
	if url == "" {
		return nil, fmt.Errorf("%s is not set", EnvURL)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvURL, err)
	}
	if cfg.ConnConfig.ConnectTimeout == 0 {
		cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	}
	if cfg.MaxConns > 4 {
		cfg.MaxConns = 4
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	return &PG{pool: pool, ensured: make(map[string]bool)}, nil
}

// OpenFromEnv opens the database named by CADS_DB_URL.
func OpenFromEnv(ctx context.Context) (*PG, error) {
	return Open(ctx, URLFromEnv())
}

// Close releases the connection pool.
func (p *PG) Close() {
	if p != nil && p.pool != nil {
		p.pool.Close()
	}
}

func table(site, name string) string {
	return pgx.Identifier{site, name}.Sanitize()
}

// SchemaStatements returns the idempotent DDL for one site (without the hypertable call).
func SchemaStatements(site string) []string {
	schema := pgx.Identifier{site}.Sanitize()
	channels := table(site, "channels")
	measurements := table(site, "measurements")
	runs := table(site, "runs")
	return []string{
		`CREATE SCHEMA IF NOT EXISTS ` + schema,
		`CREATE TABLE IF NOT EXISTS ` + channels + ` (
			channel text PRIMARY KEY,
			unit text NOT NULL,
			source text NOT NULL,
			sampling_interval_s double precision NOT NULL,
			batch text NOT NULL,
			ingested_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS ` + measurements + ` (
			time timestamptz NOT NULL,
			channel text NOT NULL REFERENCES ` + channels + `(channel),
			value double precision NOT NULL,
			PRIMARY KEY (time, channel)
		)`,
		`CREATE TABLE IF NOT EXISTS ` + runs + ` (
			run_id text PRIMARY KEY,
			workflow_path text NOT NULL,
			workflow_sha256 text,
			runner_version text,
			status text NOT NULL,
			started_at timestamptz,
			finished_at timestamptz,
			wall_seconds double precision,
			simulated_seconds double precision,
			provenance jsonb NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS ` + table(site, "run_outputs") + ` (
			run_id text REFERENCES ` + runs + `(run_id) ON DELETE CASCADE,
			step text NOT NULL,
			name text NOT NULL,
			value double precision,
			value_text text,
			PRIMARY KEY (run_id, step, name)
		)`,
		`CREATE TABLE IF NOT EXISTS ` + table(site, "run_series") + ` (
			run_id text REFERENCES ` + runs + `(run_id) ON DELETE CASCADE,
			step text NOT NULL,
			signal text NOT NULL,
			t double precision NOT NULL,
			value double precision,
			PRIMARY KEY (run_id, step, signal, t)
		)`,
	}
}

// EnsureSiteSchema idempotently creates the site schema and its tables, and turns
// <site>.measurements into a hypertable when the timescaledb extension is installed.
func (p *PG) EnsureSiteSchema(ctx context.Context, site string) error {
	if err := CheckSite(site); err != nil {
		return err
	}
	p.mu.Lock()
	done := p.ensured[site]
	p.mu.Unlock()
	if done {
		return nil
	}

	// When every table already exists (for example created in advance by the database
	// operator), skip the DDL: a least-privilege runtime role without CREATE rights fails
	// on CREATE ... IF NOT EXISTS even for existing objects.
	present, err := p.siteTablesExist(ctx, site)
	if err != nil {
		return fmt.Errorf("ensure schema %s: %w", site, err)
	}
	if present {
		p.mu.Lock()
		p.ensured[site] = true
		p.mu.Unlock()
		return nil
	}

	err = pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		// Serialise concurrent creators (parallel runs, ingestion) of the same site schema.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "cads-site-schema:"+site); err != nil {
			return err
		}
		for _, stmt := range SchemaStatements(site) {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return err
			}
		}
		var timescale bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')`).Scan(&timescale); err != nil {
			return err
		}
		if timescale {
			if _, err := tx.Exec(ctx, `SELECT create_hypertable($1::regclass, 'time', if_not_exists => true, migrate_data => true)`,
				site+".measurements"); err != nil {
				return fmt.Errorf("create hypertable: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("ensure schema %s: %w", site, err)
	}
	p.mu.Lock()
	p.ensured[site] = true
	p.mu.Unlock()
	return nil
}

// siteTables lists the tables SchemaStatements creates for a site.
var siteTables = []string{"channels", "measurements", "runs", "run_outputs", "run_series"}

// siteTablesExist reports whether all of a site's tables exist.
func (p *PG) siteTablesExist(ctx context.Context, site string) (bool, error) {
	names := make([]string, len(siteTables))
	for i, name := range siteTables {
		names[i] = table(site, name)
	}
	var missing int
	err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM unnest($1::text[]) AS t(name) WHERE to_regclass(name) IS NULL`, names).Scan(&missing)
	return missing == 0, err
}

func (p *PG) tableExists(ctx context.Context, site, name string) (bool, error) {
	var exists bool
	err := p.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table(site, name)).Scan(&exists)
	return exists, err
}

// ReadSeries returns the samples of channels in [from, to), ordered by time.
func (p *PG) ReadSeries(ctx context.Context, site string, channels []string, from, to time.Time) ([]Row, error) {
	if err := CheckSite(site); err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx,
		`SELECT time, channel, value FROM `+table(site, "measurements")+
			` WHERE channel = ANY($1) AND time >= $2 AND time < $3 ORDER BY time`,
		channels, from, to)
	if err != nil {
		return nil, fmt.Errorf("read %s.measurements: %w", site, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Row, error) {
		var r Row
		err := row.Scan(&r.Time, &r.Channel, &r.Value)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("read %s.measurements: %w", site, err)
	}
	return out, nil
}

// ChannelStatus lists the site's channels with their sample count and time coverage. A site
// without a schema yet has no channels.
func (p *PG) ChannelStatus(ctx context.Context, site string) ([]ChannelInfo, error) {
	if err := CheckSite(site); err != nil {
		return nil, err
	}
	for _, name := range []string{"channels", "measurements"} {
		exists, err := p.tableExists(ctx, site, name)
		if err != nil {
			return nil, err
		}
		if !exists {
			return []ChannelInfo{}, nil
		}
	}
	rows, err := p.pool.Query(ctx, `
		SELECT c.channel, c.unit, c.source, c.sampling_interval_s, c.batch, c.ingested_at,
		       COALESCE(m.points, 0), m.first_time, m.last_time
		FROM `+table(site, "channels")+` c
		LEFT JOIN (
			SELECT channel, count(*) AS points, min(time) AS first_time, max(time) AS last_time
			FROM `+table(site, "measurements")+`
			GROUP BY channel
		) m USING (channel)
		ORDER BY c.channel`)
	if err != nil {
		return nil, fmt.Errorf("read %s.channels: %w", site, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ChannelInfo, error) {
		var c ChannelInfo
		if err := row.Scan(&c.Channel, &c.Unit, &c.Source, &c.SamplingIntervalS, &c.Batch, &c.IngestedAt, &c.Points, &c.From, &c.To); err != nil {
			return c, err
		}
		c.IngestedAt = c.IngestedAt.UTC()
		for _, ts := range []*time.Time{c.From, c.To} {
			if ts != nil {
				*ts = ts.UTC()
			}
		}
		return c, nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s.channels: %w", site, err)
	}
	if out == nil {
		out = []ChannelInfo{}
	}
	return out, nil
}

// WriteRun stores one run (replacing an earlier write with the same run id). The stored
// provenance carries the same `results_sink` record the runner reports on success.
func (p *PG) WriteRun(ctx context.Context, site, runID string, results map[string]map[string]any) (*WriteSummary, error) {
	if err := CheckSite(site); err != nil {
		return nil, err
	}
	rec, err := BuildRunRecord(runID, results)
	if err != nil {
		return nil, err
	}
	summary := &WriteSummary{Site: site, RunID: runID, Outputs: len(rec.Outputs), SeriesPoints: len(rec.Series)}
	provenance := make(map[string]any, len(rec.Provenance)+1)
	for key, value := range rec.Provenance {
		provenance[key] = value
	}
	provenance["results_sink"] = summary.SinkRecord()
	provenanceJSON, err := json.Marshal(provenance)
	if err != nil {
		return nil, fmt.Errorf("encode provenance: %w", err)
	}

	if err := p.EnsureSiteSchema(ctx, site); err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM `+table(site, "runs")+` WHERE run_id = $1`, runID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+table(site, "runs")+`
			(run_id, workflow_path, workflow_sha256, runner_version, status, started_at, finished_at,
			 wall_seconds, simulated_seconds, provenance)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)`,
			rec.RunID, rec.WorkflowPath, rec.WorkflowSHA256, rec.RunnerVersion, rec.Status,
			rec.StartedAt, rec.FinishedAt, rec.WallSeconds, rec.SimulatedSeconds, string(provenanceJSON)); err != nil {
			return err
		}
		if len(rec.Outputs) > 0 {
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{site, "run_outputs"},
				[]string{"run_id", "step", "name", "value", "value_text"},
				pgx.CopyFromSlice(len(rec.Outputs), func(i int) ([]any, error) {
					o := rec.Outputs[i]
					return []any{runID, o.Step, o.Name, o.Value, o.ValueText}, nil
				})); err != nil {
				return fmt.Errorf("copy run_outputs: %w", err)
			}
		}
		if len(rec.Series) > 0 {
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{site, "run_series"},
				[]string{"run_id", "step", "signal", "t", "value"},
				pgx.CopyFromSlice(len(rec.Series), func(i int) ([]any, error) {
					s := rec.Series[i]
					return []any{runID, s.Step, s.Signal, s.T, s.Value}, nil
				})); err != nil {
				return fmt.Errorf("copy run_series: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("write run %s to %s: %w", runID, site, err)
	}
	return summary, nil
}

// SinkRecord is the `_run.results_sink` value for a successful write.
func (s *WriteSummary) SinkRecord() map[string]any {
	return map[string]any{
		"status":        "written",
		"site":          s.Site,
		"run_id":        s.RunID,
		"outputs":       s.Outputs,
		"series_points": s.SeriesPoints,
	}
}

// ReadRun rebuilds the runner result map of runID (ErrRunNotFound when absent).
func (p *PG) ReadRun(ctx context.Context, site, runID string) (map[string]map[string]any, error) {
	if err := CheckSite(site); err != nil {
		return nil, err
	}
	exists, err := p.tableExists(ctx, site, "runs")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrRunNotFound
	}
	var provenanceJSON []byte
	err = p.pool.QueryRow(ctx, `SELECT provenance FROM `+table(site, "runs")+` WHERE run_id = $1`, runID).Scan(&provenanceJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read run %s: %w", runID, err)
	}
	var provenance map[string]any
	if err := json.Unmarshal(provenanceJSON, &provenance); err != nil {
		return nil, fmt.Errorf("decode provenance of %s: %w", runID, err)
	}

	rows, err := p.pool.Query(ctx, `SELECT step, name, value, value_text FROM `+table(site, "run_outputs")+
		` WHERE run_id = $1 ORDER BY step, name`, runID)
	if err != nil {
		return nil, fmt.Errorf("read run outputs %s: %w", runID, err)
	}
	outputs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OutputRow, error) {
		var o OutputRow
		err := row.Scan(&o.Step, &o.Name, &o.Value, &o.ValueText)
		return o, err
	})
	if err != nil {
		return nil, fmt.Errorf("read run outputs %s: %w", runID, err)
	}

	rows, err = p.pool.Query(ctx, `SELECT step, signal, t, value FROM `+table(site, "run_series")+
		` WHERE run_id = $1 ORDER BY step, signal, t`, runID)
	if err != nil {
		return nil, fmt.Errorf("read run series %s: %w", runID, err)
	}
	series, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (SeriesRow, error) {
		var s SeriesRow
		err := row.Scan(&s.Step, &s.Signal, &s.T, &s.Value)
		return s, err
	})
	if err != nil {
		return nil, fmt.Errorf("read run series %s: %w", runID, err)
	}
	return AssembleRun(provenance, outputs, series), nil
}

// ReadSeriesBinned returns the samples of channels in [from, to) averaged into buckets of
// binSeconds aligned to from (Row.Time is the bucket start), ordered by bucket and channel.
// binSeconds <= 0 returns the raw samples (ReadSeries).
func (p *PG) ReadSeriesBinned(ctx context.Context, site string, channels []string, from, to time.Time, binSeconds float64) ([]Row, error) {
	if binSeconds <= 0 {
		return p.ReadSeries(ctx, site, channels, from, to)
	}
	if err := CheckSite(site); err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx,
		`SELECT date_bin(make_interval(secs => $4::double precision), time, $2) AS bucket, channel, avg(value)
		 FROM `+table(site, "measurements")+`
		 WHERE channel = ANY($1) AND time >= $2 AND time < $3
		 GROUP BY bucket, channel ORDER BY bucket, channel`,
		channels, from, to, binSeconds)
	if err != nil {
		return nil, fmt.Errorf("read %s.measurements: %w", site, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Row, error) {
		var r Row
		err := row.Scan(&r.Time, &r.Channel, &r.Value)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("read %s.measurements: %w", site, err)
	}
	return out, nil
}

// ListRuns returns up to limit stored runs (all when limit <= 0), newest first by start time
// (finish time when the start is unknown). A site without a runs table has no runs.
func (p *PG) ListRuns(ctx context.Context, site string, limit int) ([]StoredRun, error) {
	if err := CheckSite(site); err != nil {
		return nil, err
	}
	exists, err := p.tableExists(ctx, site, "runs")
	if err != nil {
		return nil, err
	}
	if !exists {
		return []StoredRun{}, nil
	}
	var limitArg *int
	if limit > 0 {
		limitArg = &limit
	}
	rows, err := p.pool.Query(ctx, `SELECT run_id, workflow_path, status, started_at, finished_at,
			wall_seconds, simulated_seconds, provenance
		FROM `+table(site, "runs")+`
		ORDER BY COALESCE(started_at, finished_at) DESC NULLS LAST, run_id DESC
		LIMIT $1`, limitArg)
	if err != nil {
		return nil, fmt.Errorf("list %s.runs: %w", site, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (StoredRun, error) {
		var r StoredRun
		var provenanceJSON []byte
		if err := row.Scan(&r.RunID, &r.WorkflowPath, &r.Status, &r.StartedAt, &r.FinishedAt,
			&r.WallSeconds, &r.SimulatedSeconds, &provenanceJSON); err != nil {
			return r, err
		}
		for _, ts := range []*time.Time{r.StartedAt, r.FinishedAt} {
			if ts != nil {
				*ts = ts.UTC()
			}
		}
		if err := json.Unmarshal(provenanceJSON, &r.Provenance); err != nil {
			return r, fmt.Errorf("decode provenance of %s: %w", r.RunID, err)
		}
		return r, nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s.runs: %w", site, err)
	}
	if out == nil {
		out = []StoredRun{}
	}
	return out, nil
}

// ReadRunOutputs returns the scalar outputs of runIDs ordered by run, step and name.
func (p *PG) ReadRunOutputs(ctx context.Context, site string, runIDs []string) ([]OutputRow, error) {
	if err := CheckSite(site); err != nil {
		return nil, err
	}
	if len(runIDs) == 0 {
		return nil, nil
	}
	exists, err := p.tableExists(ctx, site, "run_outputs")
	if err != nil || !exists {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT run_id, step, name, value, value_text FROM `+table(site, "run_outputs")+
		` WHERE run_id = ANY($1) ORDER BY run_id, step, name`, runIDs)
	if err != nil {
		return nil, fmt.Errorf("read %s.run_outputs: %w", site, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OutputRow, error) {
		var o OutputRow
		err := row.Scan(&o.RunID, &o.Step, &o.Name, &o.Value, &o.ValueText)
		return o, err
	})
	if err != nil {
		return nil, fmt.Errorf("read %s.run_outputs: %w", site, err)
	}
	return out, nil
}

// ReadRunSeries returns the stored trace samples of runIDs restricted to steps and signals,
// ordered by run, step, signal and time. Any empty filter yields no rows.
func (p *PG) ReadRunSeries(ctx context.Context, site string, runIDs, steps, signals []string) ([]RunSeriesRow, error) {
	if err := CheckSite(site); err != nil {
		return nil, err
	}
	if len(runIDs) == 0 || len(steps) == 0 || len(signals) == 0 {
		return nil, nil
	}
	exists, err := p.tableExists(ctx, site, "run_series")
	if err != nil || !exists {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT run_id, step, signal, t, value FROM `+table(site, "run_series")+
		` WHERE run_id = ANY($1) AND step = ANY($2) AND signal = ANY($3) ORDER BY run_id, step, signal, t`,
		runIDs, steps, signals)
	if err != nil {
		return nil, fmt.Errorf("read %s.run_series: %w", site, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (RunSeriesRow, error) {
		var s RunSeriesRow
		err := row.Scan(&s.RunID, &s.Step, &s.Signal, &s.T, &s.Value)
		return s, err
	})
	if err != nil {
		return nil, fmt.Errorf("read %s.run_series: %w", site, err)
	}
	return out, nil
}

// LatestSamples returns the newest sample time of each channel that has samples.
func (p *PG) LatestSamples(ctx context.Context, site string, channels []string) (map[string]time.Time, error) {
	if err := CheckSite(site); err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(channels))
	if len(channels) == 0 {
		return out, nil
	}
	exists, err := p.tableExists(ctx, site, "measurements")
	if err != nil {
		return nil, err
	}
	if !exists {
		return out, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT c.channel, m.time
		FROM unnest($1::text[]) AS c(channel)
		LEFT JOIN LATERAL (
			SELECT time FROM `+table(site, "measurements")+`
			WHERE channel = c.channel ORDER BY time DESC LIMIT 1
		) m ON true`, channels)
	if err != nil {
		return nil, fmt.Errorf("read latest samples of %s: %w", site, err)
	}
	defer rows.Close()
	for rows.Next() {
		var channel string
		var latest *time.Time
		if err := rows.Scan(&channel, &latest); err != nil {
			return nil, fmt.Errorf("read latest samples of %s: %w", site, err)
		}
		if latest != nil {
			out[channel] = latest.UTC()
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read latest samples of %s: %w", site, err)
	}
	return out, nil
}

// LatestSucceededRun returns the succeeded run of workflowPath whose recorded data window has
// the given mode and ends last (ties: newest finish), with its scalar outputs; nil, nil when
// there is none.
func (p *PG) LatestSucceededRun(ctx context.Context, site, workflowPath, mode string) (*PreviousRun, error) {
	if err := CheckSite(site); err != nil {
		return nil, err
	}
	exists, err := p.tableExists(ctx, site, "runs")
	if err != nil || !exists {
		return nil, err
	}
	var run PreviousRun
	var windowJSON []byte
	err = p.pool.QueryRow(ctx, `SELECT run_id, finished_at, provenance->'data_window'
		FROM `+table(site, "runs")+`
		WHERE workflow_path = $1 AND status = 'succeeded'
		  AND provenance->'data_window'->>'mode' = $2
		  AND provenance->'data_window'->>'to' IS NOT NULL
		ORDER BY (provenance->'data_window'->>'to')::timestamptz DESC, finished_at DESC NULLS LAST
		LIMIT 1`, workflowPath, mode).Scan(&run.RunID, &run.FinishedAt, &windowJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read previous run of %s: %w", workflowPath, err)
	}
	if run.FinishedAt != nil {
		*run.FinishedAt = run.FinishedAt.UTC()
	}
	var window map[string]any
	if err := json.Unmarshal(windowJSON, &window); err != nil {
		return nil, fmt.Errorf("decode data_window of %s: %w", run.RunID, err)
	}
	run.Mode, _ = window["mode"].(string)
	if from := parseTimestamp(window["from"]); from != nil {
		run.From = from.UTC()
	}
	to := parseTimestamp(window["to"])
	if to == nil {
		return nil, fmt.Errorf("data_window.to of %s is not an RFC 3339 time: %v", run.RunID, window["to"])
	}
	run.To = to.UTC()

	outputs, err := p.ReadRunOutputs(ctx, site, []string{run.RunID})
	if err != nil {
		return nil, err
	}
	run.Outputs = AssembleRun(nil, outputs, nil)
	delete(run.Outputs, runInfoStep)
	return &run, nil
}
