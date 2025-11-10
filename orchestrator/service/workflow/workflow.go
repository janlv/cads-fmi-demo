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
	e := &Executor{root: repoRoot}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// Run executes a workflow file (relative to repo root unless absolute).
func (e *Executor) Run(workflowPath string) (map[string]map[string]any, error) {
	absPath := workflowPath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(e.root, workflowPath)
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

		fmuPath := step.FMU
		if !filepath.IsAbs(fmuPath) {
			fmuPath = filepath.Join(e.root, fmuPath)
		}
		if _, err := os.Stat(fmuPath); err != nil {
			return nil, fmt.Errorf("step %s references missing FMU %s: %w", step.Name, fmuPath, err)
		}

		startVals, err := e.buildStartValues(step, results)
		if err != nil {
			return nil, fmt.Errorf("step %s start values invalid: %w", step.Name, err)
		}

		cfg := fmi.Config{
			FMUPath:     fmuPath,
			StartValues: startVals,
			Outputs:     step.Outputs,
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
			if err := writeResultFile(e.resolvePath(step.ResultPath), result); err != nil {
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

func (e *Executor) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(e.root, p)
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
		return v, nil
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
