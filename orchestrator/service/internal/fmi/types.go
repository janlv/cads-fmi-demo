package fmi

import (
	"errors"
	"strings"
)

// ErrCancelled is returned when a run is interrupted through RequestCancel.
var ErrCancelled = errors.New("fmi: run cancelled")

// Config describes a single FMU execution.
type Config struct {
	FMUPath     string
	StartTime   *float64
	StopTime    *float64
	StepSize    *float64
	StartValues map[string]string
	Outputs     []string
	InputSeries *InputSeriesConfig
	Trace       *TraceConfig
}

// InputSeriesConfig points at a CSV file whose columns are applied as FMU inputs over time.
type InputSeriesConfig struct {
	CSVPath string
}

// TraceConfig lists the signals sampled over the run.
type TraceConfig struct {
	Outputs     []string
	Inputs      []string
	SampleEvery *float64
}

// VarRef names one variable of one model inside a co-simulation.
type VarRef struct {
	Model string
	Var   string
}

// Connection routes one model output into another model input at every communication point.
type Connection struct {
	From VarRef
	To   VarRef
}

// Event drives Target to Value while the condition "LHS Op RHS" holds (level mode) or for one
// communication interval after a rising edge (pulse mode), and to Reset otherwise.
type Event struct {
	Name   string
	LHS    VarRef
	Op     string
	RHS    float64
	Target VarRef
	Value  float64
	Reset  float64
	Pulse  bool
}

// CoSimModel is one FMU instance participating in a co-simulation.
type CoSimModel struct {
	Name        string
	FMUPath     string
	StartValues map[string]string
	InputSeries *InputSeriesConfig
}

// CoSimConfig describes a multi-FMU co-simulation with communication points.
type CoSimConfig struct {
	Scheme            string // "jacobi" or "gauss_seidel"
	StartTime         float64
	StopTime          float64
	CommunicationStep float64
	Models            []CoSimModel
	Connections       []Connection
	Events            []Event
	Outputs           []VarRef
	TraceSignals      []VarRef
	TraceSampleEvery  *float64
}

// FMUInfo is the identity and per-run statistics of one FMU instance.
type FMUInfo struct {
	Model             string   `json:"model"`
	FMIVersion        string   `json:"fmi_version"`
	ModelName         string   `json:"model_name"`
	ModelVersion      string   `json:"model_version"`
	GUID              string   `json:"guid"`
	GenerationTool    string   `json:"generation_tool"`
	DeclaredStep      *float64 `json:"declared_step"`
	StepUsed          float64  `json:"step_used"`
	DoStepCalls       int64    `json:"do_step_calls"`
	ClippedSubsteps   int64    `json:"clipped_substeps"`
	DoStepWallSeconds float64  `json:"do_step_wall_seconds"`
}

// EventRecord logs one detected edge of a co-simulation event.
type EventRecord struct {
	Name string  `json:"name"`
	Time float64 `json:"time"`
	Edge string  `json:"edge"`
}

// Termination records which model requested termination and when.
type Termination struct {
	Model string  `json:"model"`
	Time  float64 `json:"time"`
}

// Stats summarises timing, provenance and events of one bridge call.
type Stats struct {
	WallSeconds         float64       `json:"wall_seconds"`
	SimulatedSeconds    float64       `json:"simulated_seconds"`
	CommunicationPoints int64         `json:"communication_points"`
	FMUs                []FMUInfo     `json:"fmus"`
	Events              []EventRecord `json:"events"`
	TerminatedBy        *Termination  `json:"terminated_by"`
	FailedAt            *float64      `json:"failed_at"`
}

// Result is the envelope returned by Run and RunCoSim.
type Result struct {
	Values map[string]any `json:"values"`
	Stats  Stats          `json:"stats"`
}

// Operator codes shared between the Go validation layer and the C bridge.
const (
	OpLT = iota
	OpLE
	OpGT
	OpGE
	OpEQ
	OpNE
)

// ParseOp maps a comparison operator to its bridge code.
func ParseOp(s string) (int, bool) {
	switch strings.TrimSpace(s) {
	case "<":
		return OpLT, true
	case "<=":
		return OpLE, true
	case ">":
		return OpGT, true
	case ">=":
		return OpGE, true
	case "==":
		return OpEQ, true
	case "!=":
		return OpNE, true
	}
	return 0, false
}

// SchemeCode maps a scheme name to its bridge code (0 jacobi, 1 gauss_seidel).
func SchemeCode(s string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "jacobi":
		return 0, true
	case "gauss_seidel":
		return 1, true
	}
	return 0, false
}

// String renders a VarRef as model.var.
func (v VarRef) String() string {
	return v.Model + "." + v.Var
}
