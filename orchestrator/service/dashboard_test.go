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
			Image:               "ghcr.io/janlv/cads-fmi-demo:latest",
			PollIntervalSeconds: 5,
		},
		runs: []RunSummary{{
			Name:            "cads-python-chain-20260416164333",
			WorkflowPath:    "workflows/python_chain.yaml",
			Phase:           "Succeeded",
			CreatedAt:       &createdAt,
			StartedAt:       &startedAt,
			DurationSeconds: 12,
			Progress:        "1/1",
			Image:           "ghcr.io/janlv/cads-fmi-demo:latest",
			ServiceAccount:  "playground-admin",
		}},
	}
	remote.byName = map[string]RunSummary{
		remote.runs[0].Name: remote.runs[0],
	}
	remote.results = map[string]RunResults{
		remote.runs[0].Name: {
			RunName:      remote.runs[0].Name,
			WorkflowPath: "workflows/python_chain.yaml",
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
		if len(workflows) != 2 {
			t.Fatalf("len(workflows) = %d, want 2", len(workflows))
		}
		if workflows[0].Path != "workflows/calculate_aecis.yaml" || workflows[1].StepCount != 2 {
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
		if run.WorkflowPath != "workflows/python_chain.yaml" {
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
		req := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"workflow":"workflows/python_chain.yaml"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusOK)
		}
		if len(remote.submitted) != 1 || remote.submitted[0] != "workflows/python_chain.yaml" {
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
		if !strings.Contains(body, "Kaizen Argo Playground") || !strings.Contains(body, "workflowGrid") || !strings.Contains(body, "timelineChart") || !strings.Contains(body, "simulinkResults") {
			t.Fatalf("dashboard body missing expected markers: %q", body)
		}
	})
}

func TestListWorkflowsAndResolveLaunchWorkflow(t *testing.T) {
	root := writeDashboardRepoFixture(t)

	workflows, err := ListWorkflows(root)
	if err != nil {
		t.Fatalf("ListWorkflows() error = %v", err)
	}
	if len(workflows) != 2 {
		t.Fatalf("ListWorkflows() len = %d, want 2", len(workflows))
	}
	if workflows[1].Name != "python_chain" || workflows[1].StepCount != 2 {
		t.Fatalf("ListWorkflows() workflows = %+v, want python_chain with 2 steps", workflows)
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
	if err := os.WriteFile(filepath.Join(root, "workflows", "python_chain.yaml"), []byte(`
steps:
  - name: producer
  - name: consumer
`), 0o644); err != nil {
		t.Fatalf("write python_chain workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "calculate_aecis.yaml"), []byte(`
steps:
  - name: calculate_aecis
`), 0o644); err != nil {
		t.Fatalf("write calculate_aecis workflow: %v", err)
	}
	return root
}
