package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
)

type fakeRemoteClient struct {
	config    DashboardConfig
	runs      []RunSummary
	byName    map[string]RunSummary
	results   map[string]RunResults
	submitted []string
}

func (f *fakeRemoteClient) Config() DashboardConfig {
	return f.config
}

func (f *fakeRemoteClient) ListRuns(_ context.Context, limit int) ([]RunSummary, error) {
	runs := append([]RunSummary(nil), f.runs...)
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

func (f *fakeRemoteClient) GetRun(_ context.Context, name string) (*RunSummary, error) {
	run, ok := f.byName[name]
	if !ok {
		return nil, ErrRemoteRunNotFound
	}
	copy := run
	return &copy, nil
}

func (f *fakeRemoteClient) GetRunResults(_ context.Context, name string) (*RunResults, error) {
	result, ok := f.results[name]
	if !ok {
		return nil, ErrRunResultsUnavailable
	}
	copy := result
	return &copy, nil
}

func (f *fakeRemoteClient) SubmitWorkflow(_ context.Context, workflowPath string) (*RunSummary, error) {
	f.submitted = append(f.submitted, workflowPath)
	run := RunSummary{
		Name:         "cads-python-chain-20260416170000",
		WorkflowPath: workflowPath,
		Phase:        "Running",
		Progress:     "0/1",
	}
	if f.byName == nil {
		f.byName = make(map[string]RunSummary)
	}
	f.byName[run.Name] = run
	f.runs = append([]RunSummary{run}, f.runs...)
	return &run, nil
}

func TestServerAPIsAndDashboard(t *testing.T) {
	root := writeDashboardRepoFixture(t)
	createdAt := time.Date(2026, 4, 16, 16, 43, 34, 0, time.UTC)
	startedAt := createdAt.Add(2 * time.Second)

	remote := &fakeRemoteClient{
		config: DashboardConfig{
			RemoteEnabled:       true,
			ArgoServer:          "argoworkflows.cads.kzslab.dev",
			Namespace:           "playground",
			ServiceAccount:      "playground-admin",
			Image:               "ghcr.io/janlv/cads-fmi-demo:playground",
			PollIntervalSeconds: 5,
		},
		runs: []RunSummary{{
			Name:            "cads-python-chain-20260416164333",
			WorkflowPath:    "workflows/tests/python_chain.yaml",
			Phase:           "Succeeded",
			CreatedAt:       &createdAt,
			StartedAt:       &startedAt,
			DurationSeconds: 12,
			Progress:        "1/1",
			Image:           "ghcr.io/janlv/cads-fmi-demo:playground",
			ServiceAccount:  "playground-admin",
		}},
	}
	remote.byName = map[string]RunSummary{
		remote.runs[0].Name: remote.runs[0],
	}
	remote.results = map[string]RunResults{
		remote.runs[0].Name: {
			RunName:      remote.runs[0].Name,
			WorkflowPath: "workflows/tests/python_chain.yaml",
			StepResults: map[string]map[string]any{
				"producer": {
					"mean": 1.25,
					"trace": map[string]any{
						"time": []float64{0, 1, 2},
						"signals": map[string]any{
							"mean": []float64{0.8, 1.0, 1.25},
						},
					},
				},
			},
			CollectedFrom: "argo logs",
		},
	}

	server := &Server{
		Runner: &Runner{WorkDir: root},
		Remote: remote,
	}

	t.Run("config", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusOK)
		}

		var cfg DashboardConfig
		if err := json.NewDecoder(rec.Body).Decode(&cfg); err != nil {
			t.Fatalf("decode config: %v", err)
		}
		if !cfg.RemoteEnabled || cfg.PollIntervalSeconds != 5 {
			t.Fatalf("config = %+v, want enabled remote config", cfg)
		}
	})

	t.Run("workflows", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusOK)
		}

		var workflows []WorkflowSummary
		if err := json.NewDecoder(rec.Body).Decode(&workflows); err != nil {
			t.Fatalf("decode workflows: %v", err)
		}
		if len(workflows) != 0 {
			t.Fatalf("workflows = %+v, unexpected catalog", workflows)
		}
	})

	t.Run("list runs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs?limit=1", nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusOK)
		}

		var runs []RunSummary
		if err := json.NewDecoder(rec.Body).Decode(&runs); err != nil {
			t.Fatalf("decode runs: %v", err)
		}
		if len(runs) != 1 || runs[0].Name != remote.runs[0].Name {
			t.Fatalf("runs = %+v, want first remote run", runs)
		}
	})

	t.Run("get run", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/cads-python-chain-20260416164333", nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusOK)
		}

		var run RunSummary
		if err := json.NewDecoder(rec.Body).Decode(&run); err != nil {
			t.Fatalf("decode run: %v", err)
		}
		if run.WorkflowPath != "workflows/tests/python_chain.yaml" {
			t.Fatalf("run = %+v, want workflow path", run)
		}
	})

	t.Run("get run results", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/cads-python-chain-20260416164333/results", nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusOK)
		}

		var result RunResults
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("decode run results: %v", err)
		}
		trace, ok := result.StepResults["producer"]["trace"].(map[string]any)
		if result.RunName != remote.runs[0].Name || result.StepResults["producer"]["mean"] != 1.25 || !ok || len(trace) == 0 {
			t.Fatalf("result = %+v, want run results payload", result)
		}
	})

	t.Run("submit run", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"workflow":"workflows/tests/python_chain.yaml"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusOK)
		}
		if len(remote.submitted) != 1 || remote.submitted[0] != "workflows/tests/python_chain.yaml" {
			t.Fatalf("submitted = %v, want workflow path", remote.submitted)
		}
	})

	t.Run("index", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "STOR-HY Demonstrator Dashboard") ||
			!strings.Contains(body, "plotsView") ||
			!strings.Contains(body, "demoWorkflowList") ||
			!strings.Contains(body, "selectedWorkflowPanel") ||
			!strings.Contains(body, "workflowOutput") ||
			!strings.Contains(body, "runsList") {
			t.Fatalf("dashboard body missing expected markers: %q", body)
		}
	})
}

func TestListWorkflowsAndResolveLaunchWorkflow(t *testing.T) {
	root := writeDashboardRepoFixture(t)
	nestedDir := filepath.Join(root, "workflows", "demonstrators", "vsmc", "dispatch")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("create nested workflow dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "cascade_dispatch.yaml"), []byte(`
metadata:
  display_name: Cascade dispatch
  site_id: vsmc
  category: dispatch
  result_family: storhy_mock
  description: VSMC cascade dispatch replica workflow.
  tags: [cascade, dispatch]
  limits:
    max_runtime_seconds: 300
    cpu: 500m
    memory: 512Mi
steps:
  - name: dispatch
    fmu: fmu/models/HydroCascadeDispatchReplica.fmu
    start_values: {site_id: 1, scenario_id: 2}
    outputs: [score, risk_index]
  - name: kpi_assessment
    fmu: fmu/models/KPIAssessmentReplica.fmu
    start_from:
      input_score: dispatch.score
      input_risk_index: dispatch.risk_index
    outputs: [kpi_score, risk_index]
`), 0o644); err != nil {
		t.Fatalf("write nested workflow: %v", err)
	}

	workflows, err := ListWorkflows(root)
	if err != nil {
		t.Fatalf("ListWorkflows() error = %v", err)
	}
	if len(workflows) != 1 {
		t.Fatalf("ListWorkflows() len = %d, want 1", len(workflows))
	}
	if workflows[0].Name != "cascade_dispatch" ||
		workflows[0].Path != "workflows/demonstrators/vsmc/dispatch/cascade_dispatch.yaml" ||
		workflows[0].StepCount != 2 ||
		workflows[0].Metadata.DisplayName != "Cascade dispatch" ||
		workflows[0].Metadata.SiteID != "vsmc" ||
		workflows[0].Metadata.ResultFamily != "storhy_mock" {
		t.Fatalf("ListWorkflows() nested workflow = %+v, want metadata-rich nested workflow", workflows[0])
	}
	if len(workflows[0].Models) != 2 ||
		workflows[0].Models[0].Label != "Hydro Cascade Dispatch" ||
		workflows[0].Models[1].Label != "KPI Assessment" ||
		len(workflows[0].Models[1].Inputs) != 2 ||
		workflows[0].Models[1].Inputs[0].Source != "dispatch.risk_index" ||
		workflows[0].Models[1].Inputs[0].SourceStep != "dispatch" ||
		workflows[0].Models[1].Inputs[0].SourceOutput != "risk_index" {
		t.Fatalf("ListWorkflows() models = %+v, want parsed model chain", workflows[0].Models)
	}
	if limits := workflows[0].Metadata.Limits; limits == nil || limits.MaxRuntimeSeconds != 300 || limits.CPU != "500m" || limits.Memory != "512Mi" {
		t.Fatalf("Metadata.Limits = %+v, want parsed limits", workflows[0].Metadata.Limits)
	}
	if len(workflows[0].SHA256) != 64 || len(workflows[0].Problems) != 0 {
		t.Fatalf("SHA256 = %q problems = %v, want hex digest and no problems", workflows[0].SHA256, workflows[0].Problems)
	}
	if workflows[0].Models[0].Kind != "fmu" {
		t.Fatalf("Models[0].Kind = %q, want fmu", workflows[0].Models[0].Kind)
	}
	metadata, digest, err := readWorkflowDocument(root, workflows[0].Path)
	if err != nil || digest != workflows[0].SHA256 || metadata.SiteID != "vsmc" {
		t.Fatalf("readWorkflowDocument() = %+v, %q, %v; want same digest and metadata", metadata, digest, err)
	}
	resolvedTestWorkflow, err := ResolveLaunchWorkflow(root, filepath.Join("workflows", "tests", "calculate_aecis.yaml"))
	if err != nil || resolvedTestWorkflow != "workflows/tests/calculate_aecis.yaml" {
		t.Fatalf("ResolveLaunchWorkflow() test workflow = %q, %v; want explicit test workflow path", resolvedTestWorkflow, err)
	}
	resolvedTestWorkflow, err = ResolveLaunchWorkflow(root, filepath.Join("workflows", "tests", "python_chain.yaml"))
	if err != nil || resolvedTestWorkflow != "workflows/tests/python_chain.yaml" {
		t.Fatalf("ResolveLaunchWorkflow() python test workflow = %q, %v; want explicit test workflow path", resolvedTestWorkflow, err)
	}

	if _, err := ResolveLaunchWorkflow(root, filepath.Join("..", "outside.yaml")); err == nil || !strings.Contains(err.Error(), "path escapes repository root") {
		t.Fatalf("ResolveLaunchWorkflow() error = %v, want traversal rejection", err)
	}
}

func writeDashboardRepoFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatalf("create workflows dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "fmu"), 0o755); err != nil {
		t.Fatalf("create fmu dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "workflows", "tests"), 0o755); err != nil {
		t.Fatalf("create test workflows dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workflows", "archive", "common"), 0o755); err != nil {
		t.Fatalf("create archive workflows dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "archive", "common", "retired.yaml"), []byte(`
metadata:
  display_name: Retired replica workflow
steps:
  - name: retired
    fmu: fmu/models/Retired.fmu
`), 0o644); err != nil {
		t.Fatalf("write archived workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "tests", "python_chain.yaml"), []byte(`
steps:
  - name: producer
  - name: consumer
`), 0o644); err != nil {
		t.Fatalf("write python_chain workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "tests", "calculate_aecis.yaml"), []byte(`
steps:
  - name: calculate_aecis
`), 0o644); err != nil {
		t.Fatalf("write calculate_aecis workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "tests", "ae_event_statistics.yaml"), []byte(`
steps:
  - name: ae_ch2
  - name: ae_ch6
`), 0o644); err != nil {
		t.Fatalf("write ae_event_statistics workflow: %v", err)
	}
	return root
}

func TestListWorkflowsDescribesCoSimSteps(t *testing.T) {
	root := writeDashboardRepoFixture(t)
	dir := filepath.Join(root, "workflows", "demonstrators", "alqueva", "hybrid")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "battery_ems_cosim.yaml"), []byte(`
metadata:
  display_name: Battery and EMS co-simulation
  site_id: alqueva
steps:
  - name: battery_ems
    cosim:
      scheme: gauss_seidel
      start_time: 0
      stop_time: 86400
      communication_step: 900
      models:
        - name: battery
          fmu: fmu/models/BatteryDegradation.fmu
          start_values: {capacity_kwh: 2000}
          start_from: {initial_soc: prep.soc_percent}
          input_series: {csv: data/ambient.csv}
        - name: ems
          fmu: fmu/models/EmsDispatch.fmu
      connections:
        - {from: battery.soc_percent, to: ems.soc_percent}
        - {from: ems.power_setpoint_mw, to: battery.power_setpoint_mw}
      events:
        - {name: low_soc, when: battery.soc_percent < 20, set: ems.protection_request}
      outputs: [battery.soc_percent, ems.revenue_eur]
  - name: kpi
    fmu: fmu/models/KPIAssessmentReplica.fmu
    start_from: {input_rul: battery_ems.battery.rul_days}
  - name: broken
    cosim:
      scheme: euler
      models: [{name: a, fmu: fmu/models/A.fmu}]
`), 0o644); err != nil {
		t.Fatalf("write cosim workflow: %v", err)
	}

	workflows, err := ListWorkflows(root)
	if err != nil {
		t.Fatalf("ListWorkflows() error = %v", err)
	}
	if len(workflows) != 1 || len(workflows[0].Models) != 3 {
		t.Fatalf("ListWorkflows() = %+v, want one workflow with three catalog entries", workflows)
	}
	cosimStep := workflows[0].Models[0]
	if cosimStep.Kind != "cosim" || cosimStep.Label != "Battery Ems" || cosimStep.FMU != "" || cosimStep.CoSim == nil || len(cosimStep.Problems) != 0 {
		t.Fatalf("cosim step = %+v, want valid cosim entry", cosimStep)
	}
	cosim := cosimStep.CoSim
	if cosim.Scheme != "gauss_seidel" || cosim.CommunicationStep == nil || *cosim.CommunicationStep != 900 {
		t.Fatalf("cosim = %+v, want scheme and timing", cosim)
	}
	if len(cosim.Connections) != 2 || cosim.Connections[0].FromModel != "battery" || cosim.Connections[0].ToVariable != "soc_percent" {
		t.Fatalf("connections = %+v, want two split connections", cosim.Connections)
	}
	if strings.Join(cosim.Patterns, ",") != "sequential,bidirectional,event-driven" {
		t.Fatalf("patterns = %v, want sequential,bidirectional,event-driven", cosim.Patterns)
	}
	if len(cosim.Events) != 1 || cosim.Events[0].Mode != "level" || cosim.Events[0].Set != "ems.protection_request" {
		t.Fatalf("events = %+v, want low_soc level event", cosim.Events)
	}
	if len(cosim.Models) != 2 || cosim.Models[0].InputSeries != "data/ambient.csv" || cosim.Models[0].Label != "Battery Degradation" ||
		strings.Join(cosim.Models[0].Outputs, ",") != "soc_percent" {
		t.Fatalf("cosim models = %+v, want member summaries", cosim.Models)
	}
	if strings.Join(cosimStep.Outputs, ",") != "battery.soc_percent,ems.revenue_eur" ||
		len(cosimStep.Inputs) != 1 || cosimStep.Inputs[0].Name != "battery.initial_soc" || cosimStep.Inputs[0].SourceStep != "prep" {
		t.Fatalf("cosim step io = %+v / %+v, want flattened outputs and prefixed inputs", cosimStep.Outputs, cosimStep.Inputs)
	}
	tail := workflows[0].Models[1]
	if tail.Kind != "fmu" || tail.Inputs[0].SourceStep != "battery_ems" || tail.Inputs[0].SourceOutput != "battery.rul_days" {
		t.Fatalf("tail = %+v, want dotted start_from resolution", tail)
	}
	broken := workflows[0].Models[2]
	if broken.Kind != "cosim" || len(broken.Problems) == 0 {
		t.Fatalf("broken = %+v, want problems instead of a listing failure", broken)
	}
}

func TestSubmitSiteRunsLaunchesEveryWorkflowOfTheSite(t *testing.T) {
	root := writeDashboardRepoFixture(t)
	write := func(rel, siteID string) {
		t.Helper()
		path := filepath.Join(root, "workflows", "demonstrators", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		doc := "metadata:\n  display_name: " + rel + "\n  site_id: " + siteID + "\nsteps:\n  - name: s\n    fmu: fmu/models/Rul.fmu\n"
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("cheylas/a/one.yaml", "cheylas")
	write("cheylas/b/two.yaml", "cheylas")
	write("alqueva/c/three.yaml", "alqueva")

	remote := &fakeRemoteClient{config: DashboardConfig{RemoteEnabled: true}}
	server := &Server{Runner: &Runner{WorkDir: root}, Remote: remote}

	req := httptest.NewRequest(http.MethodPost, "/api/sites/cheylas/runs", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp siteRunsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Site != "cheylas" || len(resp.Runs) != 2 || len(resp.Errors) != 0 {
		t.Fatalf("response = %+v", resp)
	}
	if len(remote.submitted) != 2 ||
		remote.submitted[0] != "workflows/demonstrators/cheylas/a/one.yaml" ||
		remote.submitted[1] != "workflows/demonstrators/cheylas/b/two.yaml" {
		t.Fatalf("submitted = %v", remote.submitted)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/sites/nowhere/runs", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown site status = %d", rec.Code)
	}
}

const windowedSiteWorkflow = `
metadata: {display_name: Windowed, site_id: cheylas}
data_window: {mode: since_previous_run, initial_span: 24h, max_span: 7d}
steps:
  - name: health
    cosim:
      scheme: jacobi
      communication_step: 900
      models:
        - name: iot
          fmu: fmu/a.fmu
          input_series: {db: {site: cheylas, channels: {v: vib}}}
        - name: rul
          fmu: fmu/b.fmu
          start_values: {initial_damage: 0.35}
          carry_over: {initial_damage: health.rul.damage_index}
`

func TestLocalExecutionSiteRunsListAndResults(t *testing.T) {
	root := writeDashboardRepoFixture(t)
	write := func(rel, doc string) {
		t.Helper()
		path := filepath.Join(root, "workflows", "demonstrators", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cheylas/a/windowed.yaml", windowedSiteWorkflow)
	write("cheylas/b/plain.yaml", "metadata:\n  display_name: Plain\n  site_id: cheylas\nsteps:\n  - name: s\n    fmu: fmu/models/Rul.fmu\n")

	dayEnd := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	dbStarted := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	argoStarted := dbStarted.Add(-time.Hour)
	store := &fakeDataStore{
		latest: map[string]time.Time{"vib": dayEnd},
		previous: map[string]*cadsdb.PreviousRun{
			"workflows/demonstrators/cheylas/a/windowed.yaml": {RunID: "local-db1", Mode: "since_previous_run", From: dayEnd.Add(-24 * time.Hour), To: dayEnd},
		},
		runList: []cadsdb.StoredRun{
			{RunID: "local-db1", WorkflowPath: "workflows/demonstrators/cheylas/a/windowed.yaml", Status: "succeeded", StartedAt: &dbStarted,
				Provenance: map[string]any{"data_window": map[string]any{"mode": "since_previous_run", "from": "2026-05-20T00:00:00Z", "to": "2026-05-21T00:00:00Z"}}},
			{RunID: "local-db0", WorkflowPath: "workflows/demonstrators/cheylas/a/windowed.yaml", Status: "skipped", StartedAt: &argoStarted},
			{RunID: "cads-hosted-x", WorkflowPath: "workflows/demonstrators/cheylas/b/plain.yaml", Status: "cancelled", StartedAt: &argoStarted},
		},
		runs: map[string]map[string]map[string]any{
			"cheylas/local-db1": {
				"health": {"rul.rul_days": 410.0},
				"_run":   {"status": "succeeded", "workflow": map[string]any{"path": "workflows/demonstrators/cheylas/a/windowed.yaml"}},
			},
		},
	}
	fake := &fakeLocalExec{release: make(chan struct{}), stdout: `{"s":{"y":1},"_run":{"status":"succeeded"}}`}
	local := NewLocalRunner(LocalRunnerOptions{WorkDir: root, RunnerBin: "runner"})
	local.exec = fake.run
	server := &Server{Runner: &Runner{WorkDir: root}, Data: store, Local: local, ExecutionMode: ExecutionLocal}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var cfg DashboardConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.LocalRunsEnabled || cfg.ExecutionMode != "local" || cfg.RemoteEnabled || !cfg.DatabaseConfigured {
		t.Fatalf("config = %+v", cfg)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/sites/cheylas/runs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("site runs status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp siteRunsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Runs) != 1 || resp.Runs[0].WorkflowPath != "workflows/demonstrators/cheylas/b/plain.yaml" || resp.Runs[0].Source != "local" || len(resp.Errors) != 0 {
		t.Fatalf("response = %+v", resp)
	}
	if len(resp.UpToDate) != 1 || resp.UpToDate[0].Workflow != "workflows/demonstrators/cheylas/a/windowed.yaml" ||
		resp.UpToDate[0].LatestSample != "2026-05-21T00:00:00Z" || resp.UpToDate[0].CoveredTo != "2026-05-21T00:00:00Z" ||
		!strings.Contains(resp.UpToDate[0].Reason, "no new data") {
		t.Fatalf("upToDate = %+v", resp.UpToDate)
	}
	localName := resp.Runs[0].Name

	// While the run is in flight its results are not available yet.
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/"+localName+"/results", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("in-flight results status = %d, body %s", rec.Code, rec.Body.String())
	}
	// A second refresh does not start a second run of the same workflow.
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/sites/cheylas/runs", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "already has a local run in flight") {
		t.Fatalf("second refresh = %d %s", rec.Code, rec.Body.String())
	}

	close(fake.release)
	local.Wait()

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs?limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", rec.Code, rec.Body.String())
	}
	var runs []RunSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &runs); err != nil {
		t.Fatal(err)
	}
	byName := map[string]RunSummary{}
	for _, run := range runs {
		if _, dup := byName[run.Name]; dup {
			t.Fatalf("duplicate run %s in %+v", run.Name, runs)
		}
		byName[run.Name] = run
	}
	if len(runs) != 4 || runs[0].Name != localName {
		t.Fatalf("runs = %+v, want the new local run first and 4 entries", runs)
	}
	if run := byName["local-db1"]; run.Source != "local" || run.Phase != "Succeeded" || run.DataWindow == nil || run.DataWindow.To != "2026-05-21T00:00:00Z" {
		t.Fatalf("db run = %+v", run)
	}
	if run := byName["local-db0"]; run.Phase != "Succeeded" || run.Message != "no new data" {
		t.Fatalf("skipped db run = %+v", run)
	}
	if run := byName["cads-hosted-x"]; run.Source != "argo" || run.Phase != "Failed" {
		t.Fatalf("hosted db run = %+v", run)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/local-db1/results", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("db results status = %d, body %s", rec.Code, rec.Body.String())
	}
	var results RunResults
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	if results.CollectedFrom != "database" || results.Status != "succeeded" || results.Partial ||
		results.WorkflowPath != "workflows/demonstrators/cheylas/a/windowed.yaml" || results.StepResults["health"]["rul.rul_days"] != 410.0 {
		t.Fatalf("db results = %+v", results)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/"+localName+"/results", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"collectedFrom":"local runner"`) {
		t.Fatalf("local results = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/"+localName, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"phase":"Succeeded"`) {
		t.Fatalf("local run = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/local-missing/results", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing run status = %d", rec.Code)
	}

	// POST /api/runs also runs locally.
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"workflow":"workflows/demonstrators/cheylas/b/plain.yaml"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":"local"`) {
		t.Fatalf("submit run = %d %s", rec.Code, rec.Body.String())
	}
	local.Wait()
}
