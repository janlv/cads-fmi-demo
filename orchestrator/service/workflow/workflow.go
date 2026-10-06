package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
)

var ErrPathEscapesRoot = errors.New("path escapes repository root")

const syntheticCaseStepName = "_synthetic_case"

// Executor runs workflow YAML definitions directly against FMUs via FMIL.
type Executor struct {
	root         string
	logger       func(string, ...any)
	s3Downloader s3DownloadFunc
	runFMU       func(fmi.Config) (*fmi.Result, error)
	runCoSim     func(fmi.CoSimConfig) (*fmi.Result, error)
	now          func() time.Time
	version      string
	cancelled    atomic.Bool
}

// Option configures the executor.
type Option func(*Executor)

// WithLogger installs a printf-style logger for workflow progress.
func WithLogger(logger func(string, ...any)) Option {
	return func(e *Executor) {
		e.logger = logger
	}
}

// WithS3Downloader overrides the S3 object fetcher used for workflow input downloads.
func WithS3Downloader(downloader s3DownloadFunc) Option {
	return func(e *Executor) {
		e.s3Downloader = downloader
	}
}

// WithRunnerVersion stamps the runner version into the `_run` provenance block.
func WithRunnerVersion(version string) Option {
	return func(e *Executor) {
		e.version = version
	}
}

// WithClock overrides the wall clock (tests).
func WithClock(now func() time.Time) Option {
	return func(e *Executor) {
		e.now = now
	}
}

// withFMIRunners replaces the bridge entry points (tests).
func withFMIRunners(runFMU func(fmi.Config) (*fmi.Result, error), runCoSim func(fmi.CoSimConfig) (*fmi.Result, error)) Option {
	return func(e *Executor) {
		if runFMU != nil {
			e.runFMU = runFMU
		}
		if runCoSim != nil {
			e.runCoSim = runCoSim
		}
	}
}

// NewExecutor creates a workflow executor rooted at repoRoot.
func NewExecutor(repoRoot string, opts ...Option) (*Executor, error) {
	if repoRoot == "" {
		return nil, errors.New("workflow executor requires a repository root")
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workflow root %s: %w", repoRoot, err)
	}
	e := &Executor{
		root:     absRoot,
		runFMU:   fmi.Run,
		runCoSim: fmi.RunCoSim,
		now:      time.Now,
		version:  "dev",
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.s3Downloader == nil {
		e.s3Downloader = defaultS3Downloader
	}
	fmi.ResetCancel()
	return e, nil
}

// Cancel asks the running workflow to stop at the next step boundary and the bridge to stop at
// the next communication point. The run then finishes with status "cancelled".
func (e *Executor) Cancel() {
	e.cancelled.Store(true)
	fmi.RequestCancel()
}

// Run executes a workflow file (relative to repo root unless absolute).
//
// The returned map always carries the `_run` pseudo-step (status, timing, provenance), also when
// err is non-nil, so callers can report partial results and the failing step (ARCH-COMP-017).
func (e *Executor) Run(workflowPath string) (map[string]map[string]any, error) {
	rec := newRunRecorder(e.now, e.version, e.root)
	results := make(map[string]map[string]any)
	// The cancel flag is deliberately NOT reset here: a cancellation requested before Run starts
	// (e.g. SIGTERM delivered between signal.Notify and Run) must still take effect.
	err := e.run(workflowPath, results, rec)
	results[RunInfoStepName] = rec.finish(err)
	return results, err
}

func (e *Executor) run(workflowPath string, results map[string]map[string]any, rec *runRecorder) error {
	absPath, err := e.resolveRepoPath(workflowPath, "workflow")
	if err != nil {
		return fmt.Errorf("invalid workflow path: %w", err)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read workflow %s: %w", absPath, err)
	}
	if rel, relErr := filepath.Rel(e.root, absPath); relErr == nil {
		rec.setWorkflow(rel, data)
	} else {
		rec.setWorkflow(workflowPath, data)
	}

	var doc workflowFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse workflow %s: %w", absPath, err)
	}
	if len(doc.Steps) == 0 {
		return fmt.Errorf("workflow %s does not define any steps", absPath)
	}
	if err := validateSteps(absPath, doc.Steps); err != nil {
		return err
	}

	if doc.SyntheticCase != nil {
		syntheticCase, err := e.loadSyntheticCase(doc.SyntheticCase)
		if err != nil {
			return fmt.Errorf("synthetic_case invalid: %w", err)
		}
		results[syntheticCaseStepName] = syntheticCase
	}

	for index, step := range doc.Steps {
		if e.cancelled.Load() {
			for _, rest := range doc.Steps[index:] {
				rec.stepSkipped(rest.Name, stepKind(rest))
			}
			return &StepError{Step: step.Name, Err: fmi.ErrCancelled}
		}
		var stepErr error
		if step.CoSim != nil {
			stepErr = e.runCoSimStep(step, results, rec)
		} else {
			stepErr = e.runFMUStep(step, results, rec)
		}
		if stepErr != nil {
			for _, rest := range doc.Steps[index+1:] {
				rec.stepSkipped(rest.Name, stepKind(rest))
			}
			return &StepError{Step: step.Name, Err: stepErr}
		}
		if step.ResultPath != "" {
			resultPath, err := e.resolveRepoPath(step.ResultPath, "result")
			if err != nil {
				return &StepError{Step: step.Name, Err: fmt.Errorf("invalid result path: %w", err)}
			}
			if err := writeResultFile(resultPath, results[step.Name]); err != nil {
				return &StepError{Step: step.Name, Err: fmt.Errorf("write result: %w", err)}
			}
		}
		e.logf("[workflow] Step %s completed. Outputs: %v", step.Name, results[step.Name])
	}
	return nil
}

func stepKind(step workflowStep) string {
	if step.CoSim != nil {
		return StepKindCoSim
	}
	return StepKindFMU
}

// validateSteps checks names and the fmu/cosim exclusivity for every step before anything runs.
func validateSteps(workflowPath string, steps []workflowStep) error {
	seen := make(map[string]struct{}, len(steps))
	for _, step := range steps {
		if step.Name == "" {
			return fmt.Errorf("workflow %s contains a step without name", workflowPath)
		}
		if strings.HasPrefix(step.Name, "_") {
			return fmt.Errorf("workflow step name %s is reserved (names starting with _ are reserved)", step.Name)
		}
		if strings.Contains(step.Name, ".") {
			return fmt.Errorf("workflow step name %s must not contain a dot", step.Name)
		}
		if _, dup := seen[step.Name]; dup {
			return fmt.Errorf("workflow step %s defined multiple times", step.Name)
		}
		seen[step.Name] = struct{}{}
		if step.CoSim != nil {
			if step.FMU != "" || step.StepSize != nil || step.StartTime != nil || step.StopTime != nil ||
				len(step.StartValues) > 0 || len(step.StartFrom) > 0 || step.InputSeries != nil ||
				step.Trace != nil || len(step.Outputs) > 0 {
				return fmt.Errorf("step %s: cosim steps must not also set fmu, timing, start values, input_series, trace or outputs at step level", step.Name)
			}
			if err := ValidateCoSim(step.Name, step.CoSim); err != nil {
				return err
			}
			continue
		}
		if step.FMU == "" {
			return fmt.Errorf("step %s is missing its fmu path", step.Name)
		}
	}
	return nil
}

func (e *Executor) runFMUStep(step workflowStep, results map[string]map[string]any, rec *runRecorder) error {
	info := StepInfo{Name: step.Name, Kind: StepKindFMU}
	started := e.now()
	fail := func(err error) error {
		rec.stepDone(info, e.now().Sub(started), nil, err)
		return err
	}

	fmuPath, err := e.resolveRepoPath(step.FMU, "fmu")
	if err != nil {
		return fail(fmt.Errorf("invalid fmu path: %w", err))
	}
	if _, err := os.Stat(fmuPath); err != nil {
		return fail(fmt.Errorf("references missing FMU %s: %w", fmuPath, err))
	}
	info.FMUs = []FMUDescriptor{rec.describeFMU(step.Name, fmuPath)}

	startVals, err := e.buildStartValues(step.StartValues, step.StartFrom, results)
	if err != nil {
		return fail(fmt.Errorf("start values invalid: %w", err))
	}
	inputSeries, err := e.buildInputSeries(step.InputSeries)
	if err != nil {
		return fail(fmt.Errorf("input series invalid: %w", err))
	}
	trace, err := e.buildTraceConfig(step)
	if err != nil {
		return fail(fmt.Errorf("trace config invalid: %w", err))
	}

	cfg := fmi.Config{
		FMUPath:     fmuPath,
		StartValues: startVals,
		Outputs:     step.Outputs,
		Trace:       trace,
		StartTime:   step.StartTime,
		StopTime:    step.StopTime,
		StepSize:    step.StepSize,
	}
	if inputSeries != nil {
		cfg.InputSeries = inputSeries.Config
	}

	result, runErr := e.runFMU(cfg)
	if inputSeries != nil && inputSeries.Cleanup != nil {
		inputSeries.Cleanup()
	}
	var stats *fmi.Stats
	if result != nil {
		stats = &result.Stats
	}
	rec.stepDone(info, e.now().Sub(started), stats, runErr)
	if runErr != nil {
		return runErr
	}
	if result.Values == nil {
		result.Values = map[string]any{}
	}
	results[step.Name] = result.Values
	return nil
}

func (e *Executor) runCoSimStep(step workflowStep, results map[string]map[string]any, rec *runRecorder) error {
	info := StepInfo{
		Name:              step.Name,
		Kind:              StepKindCoSim,
		Scheme:            strings.ToLower(strings.TrimSpace(step.CoSim.Scheme)),
		CommunicationStep: step.CoSim.CommunicationStep,
	}
	started := e.now()
	fail := func(err error) error {
		rec.stepDone(info, e.now().Sub(started), nil, err)
		return err
	}

	resolved, err := e.buildCoSimConfig(step.Name, step.CoSim, results)
	if err != nil {
		return fail(err)
	}
	defer resolved.Cleanup()
	for _, model := range step.CoSim.Models {
		info.FMUs = append(info.FMUs, rec.describeFMU(model.Name, resolved.FMUPaths[model.Name]))
	}

	result, runErr := e.runCoSim(resolved.Config)
	var stats *fmi.Stats
	if result != nil {
		stats = &result.Stats
	}
	rec.stepDone(info, e.now().Sub(started), stats, runErr)
	if runErr != nil {
		return runErr
	}
	if result.Values == nil {
		result.Values = map[string]any{}
	}
	results[step.Name] = result.Values
	return nil
}

func (e *Executor) logf(format string, args ...any) {
	if e.logger != nil {
		e.logger(format, args...)
	}
}

func (e *Executor) resolveRepoPath(path string, kind string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%s path is required", kind)
	}

	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(e.root, resolved)
	}
	resolved = filepath.Clean(resolved)

	rel, err := filepath.Rel(e.root, resolved)
	if err != nil {
		return "", fmt.Errorf("resolve %s path %q: %w", kind, path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %s %q", ErrPathEscapesRoot, kind, path)
	}
	return resolved, nil
}

type workflowFile struct {
	SyntheticCase any            `yaml:"synthetic_case"`
	Steps         []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	Name        string            `yaml:"name"`
	FMU         string            `yaml:"fmu"`
	Outputs     []string          `yaml:"outputs"`
	StartTime   *float64          `yaml:"start_time"`
	StopTime    *float64          `yaml:"stop_time"`
	StepSize    *float64          `yaml:"step_size"`
	ResultPath  string            `yaml:"result"`
	StartValues map[string]any    `yaml:"start_values"`
	StartFrom   map[string]string `yaml:"start_from"`
	InputSeries *inputSeriesSpec  `yaml:"input_series"`
	Trace       *traceSpec        `yaml:"trace"`
	CoSim       *CoSimSpec        `yaml:"cosim"`
}

type inputSeriesSpec struct {
	CSV string             `yaml:"csv"`
	S3  *s3InputSeriesSpec `yaml:"s3"`
}

type s3InputSeriesSpec struct {
	Bucket         string `yaml:"bucket"`
	Key            string `yaml:"key"`
	Region         string `yaml:"region"`
	Endpoint       string `yaml:"endpoint"`
	ForcePathStyle *bool  `yaml:"force_path_style"`
}

type traceSpec struct {
	Outputs     []string `yaml:"outputs"`
	Inputs      []string `yaml:"inputs"`
	SampleEvery *float64 `yaml:"sample_every"`
}

func (e *Executor) loadSyntheticCase(spec any) (map[string]any, error) {
	switch value := spec.(type) {
	case string:
		path := strings.TrimSpace(value)
		if path == "" {
			return nil, fmt.Errorf("path is required")
		}
		casePath, err := e.resolveRepoPath(path, "synthetic case")
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(casePath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", casePath, err)
		}
		var loaded map[string]any
		if err := yaml.Unmarshal(data, &loaded); err != nil {
			return nil, fmt.Errorf("parse %s: %w", casePath, err)
		}
		loaded, err = normalizeStringMap(loaded)
		if err != nil {
			return nil, err
		}
		loaded["source"] = filepath.ToSlash(path)
		return loaded, nil
	case map[string]any:
		return normalizeStringMap(value)
	default:
		return nil, fmt.Errorf("must be a repo-local YAML/JSON path or mapping")
	}
}

func normalizeStringMap(input map[string]any) (map[string]any, error) {
	output := make(map[string]any, len(input))
	for key, value := range input {
		normalized, err := normalizeSyntheticCaseValue(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		output[key] = normalized
	}
	return output, nil
}

func normalizeSyntheticCaseValue(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		return normalizeStringMap(typed)
	case []any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			normalized, err := normalizeSyntheticCaseValue(item)
			if err != nil {
				return nil, err
			}
			items = append(items, normalized)
		}
		return items, nil
	default:
		return typed, nil
	}
}

// buildStartValues encodes literal start values and resolves `start_from` references of the form
// `step.variable` against earlier results. Cosim results use flattened `model.variable` keys, so a
// reference like `cosim_step.battery.soc_percent` splits into step `cosim_step` and key
// `battery.soc_percent`.
func (e *Executor) buildStartValues(literal map[string]any, from map[string]string, results map[string]map[string]any) (map[string]string, error) {
	values := make(map[string]string)
	for _, key := range sortedKeys(literal) {
		encoded, err := encodeScalar(literal[key])
		if err != nil {
			return nil, fmt.Errorf("start_values[%s]: %w", key, err)
		}
		values[key] = encoded
	}

	for _, target := range sortedKeys(from) {
		reference := from[target]
		stepName, variable, ok := strings.Cut(reference, ".")
		if !ok || stepName == "" || variable == "" {
			return nil, fmt.Errorf("start_from[%s] must use format step.variable", target)
		}
		stepResult, exists := results[stepName]
		if !exists {
			return nil, fmt.Errorf("start_from[%s] references unknown step %s", target, stepName)
		}
		value, ok := stepResult[variable]
		if !ok {
			return nil, fmt.Errorf("start_from[%s] missing variable %s in step %s", target, variable, stepName)
		}
		encoded, err := encodeScalar(value)
		if err != nil {
			return nil, fmt.Errorf("start_from[%s]: %w", target, err)
		}
		values[target] = encoded
	}

	return values, nil
}

type resolvedInputSeries struct {
	Config  *fmi.InputSeriesConfig
	Cleanup func()
}

func (e *Executor) buildInputSeries(spec *inputSeriesSpec) (*resolvedInputSeries, error) {
	if spec == nil {
		return nil, nil
	}

	hasCSV := strings.TrimSpace(spec.CSV) != ""
	hasS3 := spec.S3 != nil

	switch {
	case hasCSV && hasS3:
		return nil, fmt.Errorf("input_series must define exactly one source")
	case hasCSV:
		csvPath, err := e.resolveRepoPath(spec.CSV, "input series")
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(csvPath); err != nil {
			return nil, fmt.Errorf("missing CSV %s: %w", csvPath, err)
		}
		return &resolvedInputSeries{Config: &fmi.InputSeriesConfig{CSVPath: csvPath}}, nil
	case hasS3:
		return e.buildS3InputSeries(*spec.S3)
	default:
		return nil, fmt.Errorf("input_series.csv or input_series.s3 is required")
	}
}

func (e *Executor) buildTraceConfig(step workflowStep) (*fmi.TraceConfig, error) {
	if step.Trace == nil {
		return nil, nil
	}
	trace := &fmi.TraceConfig{
		Outputs: append([]string(nil), step.Trace.Outputs...),
		Inputs:  append([]string(nil), step.Trace.Inputs...),
	}
	if step.Trace.SampleEvery != nil {
		if *step.Trace.SampleEvery <= 0 {
			return nil, fmt.Errorf("sample_every must be positive")
		}
		trace.SampleEvery = step.Trace.SampleEvery
	}
	if len(trace.Outputs) == 0 && len(trace.Inputs) == 0 {
		return nil, fmt.Errorf("trace must request at least one input or output")
	}
	return trace, nil
}

func encodeScalar(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", errors.New("value is null")
	case bool:
		if v {
			return "1", nil
		}
		return "0", nil
	case int:
		return fmt.Sprintf("%d", v), nil
	case int64:
		return fmt.Sprintf("%d", v), nil
	case int32:
		return fmt.Sprintf("%d", v), nil
	case uint:
		return fmt.Sprintf("%d", v), nil
	case uint64:
		return fmt.Sprintf("%d", v), nil
	case float64:
		return formatFloat(v), nil
	case float32:
		return formatFloat(float64(v)), nil
	case json.Number:
		return v.String(), nil
	case string:
		return "", errors.New("string values are not supported by the FMIL runner")
	default:
		return "", fmt.Errorf("unsupported value type %T", value)
	}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func writeResultFile(path string, result map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
