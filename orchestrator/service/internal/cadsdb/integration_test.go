//go:build dbintegration

package cadsdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
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
