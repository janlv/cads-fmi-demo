package workflow

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
)

// CoSimSpec is the YAML `cosim:` block of a workflow step: several FMUs advanced together with
// communication points, value exchange over declared connections, and optional condition-driven
// events (ARCH-COMP-003).
type CoSimSpec struct {
	Scheme            string           `yaml:"scheme" json:"scheme"`
	StartTime         *float64         `yaml:"start_time" json:"startTime,omitempty"`
	StopTime          *float64         `yaml:"stop_time" json:"stopTime,omitempty"`
	CommunicationStep *float64         `yaml:"communication_step" json:"communicationStep,omitempty"`
	Models            []CoSimModelSpec `yaml:"models" json:"models"`
	Connections       []ConnectionSpec `yaml:"connections" json:"connections"`
	Events            []EventSpec      `yaml:"events" json:"events,omitempty"`
	Outputs           []string         `yaml:"outputs" json:"outputs,omitempty"`
	Trace             *CoSimTraceSpec  `yaml:"trace" json:"trace,omitempty"`
}

// CoSimModelSpec is one participating FMU. StepSize is only parsed so it can be rejected: each FMU
// advances with the step size declared in its own model description (ARCH-COMP-002).
type CoSimModelSpec struct {
	Name        string            `yaml:"name" json:"name"`
	FMU         string            `yaml:"fmu" json:"fmu"`
	StartValues map[string]any    `yaml:"start_values" json:"startValues,omitempty"`
	StartFrom   map[string]string `yaml:"start_from" json:"startFrom,omitempty"`
	InputSeries *inputSeriesSpec  `yaml:"input_series" json:"-"`
	StepSize    *float64          `yaml:"step_size" json:"-"`
}

// ConnectionSpec routes `from: model.var` into `to: model.var` at every communication point.
type ConnectionSpec struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
}

// EventSpec drives `set` while `when` holds (level) or for one interval after a rising edge (pulse).
type EventSpec struct {
	Name  string   `yaml:"name" json:"name"`
	When  string   `yaml:"when" json:"when"`
	Set   string   `yaml:"set" json:"set"`
	Value *float64 `yaml:"value" json:"value,omitempty"`
	Reset *float64 `yaml:"reset" json:"reset,omitempty"`
	Mode  string   `yaml:"mode" json:"mode,omitempty"`
}

// CoSimTraceSpec samples `model.var` signals at communication points.
type CoSimTraceSpec struct {
	Signals     []string `yaml:"signals" json:"signals"`
	SampleEvery *float64 `yaml:"sample_every" json:"sampleEvery,omitempty"`
}

const (
	// SchemeJacobi advances every model from the same exchanged inputs, then exchanges (lock-step).
	SchemeJacobi = "jacobi"
	// SchemeGaussSeidel advances models in listed order, each seeing the outputs of the models
	// already advanced in the current interval (ping-pong).
	SchemeGaussSeidel = "gauss_seidel"

	eventModeLevel = "level"
	eventModePulse = "pulse"

	reservedCoSimModelName = "events"
)

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	conditionPattern  = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9_]*)\.(\S+?)\s*(<=|>=|==|!=|<|>)\s*([-+]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][-+]?[0-9]+)?)\s*$`)
)

// IsValidIdentifier reports whether s is a legal step or model name.
func IsValidIdentifier(s string) bool {
	return identifierPattern.MatchString(s)
}

// ValidateCoSim checks a cosim block for structural errors before any FMU is loaded. Variable
// existence is checked by the bridge, which can read the model descriptions.
func ValidateCoSim(stepName string, spec *CoSimSpec) error {
	if spec == nil {
		return fmt.Errorf("step %s: cosim block is empty", stepName)
	}
	if _, ok := fmi.SchemeCode(spec.Scheme); !ok {
		return fmt.Errorf("step %s: cosim.scheme must be %s or %s (got %q)", stepName, SchemeGaussSeidel, SchemeJacobi, spec.Scheme)
	}
	if spec.StartTime == nil || spec.StopTime == nil || spec.CommunicationStep == nil {
		return fmt.Errorf("step %s: cosim requires start_time, stop_time and communication_step", stepName)
	}
	if *spec.CommunicationStep <= 0 {
		return fmt.Errorf("step %s: cosim.communication_step must be positive", stepName)
	}
	if *spec.StopTime <= *spec.StartTime {
		return fmt.Errorf("step %s: cosim.stop_time must be greater than start_time", stepName)
	}
	if len(spec.Models) == 0 {
		return fmt.Errorf("step %s: cosim requires at least one model", stepName)
	}
	models := make(map[string]struct{}, len(spec.Models))
	for i, model := range spec.Models {
		if !IsValidIdentifier(model.Name) {
			return fmt.Errorf("step %s: cosim model %d has invalid name %q", stepName, i, model.Name)
		}
		if model.Name == reservedCoSimModelName {
			return fmt.Errorf("step %s: cosim model name %q is reserved", stepName, model.Name)
		}
		if _, dup := models[model.Name]; dup {
			return fmt.Errorf("step %s: cosim model %s defined multiple times", stepName, model.Name)
		}
		models[model.Name] = struct{}{}
		if strings.TrimSpace(model.FMU) == "" {
			return fmt.Errorf("step %s: cosim model %s is missing its fmu path", stepName, model.Name)
		}
		if model.StepSize != nil {
			return fmt.Errorf("step %s: cosim model %s: step_size is not allowed; each FMU advances with its modelDescription DefaultExperiment stepSize (ARCH-COMP-002)", stepName, model.Name)
		}
	}

	targets := make(map[string]string, len(spec.Connections))
	for i, conn := range spec.Connections {
		from, err := parseVarRef(conn.From)
		if err != nil {
			return fmt.Errorf("step %s: connection %d from: %w", stepName, i, err)
		}
		to, err := parseVarRef(conn.To)
		if err != nil {
			return fmt.Errorf("step %s: connection %d to: %w", stepName, i, err)
		}
		if _, ok := models[from.Model]; !ok {
			return fmt.Errorf("step %s: connection %d references unknown model %s", stepName, i, from.Model)
		}
		if _, ok := models[to.Model]; !ok {
			return fmt.Errorf("step %s: connection %d references unknown model %s", stepName, i, to.Model)
		}
		if from.Model == to.Model {
			return fmt.Errorf("step %s: connection %d connects model %s to itself", stepName, i, from.Model)
		}
		if prev, dup := targets[to.String()]; dup {
			return fmt.Errorf("step %s: input %s is driven by both %s and %s", stepName, to, prev, from)
		}
		targets[to.String()] = from.String()
	}

	eventNames := make(map[string]struct{}, len(spec.Events))
	eventTargets := make(map[string]string, len(spec.Events))
	for i, event := range spec.Events {
		if !IsValidIdentifier(event.Name) {
			return fmt.Errorf("step %s: event %d has invalid name %q", stepName, i, event.Name)
		}
		if _, dup := eventNames[event.Name]; dup {
			return fmt.Errorf("step %s: event %s defined multiple times", stepName, event.Name)
		}
		eventNames[event.Name] = struct{}{}
		lhs, _, _, err := parseCondition(event.When)
		if err != nil {
			return fmt.Errorf("step %s: event %s: %w", stepName, event.Name, err)
		}
		if _, ok := models[lhs.Model]; !ok {
			return fmt.Errorf("step %s: event %s references unknown model %s", stepName, event.Name, lhs.Model)
		}
		target, err := parseVarRef(event.Set)
		if err != nil {
			return fmt.Errorf("step %s: event %s set: %w", stepName, event.Name, err)
		}
		if _, ok := models[target.Model]; !ok {
			return fmt.Errorf("step %s: event %s sets unknown model %s", stepName, event.Name, target.Model)
		}
		if _, conflict := targets[target.String()]; conflict {
			return fmt.Errorf("step %s: event %s sets %s, which is already driven by a connection", stepName, event.Name, target)
		}
		if other, conflict := eventTargets[target.String()]; conflict {
			return fmt.Errorf("step %s: events %s and %s both set %s", stepName, other, event.Name, target)
		}
		eventTargets[target.String()] = event.Name
		switch strings.ToLower(strings.TrimSpace(event.Mode)) {
		case "", eventModeLevel, eventModePulse:
		default:
			return fmt.Errorf("step %s: event %s: mode must be level or pulse", stepName, event.Name)
		}
	}

	for i, output := range spec.Outputs {
		ref, err := parseVarRef(output)
		if err != nil {
			return fmt.Errorf("step %s: outputs[%d]: %w", stepName, i, err)
		}
		if _, ok := models[ref.Model]; !ok && ref.Model != reservedCoSimModelName {
			return fmt.Errorf("step %s: outputs[%d] references unknown model %s", stepName, i, ref.Model)
		}
	}
	if spec.Trace != nil {
		if len(spec.Trace.Signals) == 0 {
			return fmt.Errorf("step %s: cosim.trace must list at least one signal", stepName)
		}
		if spec.Trace.SampleEvery != nil && *spec.Trace.SampleEvery <= 0 {
			return fmt.Errorf("step %s: cosim.trace.sample_every must be positive", stepName)
		}
		for i, signal := range spec.Trace.Signals {
			ref, err := parseVarRef(signal)
			if err != nil {
				return fmt.Errorf("step %s: trace.signals[%d]: %w", stepName, i, err)
			}
			if _, ok := models[ref.Model]; !ok && ref.Model != reservedCoSimModelName {
				return fmt.Errorf("step %s: trace.signals[%d] references unknown model %s", stepName, i, ref.Model)
			}
		}
	}
	return nil
}

// CoSimPatterns derives the ARCH-COMP-003 interaction patterns a cosim block exercises.
func CoSimPatterns(spec *CoSimSpec) []string {
	if spec == nil {
		return nil
	}
	patterns := []string{}
	switch strings.ToLower(spec.Scheme) {
	case SchemeJacobi:
		patterns = append(patterns, "parallel")
	default:
		patterns = append(patterns, "sequential")
	}
	if len(spec.Connections) > 0 {
		if coSimHasCycle(spec) {
			patterns = append(patterns, "bidirectional")
		} else {
			patterns = append(patterns, "one-way")
		}
	}
	if len(spec.Events) > 0 {
		patterns = append(patterns, "event-driven")
	}
	return patterns
}

func coSimHasCycle(spec *CoSimSpec) bool {
	edges := make(map[string]map[string]struct{})
	indegree := make(map[string]int)
	for _, model := range spec.Models {
		indegree[model.Name] = 0
	}
	for _, conn := range spec.Connections {
		from, err1 := parseVarRef(conn.From)
		to, err2 := parseVarRef(conn.To)
		if err1 != nil || err2 != nil || from.Model == to.Model {
			continue
		}
		if edges[from.Model] == nil {
			edges[from.Model] = make(map[string]struct{})
		}
		if _, seen := edges[from.Model][to.Model]; !seen {
			edges[from.Model][to.Model] = struct{}{}
			indegree[to.Model]++
		}
	}
	queue := make([]string, 0, len(indegree))
	for name, deg := range indegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	visited := 0
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		visited++
		for next := range edges[name] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	return visited < len(indegree)
}

func parseVarRef(reference string) (fmi.VarRef, error) {
	model, variable, ok := strings.Cut(strings.TrimSpace(reference), ".")
	if !ok || model == "" || variable == "" {
		return fmi.VarRef{}, fmt.Errorf("%q must use format model.variable", reference)
	}
	return fmi.VarRef{Model: model, Var: variable}, nil
}

func parseCondition(when string) (fmi.VarRef, string, float64, error) {
	match := conditionPattern.FindStringSubmatch(when)
	if match == nil {
		return fmi.VarRef{}, "", 0, fmt.Errorf("condition %q must look like model.variable <op> number (op: < <= > >= == !=)", when)
	}
	value, err := strconv.ParseFloat(match[4], 64)
	if err != nil {
		return fmi.VarRef{}, "", 0, fmt.Errorf("condition %q: bad number %q", when, match[4])
	}
	return fmi.VarRef{Model: match[1], Var: match[2]}, match[3], value, nil
}

type resolvedCoSim struct {
	Config  fmi.CoSimConfig
	Cleanup func()
	// FMUPaths maps model name to the resolved absolute FMU path, for provenance.
	FMUPaths map[string]string
}

// buildCoSimConfig resolves FMU paths, start values (including start_from references into earlier
// results), input series and the exchange/event tables for the bridge.
func (e *Executor) buildCoSimConfig(stepName string, spec *CoSimSpec, results map[string]map[string]any) (*resolvedCoSim, error) {
	if err := ValidateCoSim(stepName, spec); err != nil {
		return nil, err
	}
	scheme := strings.ToLower(strings.TrimSpace(spec.Scheme))
	resolved := &resolvedCoSim{
		Config: fmi.CoSimConfig{
			Scheme:            scheme,
			StartTime:         *spec.StartTime,
			StopTime:          *spec.StopTime,
			CommunicationStep: *spec.CommunicationStep,
		},
		FMUPaths: make(map[string]string, len(spec.Models)),
	}
	var cleanups []func()
	resolved.Cleanup = func() {
		for _, fn := range cleanups {
			if fn != nil {
				fn()
			}
		}
	}
	fail := func(err error) (*resolvedCoSim, error) {
		resolved.Cleanup()
		return nil, err
	}

	for _, model := range spec.Models {
		fmuPath, err := e.resolveRepoPath(model.FMU, "fmu")
		if err != nil {
			return fail(fmt.Errorf("model %s invalid fmu path: %w", model.Name, err))
		}
		if _, err := os.Stat(fmuPath); err != nil {
			return fail(fmt.Errorf("model %s references missing FMU %s: %w", model.Name, fmuPath, err))
		}
		startVals, err := e.buildStartValues(model.StartValues, model.StartFrom, results)
		if err != nil {
			return fail(fmt.Errorf("model %s start values invalid: %w", model.Name, err))
		}
		cfgModel := fmi.CoSimModel{Name: model.Name, FMUPath: fmuPath, StartValues: startVals}
		if model.InputSeries != nil {
			series, err := e.buildInputSeries(model.InputSeries)
			if err != nil {
				return fail(fmt.Errorf("model %s input series invalid: %w", model.Name, err))
			}
			if series != nil {
				cfgModel.InputSeries = series.Config
				cleanups = append(cleanups, series.Cleanup)
			}
		}
		resolved.Config.Models = append(resolved.Config.Models, cfgModel)
		resolved.FMUPaths[model.Name] = fmuPath
	}

	for _, conn := range spec.Connections {
		from, _ := parseVarRef(conn.From)
		to, _ := parseVarRef(conn.To)
		resolved.Config.Connections = append(resolved.Config.Connections, fmi.Connection{From: from, To: to})
	}
	for _, event := range spec.Events {
		lhs, op, rhs, _ := parseCondition(event.When)
		target, _ := parseVarRef(event.Set)
		cfgEvent := fmi.Event{Name: event.Name, LHS: lhs, Op: op, RHS: rhs, Target: target, Value: 1, Reset: 0}
		if event.Value != nil {
			cfgEvent.Value = *event.Value
		}
		if event.Reset != nil {
			cfgEvent.Reset = *event.Reset
		}
		cfgEvent.Pulse = strings.EqualFold(strings.TrimSpace(event.Mode), eventModePulse)
		resolved.Config.Events = append(resolved.Config.Events, cfgEvent)
	}
	for _, output := range spec.Outputs {
		ref, _ := parseVarRef(output)
		resolved.Config.Outputs = append(resolved.Config.Outputs, ref)
	}
	if spec.Trace != nil {
		for _, signal := range spec.Trace.Signals {
			ref, _ := parseVarRef(signal)
			resolved.Config.TraceSignals = append(resolved.Config.TraceSignals, ref)
		}
		resolved.Config.TraceSampleEvery = spec.Trace.SampleEvery
	}
	return resolved, nil
}

// sortedModelNames returns model names in YAML order for deterministic provenance output.
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
