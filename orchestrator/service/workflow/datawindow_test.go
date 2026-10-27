package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
)

// fakeWindowReader adds window resolution to fakeSeriesReader.
type fakeWindowReader struct {
	fakeSeriesReader
	latest      map[string]time.Time
	prev        *cadsdb.PreviousRun
	latestCalls []string
	prevCalls   []string
}

func (f *fakeWindowReader) LatestSamples(_ context.Context, site string, channels []string) (map[string]time.Time, error) {
	f.latestCalls = append(f.latestCalls, site+"|"+strings.Join(channels, ","))
	out := make(map[string]time.Time)
	for _, channel := range channels {
		if at, ok := f.latest[channel]; ok {
			out[channel] = at
		}
	}
	return out, nil
}

func (f *fakeWindowReader) LatestSucceededRun(_ context.Context, site, workflowPath, mode string) (*cadsdb.PreviousRun, error) {
	f.prevCalls = append(f.prevCalls, site+"|"+workflowPath+"|"+mode)
	return f.prev, nil
}

func ts(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed.UTC()
}

func TestParseWindowDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"24h": 24 * time.Hour, "90m": 90 * time.Minute, "7d": 7 * 24 * time.Hour, "1.5d": 36 * time.Hour,
		"86400": 24 * time.Hour, " 900 ": 15 * time.Minute,
	}
	for input, want := range cases {
		got, err := parseWindowDuration(input)
		if err != nil || got != want {
			t.Fatalf("parseWindowDuration(%q) = %v, %v; want %v", input, got, err, want)
		}
	}
	for _, bad := range []string{"", "soon", "xd", "3w"} {
		if _, err := parseWindowDuration(bad); err == nil {
			t.Fatalf("parseWindowDuration(%q) error = nil", bad)
		}
	}
}

func TestResolveWindow(t *testing.T) {
	since := &dataWindowPlan{mode: DataWindowSincePreviousRun, initialSpan: 24 * time.Hour, maxSpan: 7 * 24 * time.Hour, align: 900 * time.Second}
	latestPlan := &dataWindowPlan{mode: DataWindowLatest, span: 6 * time.Hour, align: 900 * time.Second}
	prevTo := func(value string) *cadsdb.PreviousRun { return &cadsdb.PreviousRun{RunID: "p", To: ts(value)} }
	cases := []struct {
		name     string
		plan     *dataWindowPlan
		latest   string
		prev     *cadsdb.PreviousRun
		from, to string
		backlog  time.Duration
		errText  string
	}{
		{name: "first run", plan: since, latest: "2026-05-21T00:00:00Z", from: "2026-05-20T00:00:00Z", to: "2026-05-21T00:00:00Z"},
		{name: "continue", plan: since, latest: "2026-05-22T00:00:00Z", prev: prevTo("2026-05-21T00:00:00Z"), from: "2026-05-21T00:00:00Z", to: "2026-05-22T00:00:00Z"},
		{name: "snap to alignment", plan: since, latest: "2026-05-21T06:10:00Z", prev: prevTo("2026-05-21T00:00:00Z"),
			from: "2026-05-21T00:00:00Z", to: "2026-05-21T06:00:00Z", backlog: 10 * time.Minute},
		{name: "cap and backlog", plan: since, latest: "2026-05-31T00:00:00Z", prev: prevTo("2026-05-21T00:00:00Z"),
			from: "2026-05-21T00:00:00Z", to: "2026-05-28T00:00:00Z", backlog: 3 * 24 * time.Hour},
		{name: "no new samples", plan: since, latest: "2026-05-21T00:00:00Z", prev: prevTo("2026-05-21T00:00:00Z"), errText: "is not after the end of the previous assessment"},
		{name: "less than one step", plan: since, latest: "2026-05-21T00:10:00Z", prev: prevTo("2026-05-21T00:00:00Z"), errText: "less than one communication step"},
		{name: "latest first", plan: latestPlan, latest: "2026-05-21T00:00:00Z", from: "2026-05-20T18:00:00Z", to: "2026-05-21T00:00:00Z"},
		{name: "latest newer", plan: latestPlan, latest: "2026-05-21T00:05:00Z", prev: prevTo("2026-05-21T00:00:00Z"), from: "2026-05-20T18:05:00Z", to: "2026-05-21T00:05:00Z"},
		{name: "latest up to date", plan: latestPlan, latest: "2026-05-21T00:00:00Z", prev: prevTo("2026-05-21T00:00:00Z"), errText: "is not after"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			from, to, backlog, err := resolveWindow(tc.plan, ts(tc.latest), tc.prev)
			if tc.errText != "" {
				if !errors.Is(err, ErrNoNewData) || !strings.Contains(err.Error(), tc.errText) {
					t.Fatalf("error = %v, want ErrNoNewData mentioning %q", err, tc.errText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !from.Equal(ts(tc.from)) || !to.Equal(ts(tc.to)) || backlog != tc.backlog {
				t.Fatalf("window = %s..%s backlog %v, want %s..%s backlog %v", from, to, backlog, tc.from, tc.to, tc.backlog)
			}
		})
	}
}

func TestSecondsOfDay(t *testing.T) {
	if got := secondsOfDay(ts("2026-05-21T06:00:00Z")); got != 21600 {
		t.Fatalf("secondsOfDay = %v, want 21600", got)
	}
	if got := secondsOfDay(time.Date(2026, 5, 21, 8, 0, 0, 0, time.FixedZone("CEST", 2*3600))); got != 21600 {
		t.Fatalf("secondsOfDay (CEST) = %v, want 21600 (UTC time of day)", got)
	}
}

const windowedBase = `
metadata: {site_id: cheylas}
data_window: {mode: since_previous_run, initial_span: 24h, max_span: 7d}
steps:
  - name: health
    cosim:
      scheme: jacobi
      communication_step: 900
      models:
        - name: iot
          fmu: fmu/a.fmu
          input_series: {db: {site: cheylas, channels: {v: vib}}}
        - name: rul
          fmu: fmu/b.fmu
          start_values: {initial_damage: 0.35}
          carry_over: {initial_damage: health.rul.damage_index}
  - name: cost
    fmu: fmu/a.fmu
    start_from: {rul_days: health.rul.rul_days}
`

func TestValidateDataWindow(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		errText string
	}{
		{name: "valid", yaml: windowedBase},
		{name: "bare seconds", yaml: strings.Replace(windowedBase, "initial_span: 24h, max_span: 7d", "initial_span: 86400, max_span: 7d", 1)},
		{name: "latest valid", yaml: strings.Replace(strings.Replace(windowedBase, "{mode: since_previous_run, initial_span: 24h, max_span: 7d}", "{mode: latest, span: 6h}", 1),
			"          carry_over: {initial_damage: health.rul.damage_index}\n", "", 1)},
		{name: "mode", yaml: strings.Replace(windowedBase, "mode: since_previous_run", "mode: rolling", 1), errText: "data_window.mode must be"},
		{name: "missing max_span", yaml: strings.Replace(windowedBase, ", max_span: 7d", "", 1), errText: "data_window.max_span is required"},
		{name: "span with since", yaml: strings.Replace(windowedBase, "max_span: 7d", "max_span: 7d, span: 1h", 1), errText: "data_window.span is not used"},
		{name: "latest without span", yaml: strings.Replace(windowedBase, "{mode: since_previous_run, initial_span: 24h, max_span: 7d}", "{mode: latest}", 1), errText: "data_window.span is required"},
		{name: "bad duration", yaml: strings.Replace(windowedBase, "initial_span: 24h", "initial_span: soon", 1), errText: "data_window.initial_span"},
		{name: "not aligned", yaml: strings.Replace(windowedBase, "initial_span: 24h", "initial_span: 10m", 1), errText: "multiple of the communication step 15m0s"},
		{name: "negative", yaml: strings.Replace(windowedBase, "initial_span: 24h", "initial_span: -24h", 1), errText: "must be positive"},
		{name: "no site", yaml: strings.Replace(windowedBase, "metadata: {site_id: cheylas}", "metadata: {}", 1), errText: "requires metadata.site_id"},
		{name: "no db input", yaml: strings.Replace(windowedBase, "          input_series: {db: {site: cheylas, channels: {v: vib}}}\n", "", 1), errText: "at least one input_series.db"},
		{name: "own window", yaml: strings.Replace(windowedBase, "channels: {v: vib}}", `channels: {v: vib}, window: {from: "2026-05-20T00:00:00Z", to: "2026-05-21T00:00:00Z"}}`, 1),
			errText: "step health model iot: input_series.db.window must be omitted"},
		{name: "other site", yaml: strings.Replace(windowedBase, "{db: {site: cheylas", "{db: {site: alqueva", 1), errText: `input_series.db.site "alqueva" must match metadata.site_id "cheylas"`},
		{name: "times on db step", yaml: strings.Replace(windowedBase, "      communication_step: 900\n", "      start_time: 0\n      stop_time: 86400\n      communication_step: 900\n", 1),
			errText: "step health: start_time and stop_time must be omitted"},
		{name: "carry with latest", yaml: strings.Replace(windowedBase, "{mode: since_previous_run, initial_span: 24h, max_span: 7d}", "{mode: latest, span: 6h}", 1),
			errText: "step health model rul: carry_over requires data_window mode since_previous_run (got latest)"},
		{name: "carry collides", yaml: strings.Replace(windowedBase, "          carry_over: {initial_damage: health.rul.damage_index}\n",
			"          carry_over: {initial_damage: health.rul.damage_index}\n          start_from: {initial_damage: cost.y}\n", 1),
			errText: "carry_over[initial_damage] is also set by start_from"},
		{name: "carry format", yaml: strings.Replace(windowedBase, "{initial_damage: health.rul.damage_index}", "{initial_damage: damage_index}", 1),
			errText: "carry_over[initial_damage] must use format step.variable"},
		{name: "carry unknown step", yaml: strings.Replace(windowedBase, "{initial_damage: health.rul.damage_index}", "{initial_damage: other.rul.damage_index}", 1),
			errText: "carry_over[initial_damage] references unknown step other"},
		{name: "carry without window", yaml: strings.Replace(strings.Replace(strings.Replace(windowedBase,
			"data_window: {mode: since_previous_run, initial_span: 24h, max_span: 7d}\n", "", 1),
			"{db: {site: cheylas, channels: {v: vib}}}", `{db: {site: cheylas, channels: {v: vib}, window: {from: "2026-05-20T00:00:00Z", to: "2026-05-21T00:00:00Z"}}}`, 1),
			"      communication_step: 900\n", "      start_time: 0\n      stop_time: 86400\n      communication_step: 900\n", 1),
			errText: "step health model rul: carry_over requires data_window with mode since_previous_run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var doc workflowFile
			if err := yaml.Unmarshal([]byte(tc.yaml), &doc); err != nil {
				t.Fatal(err)
			}
			err := validateSteps("wf.yaml", doc.Steps, doc.DataWindow != nil)
			if err == nil {
				_, err = validateDataWindow(&doc)
			}
			if tc.errText == "" {
				if err != nil {
					t.Fatalf("error = %v, want valid", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.errText) {
				t.Fatalf("error = %v, want mention of %q", err, tc.errText)
			}
		})
	}
}

func TestValidateDataWindowPlan(t *testing.T) {
	var doc workflowFile
	if err := yaml.Unmarshal([]byte(windowedBase), &doc); err != nil {
		t.Fatal(err)
	}
	plan, err := validateDataWindow(&doc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.align != 900*time.Second || plan.site != "cheylas" || !reflect.DeepEqual(plan.channels, []string{"vib"}) ||
		plan.initialSpan != 24*time.Hour || plan.maxSpan != 7*24*time.Hour {
		t.Fatalf("plan = %+v", plan)
	}
	if got := windowAlign([]workflowStep{{StepSize: f64(60)}, {StepSize: f64(300)}}); got != 300*time.Second {
		t.Fatalf("align from step_size = %v", got)
	}
	if got := windowAlign([]workflowStep{{}}); got != time.Second {
		t.Fatalf("default align = %v", got)
	}
}

func TestValidateCoSimWithWindow(t *testing.T) {
	spec := validCoSim()
	spec.StartTime, spec.StopTime = nil, nil
	if err := ValidateCoSim("s", spec); err == nil {
		t.Fatal("ValidateCoSim accepted missing times")
	}
	if err := ValidateCoSimWithWindow("s", spec, true); err != nil {
		t.Fatalf("windowed validation error = %v", err)
	}
	spec.StartTime = f64(0)
	if err := ValidateCoSimWithWindow("s", spec, true); err == nil || !strings.Contains(err.Error(), "both start_time and stop_time or neither") {
		t.Fatalf("windowed validation with only start_time = %v", err)
	}
}

func TestBuildWindowedDBSeriesCSV(t *testing.T) {
	from := ts("2026-05-21T06:00:00Z")
	rows := []cadsdb.Row{
		{Time: from, Channel: "vib", Value: 1},
		{Time: from.Add(300 * time.Second), Channel: "vib", Value: 2},
	}
	csv, counts, err := buildWindowedDBSeriesCSV([]string{"v"}, map[string]string{"v": "vib"}, from, 21600, rows)
	if err != nil {
		t.Fatal(err)
	}
	if csv != "time,v\n21600,1\n21900,2\n" || counts["vib"] != 2 {
		t.Fatalf("csv = %q counts = %v", csv, counts)
	}
	_, _, err = buildWindowedDBSeriesCSV([]string{"v"}, map[string]string{"v": "vib"}, from, 21600, rows[1:])
	if err == nil || !strings.Contains(err.Error(), "channel(s) vib have no sample exactly at the window start 2026-05-21T06:00:00Z") {
		t.Fatalf("error = %v, want missing-first-sample error", err)
	}
}

// --- executor tests against the real Cheylas workflows ---------------------------------------

const (
	cheylasRunnerYAML = "workflows/demonstrators/cheylas/maintenance/runner_rul_lockstep.yaml"
	cheylasFixedYAML  = "workflows/tests/cheylas_runner_rul_fixed_window.yaml"
	cheylasSedYAML    = "workflows/demonstrators/cheylas/monitoring/sediment_erosion_events.yaml"
)

// cheylasRepo copies the Cheylas workflows (and their synthetic case) into a temporary repo
// with stub FMU files.
func cheylasRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join("..", "..", "..")
	root := t.TempDir()
	for _, rel := range []string{cheylasRunnerYAML, cheylasFixedYAML, cheylasSedYAML, "data/storhy/synthetic/cheylas_sediment_cycling.yaml"} {
		data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, root, rel, string(data))
	}
	for _, name := range []string{"RunnerStress", "IoTIndicator", "Rul", "DegradationCost", "SedimentExposure"} {
		writeFile(t, root, "fmu/models/"+name+".fmu", "stub-"+name)
	}
	return root
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// vibrationRows returns 300 s samples on [start, end] (inclusive).
func vibrationRows(start, end time.Time) []cadsdb.Row {
	var rows []cadsdb.Row
	for at, i := start, 0; !at.After(end); at, i = at.Add(300*time.Second), i+1 {
		rows = append(rows, cadsdb.Row{Time: at, Channel: "vibration_rms_mm_s", Value: 1.5 + float64(i%7)*0.25})
	}
	return rows
}

type capturedRun struct {
	results map[string]map[string]any
	err     error
	cosim   []fmi.CoSimConfig
	fmu     []fmi.Config
	csv     []string
}

var cosimValues = map[string]any{
	"rul.rul_days": 400.0, "rul.damage_rate_per_day": 0.001, "runner.energy_mwh": 10.0,
	"rul.damage_index": 0.36, "rul.planning_rate_per_day": 0.0012,
}

func runCaptured(t *testing.T, root, wf string, opts ...Option) capturedRun {
	t.Helper()
	var run capturedRun
	readCSV := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read input CSV: %v", err)
		}
		return string(data)
	}
	opts = append(opts, withFMIRunners(
		func(cfg fmi.Config) (*fmi.Result, error) {
			run.fmu = append(run.fmu, cfg)
			return &fmi.Result{Values: map[string]any{"net_benefit_eur": 1.0}, Stats: fmi.Stats{SimulatedSeconds: 1}}, nil
		},
		func(cfg fmi.CoSimConfig) (*fmi.Result, error) {
			for i, model := range cfg.Models {
				if model.InputSeries != nil {
					run.csv = append(run.csv, readCSV(model.InputSeries.CSVPath))
					cfg.Models[i].InputSeries = &fmi.InputSeriesConfig{CSVPath: "<csv>"}
				}
			}
			run.cosim = append(run.cosim, cfg)
			return &fmi.Result{Values: cosimValues, Stats: fmi.Stats{SimulatedSeconds: cfg.StopTime - cfg.StartTime}}, nil
		}))
	exec, err := NewExecutor(root, opts...)
	if err != nil {
		t.Fatal(err)
	}
	run.results, run.err = exec.Run(wf)
	return run
}

func TestWindowedRunnerWorkflowFirstRunMatchesFixedWindow(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root := cheylasRepo(t)
	end := ts("2026-05-21T00:00:00Z")
	rows := vibrationRows(ts("2026-05-20T00:00:00Z"), end)
	reader := &fakeWindowReader{fakeSeriesReader: fakeSeriesReader{rows: rows}, latest: map[string]time.Time{"vibration_rms_mm_s": end}}

	fixed := runCaptured(t, root, cheylasFixedYAML, WithSeriesReader(&fakeSeriesReader{rows: rows}))
	windowed := runCaptured(t, root, cheylasRunnerYAML, WithSeriesReader(reader))
	if fixed.err != nil || windowed.err != nil {
		t.Fatalf("errors: fixed=%v windowed=%v", fixed.err, windowed.err)
	}
	if len(windowed.cosim) != 1 || len(fixed.cosim) != 1 || len(windowed.fmu) != 1 || len(fixed.fmu) != 1 {
		t.Fatalf("calls: windowed %d/%d fixed %d/%d", len(windowed.cosim), len(windowed.fmu), len(fixed.cosim), len(fixed.fmu))
	}
	if windowed.cosim[0].StartTime != 0 || windowed.cosim[0].StopTime != 86400 {
		t.Fatalf("windowed cosim time = %v..%v", windowed.cosim[0].StartTime, windowed.cosim[0].StopTime)
	}
	// The windowed run adds rul.planning_rate_per_day to its outputs; everything else matches.
	wantCoSim := fixed.cosim[0]
	wantCoSim.Outputs = append(append([]fmi.VarRef(nil), wantCoSim.Outputs[:7]...), fmi.VarRef{Model: "rul", Var: "planning_rate_per_day"}, wantCoSim.Outputs[7])
	if !reflect.DeepEqual(windowed.cosim[0], wantCoSim) {
		t.Fatalf("cosim config differs:\nwindowed %+v\nfixed    %+v", windowed.cosim[0], wantCoSim)
	}
	if !reflect.DeepEqual(windowed.csv, fixed.csv) || !strings.HasPrefix(windowed.csv[0], "time,vibration_rms_measured_mm_s\n0,") {
		t.Fatalf("csv differs or starts wrong: %q", windowed.csv[0][:60])
	}
	if !reflect.DeepEqual(windowed.fmu[0], fixed.fmu[0]) {
		t.Fatalf("degradation_cost config differs:\nwindowed %+v\nfixed    %+v", windowed.fmu[0], fixed.fmu[0])
	}
	if reader.calls[0] != "cheylas|vibration_rms_mm_s|2026-05-20T00:00:00Z|2026-05-21T00:00:00Z" ||
		reader.prevCalls[0] != "cheylas|"+cheylasRunnerYAML+"|since_previous_run" {
		t.Fatalf("reader calls = %v %v", reader.calls, reader.prevCalls)
	}

	info := runInfoOf(t, windowed.results)
	dw := info.DataWindow
	if dw == nil || !dw.Resolved || dw.Source != DataWindowSourceDB || dw.From != "2026-05-20T00:00:00Z" || dw.To != "2026-05-21T00:00:00Z" ||
		dw.SpanSeconds != 86400 || dw.SimStartTime != 0 || dw.SimStopTime != 86400 || dw.LatestSample != "2026-05-21T00:00:00Z" ||
		dw.PreviousRunID != "" || dw.Site != "cheylas" || !reflect.DeepEqual(dw.Channels, []string{"vibration_rms_mm_s"}) {
		t.Fatalf("data_window = %+v", dw)
	}
	carry := info.Steps[0].FMUs[2].CarryOver
	if carry == nil || carry.Source != CarryOverSourceStartValues || carry.RunID != "" || carry.Values["initial_damage"] != 0.35 ||
		carry.Refs["initial_planning_rate_per_day"] != "runner_health.rul.planning_rate_per_day" {
		t.Fatalf("carry_over = %+v", carry)
	}
	if fixedInfo := runInfoOf(t, fixed.results); fixedInfo.DataWindow != nil || fixedInfo.Steps[0].FMUs[2].CarryOver != nil {
		t.Fatalf("fixed-window run must not record data_window/carry_over: %+v", fixedInfo)
	}
}

func TestWindowedRunnerWorkflowSecondRunCarriesState(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root := cheylasRepo(t)
	end := ts("2026-05-22T00:00:00Z")
	reader := &fakeWindowReader{
		fakeSeriesReader: fakeSeriesReader{rows: vibrationRows(ts("2026-05-20T00:00:00Z"), end)},
		latest:           map[string]time.Time{"vibration_rms_mm_s": end},
		prev: &cadsdb.PreviousRun{RunID: "run-1", Mode: DataWindowSincePreviousRun, From: ts("2026-05-20T00:00:00Z"), To: ts("2026-05-21T00:00:00Z"),
			Outputs: map[string]map[string]any{"runner_health": {"rul.damage_index": 0.37, "rul.planning_rate_per_day": 0.002}}},
	}
	run := runCaptured(t, root, cheylasRunnerYAML, WithSeriesReader(reader))
	if run.err != nil {
		t.Fatal(run.err)
	}
	cfg := run.cosim[0]
	if cfg.StartTime != 0 || cfg.StopTime != 86400 {
		t.Fatalf("cosim time = %v..%v", cfg.StartTime, cfg.StopTime)
	}
	rul := cfg.Models[2].StartValues
	if rul["initial_damage"] != "0.37" || rul["initial_planning_rate_per_day"] != "0.002" {
		t.Fatalf("rul start values = %v", rul)
	}
	if !strings.HasPrefix(run.csv[0], "time,vibration_rms_measured_mm_s\n0,") || strings.Count(run.csv[0], "\n") != 289 {
		t.Fatalf("csv = %q... (%d lines)", run.csv[0][:50], strings.Count(run.csv[0], "\n"))
	}
	if reader.calls[0] != "cheylas|vibration_rms_mm_s|2026-05-21T00:00:00Z|2026-05-22T00:00:00Z" {
		t.Fatalf("reader calls = %v", reader.calls)
	}
	info := runInfoOf(t, run.results)
	if info.DataWindow.PreviousRunID != "run-1" || info.DataWindow.PreviousTo != "2026-05-21T00:00:00Z" || info.DataWindow.From != "2026-05-21T00:00:00Z" {
		t.Fatalf("data_window = %+v", info.DataWindow)
	}
	carry := info.Steps[0].FMUs[2].CarryOver
	if carry == nil || carry.Source != CarryOverSourcePreviousRun || carry.RunID != "run-1" || carry.Values["initial_damage"] != 0.37 {
		t.Fatalf("carry_over = %+v", carry)
	}
}

func TestWindowedRunMidDayWindow(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root := cheylasRepo(t)
	end := ts("2026-05-22T06:00:00Z")
	reader := &fakeWindowReader{
		fakeSeriesReader: fakeSeriesReader{rows: vibrationRows(ts("2026-05-21T00:00:00Z"), end)},
		latest:           map[string]time.Time{"vibration_rms_mm_s": end},
		prev: &cadsdb.PreviousRun{RunID: "run-1", To: ts("2026-05-21T06:00:00Z"),
			Outputs: map[string]map[string]any{"runner_health": {"rul.damage_index": 0.37, "rul.planning_rate_per_day": 0.002}}},
	}
	run := runCaptured(t, root, cheylasRunnerYAML, WithSeriesReader(reader))
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.cosim[0].StartTime != 21600 || run.cosim[0].StopTime != 21600+86400 {
		t.Fatalf("cosim time = %v..%v", run.cosim[0].StartTime, run.cosim[0].StopTime)
	}
	if !strings.HasPrefix(run.csv[0], "time,vibration_rms_measured_mm_s\n21600,") {
		t.Fatalf("csv = %q", run.csv[0][:60])
	}
	// The plain degradation_cost step has no times: it inherits the window span.
	cost := run.fmu[0]
	if cost.StartTime == nil || cost.StopTime == nil || *cost.StartTime != 21600 || *cost.StopTime != 108000 {
		t.Fatalf("degradation_cost time = %v..%v", cost.StartTime, cost.StopTime)
	}
}

func TestWindowedRunSkipsWhenUpToDate(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root := cheylasRepo(t)
	end := ts("2026-05-21T00:00:00Z")
	reader := &fakeWindowReader{
		latest: map[string]time.Time{"vibration_rms_mm_s": end},
		prev:   &cadsdb.PreviousRun{RunID: "run-1", To: end},
	}
	run := runCaptured(t, root, cheylasRunnerYAML, WithSeriesReader(reader))
	if !errors.Is(run.err, ErrNoNewData) {
		t.Fatalf("error = %v, want ErrNoNewData", run.err)
	}
	if len(run.cosim) != 0 || len(run.fmu) != 0 || len(reader.calls) != 0 {
		t.Fatalf("nothing may run when up to date: %d cosim, %d fmu, %v reads", len(run.cosim), len(run.fmu), reader.calls)
	}
	info := runInfoOf(t, run.results)
	if info.Status != StatusSkipped || len(info.Steps) != 2 || info.Steps[0].Status != StatusSkipped || info.Steps[1].Status != StatusSkipped {
		t.Fatalf("run info = %+v", info)
	}
	if info.DataWindow == nil || info.DataWindow.Resolved || info.DataWindow.To != "" || info.DataWindow.PreviousRunID != "run-1" || info.DataWindow.LatestSample != "2026-05-21T00:00:00Z" {
		t.Fatalf("data_window = %+v", info.DataWindow)
	}
	if _, err := json.Marshal(run.results); err != nil {
		t.Fatal(err)
	}
}

func TestWindowedRunFallbackWithoutDatabase(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root := cheylasRepo(t)
	var csv strings.Builder
	csv.WriteString("time,vibration_rms_measured_mm_s\n")
	for s := 0; s <= 86400; s += 300 {
		fmt.Fprintf(&csv, "%d,1.5\n", s)
	}
	writeFile(t, root, "data/storhy/cheylas/measurements/vibration_rms_mm_s.csv", csv.String())
	run := runCaptured(t, root, cheylasRunnerYAML)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.cosim[0].StartTime != 0 || run.cosim[0].StopTime != 86400 || run.cosim[0].Models[2].StartValues["initial_damage"] != "0.35" {
		t.Fatalf("cosim = %+v", run.cosim[0])
	}
	info := runInfoOf(t, run.results)
	dw := info.DataWindow
	if dw == nil || dw.Resolved || dw.Source != DataWindowSourceFallback || dw.SpanSeconds != 86400 || dw.SimStopTime != 86400 || dw.From != "" {
		t.Fatalf("data_window = %+v", dw)
	}
	if carry := info.Steps[0].FMUs[2].CarryOver; carry == nil || carry.Source != CarryOverSourceStartValues {
		t.Fatalf("carry_over = %+v", carry)
	}
	inputs := info.Steps[0].FMUs[1].Inputs
	if len(inputs) != 1 || inputs[0].Source != InputSourceDBFallback || inputs[0].From != "" || inputs[0].To != "" {
		t.Fatalf("inputs = %+v", inputs)
	}
}

func TestWindowedRunMissingCarryKeyFails(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root := cheylasRepo(t)
	end := ts("2026-05-22T00:00:00Z")
	reader := &fakeWindowReader{
		fakeSeriesReader: fakeSeriesReader{rows: vibrationRows(ts("2026-05-21T00:00:00Z"), end)},
		latest:           map[string]time.Time{"vibration_rms_mm_s": end},
		prev: &cadsdb.PreviousRun{RunID: "run-1", To: ts("2026-05-21T00:00:00Z"),
			Outputs: map[string]map[string]any{"runner_health": {"rul.damage_index": 0.37}}},
	}
	run := runCaptured(t, root, cheylasRunnerYAML, WithSeriesReader(reader))
	if run.err == nil || !strings.Contains(run.err.Error(), "carry_over[initial_planning_rate_per_day] missing variable rul.planning_rate_per_day in step runner_health of previous run run-1") {
		t.Fatalf("error = %v", run.err)
	}
	reader.prev.Outputs["runner_health"]["rul.planning_rate_per_day"] = "high"
	run = runCaptured(t, root, cheylasRunnerYAML, WithSeriesReader(reader))
	if run.err == nil || !strings.Contains(run.err.Error(), "is not numeric") {
		t.Fatalf("error = %v, want non-numeric error", run.err)
	}
	if len(run.cosim) != 0 {
		t.Fatal("cosim must not run")
	}
}

func TestWindowedRunReaderErrors(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root := cheylasRepo(t)
	run := runCaptured(t, root, cheylasRunnerYAML, WithSeriesReader(&fakeWindowReader{latest: map[string]time.Time{}}))
	if run.err == nil || !strings.Contains(run.err.Error(), "channel(s) vibration_rms_mm_s have no samples in cheylas.measurements") {
		t.Fatalf("error = %v", run.err)
	}
	run = runCaptured(t, root, cheylasRunnerYAML, WithSeriesReader(&fakeSeriesReader{}))
	if run.err == nil || !strings.Contains(run.err.Error(), "data_window: database reader does not support window resolution") {
		t.Fatalf("error = %v", run.err)
	}
	if info := runInfoOf(t, run.results); info.Status != StatusFailed {
		t.Fatalf("status = %s", info.Status)
	}
}

func TestSedimentWorkflowCarriesExposureAndCleaningState(t *testing.T) {
	t.Setenv(cadsdb.EnvURL, "")
	root := cheylasRepo(t)
	end := ts("2026-05-22T00:00:00Z")
	var rows []cadsdb.Row
	for at := ts("2026-05-21T00:00:00Z"); !at.After(end); at = at.Add(300 * time.Second) {
		rows = append(rows, cadsdb.Row{Time: at, Channel: "turbidity_g_l", Value: 0.5})
	}
	reader := &fakeWindowReader{
		fakeSeriesReader: fakeSeriesReader{rows: rows},
		latest:           map[string]time.Time{"turbidity_g_l": end},
		prev: &cadsdb.PreviousRun{RunID: "run-7", To: ts("2026-05-21T00:00:00Z"), Outputs: map[string]map[string]any{"sediment_erosion": {
			"sediment.sediment_exposure": 0.42, "sediment.hours_since_cleaning": 7.5, "rul.damage_index": 0.31}}},
	}
	run := runCaptured(t, root, cheylasSedYAML, WithSeriesReader(reader))
	if run.err != nil {
		t.Fatal(run.err)
	}
	sediment, rul := run.cosim[0].Models[0].StartValues, run.cosim[0].Models[1].StartValues
	if sediment["initial_exposure"] != "0.42" || sediment["initial_hours_since_cleaning"] != "7.5" || sediment["cycles_per_day"] != "8" ||
		rul["initial_damage"] != "0.31" || rul["demo_acceleration"] != "20" {
		t.Fatalf("start values: sediment %v rul %v", sediment, rul)
	}
}

func TestPlanDataWindow(t *testing.T) {
	root := cheylasRepo(t)
	ctx := context.Background()
	if info, err := PlanDataWindow(ctx, root, cheylasFixedYAML, nil); info != nil || err != nil {
		t.Fatalf("fixed workflow: %+v, %v; want nil, nil", info, err)
	}
	end := ts("2026-05-21T00:00:00Z")
	reader := &fakeWindowReader{latest: map[string]time.Time{"vibration_rms_mm_s": end}}
	info, err := PlanDataWindow(ctx, root, cheylasRunnerYAML, reader)
	if err != nil || info == nil || !info.Resolved || info.From != "2026-05-20T00:00:00Z" || info.To != "2026-05-21T00:00:00Z" {
		t.Fatalf("first plan: %+v, %v", info, err)
	}
	if reader.prevCalls[0] != "cheylas|"+cheylasRunnerYAML+"|since_previous_run" {
		t.Fatalf("prev calls = %v", reader.prevCalls)
	}
	reader.prev = &cadsdb.PreviousRun{RunID: "run-1", To: end}
	info, err = PlanDataWindow(ctx, root, cheylasRunnerYAML, reader)
	if !errors.Is(err, ErrNoNewData) || info == nil || info.LatestSample != "2026-05-21T00:00:00Z" || info.PreviousRunID != "run-1" {
		t.Fatalf("up-to-date plan: %+v, %v", info, err)
	}
	info, err = PlanDataWindow(ctx, root, cheylasRunnerYAML, nil)
	if err != nil || info == nil || info.Resolved || info.Source != DataWindowSourceFallback {
		t.Fatalf("fallback plan: %+v, %v", info, err)
	}
	if _, err := PlanDataWindow(ctx, root, "../outside.yaml", nil); !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatalf("escape error = %v", err)
	}
}

func TestRunInfoOmitsWindowFieldsWhenUnused(t *testing.T) {
	root, wf := writeRepo(t, twoStepYAML)
	exec, err := NewExecutor(root, withFMIRunners(
		func(fmi.Config) (*fmi.Result, error) { return &fmi.Result{Values: map[string]any{"y": 1.0}}, nil },
		func(fmi.CoSimConfig) (*fmi.Result, error) { return &fmi.Result{Values: map[string]any{}}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	results, err := exec.Run(wf)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(results[RunInfoStepName])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "data_window") || strings.Contains(string(data), "carry_over") {
		t.Fatalf("_run of a workflow without data_window mentions window fields: %s", data)
	}
}
