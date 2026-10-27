//go:build dbintegration

package cadsdb

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Run with:
//
//	CADS_DB_URL=postgres://cads:cads@127.0.0.1:5432/storhy CGO_ENABLED=0 GOWORK=off \
//	  go test -tags dbintegration ./internal/cadsdb -v
func TestPostgresRoundTrip(t *testing.T) {
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Skip("CADS_DB_URL not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(store.Close) // registered first, so it runs after the schema drop below

	site := fmt.Sprintf("cadsit_%d", time.Now().UnixNano()%1_000_000_000)
	t.Cleanup(func() {
		if _, err := store.pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+site+` CASCADE`); err != nil {
			t.Logf("drop schema %s: %v", site, err)
		}
	})

	if err := store.EnsureSiteSchema(ctx, site); err != nil {
		t.Fatalf("EnsureSiteSchema() error = %v", err)
	}
	// Idempotent, also from a fresh connection (no cache).
	other, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.EnsureSiteSchema(ctx, site); err != nil {
		t.Fatalf("second EnsureSiteSchema() error = %v", err)
	}
	other.Close()

	var timescale bool
	if err := store.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')`).Scan(&timescale); err != nil {
		t.Fatal(err)
	}
	if timescale {
		var hyper bool
		if err := store.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM timescaledb_information.hypertables WHERE hypertable_schema = $1 AND hypertable_name = 'measurements')`, site).Scan(&hyper); err != nil {
			t.Fatal(err)
		}
		if !hyper {
			t.Fatal("measurements is not a hypertable")
		}
	}

	status, err := store.ChannelStatus(ctx, "cadsit_missing_site")
	if err != nil || len(status) != 0 {
		t.Fatalf("ChannelStatus(missing) = %v, %v; want empty", status, err)
	}

	from := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	for _, ch := range []struct{ name, unit string }{{"vibration_rms_mm_s", "mm/s"}, {"bearing_temp_c", "degC"}} {
		if _, err := store.pool.Exec(ctx, `INSERT INTO `+site+`.channels (channel, unit, source, sampling_interval_s, batch) VALUES ($1, $2, 'synthetic', 600, 'it-batch')`, ch.name, ch.unit); err != nil {
			t.Fatal(err)
		}
	}
	for i := -2; i < 10; i++ {
		ts := from.Add(time.Duration(i) * 10 * time.Minute)
		if _, err := store.pool.Exec(ctx, `INSERT INTO `+site+`.measurements (time, channel, value) VALUES ($1, 'vibration_rms_mm_s', $2), ($1, 'bearing_temp_c', $3)`, ts, float64(i), 40+float64(i)/10); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.ReadSeries(ctx, site, []string{"vibration_rms_mm_s"}, from, from.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("ReadSeries() error = %v", err)
	}
	if len(rows) != 3 || !rows[0].Time.Equal(from) || rows[0].Value != 0 || rows[2].Value != 2 || rows[0].Channel != "vibration_rms_mm_s" {
		t.Fatalf("ReadSeries() = %+v, want 3 samples from window start", rows)
	}
	both, err := store.ReadSeries(ctx, site, []string{"vibration_rms_mm_s", "bearing_temp_c"}, from, from.Add(time.Hour))
	if err != nil || len(both) != 12 {
		t.Fatalf("ReadSeries(both) = %d rows, %v; want 12", len(both), err)
	}

	status, err = store.ChannelStatus(ctx, site)
	if err != nil {
		t.Fatalf("ChannelStatus() error = %v", err)
	}
	if len(status) != 2 || status[0].Channel != "bearing_temp_c" || status[0].Unit != "degC" || status[0].Points != 12 ||
		status[0].From == nil || !status[0].From.Equal(from.Add(-20*time.Minute)) || status[0].To == nil ||
		!status[0].To.Equal(from.Add(90*time.Minute)) || status[0].Batch != "it-batch" || status[0].SamplingIntervalS != 600 ||
		status[0].IngestedAt.IsZero() {
		t.Fatalf("ChannelStatus() = %+v", status)
	}

	results := map[string]map[string]any{
		"monitor": {
			"rul_days": 42.5,
			"alarm":    true,
			"label":    "ok",
			"trace": map[string]any{
				"time":    []float64{0, 600, 1200},
				"signals": map[string]any{"vib": []float64{1, 2, 3}},
			},
		},
		"_run": {
			"status":         "succeeded",
			"started_at":     "2026-10-08T10:00:00Z",
			"finished_at":    "2026-10-08T10:00:03Z",
			"wall_seconds":   3.0,
			"runner_version": "it",
			"workflow":       map[string]any{"path": "workflows/demo.yaml", "sha256": "abc", "site": site},
		},
	}
	for attempt := 0; attempt < 2; attempt++ { // a second write replaces the first
		summary, err := store.WriteRun(ctx, site, "cads-it-1", results)
		if err != nil {
			t.Fatalf("WriteRun() error = %v", err)
		}
		if summary.Outputs != 3 || summary.SeriesPoints != 3 || summary.Site != site || summary.RunID != "cads-it-1" {
			t.Fatalf("WriteRun() summary = %+v", summary)
		}
	}

	back, err := store.ReadRun(ctx, site, "cads-it-1")
	if err != nil {
		t.Fatalf("ReadRun() error = %v", err)
	}
	if back["monitor"]["rul_days"] != 42.5 || back["monitor"]["alarm"] != 1.0 || back["monitor"]["label"] != "ok" {
		t.Fatalf("ReadRun() outputs = %v", back["monitor"])
	}
	trace, ok := back["monitor"]["trace"].(map[string]any)
	if !ok || fmt.Sprint(trace["time"]) != "[0 600 1200]" || fmt.Sprint(trace["signals"].(map[string]any)["vib"]) != "[1 2 3]" {
		t.Fatalf("ReadRun() trace = %v", back["monitor"]["trace"])
	}
	sink, ok := back["_run"]["results_sink"].(map[string]any)
	if back["_run"]["status"] != "succeeded" || !ok || sink["status"] != "written" || sink["series_points"] != 3.0 {
		t.Fatalf("ReadRun() _run = %v", back["_run"])
	}
	var status2 string
	if err := store.pool.QueryRow(ctx, `SELECT status FROM `+site+`.runs WHERE run_id = 'cads-it-1'`).Scan(&status2); err != nil || status2 != "succeeded" {
		t.Fatalf("runs.status = %q, %v", status2, err)
	}

	if _, err := store.ReadRun(ctx, site, "missing"); err != ErrRunNotFound {
		t.Fatalf("ReadRun(missing) error = %v, want ErrRunNotFound", err)
	}
}

func openThrowawaySite(t *testing.T) (*PG, string) {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Skip("CADS_DB_URL not set")
	}
	store, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(store.Close)
	site := fmt.Sprintf("cadsit_%d", time.Now().UnixNano()%1_000_000_000)
	t.Cleanup(func() {
		if _, err := store.pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+site+` CASCADE`); err != nil {
			t.Logf("drop schema %s: %v", site, err)
		}
	})
	return store, site
}

func TestPostgresHistoryAndWindowReads(t *testing.T) {
	store, site := openThrowawaySite(t)
	ctx := context.Background()

	// Before the schema exists: empty results, no errors.
	if runs, err := store.ListRuns(ctx, site, 10); err != nil || runs == nil || len(runs) != 0 {
		t.Fatalf("ListRuns(missing table) = %v, %v; want empty slice", runs, err)
	}
	if latest, err := store.LatestSamples(ctx, site, []string{"a"}); err != nil || len(latest) != 0 {
		t.Fatalf("LatestSamples(missing table) = %v, %v; want empty", latest, err)
	}
	if prev, err := store.LatestSucceededRun(ctx, site, "workflows/w.yaml", "since_previous_run"); err != nil || prev != nil {
		t.Fatalf("LatestSucceededRun(missing table) = %v, %v; want nil, nil", prev, err)
	}

	if err := store.EnsureSiteSchema(ctx, site); err != nil {
		t.Fatalf("EnsureSiteSchema() error = %v", err)
	}
	for _, ch := range []string{"a", "b", "empty"} {
		if _, err := store.pool.Exec(ctx, `INSERT INTO `+site+`.channels (channel, unit, source, sampling_interval_s, batch) VALUES ($1, 'u', 'synthetic', 600, 'it')`, ch); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ { // a = 0..11 every 10 min, b = 100+i
		ts := base.Add(time.Duration(i) * 10 * time.Minute)
		if _, err := store.pool.Exec(ctx, `INSERT INTO `+site+`.measurements (time, channel, value) VALUES ($1, 'a', $2), ($1, 'b', $3)`, ts, float64(i), 100+float64(i)); err != nil {
			t.Fatal(err)
		}
	}

	// ReadSeriesBinned: 30-min buckets average 3 samples each.
	binned, err := store.ReadSeriesBinned(ctx, site, []string{"a", "b"}, base, base.Add(2*time.Hour), 1800)
	if err != nil {
		t.Fatalf("ReadSeriesBinned() error = %v", err)
	}
	if len(binned) != 8 {
		t.Fatalf("ReadSeriesBinned() = %d rows, want 8: %+v", len(binned), binned)
	}
	wantA := []float64{1, 4, 7, 10}
	for i := 0; i < 4; i++ {
		ra, rb := binned[2*i], binned[2*i+1]
		bucket := base.Add(time.Duration(i) * 30 * time.Minute)
		if ra.Channel != "a" || rb.Channel != "b" || !ra.Time.Equal(bucket) || !rb.Time.Equal(bucket) ||
			ra.Value != wantA[i] || rb.Value != 100+wantA[i] {
			t.Fatalf("ReadSeriesBinned()[%d] = %+v, %+v; want bucket %v avg %v", i, ra, rb, bucket, wantA[i])
		}
	}
	// Buckets align to `from`, not to the epoch.
	shifted, err := store.ReadSeriesBinned(ctx, site, []string{"a"}, base.Add(10*time.Minute), base.Add(40*time.Minute), 1800)
	if err != nil || len(shifted) != 1 || !shifted[0].Time.Equal(base.Add(10*time.Minute)) || shifted[0].Value != 2 {
		t.Fatalf("ReadSeriesBinned(shifted) = %+v, %v", shifted, err)
	}
	raw, err := store.ReadSeries(ctx, site, []string{"a"}, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	unbinned, err := store.ReadSeriesBinned(ctx, site, []string{"a"}, base, base.Add(time.Hour), 0)
	if err != nil || fmt.Sprint(unbinned) != fmt.Sprint(raw) || len(raw) != 6 {
		t.Fatalf("ReadSeriesBinned(bin 0) = %v, %v; want ReadSeries %v", unbinned, err, raw)
	}

	// LatestSamples, including a channel without samples and an unknown channel.
	latest, err := store.LatestSamples(ctx, site, []string{"a", "b", "empty", "unknown"})
	if err != nil {
		t.Fatalf("LatestSamples() error = %v", err)
	}
	last := base.Add(110 * time.Minute)
	if len(latest) != 2 || !latest["a"].Equal(last) || !latest["b"].Equal(last) {
		t.Fatalf("LatestSamples() = %v, want a and b at %v", latest, last)
	}
	if _, ok := latest["empty"]; ok {
		t.Fatal("LatestSamples() reported a channel without samples")
	}

	// Runs.
	const wf = "workflows/w.yaml"
	type spec struct {
		id, workflow, status, started string
		window                        map[string]any
		rul                           float64
	}
	window := func(mode, from, to string) map[string]any {
		return map[string]any{"mode": mode, "from": from, "to": to}
	}
	specs := []spec{
		{"r-old", wf, "succeeded", "2026-10-08T08:00:00Z", window("since_previous_run", "2026-05-19T00:00:00Z", "2026-05-20T00:00:00Z"), 400},
		// Started later but its window ends earlier: must not win LatestSucceededRun.
		{"r-backfill", wf, "succeeded", "2026-10-08T12:00:00Z", window("since_previous_run", "2026-05-18T00:00:00Z", "2026-05-19T00:00:00Z"), 450},
		{"r-best", wf, "succeeded", "2026-10-08T10:00:00Z", window("since_previous_run", "2026-05-20T00:00:00Z", "2026-05-21T00:00:00Z"), 390},
		{"r-failed", wf, "failed", "2026-10-08T11:00:00Z", window("since_previous_run", "2026-05-21T00:00:00Z", "2026-05-22T00:00:00Z"), 1},
		{"r-mode", wf, "succeeded", "2026-10-08T11:30:00Z", window("fixed", "2026-05-21T00:00:00Z", "2026-05-23T00:00:00Z"), 2},
		{"r-nowindow", wf, "succeeded", "2026-10-08T13:00:00Z", nil, 3},
		{"r-other", "workflows/other.yaml", "succeeded", "2026-10-08T14:00:00Z", window("since_previous_run", "2026-05-21T00:00:00Z", "2026-05-24T00:00:00Z"), 4},
	}
	for _, s := range specs {
		info := map[string]any{
			"status":            s.status,
			"started_at":        s.started,
			"simulated_seconds": 1200.0,
			"workflow":          map[string]any{"path": s.workflow},
		}
		if s.window != nil {
			info["data_window"] = s.window
		}
		results := map[string]map[string]any{
			"_run": info,
			"monitor": {
				"rul.rul_days": s.rul,
				"label":        "ok",
				"trace": map[string]any{
					"time":    []float64{0, 600, 1200},
					"signals": map[string]any{"rul.rul_days": []float64{s.rul, s.rul - 1, s.rul - 2}, "other": []float64{9, 9, 9}},
				},
			},
			"aux": {"x": 1.0},
		}
		if _, err := store.WriteRun(ctx, site, s.id, results); err != nil {
			t.Fatalf("WriteRun(%s) error = %v", s.id, err)
		}
	}

	runs, err := store.ListRuns(ctx, site, 3)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 3 || runs[0].RunID != "r-other" || runs[1].RunID != "r-nowindow" || runs[2].RunID != "r-backfill" {
		t.Fatalf("ListRuns(3) = %+v", runs)
	}
	if runs[2].WorkflowPath != wf || runs[2].Status != "succeeded" || runs[2].StartedAt == nil ||
		!runs[2].StartedAt.Equal(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)) || runs[2].SimulatedSeconds == nil ||
		*runs[2].SimulatedSeconds != 1200 || runs[2].Provenance["data_window"] == nil {
		t.Fatalf("ListRuns()[2] = %+v", runs[2])
	}
	if from, to, source, ok := RunWindow(runs[2].Provenance, runs[2].StartedAt, runs[2].SimulatedSeconds); !ok || source != WindowSourceDataWindow ||
		!from.Equal(time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("RunWindow(stored) = %v %v %q %v", from, to, source, ok)
	}
	if all, err := store.ListRuns(ctx, site, 0); err != nil || len(all) != len(specs) {
		t.Fatalf("ListRuns(0) = %d, %v; want %d", len(all), err, len(specs))
	}

	outputs, err := store.ReadRunOutputs(ctx, site, []string{"r-old", "r-best", "missing"})
	if err != nil {
		t.Fatalf("ReadRunOutputs() error = %v", err)
	}
	var got []string
	for _, o := range outputs {
		v := "<nil>"
		if o.Value != nil {
			v = fmt.Sprint(*o.Value)
		} else if o.ValueText != nil {
			v = *o.ValueText
		}
		got = append(got, o.RunID+"/"+o.Step+"/"+o.Name+"="+v)
	}
	want := "[r-best/aux/x=1 r-best/monitor/label=ok r-best/monitor/rul.rul_days=390 r-old/aux/x=1 r-old/monitor/label=ok r-old/monitor/rul.rul_days=400]"
	if fmt.Sprint(got) != want {
		t.Fatalf("ReadRunOutputs() = %v, want %v", got, want)
	}
	if none, err := store.ReadRunOutputs(ctx, site, nil); err != nil || len(none) != 0 {
		t.Fatalf("ReadRunOutputs(nil) = %v, %v", none, err)
	}

	series, err := store.ReadRunSeries(ctx, site, []string{"r-best", "r-old"}, []string{"monitor"}, []string{"rul.rul_days"})
	if err != nil {
		t.Fatalf("ReadRunSeries() error = %v", err)
	}
	got = got[:0]
	for _, s := range series {
		got = append(got, fmt.Sprintf("%s/%s/%s@%v=%v", s.RunID, s.Step, s.Signal, s.T, *s.Value))
	}
	want = "[r-best/monitor/rul.rul_days@0=390 r-best/monitor/rul.rul_days@600=389 r-best/monitor/rul.rul_days@1200=388 " +
		"r-old/monitor/rul.rul_days@0=400 r-old/monitor/rul.rul_days@600=399 r-old/monitor/rul.rul_days@1200=398]"
	if fmt.Sprint(got) != want {
		t.Fatalf("ReadRunSeries() = %v, want %v", got, want)
	}
	if none, err := store.ReadRunSeries(ctx, site, nil, []string{"monitor"}, []string{"other"}); err != nil || none != nil {
		t.Fatalf("ReadRunSeries(no runs) = %v, %v", none, err)
	}

	prev, err := store.LatestSucceededRun(ctx, site, wf, "since_previous_run")
	if err != nil {
		t.Fatalf("LatestSucceededRun() error = %v", err)
	}
	if prev == nil || prev.RunID != "r-best" || prev.Mode != "since_previous_run" ||
		!prev.From.Equal(time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)) || !prev.To.Equal(time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("LatestSucceededRun() = %+v", prev)
	}
	if _, has := prev.Outputs["_run"]; has || prev.Outputs["monitor"]["rul.rul_days"] != 390.0 ||
		prev.Outputs["monitor"]["label"] != "ok" || prev.Outputs["aux"]["x"] != 1.0 || prev.Outputs["monitor"]["trace"] != nil {
		t.Fatalf("LatestSucceededRun().Outputs = %v", prev.Outputs)
	}
	if fixed, err := store.LatestSucceededRun(ctx, site, wf, "fixed"); err != nil || fixed == nil || fixed.RunID != "r-mode" {
		t.Fatalf("LatestSucceededRun(fixed) = %+v, %v", fixed, err)
	}
	if none, err := store.LatestSucceededRun(ctx, site, "workflows/none.yaml", "since_previous_run"); err != nil || none != nil {
		t.Fatalf("LatestSucceededRun(none) = %+v, %v", none, err)
	}
}

// A runtime role with only DML grants on tables created in advance can write runs:
// EnsureSiteSchema must not run DDL it has no rights for.
func TestPostgresLeastPrivilegeWriter(t *testing.T) {
	admin, site := openThrowawaySite(t)
	ctx := context.Background()
	if err := admin.EnsureSiteSchema(ctx, site); err != nil {
		t.Fatalf("EnsureSiteSchema(admin) error = %v", err)
	}

	role := site + "_writer"
	t.Cleanup(func() {
		ctx := context.Background()
		admin.pool.Exec(ctx, `DROP OWNED BY `+role)
		admin.pool.Exec(ctx, `DROP ROLE IF EXISTS `+role)
	})
	for _, stmt := range []string{
		`CREATE ROLE ` + role + ` LOGIN PASSWORD 'it-writer'`,
		`GRANT USAGE ON SCHEMA ` + site + ` TO ` + role,
		`GRANT SELECT ON ` + site + `.channels, ` + site + `.measurements TO ` + role,
		`GRANT SELECT, INSERT, DELETE ON ` + site + `.runs, ` + site + `.run_outputs, ` + site + `.run_series TO ` + role,
	} {
		if _, err := admin.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	cfg, err := pgx.ParseConfig(os.Getenv(EnvURL))
	if err != nil {
		t.Fatal(err)
	}
	writerURL := (&url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(role, "it-writer"),
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))),
		Path:   "/" + cfg.Database,
		// Also exercise the mode recommended behind a transaction-mode pooler.
		RawQuery: "sslmode=disable&default_query_exec_mode=exec",
	}).String()
	writer, err := Open(ctx, writerURL)
	if err != nil {
		t.Fatalf("Open(writer) error = %v", err)
	}
	t.Cleanup(writer.Close)

	results := map[string]map[string]any{
		"_run": {
			"status":   "succeeded",
			"workflow": map[string]any{"path": "workflows/demo.yaml", "site": site},
		},
		"step": {"y": 1.5},
	}
	if _, err := writer.WriteRun(ctx, site, "cads-it-writer", results); err != nil {
		t.Fatalf("WriteRun(writer) error = %v", err)
	}
}
