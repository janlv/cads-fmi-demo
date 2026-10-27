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
	// Local executes runs on this machine when ExecutionMode is "local".
	Local *LocalRunner
	// ExecutionMode is "argo" (default) or "local".
	ExecutionMode string
}

const (
	ExecutionArgo  = "argo"
	ExecutionLocal = "local"
)

// localMode reports whether submissions run on this machine.
func (s *Server) localMode() bool {
	return s.Local != nil && s.ExecutionMode == ExecutionLocal
}

func (s *Server) executionMode() string {
	if s.localMode() {
		return ExecutionLocal
	}
	return ExecutionArgo
}

// DataStore is the part of the CADS Data interface the dashboard service reads.
type DataStore interface {
	ChannelStatus(ctx context.Context, site string) ([]cadsdb.ChannelInfo, error)
	ReadSeriesBinned(ctx context.Context, site string, channels []string, from, to time.Time, binSeconds float64) ([]cadsdb.Row, error)
	ListRuns(ctx context.Context, site string, limit int) ([]cadsdb.StoredRun, error)
	ReadRunOutputs(ctx context.Context, site string, runIDs []string) ([]cadsdb.OutputRow, error)
	ReadRunSeries(ctx context.Context, site string, runIDs, steps, signals []string) ([]cadsdb.RunSeriesRow, error)
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
	case strings.HasPrefix(r.URL.Path, "/api/sites/") && strings.HasSuffix(r.URL.Path, "/history") && r.Method == http.MethodGet:
		s.handleSiteHistory(w, r)
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
	cfg.LocalRunsEnabled = s.localMode()
	cfg.ExecutionMode = s.executionMode()
	if cfg.LocalRunsEnabled && s.Local != nil {
		cfg.LocalRunner = s.Local.Describe()
	}
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

	remote := s.remoteClient()
	remoteEnabled := remote.Config().RemoteEnabled
	if !remoteEnabled && s.Local == nil && s.Data == nil {
		_, err := remote.ListRuns(r.Context(), limit)
		writeHandlerError(w, err)
		return
	}

	// Priority order for duplicates: live Argo, in-flight local, recent local, stored runs.
	var groups [][]RunSummary
	if remoteEnabled {
		runs, err := remote.ListRuns(r.Context(), limit)
		if err != nil {
			if s.Local == nil && s.Data == nil {
				writeHandlerError(w, err)
				return
			}
			log.Printf("list argo runs: %v", err)
		}
		for i := range runs {
			if runs[i].Source == "" {
				runs[i].Source = RunSourceArgo
			}
		}
		groups = append(groups, runs)
	}
	if s.Local != nil {
		groups = append(groups, s.Local.InFlight(), s.Local.Recent())
	}
	if s.Data != nil {
		stored, err := s.storedRuns(r.Context(), limit)
		if err != nil {
			log.Printf("list stored runs: %v", err)
		}
		groups = append(groups, stored)
	}
	writeJSON(w, http.StatusOK, mergeRunLists(groups, limit))
}

// mergeRunLists dedupes by name (earlier groups win), sorts newest first and cuts to limit.
func mergeRunLists(groups [][]RunSummary, limit int) []RunSummary {
	seen := map[string]bool{}
	merged := []RunSummary{}
	for _, group := range groups {
		for _, run := range group {
			if run.Name == "" || seen[run.Name] {
				continue
			}
			seen[run.Name] = true
			merged = append(merged, run)
		}
	}
	sortRunsNewestFirst(merged)
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// catalogSites lists the distinct metadata.site_id values of the workflow catalog.
func (s *Server) catalogSites() []string {
	workDir, err := s.requireWorkDir()
	if err != nil {
		return nil
	}
	workflows, err := ListWorkflows(workDir)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var sites []string
	for _, wf := range workflows {
		site := strings.TrimSpace(wf.Metadata.SiteID)
		if site == "" || seen[site] || !cadsdb.ValidSite(site) {
			continue
		}
		seen[site] = true
		sites = append(sites, site)
	}
	return sites
}

// storedRuns lists the runs recorded in the CADS database for every catalog site.
func (s *Server) storedRuns(ctx context.Context, limit int) ([]RunSummary, error) {
	var out []RunSummary
	var errs []error
	for _, site := range s.catalogSites() {
		runs, err := s.Data.ListRuns(ctx, site, limit)
		if err != nil {
			errs = append(errs, fmt.Errorf("site %s: %w", site, err))
			continue
		}
		for _, run := range runs {
			out = append(out, storedRunSummary(run))
		}
	}
	return out, errors.Join(errs...)
}

// storedRunSummary converts a <site>.runs row to a RunSummary.
func storedRunSummary(run cadsdb.StoredRun) RunSummary {
	summary := RunSummary{
		Name:         run.RunID,
		WorkflowPath: run.WorkflowPath,
		Phase:        phaseFromStoredStatus(run.Status),
		CreatedAt:    run.StartedAt,
		StartedAt:    run.StartedAt,
		FinishedAt:   run.FinishedAt,
		Source:       runSourceForID(run.RunID),
	}
	if run.WallSeconds != nil {
		summary.DurationSeconds = *run.WallSeconds
	} else if run.FinishedAt != nil {
		summary.DurationSeconds = computeDurationSeconds(run.StartedAt, run.FinishedAt, *run.FinishedAt)
	}
	if run.Provenance != nil {
		summary.DataWindow = decodeDataWindow(run.Provenance["data_window"])
		if message, ok := run.Provenance["error"].(string); ok {
			summary.Message = strings.TrimSpace(message)
		}
		if wf, ok := run.Provenance["workflow"].(map[string]any); ok {
			if digest, ok := wf["sha256"].(string); ok {
				summary.WorkflowSHA256 = digest
			}
		}
	}
	if strings.EqualFold(run.Status, "skipped") && summary.Message == "" {
		summary.Message = "no new data"
	}
	return summary
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

	var run *RunSummary
	var err error
	if s.localMode() {
		run, err = s.Local.Submit(req.Workflow)
	} else {
		run, err = s.remoteClient().SubmitWorkflow(r.Context(), req.Workflow)
	}
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// servesLocally reports whether a run name is looked up locally (in memory, then the
// database) rather than in Argo.
func (s *Server) servesLocally(name string) bool {
	if s.Local == nil && s.Data == nil {
		return false
	}
	return strings.HasPrefix(name, "local-") || !s.remoteClient().Config().RemoteEnabled
}

// runReader is the database read side for single runs (nil without a database).
func (s *Server) runReader() RunReader {
	if s.Data == nil {
		return nil
	}
	reader, _ := s.Data.(RunReader)
	return reader
}

// readStoredRunAnySite reads a run from the database, trying every catalog site.
func (s *Server) readStoredRunAnySite(ctx context.Context, name string) (map[string]map[string]any, error) {
	reader := s.runReader()
	if reader == nil {
		return nil, fmt.Errorf("%w: %s", ErrRemoteRunNotFound, name)
	}
	for _, site := range s.catalogSites() {
		results, err := reader.ReadRun(ctx, site, name)
		if err == nil && len(results) > 0 {
			return results, nil
		}
		if err != nil && !errors.Is(err, cadsdb.ErrRunNotFound) {
			return nil, fmt.Errorf("read run %s (site %s): %w", name, site, err)
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrRemoteRunNotFound, name)
}

// summaryFromRunInfo builds a RunSummary from a stored result's `_run` record.
func summaryFromRunInfo(name string, results map[string]map[string]any) RunSummary {
	summary := RunSummary{Name: name, Source: runSourceForID(name)}
	info := results[workflowpkg.RunInfoStepName]
	status, _ := info["status"].(string)
	summary.Phase = phaseFromStoredStatus(status)
	if wf, ok := info["workflow"].(map[string]any); ok {
		summary.WorkflowPath, _ = wf["path"].(string)
		summary.WorkflowSHA256, _ = wf["sha256"].(string)
	}
	parse := func(key string) *time.Time {
		raw, _ := info[key].(string)
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); err == nil {
			return &parsed
		}
		return nil
	}
	summary.StartedAt = parse("started_at")
	summary.CreatedAt = summary.StartedAt
	summary.FinishedAt = parse("finished_at")
	if wall, ok := info["wall_seconds"].(float64); ok {
		summary.DurationSeconds = wall
	}
	summary.Message = runInfoError(results)
	summary.DataWindow = dataWindowFromResults(results)
	return summary
}

// localRunByName serves a run from the local runner, then the database.
func (s *Server) localRunByName(ctx context.Context, name string) (*RunSummary, error) {
	if s.Local != nil {
		if summary, _, _, ok := s.Local.lookup(name); ok {
			return &summary, nil
		}
	}
	results, err := s.readStoredRunAnySite(ctx, name)
	if err != nil {
		return nil, err
	}
	summary := summaryFromRunInfo(name, results)
	return &summary, nil
}

// localRunResults serves results from the local runner, then the database.
func (s *Server) localRunResults(ctx context.Context, name string) (*RunResults, error) {
	if s.Local != nil {
		if summary, results, inFlight, ok := s.Local.lookup(name); ok {
			if inFlight {
				return nil, fmt.Errorf("%w: workflow phase is %s", ErrRunResultsUnavailable, summary.Phase)
			}
			response := &RunResults{
				RunName:       summary.Name,
				WorkflowPath:  summary.WorkflowPath,
				CollectedFrom: "local runner",
				Phase:         summary.Phase,
				Status:        strings.ToLower(summary.Phase),
				Partial:       summary.Phase != "Succeeded",
			}
			if results == nil {
				response.StepResults = map[string]map[string]any{}
				response.Error = summary.Message
				return response, nil
			}
			applyRunInfo(response, results, &summary)
			if response.Status == "skipped" {
				response.Partial = false
			}
			return response, nil
		}
	}
	results, err := s.readStoredRunAnySite(ctx, name)
	if err != nil {
		return nil, err
	}
	summary := summaryFromRunInfo(name, results)
	response := &RunResults{
		RunName:       name,
		WorkflowPath:  summary.WorkflowPath,
		CollectedFrom: "database",
		Phase:         summary.Phase,
		Status:        strings.ToLower(summary.Phase),
		Partial:       summary.Phase != "Succeeded",
	}
	applyRunInfo(response, results, &summary)
	if response.Status == "skipped" {
		response.Partial = false
	}
	return response, nil
}

func (s *Server) handleRunByName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}

	var run *RunSummary
	var err error
	if s.servesLocally(name) {
		run, err = s.localRunByName(r.Context(), name)
	} else {
		run, err = s.remoteClient().GetRun(r.Context(), name)
	}
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

	var results *RunResults
	var err error
	if s.servesLocally(name) {
		results, err = s.localRunResults(r.Context(), name)
	} else {
		results, err = s.remoteClient().GetRunResults(r.Context(), name)
	}
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
	if err != nil && errors.Is(err, workflowpkg.ErrNoNewData) {
		// Up to date: the run finished as "skipped", which is a successful no-op.
		writeJSON(w, http.StatusOK, runResponse{Workflow: req.Workflow, Results: results})
		return
	}
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
	// UpToDate lists the workflows not started because no new data arrived since their
	// previous assessment (local execution only).
	UpToDate []siteRunUpToDate `json:"upToDate,omitempty"`
}

type siteRunUpToDate struct {
	Workflow     string `json:"workflow"`
	Reason       string `json:"reason"`
	CoveredTo    string `json:"coveredTo,omitempty"`
	LatestSample string `json:"latestSample,omitempty"`
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
		var run *RunSummary
		var err error
		if s.localMode() {
			if upToDate, planErr := s.planLocalRun(r.Context(), workDir, wf.Path); planErr != nil {
				resp.Errors = append(resp.Errors, siteRunFailure{Workflow: wf.Path, Error: planErr.Error()})
				continue
			} else if upToDate != nil {
				resp.UpToDate = append(resp.UpToDate, *upToDate)
				continue
			}
			run, err = s.Local.Submit(wf.Path)
		} else {
			run, err = s.remoteClient().SubmitWorkflow(r.Context(), wf.Path)
		}
		if err != nil {
			resp.Errors = append(resp.Errors, siteRunFailure{Workflow: wf.Path, Error: err.Error()})
			continue
		}
		if run != nil {
			resp.Runs = append(resp.Runs, *run)
		}
	}
	if len(resp.Runs) == 0 && len(resp.Errors) == 0 && len(resp.UpToDate) == 0 {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("no workflows mapped to site %q", site))
		return
	}
	status := http.StatusOK
	if len(resp.Runs) == 0 && len(resp.UpToDate) == 0 {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, resp)
}

// planLocalRun resolves the workflow's data window before a local run. It returns an up-to-date
// entry when there is no new data, nil, nil when the run should start (also for workflows without
// data_window or without a database that can resolve windows).
func (s *Server) planLocalRun(ctx context.Context, workDir, workflowPath string) (*siteRunUpToDate, error) {
	if s.Data == nil {
		return nil, nil
	}
	reader, ok := s.Data.(cadsdb.WindowReader)
	if !ok {
		return nil, nil
	}
	info, err := workflowpkg.PlanDataWindow(ctx, workDir, workflowPath, reader)
	if err == nil {
		return nil, nil
	}
	if !errors.Is(err, workflowpkg.ErrNoNewData) {
		return nil, err
	}
	entry := &siteRunUpToDate{Workflow: workflowPath, Reason: err.Error()}
	if info != nil {
		entry.CoveredTo = info.PreviousTo
		entry.LatestSample = info.LatestSample
	}
	return entry, nil
}
