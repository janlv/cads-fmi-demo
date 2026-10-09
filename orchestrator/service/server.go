package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
	workflowpkg "github.com/norceresearch/cads-fmi-demo/orchestrator/service/workflow"
)

type Server struct {
	Runner *Runner
	Remote RemoteClient
	// Data is the CADS database (nil when CADS_DB_URL is not configured).
	Data DataStore
}

// DataStore is the part of the CADS Data interface the dashboard service reads.
type DataStore interface {
	ChannelStatus(ctx context.Context, site string) ([]cadsdb.ChannelInfo, error)
}

// dataResponse is the `/api/data?site=<id>` payload.
type dataResponse struct {
	Configured bool                 `json:"configured"`
	Site       string               `json:"site"`
	Channels   []cadsdb.ChannelInfo `json:"channels"`
}

type runRequest struct {
	Workflow string `json:"workflow"`
}

type runResponse struct {
	Workflow string                    `json:"workflow"`
	Results  map[string]map[string]any `json:"results"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// localRunErrorResponse carries whatever the runner produced (including `_run`) alongside the error.
type localRunErrorResponse struct {
	Error    string                    `json:"error"`
	Workflow string                    `json:"workflow,omitempty"`
	Results  map[string]map[string]any `json:"results,omitempty"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		s.handleIndex(w, r)
	case strings.HasPrefix(r.URL.Path, "/static/") && r.Method == http.MethodGet:
		http.StripPrefix("/static/", http.FileServer(http.FS(dashboardStaticFS))).ServeHTTP(w, r)
	case r.URL.Path == "/api/config" && r.Method == http.MethodGet:
		s.handleConfig(w, r)
	case r.URL.Path == "/api/data" && r.Method == http.MethodGet:
		s.handleData(w, r)
	case r.URL.Path == "/api/workflows" && r.Method == http.MethodGet:
		s.handleWorkflows(w, r)
	case r.URL.Path == "/api/runs":
		s.handleRuns(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/sites/") && strings.HasSuffix(r.URL.Path, "/runs") && r.Method == http.MethodPost:
		s.handleSubmitSiteRuns(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/runs/") && strings.HasSuffix(r.URL.Path, "/results") && r.Method == http.MethodGet:
		s.handleRunResults(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/runs/") && r.Method == http.MethodGet:
		s.handleRunByName(w, r)
	case r.URL.Path == "/run" && r.Method == http.MethodPost:
		s.handleLocalRun(w, r)
	case r.URL.Path == "/run":
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	data, err := fs.ReadFile(dashboardAssetFS, "index.html")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("load dashboard: %v", err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.remoteClient().Config()
	cfg.DatabaseConfigured = s.Data != nil
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	site := strings.TrimSpace(r.URL.Query().Get("site"))
	if err := cadsdb.CheckSite(site); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	response := dataResponse{Configured: s.Data != nil, Site: site, Channels: []cadsdb.ChannelInfo{}}
	if s.Data != nil {
		channels, err := s.Data.ChannelStatus(r.Context(), site)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("read channels of %s: %v", site, err))
			return
		}
		if channels != nil {
			response.Channels = channels
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleWorkflows(w http.ResponseWriter, _ *http.Request) {
	workDir, err := s.requireWorkDir()
	if err != nil {
		writeHandlerError(w, err)
		return
	}

	workflows, err := ListWorkflows(workDir)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workflows)
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListRuns(w, r)
	case http.MethodPost:
		s.handleSubmitRun(w, r)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeJSONError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}

	runs, err := s.remoteClient().ListRuns(r.Context(), limit)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleSubmitRun(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	if strings.TrimSpace(req.Workflow) == "" {
		writeJSONError(w, http.StatusBadRequest, "workflow is required")
		return
	}

	run, err := s.remoteClient().SubmitWorkflow(r.Context(), req.Workflow)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleRunByName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}

	run, err := s.remoteClient().GetRun(r.Context(), name)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleRunResults(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	name = strings.TrimSuffix(name, "/results")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}

	results, err := s.remoteClient().GetRunResults(r.Context(), name)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleLocalRun(w http.ResponseWriter, r *http.Request) {
	if s.Runner == nil {
		writeJSONError(w, http.StatusInternalServerError, "runner is not configured")
		return
	}

	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	if req.Workflow == "" {
		writeJSONError(w, http.StatusBadRequest, "workflow is required")
		return
	}

	results, err := s.Runner.Run(req.Workflow)
	if err != nil {
		log.Printf("workflow %s failed: %v", req.Workflow, err)
		if len(results) > 0 {
			writeJSON(w, handlerErrorStatus(err), localRunErrorResponse{Error: err.Error(), Workflow: req.Workflow, Results: results})
			return
		}
		writeHandlerError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, runResponse{Workflow: req.Workflow, Results: results})
}

func (s *Server) requireWorkDir() (string, error) {
	if s.Runner == nil {
		return "", errors.New("runner is not configured")
	}
	return s.Runner.WorkDir, nil
}

func (s *Server) remoteClient() RemoteClient {
	if s.Remote != nil {
		return s.Remote
	}
	return disabledRemoteClient{
		config: DashboardConfig{
			RemoteEnabled:       false,
			PollIntervalSeconds: int(defaultPollInterval / time.Second),
			Problems:            []string{"remote playground client is not configured"},
			Version:             ResolvedVersion(),
		},
	}
}

type disabledRemoteClient struct {
	config DashboardConfig
}

func (c disabledRemoteClient) Config() DashboardConfig {
	return c.config
}

func (c disabledRemoteClient) ListRuns(context.Context, int) ([]RunSummary, error) {
	return nil, ErrRemoteUnavailable
}

func (c disabledRemoteClient) GetRun(context.Context, string) (*RunSummary, error) {
	return nil, ErrRemoteUnavailable
}

func (c disabledRemoteClient) GetRunResults(context.Context, string) (*RunResults, error) {
	return nil, ErrRemoteUnavailable
}

func (c disabledRemoteClient) SubmitWorkflow(context.Context, string) (*RunSummary, error) {
	return nil, ErrRemoteUnavailable
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func writeHandlerError(w http.ResponseWriter, err error) {
	writeJSONError(w, handlerErrorStatus(err), err.Error())
}

func handlerErrorStatus(err error) int {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrRemoteUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrRemoteRunNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrRunResultsUnavailable):
		status = http.StatusConflict
	case errors.Is(err, workflowpkg.ErrPathEscapesRoot):
		status = http.StatusBadRequest
	case errors.Is(err, ErrWorkflowOutsideDirectory):
		status = http.StatusBadRequest
	case os.IsNotExist(err):
		status = http.StatusNotFound
	}
	return status
}

// siteRunsResponse is the result of launching every catalog workflow mapped to one demonstrator
// (the operator's "run assessment" action; ARCH-VIS-001 one view per demonstrator).
type siteRunsResponse struct {
	Site   string           `json:"site"`
	Runs   []RunSummary     `json:"runs"`
	Errors []siteRunFailure `json:"errors,omitempty"`
}

type siteRunFailure struct {
	Workflow string `json:"workflow"`
	Error    string `json:"error"`
}

// handleSubmitSiteRuns submits each workflow whose metadata site_id matches the path site.
func (s *Server) handleSubmitSiteRuns(w http.ResponseWriter, r *http.Request) {
	site := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sites/"), "/runs")
	if site == "" || strings.Contains(site, "/") {
		http.NotFound(w, r)
		return
	}
	workDir, err := s.requireWorkDir()
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	workflows, err := ListWorkflows(workDir)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	resp := siteRunsResponse{Site: site, Runs: []RunSummary{}}
	for _, wf := range workflows {
		if wf.Metadata.SiteID != site {
			continue
		}
		run, err := s.remoteClient().SubmitWorkflow(r.Context(), wf.Path)
		if err != nil {
			resp.Errors = append(resp.Errors, siteRunFailure{Workflow: wf.Path, Error: err.Error()})
			continue
		}
		if run != nil {
			resp.Runs = append(resp.Runs, *run)
		}
	}
	if len(resp.Runs) == 0 && len(resp.Errors) == 0 {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("no workflows mapped to site %q", site))
		return
	}
	status := http.StatusOK
	if len(resp.Runs) == 0 {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, resp)
}
