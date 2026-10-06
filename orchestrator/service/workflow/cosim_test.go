package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
)

func f64(v float64) *float64 { return &v }

func validCoSim() *CoSimSpec {
	return &CoSimSpec{
		Scheme:            SchemeGaussSeidel,
		StartTime:         f64(0),
		StopTime:          f64(3600),
		CommunicationStep: f64(900),
		Models: []CoSimModelSpec{
			{Name: "battery", FMU: "fmu/a.fmu", StartValues: map[string]any{"capacity": 40.0}},
			{Name: "ems", FMU: "fmu/b.fmu"},
		},
		Connections: []ConnectionSpec{
			{From: "battery.soc", To: "ems.soc_in"},
			{From: "ems.power", To: "battery.power_in"},
		},
		Events: []EventSpec{
			{Name: "low_soc", When: "battery.soc < 20", Set: "ems.protect", Mode: "pulse"},
		},
		Outputs: []string{"battery.soc", "ems.power"},
		Trace:   &CoSimTraceSpec{Signals: []string{"battery.soc"}, SampleEvery: f64(900)},
	}
}

func TestValidateCoSimAcceptsValidSpec(t *testing.T) {
	if err := ValidateCoSim("s", validCoSim()); err != nil {
		t.Fatalf("ValidateCoSim() error = %v", err)
	}
	spec := validCoSim()
	spec.Scheme = SchemeJacobi
	spec.Events = nil
	if err := ValidateCoSim("s", spec); err != nil {
		t.Fatalf("ValidateCoSim(jacobi) error = %v", err)
	}
}

func TestValidateCoSimRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CoSimSpec)
		want   string
	}{
		{"bad scheme", func(s *CoSimSpec) { s.Scheme = "euler" }, "scheme"},
		{"zero step", func(s *CoSimSpec) { s.CommunicationStep = f64(0) }, "communication_step"},
		{"stop before start", func(s *CoSimSpec) { s.StopTime = f64(0) }, "stop_time"},
		{"duplicate model", func(s *CoSimSpec) { s.Models[1].Name = "battery" }, "multiple times"},
		{"reserved model", func(s *CoSimSpec) {
			s.Models[1].Name = "events"
			s.Connections = nil
			s.Events = nil
			s.Outputs = nil
		}, "reserved"},
		{"bad model name", func(s *CoSimSpec) { s.Models[1].Name = "1ems" }, "invalid name"},
		{"model step_size", func(s *CoSimSpec) { s.Models[0].StepSize = f64(1) }, "ARCH-COMP-002"},
		{"unknown from model", func(s *CoSimSpec) { s.Connections[0].From = "pv.p" }, "unknown model"},
		{"unknown to model", func(s *CoSimSpec) { s.Connections[0].To = "pv.p" }, "unknown model"},
		{"self connection", func(s *CoSimSpec) { s.Connections[0].To = "battery.x" }, "to itself"},
		{"duplicate target", func(s *CoSimSpec) {
			s.Connections = append(s.Connections, ConnectionSpec{From: "battery.soh", To: "ems.soc_in"})
		}, "driven by both"},
		{"bad condition", func(s *CoSimSpec) { s.Events[0].When = "battery >> 1" }, "condition"},
		{"condition unknown model", func(s *CoSimSpec) { s.Events[0].When = "pv.p > 1" }, "unknown model"},
		{"event sets connection target", func(s *CoSimSpec) { s.Events[0].Set = "ems.soc_in" }, "already driven"},
		{"bad event mode", func(s *CoSimSpec) { s.Events[0].Mode = "edge" }, "mode"},
		{"duplicate event target", func(s *CoSimSpec) {
			s.Events = append(s.Events, EventSpec{Name: "other", When: "battery.soc > 90", Set: "ems.protect"})
		}, "both set"},
		{"unknown output model", func(s *CoSimSpec) { s.Outputs = []string{"pv.p"} }, "outputs[0]"},
		{"unknown trace model", func(s *CoSimSpec) { s.Trace.Signals = []string{"pv.p"} }, "trace.signals[0]"},
		{"empty trace", func(s *CoSimSpec) { s.Trace.Signals = nil }, "at least one signal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := validCoSim()
			tc.mutate(spec)
			err := ValidateCoSim("s", spec)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseCondition(t *testing.T) {
	good := map[string]struct {
		model, variable, op string
		value               float64
	}{
		"a.b > 0.6":         {"a", "b", ">", 0.6},
		"a.b>=-1e3":         {"a", "b", ">=", -1000},
		"a.b == 1":          {"a", "b", "==", 1},
		"a.x.y < 2":         {"a", "x.y", "<", 2},
		"  a.b != .5 ":      {"a", "b", "!=", 0.5},
		"battery.soc <= 20": {"battery", "soc", "<=", 20},
	}
	for input, want := range good {
		ref, op, value, err := parseCondition(input)
		if err != nil {
			t.Fatalf("parseCondition(%q) error = %v", input, err)
		}
		if ref.Model != want.model || ref.Var != want.variable || op != want.op || value != want.value {
			t.Fatalf("parseCondition(%q) = %v %s %v", input, ref, op, value)
		}
	}
	for _, bad := range []string{"a > 1", "a.b >> 1", "a.b > x", "", "a.b"} {
		if _, _, _, err := parseCondition(bad); err == nil {
			t.Fatalf("parseCondition(%q) expected error", bad)
		}
	}
}

func TestCoSimPatterns(t *testing.T) {
	got := CoSimPatterns(validCoSim())
	want := []string{"sequential", "bidirectional", "event-driven"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("CoSimPatterns() = %v, want %v", got, want)
	}
	spec := validCoSim()
	spec.Scheme = SchemeJacobi
	spec.Connections = spec.Connections[:1]
	spec.Events = nil
	got = CoSimPatterns(spec)
	want = []string{"parallel", "one-way"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("CoSimPatterns(jacobi dag) = %v, want %v", got, want)
	}
}

func TestBuildCoSimConfigResolvesPathsAndStartFrom(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.fmu", "b.fmu"} {
		if err := os.WriteFile(filepath.Join(root, "fmu", name), []byte(name), 0o644); err != nil {
			if mkErr := os.MkdirAll(filepath.Join(root, "fmu"), 0o755); mkErr != nil {
				t.Fatal(mkErr)
			}
			if err := os.WriteFile(filepath.Join(root, "fmu", name), []byte(name), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec, err := NewExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	spec := validCoSim()
	spec.Models[0].StartFrom = map[string]string{"initial_soc": "prev.battery.soc"}
	spec.Events[0].Value = f64(2)
	results := map[string]map[string]any{"prev": {"battery.soc": 42.5}}

	resolved, err := exec.buildCoSimConfig("s", spec, results)
	if err != nil {
		t.Fatalf("buildCoSimConfig() error = %v", err)
	}
	defer resolved.Cleanup()
	cfg := resolved.Config
	if cfg.Scheme != SchemeGaussSeidel || cfg.CommunicationStep != 900 || cfg.StopTime != 3600 {
		t.Fatalf("unexpected timing/scheme: %+v", cfg)
	}
	if len(cfg.Models) != 2 || !filepath.IsAbs(cfg.Models[0].FMUPath) {
		t.Fatalf("models not resolved: %+v", cfg.Models)
	}
	if cfg.Models[0].StartValues["initial_soc"] != "42.5" || cfg.Models[0].StartValues["capacity"] != "40" {
		t.Fatalf("start values = %v", cfg.Models[0].StartValues)
	}
	if len(cfg.Connections) != 2 || cfg.Connections[1].To != (fmi.VarRef{Model: "battery", Var: "power_in"}) {
		t.Fatalf("connections = %+v", cfg.Connections)
	}
	ev := cfg.Events[0]
	if ev.Name != "low_soc" || ev.LHS.Var != "soc" || ev.Op != "<" || ev.RHS != 20 || ev.Target.Model != "ems" || ev.Value != 2 || ev.Reset != 0 || !ev.Pulse {
		t.Fatalf("event = %+v", ev)
	}
	if len(cfg.Outputs) != 2 || len(cfg.TraceSignals) != 1 || cfg.TraceSampleEvery == nil {
		t.Fatalf("outputs/trace = %+v %+v", cfg.Outputs, cfg.TraceSignals)
	}
	if resolved.FMUPaths["ems"] != filepath.Join(root, "fmu", "b.fmu") {
		t.Fatalf("FMUPaths = %v", resolved.FMUPaths)
	}
}

func TestBuildCoSimConfigRejectsMissingFMU(t *testing.T) {
	root := t.TempDir()
	exec, err := NewExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = exec.buildCoSimConfig("s", validCoSim(), nil)
	if err == nil || !strings.Contains(err.Error(), "missing FMU") {
		t.Fatalf("expected missing FMU error, got %v", err)
	}
}
