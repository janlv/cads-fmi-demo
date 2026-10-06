package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
)

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time {
	c.t = c.t.Add(250 * time.Millisecond)
	return c.t
}

func writeRepo(t *testing.T, workflowYAML string) (string, string) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"workflows", "fmu"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a.fmu", "b.fmu"} {
		if err := os.WriteFile(filepath.Join(root, "fmu", name), []byte("fmu-bytes-"+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wfPath := filepath.Join(root, "workflows", "demo.yaml")
	if err := os.WriteFile(wfPath, []byte(workflowYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, "workflows/demo.yaml"
}

const twoStepYAML = `
steps:
  - name: s1
    fmu: fmu/a.fmu
    outputs: [y]
  - name: s2
    fmu: fmu/b.fmu
    start_from: {u: s1.y}
  - name: s3
    cosim:
      scheme: jacobi
      start_time: 0
      stop_time: 10
      communication_step: 1
      models:
        - {name: m1, fmu: fmu/a.fmu}
        - {name: m2, fmu: fmu/b.fmu, start_from: {u0: s2.y}}
      connections:
        - {from: m1.y, to: m2.u}
`

func sha(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func runInfoOf(t *testing.T, results map[string]map[string]any) RunInfo {
	t.Helper()
	raw, ok := results[RunInfoStepName]
	if !ok {
		t.Fatalf("results lack %s: %v", RunInfoStepName, results)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var info RunInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestRunRecordsProvenanceOnSuccess(t *testing.T) {
	root, wf := writeRepo(t, twoStepYAML)
	clock := &fakeClock{t: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	var fmuCalls []fmi.Config
	var cosimCalls []fmi.CoSimConfig
	exec, err := NewExecutor(root,
		WithClock(clock.now),
		WithRunnerVersion("v1.2.3"),
		withFMIRunners(
			func(cfg fmi.Config) (*fmi.Result, error) {
				fmuCalls = append(fmuCalls, cfg)
				return &fmi.Result{
					Values: map[string]any{"y": 1.5},
					Stats:  fmi.Stats{SimulatedSeconds: 10, FMUs: []fmi.FMUInfo{{FMIVersion: "2.0", ModelName: "A", DoStepCalls: 10}}},
				}, nil
			},
			func(cfg fmi.CoSimConfig) (*fmi.Result, error) {
				cosimCalls = append(cosimCalls, cfg)
				return &fmi.Result{
					Values: map[string]any{"m1.y": 2.0, "m2.z": 3.0},
					Stats: fmi.Stats{SimulatedSeconds: 10, CommunicationPoints: 10, FMUs: []fmi.FMUInfo{
						{Model: "m1", FMIVersion: "3.0", ModelName: "A"},
						{Model: "m2", FMIVersion: "3.0", ModelName: "B", DoStepCalls: 30},
					}},
				}, nil
			}))
	if err != nil {
		t.Fatal(err)
	}

	results, err := exec.Run(wf)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(fmuCalls) != 2 || len(cosimCalls) != 1 {
		t.Fatalf("calls: fmu=%d cosim=%d", len(fmuCalls), len(cosimCalls))
	}
	if fmuCalls[1].StartValues["u"] != "1.5" {
		t.Fatalf("start_from not wired: %v", fmuCalls[1].StartValues)
	}
	if cosimCalls[0].Models[1].StartValues["u0"] != "1.5" {
		t.Fatalf("cosim start_from not wired: %v", cosimCalls[0].Models[1].StartValues)
	}
	if results["s3"]["m2.z"] != 3.0 {
		t.Fatalf("cosim results missing: %v", results["s3"])
	}

	info := runInfoOf(t, results)
	if info.Status != StatusSucceeded || info.Error != "" || info.FailedStep != "" {
		t.Fatalf("unexpected run status: %+v", info)
	}
	if info.RunnerVersion != "v1.2.3" {
		t.Fatalf("runner_version = %q", info.RunnerVersion)
	}
	if info.Workflow.Path != "workflows/demo.yaml" || info.Workflow.SHA256 != sha(twoStepYAML) {
		t.Fatalf("workflow provenance = %+v", info.Workflow)
	}
	if info.StartedAt == "" || info.FinishedAt == "" || info.WallSeconds <= 0 {
		t.Fatalf("timing missing: %+v", info)
	}
	if info.SimulatedSeconds != 30 || info.Ratio == nil || *info.Ratio <= 0 {
		t.Fatalf("simulated/ratio = %v %v", info.SimulatedSeconds, info.Ratio)
	}
	if len(info.Steps) != 3 {
		t.Fatalf("steps = %+v", info.Steps)
	}
	s1 := info.Steps[0]
	if s1.Kind != StepKindFMU || s1.Status != StatusSucceeded || len(s1.FMUs) != 1 {
		t.Fatalf("s1 = %+v", s1)
	}
	if s1.FMUs[0].Path != "fmu/a.fmu" || s1.FMUs[0].SHA256 != sha("fmu-bytes-a.fmu") || s1.FMUs[0].FMIVersion != "2.0" || s1.FMUs[0].DoStepCalls != 10 {
		t.Fatalf("s1 fmu descriptor = %+v", s1.FMUs[0])
	}
	s3 := info.Steps[2]
	if s3.Kind != StepKindCoSim || s3.Scheme != SchemeJacobi || s3.CommunicationPoints != 10 || s3.CommunicationStep == nil || *s3.CommunicationStep != 1 {
		t.Fatalf("s3 = %+v", s3)
	}
	if len(s3.FMUs) != 2 || s3.FMUs[1].Model != "m2" || s3.FMUs[1].DoStepCalls != 30 || s3.FMUs[1].SHA256 != sha("fmu-bytes-b.fmu") {
		t.Fatalf("s3 fmus = %+v", s3.FMUs)
	}

	// The map must survive a JSON round trip as map[string]map[string]any (dashboard contract).
	data, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]map[string]any
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if roundTrip[RunInfoStepName]["status"] != StatusSucceeded {
		t.Fatalf("round trip status = %v", roundTrip[RunInfoStepName]["status"])
	}
}

func TestRunReportsFailedStepAndSkipsRest(t *testing.T) {
	root, wf := writeRepo(t, twoStepYAML)
	exec, err := NewExecutor(root, withFMIRunners(
		func(cfg fmi.Config) (*fmi.Result, error) {
			if strings.HasSuffix(cfg.FMUPath, "b.fmu") {
				return &fmi.Result{Stats: fmi.Stats{FailedAt: f64(4)}}, errors.New("fmi2_do_step failed at t=4")
			}
			return &fmi.Result{Values: map[string]any{"y": 1.0}}, nil
		}, nil))
	if err != nil {
		t.Fatal(err)
	}
	results, err := exec.Run(wf)
	if err == nil {
		t.Fatal("expected error")
	}
	var stepErr *StepError
	if !errors.As(err, &stepErr) || stepErr.Step != "s2" {
		t.Fatalf("error = %v", err)
	}
	if _, ok := results["s1"]; !ok {
		t.Fatalf("completed step s1 missing from results")
	}
	if _, ok := results["s2"]; ok {
		t.Fatalf("failed step must not appear in results")
	}
	info := runInfoOf(t, results)
	if info.Status != StatusFailed || info.FailedStep != "s2" || !strings.Contains(info.Error, "fmi2_do_step") {
		t.Fatalf("run info = %+v", info)
	}
	statuses := []string{}
	for _, s := range info.Steps {
		statuses = append(statuses, s.Status)
	}
	if strings.Join(statuses, ",") != "succeeded,failed,skipped" {
		t.Fatalf("step statuses = %v", statuses)
	}
	if info.Steps[1].FailedAt == nil || *info.Steps[1].FailedAt != 4 {
		t.Fatalf("failed_at = %v", info.Steps[1].FailedAt)
	}
}

func TestRunReportsCancelled(t *testing.T) {
	root, wf := writeRepo(t, twoStepYAML)
	exec, err := NewExecutor(root, withFMIRunners(
		func(cfg fmi.Config) (*fmi.Result, error) { return nil, fmi.ErrCancelled }, nil))
	if err != nil {
		t.Fatal(err)
	}
	results, err := exec.Run(wf)
	if !errors.Is(err, fmi.ErrCancelled) {
		t.Fatalf("error = %v", err)
	}
	info := runInfoOf(t, results)
	if info.Status != StatusCancelled || info.Steps[0].Status != StatusCancelled {
		t.Fatalf("run info = %+v", info)
	}
}

func TestRunRejectsReservedAndInvalidNames(t *testing.T) {
	for _, yamlDoc := range []string{
		"steps:\n  - name: _run\n    fmu: fmu/a.fmu\n",
		"steps:\n  - name: a.b\n    fmu: fmu/a.fmu\n",
		"steps:\n  - name: s\n    fmu: fmu/a.fmu\n    cosim: {scheme: jacobi}\n",
	} {
		root, wf := writeRepo(t, yamlDoc)
		exec, err := NewExecutor(root, withFMIRunners(
			func(cfg fmi.Config) (*fmi.Result, error) { return &fmi.Result{Values: map[string]any{}}, nil }, nil))
		if err != nil {
			t.Fatal(err)
		}
		results, err := exec.Run(wf)
		if err == nil {
			t.Fatalf("expected rejection for %q", yamlDoc)
		}
		info := runInfoOf(t, results)
		if info.Status != StatusFailed {
			t.Fatalf("status = %s", info.Status)
		}
	}
}

func TestRunParseErrorStillReportsRunInfo(t *testing.T) {
	root, wf := writeRepo(t, "steps: [\n")
	exec, err := NewExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	results, err := exec.Run(wf)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if len(results) != 1 {
		t.Fatalf("results = %v", results)
	}
	info := runInfoOf(t, results)
	if info.Status != StatusFailed || info.Workflow.SHA256 == "" {
		t.Fatalf("run info = %+v", info)
	}
}
