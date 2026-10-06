//go:build cgo && fmiintegration

// Integration tests for the co-simulation master. They need FMIL and the replica FMUs built
// into fmu/models (create_fmu/build_python_fmus.sh, or run inside the container image):
//
//	CGO_ENABLED=1 go test -tags fmiintegration ./internal/fmi -run CoSim -v
package fmi

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if isDir(filepath.Join(dir, "workflows")) && isDir(filepath.Join(dir, "fmu")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repository root (workflows/ + fmu/) not found")
		}
		dir = parent
	}
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// replicaFMUs returns the HybridEMSReplica and PredictiveMaintenanceReplica paths, skipping the
// test when they have not been built.
func replicaFMUs(t *testing.T) (ems, pm string) {
	t.Helper()
	root := repoRoot(t)
	ems = filepath.Join(root, "fmu", "models", "HybridEMSReplica.fmu")
	pm = filepath.Join(root, "fmu", "models", "PredictiveMaintenanceReplica.fmu")
	for _, p := range []string{ems, pm} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("replica FMU missing (%s); build with create_fmu/build_python_fmus.sh", p)
		}
	}
	return ems, pm
}

func floatPtr(v float64) *float64 { return &v }

// emsToPM couples ems.risk_index into the tunable parameter pm.input_risk_index.
func emsToPM(t *testing.T, scheme string) CoSimConfig {
	ems, pm := replicaFMUs(t)
	return CoSimConfig{
		Scheme:            scheme,
		StartTime:         0,
		StopTime:          12,
		CommunicationStep: 1,
		Models: []CoSimModel{
			{Name: "ems", FMUPath: ems},
			{Name: "pm", FMUPath: pm},
		},
		Connections:  []Connection{{From: VarRef{"ems", "risk_index"}, To: VarRef{"pm", "input_risk_index"}}},
		Outputs:      []VarRef{{"ems", "risk_index"}, {"pm", "risk_index"}},
		TraceSignals: []VarRef{{"ems", "risk_index"}, {"pm", "risk_index"}},
	}
}

func traceSignal(t *testing.T, res *Result, name string) []any {
	t.Helper()
	trace, ok := res.Values["trace"].(map[string]any)
	if !ok {
		t.Fatalf("result has no trace: %v", res.Values)
	}
	signals, _ := trace["signals"].(map[string]any)
	values, ok := signals[name].([]any)
	if !ok {
		t.Fatalf("trace has no signal %q: %v", name, signals)
	}
	return values
}

func traceFloats(t *testing.T, res *Result, name string) []float64 {
	t.Helper()
	raw := traceSignal(t, res, name)
	out := make([]float64, len(raw))
	for i, v := range raw {
		f, ok := v.(float64)
		if !ok {
			t.Fatalf("%s[%d] = %v (%T), want number", name, i, v, v)
		}
		out[i] = f
	}
	return out
}

func countTempDirs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cads-fmi-") {
			n++
		}
	}
	return n
}

// (a) Gauss-Seidel feeds pm with the ems value of the same interval; Jacobi with the value of
// the previous interval, so the Jacobi pm trace lags the Gauss-Seidel one by one interval.
func TestCoSimGaussSeidelVsJacobiLag(t *testing.T) {
	gs, err := RunCoSim(emsToPM(t, "gauss_seidel"))
	if err != nil {
		t.Fatalf("gauss_seidel: %v", err)
	}
	jac, err := RunCoSim(emsToPM(t, "jacobi"))
	if err != nil {
		t.Fatalf("jacobi: %v", err)
	}

	gsPM := traceFloats(t, gs, "pm.risk_index")
	jacPM := traceFloats(t, jac, "pm.risk_index")
	if len(gsPM) != 13 || len(jacPM) != 13 {
		t.Fatalf("trace lengths gs=%d jacobi=%d, want 13", len(gsPM), len(jacPM))
	}
	differs := false
	for i := 2; i < len(jacPM); i++ {
		if math.Abs(jacPM[i]-gsPM[i-1]) > 1e-12 {
			t.Errorf("jacobi[%d]=%v, want gauss_seidel[%d]=%v", i, jacPM[i], i-1, gsPM[i-1])
		}
		if math.Abs(jacPM[i]-gsPM[i]) > 1e-12 {
			differs = true
		}
	}
	if !differs {
		t.Errorf("jacobi and gauss_seidel traces are identical; coupling had no effect")
	}

	for _, res := range []*Result{gs, jac} {
		if res.Stats.CommunicationPoints != 12 {
			t.Errorf("communication_points = %d, want 12", res.Stats.CommunicationPoints)
		}
		if len(res.Stats.FMUs) != 2 || res.Stats.FMUs[0].Model != "ems" || res.Stats.FMUs[1].Model != "pm" {
			t.Fatalf("fmus = %+v", res.Stats.FMUs)
		}
		for _, f := range res.Stats.FMUs {
			if f.FMIVersion != "2.0" || f.StepUsed != 1 || f.DoStepCalls != 12 || f.GUID == "" {
				t.Errorf("fmu stats %+v", f)
			}
		}
		if _, ok := res.Values["pm.risk_index"].(float64); !ok {
			t.Errorf("missing flattened output pm.risk_index: %v", res.Values)
		}
		if res.Stats.FailedAt != nil || res.Stats.TerminatedBy != nil {
			t.Errorf("unexpected failed_at/terminated_by: %+v", res.Stats)
		}
	}
}

// (b) A level event drives pm.input_damage_index while ems.soc_percent > 60; pm.damage_index
// follows the event state one interval later.
func TestCoSimEventToggle(t *testing.T) {
	cfg := emsToPM(t, "gauss_seidel")
	cfg.StopTime = 24
	cfg.Events = []Event{{
		Name:   "high_soc",
		LHS:    VarRef{"ems", "soc_percent"},
		Op:     ">",
		RHS:    60,
		Target: VarRef{"pm", "input_damage_index"},
		Value:  1,
		Reset:  0,
	}}
	cfg.TraceSignals = []VarRef{{"ems", "soc_percent"}, {"pm", "damage_index"}}
	res, err := RunCoSim(cfg)
	if err != nil {
		t.Fatal(err)
	}

	active := traceSignal(t, res, "events.high_soc.active")
	damage := traceFloats(t, res, "pm.damage_index")
	if len(active) != 25 || len(damage) != 25 {
		t.Fatalf("trace lengths active=%d damage=%d, want 25", len(active), len(damage))
	}
	sawTrue, sawFalse := false, false
	for i := 0; i+1 < len(active); i++ {
		on, ok := active[i].(bool)
		if !ok {
			t.Fatalf("events.high_soc.active[%d] = %v, want bool", i, active[i])
		}
		if on {
			sawTrue = true
		} else {
			sawFalse = true
		}
		want := 0.0
		if on {
			want = 1
		}
		if damage[i+1] != want {
			t.Errorf("pm.damage_index[%d] = %v, want %v (event active at %d = %v)", i+1, damage[i+1], want, i, on)
		}
	}
	if !sawTrue || !sawFalse {
		t.Errorf("event never toggled: active=%v", active)
	}
	count, ok := res.Values["events.high_soc.count"].(float64)
	if !ok || count < 1 {
		t.Errorf("events.high_soc.count = %v", res.Values["events.high_soc.count"])
	}
	rising := 0
	for _, e := range res.Stats.Events {
		if e.Name == "high_soc" && e.Edge == "rising" {
			rising++
		}
	}
	if float64(rising) != count {
		t.Errorf("rising edges %d != count %v (events %+v)", rising, count, res.Stats.Events)
	}
}

// (c) A pending cancel request stops the run with ErrCancelled and a partial result.
func TestCoSimCancel(t *testing.T) {
	cfg := emsToPM(t, "jacobi")
	RequestCancel()
	defer ResetCancel()
	res, err := RunCoSim(cfg)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
	if res == nil || res.Stats.FailedAt == nil {
		t.Fatalf("want partial result with failed_at, got %+v", res)
	}
	ResetCancel()
	if _, err := RunCoSim(cfg); err != nil {
		t.Fatalf("run after ResetCancel: %v", err)
	}
}

// (d) Unknown variables are reported as model.var and every unpacked FMU is cleaned up.
func TestCoSimMissingVariable(t *testing.T) {
	cfg := emsToPM(t, "gauss_seidel")
	cfg.Connections = append(cfg.Connections, Connection{From: VarRef{"ems", "power_mw"}, To: VarRef{"pm", "no_such_input"}})
	before := countTempDirs(t)
	res, err := RunCoSim(cfg)
	if err == nil {
		t.Fatal("expected an error for the missing variable")
	}
	if !strings.Contains(err.Error(), "pm.no_such_input") {
		t.Errorf("error %q does not name pm.no_such_input", err)
	}
	if res == nil {
		t.Errorf("expected a partial result alongside the error")
	}
	if after := countTempDirs(t); after != before {
		t.Errorf("temp dirs: before %d, after %d (leak)", before, after)
	}
}

// The single-FMU path returns the same envelope with FMU identity.
func TestCoSimSingleRunEnvelope(t *testing.T) {
	ems, _ := replicaFMUs(t)
	res, err := Run(Config{FMUPath: ems, StartTime: floatPtr(0), StopTime: floatPtr(4), StepSize: floatPtr(1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stats.FMUs) != 1 {
		t.Fatalf("fmus = %+v", res.Stats.FMUs)
	}
	f := res.Stats.FMUs[0]
	if f.FMIVersion != "2.0" || f.ModelName == "" || f.DoStepCalls != 4 || f.StepUsed != 1 || f.DeclaredStep != nil {
		t.Errorf("fmu info %+v", f)
	}
	if res.Stats.SimulatedSeconds != 4 || res.Stats.WallSeconds <= 0 {
		t.Errorf("stats %+v", res.Stats)
	}
	if _, ok := res.Values["soc_percent"]; !ok {
		t.Errorf("values %v", res.Values)
	}
}
