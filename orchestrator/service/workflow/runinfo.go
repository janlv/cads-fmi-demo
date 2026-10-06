package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
)

// RunInfoStepName is the reserved pseudo-step carrying run status, timing and provenance
// (ARCH-COMP-008, ARCH-COMP-013, ARCH-COMP-017, ARCH-COMP-018).
const RunInfoStepName = "_run"

// Run outcome states (ARCH-COMP-017).
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusSkipped   = "skipped"
)

// Step kinds.
const (
	StepKindFMU   = "fmu"
	StepKindCoSim = "cosim"
)

// StepError wraps a failure with the name of the step that produced it.
type StepError struct {
	Step string
	Err  error
}

func (e *StepError) Error() string { return fmt.Sprintf("step %s failed: %v", e.Step, e.Err) }
func (e *StepError) Unwrap() error { return e.Err }

// RunInfo is the typed form of the `_run` pseudo-step.
type RunInfo struct {
	Status           string       `json:"status"`
	Error            string       `json:"error"`
	FailedStep       string       `json:"failed_step"`
	StartedAt        string       `json:"started_at"`
	FinishedAt       string       `json:"finished_at"`
	WallSeconds      float64      `json:"wall_seconds"`
	SimulatedSeconds float64      `json:"simulated_seconds"`
	Ratio            *float64     `json:"ratio"`
	RunnerVersion    string       `json:"runner_version"`
	Workflow         WorkflowInfo `json:"workflow"`
	Steps            []StepInfo   `json:"steps"`
}

// WorkflowInfo identifies the workflow definition that was executed.
type WorkflowInfo struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// StepInfo records one step's outcome, timing and participating FMUs.
type StepInfo struct {
	Name                string            `json:"name"`
	Kind                string            `json:"kind"`
	Scheme              string            `json:"scheme,omitempty"`
	Status              string            `json:"status"`
	Error               string            `json:"error,omitempty"`
	WallSeconds         float64           `json:"wall_seconds"`
	SimulatedSeconds    float64           `json:"simulated_seconds"`
	Ratio               *float64          `json:"ratio"`
	CommunicationStep   *float64          `json:"communication_step,omitempty"`
	CommunicationPoints int64             `json:"communication_points,omitempty"`
	Events              []fmi.EventRecord `json:"events,omitempty"`
	TerminatedBy        *fmi.Termination  `json:"terminated_by"`
	FailedAt            *float64          `json:"failed_at"`
	FMUs                []FMUDescriptor   `json:"fmus"`
}

// FMUDescriptor is the identity of one FMU file plus the bridge's per-instance statistics.
type FMUDescriptor struct {
	Model string `json:"model"`
	// Path is repo-relative with forward slashes.
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	fmi.FMUInfo
}

// MarshalJSON flattens the embedded FMUInfo next to model/path/sha256.
func (d FMUDescriptor) MarshalJSON() ([]byte, error) {
	type flat struct {
		Model             string   `json:"model"`
		Path              string   `json:"path"`
		SHA256            string   `json:"sha256"`
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
	return json.Marshal(flat{
		Model: d.Model, Path: d.Path, SHA256: d.SHA256,
		FMIVersion: d.FMIVersion, ModelName: d.ModelName, ModelVersion: d.ModelVersion,
		GUID: d.GUID, GenerationTool: d.GenerationTool, DeclaredStep: d.DeclaredStep,
		StepUsed: d.StepUsed, DoStepCalls: d.DoStepCalls, ClippedSubsteps: d.ClippedSubsteps,
		DoStepWallSeconds: d.DoStepWallSeconds,
	})
}

// runRecorder accumulates RunInfo while a workflow executes.
type runRecorder struct {
	info      RunInfo
	startedAt time.Time
	now       func() time.Time
	root      string
	shaCache  map[string]string
}

func newRunRecorder(now func() time.Time, version string, root string) *runRecorder {
	start := now()
	return &runRecorder{
		info: RunInfo{
			Status:        StatusSucceeded,
			StartedAt:     start.UTC().Format(time.RFC3339Nano),
			RunnerVersion: version,
			Steps:         []StepInfo{},
		},
		startedAt: start,
		now:       now,
		root:      root,
		shaCache:  make(map[string]string),
	}
}

func (r *runRecorder) setWorkflow(path string, data []byte) {
	sum := sha256.Sum256(data)
	r.info.Workflow = WorkflowInfo{Path: filepath.ToSlash(path), SHA256: hex.EncodeToString(sum[:])}
}

// describeFMU builds the identity record for one FMU file; bridge statistics are merged later.
func (r *runRecorder) describeFMU(model string, absPath string) FMUDescriptor {
	rel := absPath
	if r.root != "" {
		if candidate, err := filepath.Rel(r.root, absPath); err == nil {
			rel = candidate
		}
	}
	desc := FMUDescriptor{Model: model, Path: filepath.ToSlash(rel)}
	desc.FMUInfo.Model = model
	if sum, err := r.sha256File(absPath); err == nil {
		desc.SHA256 = sum
	}
	return desc
}

func (r *runRecorder) sha256File(path string) (string, error) {
	if sum, ok := r.shaCache[path]; ok {
		return sum, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	r.shaCache[path] = sum
	return sum, nil
}

// stepDone records a finished step. stats may be nil when the bridge produced nothing.
func (r *runRecorder) stepDone(step StepInfo, wall time.Duration, stats *fmi.Stats, err error) {
	step.WallSeconds = wall.Seconds()
	if stats != nil {
		step.SimulatedSeconds = stats.SimulatedSeconds
		step.CommunicationPoints = stats.CommunicationPoints
		step.Events = stats.Events
		step.TerminatedBy = stats.TerminatedBy
		step.FailedAt = stats.FailedAt
		mergeFMUStats(step.FMUs, stats.FMUs)
	}
	step.Ratio = ratioOf(step.SimulatedSeconds, step.WallSeconds)
	switch {
	case err == nil:
		step.Status = StatusSucceeded
	case errors.Is(err, fmi.ErrCancelled):
		step.Status = StatusCancelled
		step.Error = err.Error()
	default:
		step.Status = StatusFailed
		step.Error = err.Error()
	}
	if step.FMUs == nil {
		step.FMUs = []FMUDescriptor{}
	}
	r.info.Steps = append(r.info.Steps, step)
	r.info.SimulatedSeconds += step.SimulatedSeconds
}

func (r *runRecorder) stepSkipped(name, kind string) {
	r.info.Steps = append(r.info.Steps, StepInfo{Name: name, Kind: kind, Status: StatusSkipped, FMUs: []FMUDescriptor{}})
}

func mergeFMUStats(descs []FMUDescriptor, infos []fmi.FMUInfo) {
	for i := range descs {
		for _, info := range infos {
			if info.Model == descs[i].Model || (info.Model == "" && len(infos) == 1 && len(descs) == 1) {
				model := descs[i].Model
				descs[i].FMUInfo = info
				descs[i].FMUInfo.Model = model
				break
			}
		}
	}
}

func ratioOf(simulated, wall float64) *float64 {
	if wall <= 0 {
		return nil
	}
	ratio := simulated / wall
	return &ratio
}

// finish closes the record and converts it to the plain map shape shared with other steps.
func (r *runRecorder) finish(err error) map[string]any {
	end := r.now()
	r.info.FinishedAt = end.UTC().Format(time.RFC3339Nano)
	r.info.WallSeconds = end.Sub(r.startedAt).Seconds()
	r.info.Ratio = ratioOf(r.info.SimulatedSeconds, r.info.WallSeconds)
	if err != nil {
		r.info.Error = err.Error()
		var stepErr *StepError
		if errors.As(err, &stepErr) {
			r.info.FailedStep = stepErr.Step
		}
		if errors.Is(err, fmi.ErrCancelled) {
			r.info.Status = StatusCancelled
		} else {
			r.info.Status = StatusFailed
		}
	}
	return runInfoToMap(r.info)
}

func runInfoToMap(info RunInfo) map[string]any {
	data, err := json.Marshal(info)
	if err != nil {
		return map[string]any{"status": StatusFailed, "error": "encode run info: " + err.Error()}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{"status": StatusFailed, "error": "decode run info: " + err.Error()}
	}
	return out
}
