package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
)

var ErrPathEscapesRoot = errors.New("path escapes repository root")

// Executor runs workflow YAML definitions directly against FMUs via FMIL.
type Executor struct {
	root   string
	logger func(string, ...any)
}

// Option configures the executor.
type Option func(*Executor)

// WithLogger installs a printf-style logger for workflow progress.
func WithLogger(logger func(string, ...any)) Option {
	return func(e *Executor) {
		e.logger = logger
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
	e := &Executor{root: absRoot}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// Run executes a workflow file (relative to repo root unless absolute).
func (e *Executor) Run(workflowPath string) (map[string]map[string]any, error) {
	absPath, err := e.resolveRepoPath(workflowPath, "workflow")
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
	if len(doc.Steps) == 0 {
		return nil, fmt.Errorf("workflow %s does not define any steps", absPath)
	}

	results := make(map[string]map[string]any, len(doc.Steps))
	for _, step := range doc.Steps {
		if step.Name == "" {
			return nil, fmt.Errorf("workflow %s contains a step without name", absPath)
		}
		if _, exists := results[step.Name]; exists {
			return nil, fmt.Errorf("workflow step %s defined multiple times", step.Name)
		}
		if step.FMU == "" {
			return nil, fmt.Errorf("step %s is missing its fmu path", step.Name)
		}

		fmuPath, err := e.resolveRepoPath(step.FMU, "fmu")
		if err != nil {
			return nil, fmt.Errorf("step %s invalid fmu path: %w", step.Name, err)
		}
		if _, err := os.Stat(fmuPath); err != nil {
			return nil, fmt.Errorf("step %s references missing FMU %s: %w", step.Name, fmuPath, err)
		}

		startVals, err := e.buildStartValues(step, results)
		if err != nil {
			return nil, fmt.Errorf("step %s start values invalid: %w", step.Name, err)
		}

		inputSeries, err := e.buildInputSeries(step)
		if err != nil {
			return nil, fmt.Errorf("step %s input series invalid: %w", step.Name, err)
		}
		trace, err := e.buildTraceConfig(step)
		if err != nil {
			return nil, fmt.Errorf("step %s trace config invalid: %w", step.Name, err)
		}

		cfg := fmi.Config{
			FMUPath:     fmuPath,
			StartValues: startVals,
			Outputs:     step.Outputs,
			InputSeries: inputSeries,
			Trace:       trace,
		}
		if step.StartTime != nil {
			cfg.StartTime = step.StartTime
		}
		if step.StopTime != nil {
			cfg.StopTime = step.StopTime
		}
		if step.StepSize != nil {
			cfg.StepSize = step.StepSize
		}

		result, err := fmi.Run(cfg)
		if err != nil {
			return nil, fmt.Errorf("step %s failed: %w", step.Name, err)
		}

		results[step.Name] = result
		if step.ResultPath != "" {
			resultPath, err := e.resolveRepoPath(step.ResultPath, "result")
			if err != nil {
				return nil, fmt.Errorf("step %s invalid result path: %w", step.Name, err)
			}
			if err := writeResultFile(resultPath, result); err != nil {
				return nil, fmt.Errorf("write result for step %s: %w", step.Name, err)
			}
		}
		e.logf("[workflow] Step %s completed. Outputs: %v", step.Name, result)
	}

	return results, nil
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
	Steps []workflowStep `yaml:"steps"`
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
	InputSeries *inputSeriesSpec   `yaml:"input_series"`
	Trace       *traceSpec         `yaml:"trace"`
}

type inputSeriesSpec struct {
	CSV string `yaml:"csv"`
}

type traceSpec struct {
	Outputs     []string `yaml:"outputs"`
	Inputs      []string `yaml:"inputs"`
	SampleEvery *float64 `yaml:"sample_every"`
}

func (e *Executor) buildStartValues(step workflowStep, results map[string]map[string]any) (map[string]string, error) {
	values := make(map[string]string)
	if len(step.StartValues) > 0 {
		keys := make([]string, 0, len(step.StartValues))
		for key := range step.StartValues {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			encoded, err := encodeScalar(step.StartValues[key])
			if err != nil {
				return nil, fmt.Errorf("start_values[%s]: %w", key, err)
			}
			values[key] = encoded
		}
	}

	for target, reference := range step.StartFrom {
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

func (e *Executor) buildInputSeries(step workflowStep) (*fmi.InputSeriesConfig, error) {
	if step.InputSeries == nil {
		return nil, nil
	}
	if strings.TrimSpace(step.InputSeries.CSV) == "" {
		return nil, fmt.Errorf("input_series.csv is required")
	}
	csvPath, err := e.resolveRepoPath(step.InputSeries.CSV, "input series")
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(csvPath); err != nil {
		return nil, fmt.Errorf("missing CSV %s: %w", csvPath, err)
	}
	return &fmi.InputSeriesConfig{CSVPath: csvPath}, nil
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
	return fmt.Sprintf("%.9g", v)
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
