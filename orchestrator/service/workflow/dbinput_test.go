package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
)

type fakeSeriesReader struct {
	rows  []cadsdb.Row
	calls []string
}

func (f *fakeSeriesReader) ReadSeries(_ context.Context, site string, channels []string, from, to time.Time) ([]cadsdb.Row, error) {
	f.calls = append(f.calls, site+"|"+strings.Join(channels, ",")+"|"+from.Format(time.RFC3339)+"|"+to.Format(time.RFC3339))
	var out []cadsdb.Row
	for _, row := range f.rows {
		if !row.Time.Before(from) && row.Time.Before(to) {
			out = append(out, row)
		}
	}
	return out, nil
}

var windowStart = time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)

func at(seconds int) time.Time { return windowStart.Add(time.Duration(seconds) * time.Second) }

func TestBuildDBSeriesCSVCarriesValuesForward(t *testing.T) {
	rows := []cadsdb.Row{
		{Time: at(0), Channel: "vib", Value: 1.5},
		{Time: at(0), Channel: "temp", Value: 20},
		{Time: at(60), Channel: "vib", Value: 2},
		{Time: at(120), Channel: "temp", Value: 21.25},
	}
	csv, counts, err := buildDBSeriesCSV([]string{"t_in", "v_in"}, map[string]string{"t_in": "temp", "v_in": "vib"}, windowStart, rows)
	if err != nil {
		t.Fatalf("buildDBSeriesCSV() error = %v", err)
	}
	want := "time,t_in,v_in\n0,20,1.5\n60,20,2\n120,21.25,2\n"
	if csv != want {
		t.Fatalf("csv =\n%s\nwant\n%s", csv, want)
	}
	if counts["vib"] != 2 || counts["temp"] != 2 {
		t.Fatalf("counts = %v, want 2 samples per channel", counts)
	}
}

func TestBuildDBSeriesCSVRequiresAllChannelsInFirstRow(t *testing.T) {
	rows := []cadsdb.Row{
		{Time: at(0), Channel: "vib", Value: 1},
		{Time: at(10), Channel: "temp", Value: 20},
	}
	_, _, err := buildDBSeriesCSV([]string{"t_in", "v_in"}, map[string]string{"t_in": "temp", "v_in": "vib"}, windowStart, rows)
	if err == nil || !strings.Contains(err.Error(), "channel temp") {
		t.Fatalf("error = %v, want first-row error naming channel temp", err)
	}
	if _, _, err := buildDBSeriesCSV([]string{"v_in"}, map[string]string{"v_in": "vib"}, windowStart, nil); err == nil || !strings.Contains(err.Error(), "vib") {
		t.Fatalf("empty window error = %v, want no-samples error naming vib", err)
	}
}

const dbStepYAML = `
metadata:
  site_id: cheylas
steps:
  - name: monitor
    fmu: fmu/a.fmu
    input_series:
      db:
        site: cheylas
        channels:
          vibration_rms_measured_mm_s: vibration_rms_mm_s
        window: {from: "2026-05-20T00:00:00Z", to: 2026-05-21T00:00:00Z}
        fallback: {csv: data/fallback.csv}
`

func runCapturingCSV(t *testing.T, root, wf string, opts ...Option) (map[string]map[string]any, string, error) {
	t.Helper()
	var csv string
	var csvPath string
	runFMU := func(cfg fmi.Config) (*fmi.Result, error) {
		if cfg.InputSeries == nil {
			t.Fatalf("runFMU called without input series")
		}
		csvPath = cfg.InputSeries.CSVPath
		data, err := os.ReadFile(csvPath)
		if err != nil {
			t.Fatalf("read input CSV: %v", err)
		}
		csv = string(data)
		return &fmi.Result{Values: map[string]any{"y": 1.0}}, nil
	}
	opts = append(opts, withFMIRunners(runFMU, nil))
	exec, err := NewExecutor(root, opts...)
	if err != nil {
		t.Fatal(err)
	}
	results, runErr := exec.Run(wf)
	if csvPath != "" && !strings.HasPrefix(csvPath, root) {
		if _, err := os.Stat(csvPath); !os.IsNotExist(err) {
			t.Fatalf("temp CSV %s not cleaned up", csvPath)
		}
	}
	return results, csv, runErr
}

func TestDBInputSeriesReadsDatabaseAndRecordsProvenance(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root, wf := writeRepo(t, dbStepYAML)
	reader := &fakeSeriesReader{rows: []cadsdb.Row{
		{Time: at(-60), Channel: "vibration_rms_mm_s", Value: 99},
		{Time: at(0), Channel: "vibration_rms_mm_s", Value: 1.25},
		{Time: at(3600), Channel: "vibration_rms_mm_s", Value: 1.5},
	}}
	results, csv, err := runCapturingCSV(t, root, wf, WithSeriesReader(reader))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if csv != "time,vibration_rms_measured_mm_s\n0,1.25\n3600,1.5\n" {
		t.Fatalf("csv = %q", csv)
	}
	if len(reader.calls) != 1 || reader.calls[0] != "cheylas|vibration_rms_mm_s|2026-05-20T00:00:00Z|2026-05-21T00:00:00Z" {
		t.Fatalf("reader calls = %v", reader.calls)
	}
	info := runInfoOf(t, results)
	if info.Workflow.Site != "cheylas" {
		t.Fatalf("workflow site = %q, want cheylas", info.Workflow.Site)
	}
	inputs := info.Steps[0].FMUs[0].Inputs
	want := InputDataset{Variable: "vibration_rms_measured_mm_s", Source: InputSourceDB, Dataset: "cheylas.measurements/vibration_rms_mm_s",
		From: "2026-05-20T00:00:00Z", To: "2026-05-21T00:00:00Z", Rows: 2}
	if len(inputs) != 1 || inputs[0] != want {
		t.Fatalf("inputs = %+v, want %+v", inputs, want)
	}
}

func TestDBInputSeriesFallsBackToCSVWithoutDatabase(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root, wf := writeRepo(t, dbStepYAML)
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Header named after the channel: renamed to the FMU variable in a temp copy.
	if err := os.WriteFile(filepath.Join(root, "data", "fallback.csv"), []byte("time,vibration_rms_mm_s\n0,1\n60,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, csv, err := runCapturingCSV(t, root, wf)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if csv != "time,vibration_rms_measured_mm_s\n0,1\n60,2\n" {
		t.Fatalf("csv = %q, want renamed fallback", csv)
	}
	inputs := runInfoOf(t, results).Steps[0].FMUs[0].Inputs
	if len(inputs) != 1 || inputs[0].Source != InputSourceDBFallback || inputs[0].Dataset != "data/fallback.csv" || inputs[0].Rows != 2 ||
		inputs[0].From != "2026-05-20T00:00:00Z" {
		t.Fatalf("inputs = %+v, want db-fallback-csv provenance", inputs)
	}
}

func TestDBInputSeriesWithoutDatabaseOrFallbackFails(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	yaml := strings.Replace(dbStepYAML, "        fallback: {csv: data/fallback.csv}\n", "", 1)
	root, wf := writeRepo(t, yaml)
	_, _, err := runCapturingCSV(t, root, wf)
	if err == nil || !strings.Contains(err.Error(), "database not configured (CADS_DB_URL) and no fallback") {
		t.Fatalf("Run() error = %v, want database-not-configured error", err)
	}
}

func TestDBInputSeriesValidation(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	cases := map[string]string{
		"site":     `site: "Bad-Site", channels: {a: b}, window: {from: "2026-05-20T00:00:00Z", to: "2026-05-21T00:00:00Z"}`,
		"channels": `site: cheylas, channels: {}, window: {from: "2026-05-20T00:00:00Z", to: "2026-05-21T00:00:00Z"}`,
		"window":   `site: cheylas, channels: {a: b}, window: {from: "yesterday", to: "2026-05-21T00:00:00Z"}`,
		"order":    `site: cheylas, channels: {a: b}, window: {from: "2026-05-21T00:00:00Z", to: "2026-05-20T00:00:00Z"}`,
		"escape":   `site: cheylas, channels: {a: b}, window: {from: "2026-05-20T00:00:00Z", to: "2026-05-21T00:00:00Z"}, fallback: {csv: ../outside.csv}`,
		"multiple": `site: cheylas, channels: {a: b}, window: {from: "2026-05-20T00:00:00Z", to: "2026-05-21T00:00:00Z"}}, csv: {x: 1`,
	}
	wants := map[string]string{
		"site": "site", "channels": "channels", "window": "window.from", "order": "after", "escape": "escapes", "multiple": "exactly one source",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			yaml := "steps:\n  - name: s\n    fmu: fmu/a.fmu\n    input_series: {db: {" + body + "}}\n"
			if name == "multiple" {
				yaml = "steps:\n  - name: s\n    fmu: fmu/a.fmu\n    input_series: {csv: data/x.csv, db: {site: cheylas, channels: {a: b}, window: {from: \"2026-05-20T00:00:00Z\", to: \"2026-05-21T00:00:00Z\"}}}\n"
			}
			root, wf := writeRepo(t, yaml)
			exec, err := NewExecutor(root, WithSeriesReader(&fakeSeriesReader{}), withFMIRunners(func(fmi.Config) (*fmi.Result, error) {
				t.Fatal("FMU must not run")
				return nil, nil
			}, nil))
			if err != nil {
				t.Fatal(err)
			}
			_, err = exec.Run(wf)
			if err == nil || !strings.Contains(err.Error(), wants[name]) {
				t.Fatalf("Run() error = %v, want mention of %q", err, wants[name])
			}
		})
	}
}

func TestCSVInputSeriesRecordsProvenance(t *testing.T) {
	root, wf := writeRepo(t, "steps:\n  - name: s\n    fmu: fmu/a.fmu\n    input_series: {csv: data/in.csv}\n")
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "in.csv"), []byte("time,u,w\n0,1,2\n1,1,2\n\n2,3,4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, _, err := runCapturingCSV(t, root, wf)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	inputs := runInfoOf(t, results).Steps[0].FMUs[0].Inputs
	if len(inputs) != 2 || inputs[0] != (InputDataset{Variable: "u", Source: InputSourceCSV, Dataset: "data/in.csv", Rows: 3}) || inputs[1].Variable != "w" {
		t.Fatalf("inputs = %+v, want csv provenance for u and w", inputs)
	}
}

func TestCoSimModelDBInputSeries(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root, wf := writeRepo(t, `
steps:
  - name: coupled
    cosim:
      scheme: jacobi
      start_time: 0
      stop_time: 10
      communication_step: 1
      models:
        - name: m1
          fmu: fmu/a.fmu
          input_series:
            db: {site: alqueva, channels: {u: flow}, window: {from: "2026-05-20T00:00:00Z", to: "2026-05-20T01:00:00Z"}}
        - {name: m2, fmu: fmu/b.fmu}
`)
	reader := &fakeSeriesReader{rows: []cadsdb.Row{{Time: at(0), Channel: "flow", Value: 3}}}
	var csv string
	exec, err := NewExecutor(root, WithSeriesReader(reader), withFMIRunners(nil, func(cfg fmi.CoSimConfig) (*fmi.Result, error) {
		data, err := os.ReadFile(cfg.Models[0].InputSeries.CSVPath)
		if err != nil {
			t.Fatal(err)
		}
		csv = string(data)
		return &fmi.Result{Values: map[string]any{}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	results, err := exec.Run(wf)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if csv != "time,u\n0,3\n" {
		t.Fatalf("csv = %q", csv)
	}
	fmus := runInfoOf(t, results).Steps[0].FMUs
	if len(fmus) != 2 || len(fmus[0].Inputs) != 1 || fmus[0].Inputs[0].Dataset != "alqueva.measurements/flow" || fmus[1].Inputs == nil || len(fmus[1].Inputs) != 0 {
		t.Fatalf("fmus = %+v, want db inputs on m1 and an empty list on m2", fmus)
	}
}
