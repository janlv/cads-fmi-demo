package cadsdb

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// runInfoStep mirrors workflow.RunInfoStepName (kept local so this package has no dependency on
// the executor).
const runInfoStep = "_run"

// OutputRow is one <site>.run_outputs row. Exactly one of Value/ValueText is set for scalar
// outputs; both stay nil for a null output. RunID is only filled by multi-run reads
// (ReadRunOutputs); BuildRunRecord and ReadRun leave it empty.
type OutputRow struct {
	RunID     string
	Step      string
	Name      string
	Value     *float64
	ValueText *string
}

// RunSeriesRow is one <site>.run_series sample tagged with its run (ReadRunSeries).
type RunSeriesRow struct {
	RunID  string
	Step   string
	Signal string
	T      float64
	Value  *float64
}

// SeriesRow is one <site>.run_series sample.
type SeriesRow struct {
	Step   string
	Signal string
	T      float64
	Value  *float64
}

// RunRecord is everything WriteRun stores for one run.
type RunRecord struct {
	RunID            string
	WorkflowPath     string
	WorkflowSHA256   *string
	RunnerVersion    *string
	Status           string
	StartedAt        *time.Time
	FinishedAt       *time.Time
	WallSeconds      *float64
	SimulatedSeconds *float64
	// Provenance is the `_run` pseudo-step, stored verbatim as jsonb.
	Provenance map[string]any
	Outputs    []OutputRow
	Series     []SeriesRow
}

// BuildRunRecord shapes a runner result map into database rows: the `_run` record, every
// non-`_` step's scalar outputs (numbers to value, booleans to 0/1, strings to value_text) and
// every step's `trace.signals` sampled against `trace.time`. Vector-valued outputs and signals
// are not representable in the scalar tables and are skipped.
func BuildRunRecord(runID string, results map[string]map[string]any) (*RunRecord, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("run id is required")
	}
	normalized, err := normalizeResults(results)
	if err != nil {
		return nil, err
	}

	rec := &RunRecord{RunID: runID, Status: "unknown", Provenance: map[string]any{}}
	if info := normalized[runInfoStep]; info != nil {
		rec.Provenance = info
		if status, ok := info["status"].(string); ok && status != "" {
			rec.Status = status
		}
		if wf, ok := info["workflow"].(map[string]any); ok {
			if path, ok := wf["path"].(string); ok {
				rec.WorkflowPath = path
			}
			rec.WorkflowSHA256 = nonEmptyString(wf["sha256"])
		}
		rec.RunnerVersion = nonEmptyString(info["runner_version"])
		rec.StartedAt = parseTimestamp(info["started_at"])
		rec.FinishedAt = parseTimestamp(info["finished_at"])
		rec.WallSeconds = floatPtr(info["wall_seconds"])
		rec.SimulatedSeconds = floatPtr(info["simulated_seconds"])
	}

	for _, step := range sortedKeys(normalized) {
		values := normalized[step]
		if !strings.HasPrefix(step, "_") {
			for _, name := range sortedKeys(values) {
				if name == "trace" {
					continue
				}
				if row, ok := outputRow(step, name, values[name]); ok {
					rec.Outputs = append(rec.Outputs, row)
				}
			}
		}
		if trace, ok := values["trace"].(map[string]any); ok {
			rec.Series = append(rec.Series, seriesRows(step, trace)...)
		}
	}
	return rec, nil
}

// normalizeResults round-trips through JSON so every value has the shape a JSON decoder
// produces (float64, string, bool, nil, []any, map[string]any), which is also exactly what the
// runner prints on stdout.
func normalizeResults(results map[string]map[string]any) (map[string]map[string]any, error) {
	data, err := json.Marshal(results)
	if err != nil {
		return nil, fmt.Errorf("encode results: %w", err)
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode results: %w", err)
	}
	if out == nil {
		out = map[string]map[string]any{}
	}
	return out, nil
}

func outputRow(step, name string, value any) (OutputRow, bool) {
	row := OutputRow{Step: step, Name: name}
	switch v := value.(type) {
	case float64:
		row.Value = &v
	case bool:
		f := 0.0
		if v {
			f = 1
		}
		row.Value = &f
	case string:
		row.ValueText = &v
	case nil:
	default:
		return row, false
	}
	return row, true
}

func seriesRows(step string, trace map[string]any) []SeriesRow {
	times, ok := trace["time"].([]any)
	if !ok {
		return nil
	}
	signals, ok := trace["signals"].(map[string]any)
	if !ok {
		return nil
	}
	var rows []SeriesRow
	for _, signal := range sortedKeys(signals) {
		samples, ok := signals[signal].([]any)
		if !ok || !scalarSamples(samples) {
			continue
		}
		// (step, signal, t) is the primary key: a repeated time keeps its last sample.
		index := make(map[float64]int, len(samples))
		for i, rawT := range times {
			if i >= len(samples) {
				break
			}
			t, ok := rawT.(float64)
			if !ok || math.IsNaN(t) {
				continue
			}
			row := SeriesRow{Step: step, Signal: signal, T: t, Value: floatPtr(samples[i])}
			if at, dup := index[t]; dup {
				rows[at] = row
				continue
			}
			index[t] = len(rows)
			rows = append(rows, row)
		}
	}
	return rows
}

func scalarSamples(samples []any) bool {
	for _, sample := range samples {
		switch sample.(type) {
		case float64, bool, nil:
		default:
			return false
		}
	}
	return true
}

// AssembleRun rebuilds the runner result map from stored rows: `_run` from provenance, scalar
// outputs per step, and `trace` objects ({time: [...], signals: {name: [...]}}) from the series.
// Signals missing at a time of the step's union time axis are null.
func AssembleRun(provenance map[string]any, outputs []OutputRow, series []SeriesRow) map[string]map[string]any {
	results := make(map[string]map[string]any)
	step := func(name string) map[string]any {
		if results[name] == nil {
			results[name] = make(map[string]any)
		}
		return results[name]
	}
	if provenance != nil {
		results[runInfoStep] = provenance
	}
	for _, row := range outputs {
		var value any
		switch {
		case row.Value != nil:
			value = *row.Value
		case row.ValueText != nil:
			value = *row.ValueText
		}
		step(row.Step)[row.Name] = value
	}

	type stepSeries struct {
		times   map[float64]struct{}
		signals map[string]map[float64]*float64
	}
	byStep := make(map[string]*stepSeries)
	for _, row := range series {
		entry := byStep[row.Step]
		if entry == nil {
			entry = &stepSeries{times: map[float64]struct{}{}, signals: map[string]map[float64]*float64{}}
			byStep[row.Step] = entry
		}
		entry.times[row.T] = struct{}{}
		if entry.signals[row.Signal] == nil {
			entry.signals[row.Signal] = make(map[float64]*float64)
		}
		entry.signals[row.Signal][row.T] = row.Value
	}
	for name, entry := range byStep {
		times := make([]float64, 0, len(entry.times))
		for t := range entry.times {
			times = append(times, t)
		}
		sort.Float64s(times)
		timeAxis := make([]any, len(times))
		for i, t := range times {
			timeAxis[i] = t
		}
		signals := make(map[string]any, len(entry.signals))
		for signal, samples := range entry.signals {
			values := make([]any, len(times))
			for i, t := range times {
				if v, ok := samples[t]; ok && v != nil {
					values[i] = *v
				}
			}
			signals[signal] = values
		}
		step(name)["trace"] = map[string]any{"time": timeAxis, "signals": signals}
	}
	return results
}

// Window sources reported by RunWindow.
const (
	WindowSourceDataWindow = "data_window"
	WindowSourceInput      = "input"
	WindowSourceStartedAt  = "started_at"
)

// RunWindow returns the measurement time window a stored run assessed. It prefers the
// `data_window` record ({from, to}), then the first input dataset of any FMU in `steps` that
// has both `from` and `to`, then startedAt extended by simulatedSeconds (to = from when that is
// nil or not positive). Malformed provenance entries are skipped; ok is false when nothing
// usable is found.
func RunWindow(provenance map[string]any, startedAt *time.Time, simulatedSeconds *float64) (from, to time.Time, source string, ok bool) {
	if dw, isMap := provenance["data_window"].(map[string]any); isMap {
		if f, t, good := windowBounds(dw); good {
			return f, t, WindowSourceDataWindow, true
		}
	}
	if steps, isList := provenance["steps"].([]any); isList {
		for _, rawStep := range steps {
			step, _ := rawStep.(map[string]any)
			fmus, _ := step["fmus"].([]any)
			for _, rawFMU := range fmus {
				fmu, _ := rawFMU.(map[string]any)
				inputs, _ := fmu["inputs"].([]any)
				for _, rawInput := range inputs {
					input, _ := rawInput.(map[string]any)
					if f, t, good := windowBounds(input); good {
						return f, t, WindowSourceInput, true
					}
				}
			}
		}
	}
	if startedAt != nil && !startedAt.IsZero() {
		from = startedAt.UTC()
		to = from
		if simulatedSeconds != nil && *simulatedSeconds > 0 && !math.IsInf(*simulatedSeconds, 0) {
			to = from.Add(time.Duration(*simulatedSeconds * float64(time.Second)))
		}
		return from, to, WindowSourceStartedAt, true
	}
	return time.Time{}, time.Time{}, "", false
}

// windowBounds parses non-empty RFC 3339 "from"/"to" strings of m.
func windowBounds(m map[string]any) (time.Time, time.Time, bool) {
	if m == nil {
		return time.Time{}, time.Time{}, false
	}
	from := parseTimestamp(m["from"])
	to := parseTimestamp(m["to"])
	if from == nil || to == nil {
		return time.Time{}, time.Time{}, false
	}
	return from.UTC(), to.UTC(), true
}

func floatPtr(value any) *float64 {
	switch v := value.(type) {
	case float64:
		return &v
	case bool:
		f := 0.0
		if v {
			f = 1
		}
		return &f
	}
	return nil
}

func nonEmptyString(value any) *string {
	if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
		return &s
	}
	return nil
}

func parseTimestamp(value any) *time.Time {
	s, ok := value.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	return &t
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
