package workflow

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
)

// Data window modes (`data_window.mode`).
const (
	// DataWindowSincePreviousRun continues from where the previous succeeded run of the same
	// workflow stopped, catching up in chunks of at most max_span.
	DataWindowSincePreviousRun = "since_previous_run"
	// DataWindowLatest always assesses the newest `span` of data.
	DataWindowLatest = "latest"
)

// Data window sources recorded in `_run.data_window.source`.
const (
	DataWindowSourceDB       = "db"
	DataWindowSourceFallback = "fallback"
)

// Carry-over sources recorded in `_run.steps[].fmus[].carry_over.source`.
const (
	CarryOverSourcePreviousRun = "previous_run"
	CarryOverSourceStartValues = "start_values"
)

// ErrNoNewData reports that the database holds no complete new data since the previous
// assessment. The run then finishes with status "skipped"; callers should treat it as a
// successful no-op (the runner exits 0 and writes nothing to the database).
var ErrNoNewData = errors.New("no new data")

// DataWindowSpec is the workflow-level `data_window:` block: the measurement window every
// `input_series.db` input reads, resolved relative to the newest data in the database.
type DataWindowSpec struct {
	Mode        string `yaml:"mode" json:"mode"`
	Span        string `yaml:"span" json:"span,omitempty"`
	InitialSpan string `yaml:"initial_span" json:"initialSpan,omitempty"`
	MaxSpan     string `yaml:"max_span" json:"maxSpan,omitempty"`
}

// DataWindowInfo is the resolved window recorded in `_run.data_window`.
type DataWindowInfo struct {
	Mode     string   `json:"mode"`
	Resolved bool     `json:"resolved"`
	Source   string   `json:"source"`
	Site     string   `json:"site,omitempty"`
	Channels []string `json:"channels,omitempty"`
	// From and To bound the half-open window [from, to) in RFC 3339 (empty when unresolved).
	From           string  `json:"from,omitempty"`
	To             string  `json:"to,omitempty"`
	SpanSeconds    float64 `json:"span_seconds"`
	SimStartTime   float64 `json:"sim_start_time"`
	SimStopTime    float64 `json:"sim_stop_time"`
	LatestSample   string  `json:"latest_sample,omitempty"`
	BacklogSeconds float64 `json:"backlog_seconds"`
	PreviousRunID  string  `json:"previous_run_id,omitempty"`
	PreviousTo     string  `json:"previous_to,omitempty"`
}

// CarryOverInfo records where carried-over start values came from
// (`_run.steps[].fmus[].carry_over`).
type CarryOverInfo struct {
	Source string            `json:"source"`
	RunID  string            `json:"run_id,omitempty"`
	Refs   map[string]string `json:"refs"`
	Values map[string]any    `json:"values"`
}

// dataWindowPlan is a validated DataWindowSpec.
type dataWindowPlan struct {
	mode        string
	span        time.Duration // latest
	initialSpan time.Duration // since_previous_run
	maxSpan     time.Duration // since_previous_run
	align       time.Duration
	site        string
	channels    []string // sorted unique db channels of all inheriting inputs
}

// firstSpan is the span of a run without a previous run (also the fallback span).
func (p *dataWindowPlan) firstSpan() time.Duration {
	if p.mode == DataWindowLatest {
		return p.span
	}
	return p.initialSpan
}

// activeWindow is the window of the current run: resolved against the database, or the
// unresolved fallback (sim time 0..span, no absolute times).
type activeWindow struct {
	info     *DataWindowInfo
	from, to time.Time
	simStart float64
	simStop  float64
}

func (w *activeWindow) resolved() bool { return w != nil && w.info.Resolved }

// parseWindowDuration accepts Go durations (`36h`, `90m`), a day suffix (`7d`, `1.5d`) or bare
// seconds (`86400`).
func parseWindowDuration(value string) (time.Duration, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return 0, errors.New("is empty")
	}
	if seconds, err := strconv.ParseFloat(text, 64); err == nil {
		return time.Duration(seconds * float64(time.Second)), nil
	}
	if strings.HasSuffix(text, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(text, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration (use e.g. 24h, 7d or seconds)", value)
		}
		return time.Duration(days * 24 * float64(time.Hour)), nil
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration (use e.g. 24h, 7d or seconds)", value)
	}
	return parsed, nil
}

// windowAlign is the largest cosim communication_step, else the largest step_size, else 1 s.
func windowAlign(steps []workflowStep) time.Duration {
	largestComm, largestStep := 0.0, 0.0
	for _, step := range steps {
		if step.CoSim != nil && step.CoSim.CommunicationStep != nil && *step.CoSim.CommunicationStep > largestComm {
			largestComm = *step.CoSim.CommunicationStep
		}
		if step.StepSize != nil && *step.StepSize > largestStep {
			largestStep = *step.StepSize
		}
	}
	seconds := 1.0
	switch {
	case largestComm > 0:
		seconds = largestComm
	case largestStep > 0:
		seconds = largestStep
	}
	return time.Duration(seconds * float64(time.Second))
}

// dbInputRef is one `input_series.db` input of a step or cosim model.
type dbInputRef struct {
	step  string
	model string // "" for plain fmu steps
	spec  *dbInputSeriesSpec
}

func (r dbInputRef) label() string {
	if r.model == "" {
		return "step " + r.step
	}
	return "step " + r.step + " model " + r.model
}

func collectDBInputs(steps []workflowStep) []dbInputRef {
	var refs []dbInputRef
	for _, step := range steps {
		if step.CoSim != nil {
			for _, model := range step.CoSim.Models {
				if model.InputSeries != nil && model.InputSeries.DB != nil {
					refs = append(refs, dbInputRef{step: step.Name, model: model.Name, spec: model.InputSeries.DB})
				}
			}
			continue
		}
		if step.InputSeries != nil && step.InputSeries.DB != nil {
			refs = append(refs, dbInputRef{step: step.Name, spec: step.InputSeries.DB})
		}
	}
	return refs
}

// validateDataWindow checks `data_window` and every `carry_over`; it returns nil, nil for
// workflows without a data window (which must not use carry_over either).
func validateDataWindow(doc *workflowFile) (*dataWindowPlan, error) {
	type carrySite struct {
		label string
		carry map[string]string
		from  map[string]string
	}
	var carries []carrySite
	stepNames := make(map[string]struct{}, len(doc.Steps))
	for _, step := range doc.Steps {
		stepNames[step.Name] = struct{}{}
		if step.CoSim != nil {
			for _, model := range step.CoSim.Models {
				if len(model.CarryOver) > 0 {
					carries = append(carries, carrySite{label: "step " + step.Name + " model " + model.Name, carry: model.CarryOver, from: model.StartFrom})
				}
			}
			continue
		}
		if len(step.CarryOver) > 0 {
			carries = append(carries, carrySite{label: "step " + step.Name, carry: step.CarryOver, from: step.StartFrom})
		}
	}

	spec := doc.DataWindow
	if spec == nil {
		if len(carries) > 0 {
			return nil, fmt.Errorf("%s: carry_over requires data_window with mode %s", carries[0].label, DataWindowSincePreviousRun)
		}
		return nil, nil
	}

	plan := &dataWindowPlan{mode: strings.TrimSpace(spec.Mode), align: windowAlign(doc.Steps)}
	checkSpan := func(field, value string) (time.Duration, error) {
		if strings.TrimSpace(value) == "" {
			return 0, fmt.Errorf("data_window.%s is required for mode %s", field, plan.mode)
		}
		span, err := parseWindowDuration(value)
		if err != nil {
			return 0, fmt.Errorf("data_window.%s %w", field, err)
		}
		if span <= 0 {
			return 0, fmt.Errorf("data_window.%s must be positive", field)
		}
		if span%plan.align != 0 {
			return 0, fmt.Errorf("data_window.%s %s must be a multiple of the communication step %s", field, span, plan.align)
		}
		return span, nil
	}
	var err error
	switch plan.mode {
	case DataWindowSincePreviousRun:
		if strings.TrimSpace(spec.Span) != "" {
			return nil, fmt.Errorf("data_window.span is not used by mode %s (use initial_span and max_span)", plan.mode)
		}
		if plan.initialSpan, err = checkSpan("initial_span", spec.InitialSpan); err != nil {
			return nil, err
		}
		if plan.maxSpan, err = checkSpan("max_span", spec.MaxSpan); err != nil {
			return nil, err
		}
	case DataWindowLatest:
		if strings.TrimSpace(spec.InitialSpan) != "" || strings.TrimSpace(spec.MaxSpan) != "" {
			return nil, fmt.Errorf("data_window.initial_span and max_span are not used by mode %s (use span)", plan.mode)
		}
		if plan.span, err = checkSpan("span", spec.Span); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("data_window.mode must be %s or %s (got %q)", DataWindowSincePreviousRun, DataWindowLatest, spec.Mode)
	}

	plan.site = strings.TrimSpace(doc.Metadata.SiteID)
	if err := cadsdb.CheckSite(plan.site); err != nil {
		return nil, fmt.Errorf("data_window requires metadata.site_id: %w", err)
	}
	inputs := collectDBInputs(doc.Steps)
	if len(inputs) == 0 {
		return nil, errors.New("data_window requires at least one input_series.db input")
	}
	channelSet := make(map[string]struct{})
	dbSteps := make(map[string]struct{})
	for _, input := range inputs {
		if input.spec.Window != nil {
			return nil, fmt.Errorf("%s: input_series.db.window must be omitted when the workflow sets data_window", input.label())
		}
		if site := strings.TrimSpace(input.spec.Site); site != plan.site {
			return nil, fmt.Errorf("%s: input_series.db.site %q must match metadata.site_id %q when the workflow sets data_window", input.label(), site, plan.site)
		}
		for _, channel := range input.spec.Channels {
			if channel = strings.TrimSpace(channel); channel != "" {
				channelSet[channel] = struct{}{}
			}
		}
		dbSteps[input.step] = struct{}{}
	}
	for channel := range channelSet {
		plan.channels = append(plan.channels, channel)
	}
	sort.Strings(plan.channels)
	for _, step := range doc.Steps {
		if _, reads := dbSteps[step.Name]; !reads {
			continue
		}
		if step.StartTime != nil || step.StopTime != nil ||
			(step.CoSim != nil && (step.CoSim.StartTime != nil || step.CoSim.StopTime != nil)) {
			return nil, fmt.Errorf("step %s: start_time and stop_time must be omitted; data_window sets them for steps reading input_series.db", step.Name)
		}
	}

	for _, site := range carries {
		if plan.mode != DataWindowSincePreviousRun {
			return nil, fmt.Errorf("%s: carry_over requires data_window mode %s (got %s)", site.label, DataWindowSincePreviousRun, plan.mode)
		}
		for _, target := range sortedKeys(site.carry) {
			if _, clash := site.from[target]; clash {
				return nil, fmt.Errorf("%s: carry_over[%s] is also set by start_from", site.label, target)
			}
			stepName, variable, ok := strings.Cut(strings.TrimSpace(site.carry[target]), ".")
			if !ok || stepName == "" || variable == "" {
				return nil, fmt.Errorf("%s: carry_over[%s] must use format step.variable", site.label, target)
			}
			if _, known := stepNames[stepName]; !known {
				return nil, fmt.Errorf("%s: carry_over[%s] references unknown step %s", site.label, target, stepName)
			}
		}
	}
	return plan, nil
}

// resolveWindow computes the half-open window [from, to) for the newest common sample time
// `latest` and the previous run (nil on the first run). backlog is the data left for later
// runs when the window was capped at max_span.
func resolveWindow(plan *dataWindowPlan, latest time.Time, prev *cadsdb.PreviousRun) (from, to time.Time, backlog time.Duration, err error) {
	latest = latest.UTC()
	switch plan.mode {
	case DataWindowLatest:
		if prev != nil && !latest.After(prev.To) {
			return time.Time{}, time.Time{}, 0, noNewDataError(latest, prev.To, 0, plan.align)
		}
		return latest.Add(-plan.span), latest, 0, nil
	default:
		if prev == nil {
			return latest.Add(-plan.initialSpan), latest, 0, nil
		}
		from = prev.To.UTC()
		available := latest.Sub(from)
		if available < plan.align {
			return time.Time{}, time.Time{}, 0, noNewDataError(latest, from, available, plan.align)
		}
		span := (available / plan.align) * plan.align
		if span > plan.maxSpan {
			span = plan.maxSpan
		}
		to = from.Add(span)
		return from, to, latest.Sub(to), nil
	}
}

func noNewDataError(latest, previousTo time.Time, available, align time.Duration) error {
	if !latest.After(previousTo) {
		return fmt.Errorf("data_window: %w: newest sample %s is not after the end of the previous assessment %s",
			ErrNoNewData, formatWindowTime(latest), formatWindowTime(previousTo))
	}
	return fmt.Errorf("data_window: %w: only %s of data since the previous assessment ended at %s, less than one communication step (%s)",
		ErrNoNewData, available, formatWindowTime(previousTo), align)
}

func formatWindowTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// secondsOfDay is the simulation start for a window starting at t: models use the time of day.
func secondsOfDay(t time.Time) float64 {
	t = t.UTC()
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return t.Sub(midnight).Seconds()
}

// planWindow resolves the window of the current run. A nil reader gives the unresolved fallback
// window (sim time 0..span). On ErrNoNewData the returned window still carries the
// latest-sample and previous-run provenance.
func planWindow(ctx context.Context, plan *dataWindowPlan, workflowPath string, reader cadsdb.WindowReader) (*activeWindow, *cadsdb.PreviousRun, error) {
	info := &DataWindowInfo{
		Mode:     plan.mode,
		Site:     plan.site,
		Channels: append([]string(nil), plan.channels...),
	}
	if reader == nil {
		span := plan.firstSpan()
		info.Source = DataWindowSourceFallback
		info.SpanSeconds = span.Seconds()
		info.SimStopTime = span.Seconds()
		return &activeWindow{info: info, simStop: span.Seconds()}, nil, nil
	}
	info.Source = DataWindowSourceDB
	latestByChannel, err := reader.LatestSamples(ctx, plan.site, plan.channels)
	if err != nil {
		return nil, nil, fmt.Errorf("data_window: %w", err)
	}
	var missing []string
	var latest time.Time
	for _, channel := range plan.channels {
		at, ok := latestByChannel[channel]
		if !ok {
			missing = append(missing, channel)
			continue
		}
		if latest.IsZero() || at.Before(latest) {
			latest = at
		}
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("data_window: channel(s) %s have no samples in %s.measurements", strings.Join(missing, ", "), plan.site)
	}
	latest = latest.UTC()
	info.LatestSample = formatWindowTime(latest)
	prev, err := reader.LatestSucceededRun(ctx, plan.site, workflowPath, plan.mode)
	if err != nil {
		return nil, nil, fmt.Errorf("data_window: %w", err)
	}
	if prev != nil {
		info.PreviousRunID = prev.RunID
		info.PreviousTo = formatWindowTime(prev.To)
	}
	window := &activeWindow{info: info}
	from, to, backlog, err := resolveWindow(plan, latest, prev)
	if err != nil {
		return window, prev, err
	}
	window.from, window.to = from, to
	window.simStart = secondsOfDay(from)
	window.simStop = window.simStart + to.Sub(from).Seconds()
	info.Resolved = true
	info.From = formatWindowTime(from)
	info.To = formatWindowTime(to)
	info.SpanSeconds = to.Sub(from).Seconds()
	info.SimStartTime = window.simStart
	info.SimStopTime = window.simStop
	info.BacklogSeconds = backlog.Seconds()
	return window, prev, nil
}

// windowReader returns the configured database reader as a WindowReader, or nil when no
// database is configured.
func (e *Executor) windowReader() (cadsdb.WindowReader, error) {
	reader, err := e.dbReader()
	if err != nil || reader == nil {
		return nil, err
	}
	windowed, ok := reader.(cadsdb.WindowReader)
	if !ok {
		return nil, errors.New("data_window: database reader does not support window resolution")
	}
	return windowed, nil
}

// resolveDataWindow sets e.window and e.prevRun for the current run.
func (e *Executor) resolveDataWindow(plan *dataWindowPlan, workflowPath string, rec *runRecorder) error {
	reader, err := e.windowReader()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbReadTimeout)
	defer cancel()
	window, prev, err := planWindow(ctx, plan, workflowPath, reader)
	if window != nil {
		rec.setDataWindow(window.info)
	}
	if err != nil {
		return err
	}
	e.window = window
	e.prevRun = prev
	if window.resolved() {
		e.logf("[workflow] data_window %s: %s..%s (sim %g..%g s)", plan.mode, window.info.From, window.info.To, window.simStart, window.simStop)
	} else {
		fmt.Fprintf(os.Stderr, "[workflow] data_window: %s not set, running the fallback inputs over %g s without carry-over\n", cadsdb.EnvURL, window.simStop)
	}
	return nil
}

// PlanDataWindow resolves the data window the workflow would assess next, without running it
// (CGO-free; used by the service before starting a local run). It returns nil, nil for
// workflows without data_window and an error wrapping ErrNoNewData, together with the
// latest-sample provenance, when the previous assessment is up to date. A nil reader gives the
// unresolved fallback window.
func PlanDataWindow(ctx context.Context, root, workflowPath string, reader cadsdb.WindowReader) (*DataWindowInfo, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workflow root %s: %w", root, err)
	}
	probe := &Executor{root: absRoot}
	absPath, err := probe.resolveRepoPath(workflowPath, "workflow")
	if err != nil {
		return nil, fmt.Errorf("invalid workflow path: %w", err)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read workflow %s: %w", absPath, err)
	}
	var doc workflowFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse workflow %s: %w", absPath, err)
	}
	if doc.DataWindow == nil {
		return nil, nil
	}
	if err := validateSteps(absPath, doc.Steps, true); err != nil {
		return nil, err
	}
	plan, err := validateDataWindow(&doc)
	if err != nil {
		return nil, err
	}
	window, _, err := planWindow(ctx, plan, probe.relPath(absPath), reader)
	if window == nil {
		return nil, err
	}
	return window.info, err
}

// resolveCarryOver resolves `carry_over` references against the previous run's outputs.
func (e *Executor) resolveCarryOver(carry map[string]string, literal map[string]any) (map[string]string, *CarryOverInfo, error) {
	if len(carry) == 0 {
		return nil, nil, nil
	}
	refs := make(map[string]string, len(carry))
	for target, ref := range carry {
		refs[target] = strings.TrimSpace(ref)
	}
	info := &CarryOverInfo{Refs: refs, Values: map[string]any{}}
	if e.prevRun == nil {
		info.Source = CarryOverSourceStartValues
		for target := range refs {
			if value, ok := literal[target]; ok {
				info.Values[target] = value
			}
		}
		return nil, info, nil
	}
	info.Source = CarryOverSourcePreviousRun
	info.RunID = e.prevRun.RunID
	encoded := make(map[string]string, len(refs))
	for _, target := range sortedKeys(refs) {
		stepName, variable, ok := strings.Cut(refs[target], ".")
		if !ok || stepName == "" || variable == "" {
			return nil, nil, fmt.Errorf("carry_over[%s] must use format step.variable", target)
		}
		value, ok := e.prevRun.Outputs[stepName][variable]
		if !ok {
			return nil, nil, fmt.Errorf("carry_over[%s] missing variable %s in step %s of previous run %s (add it to that step's outputs, or reset the assessment history)",
				target, variable, stepName, e.prevRun.RunID)
		}
		number, ok := numericValue(value)
		if !ok {
			return nil, nil, fmt.Errorf("carry_over[%s]: %s.%s of previous run %s is not numeric (%v)", target, stepName, variable, e.prevRun.RunID, value)
		}
		encoded[target] = formatFloat(number)
		info.Values[target] = number
	}
	return encoded, info, nil
}

func numericValue(value any) (float64, bool) {
	var number float64
	switch v := value.(type) {
	case float64:
		number = v
	case float32:
		number = float64(v)
	case int:
		number = float64(v)
	case int64:
		number = float64(v)
	case int32:
		number = float64(v)
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}
