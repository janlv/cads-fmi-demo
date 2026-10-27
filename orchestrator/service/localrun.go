package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	workflowpkg "github.com/norceresearch/cads-fmi-demo/orchestrator/service/workflow"
)

const (
	// RunSourceLocal and RunSourceArgo tag RunSummary.Source.
	RunSourceLocal = "local"
	RunSourceArgo  = "argo"

	localRecentCap      = 50
	localStderrTailSize = 20
	containerAppRoot    = "/app"
	containerRunnerBin  = "/app/bin/cads-workflow-runner"
)

// LocalCommand is one process the LocalRunner starts.
type LocalCommand struct {
	Name string
	Args []string
	// Env is the full process environment.
	Env []string
	Dir string
}

// LocalExecFunc runs a command and returns its stdout and stderr separately.
type LocalExecFunc func(ctx context.Context, cmd LocalCommand) (stdout, stderr []byte, err error)

// LocalRunnerOptions configure NewLocalRunner.
type LocalRunnerOptions struct {
	WorkDir string
	// RunnerBin is the host runner binary (direct mode).
	RunnerBin string
	// Image, when set, runs the runner inside this container image instead (container mode).
	Image string
	// ContainerTool is podman or docker; empty means podman when on PATH, else docker.
	ContainerTool string
	// DBURL is the CADS database URL given to the runner (empty: no database).
	DBURL string
	// ContainerDBURL overrides the database URL inside the container; empty derives it from
	// DBURL by pointing a loopback host at the container host gateway.
	ContainerDBURL string
	// MaxRuntimeSeconds is the deadline of workflows without limits.max_runtime_seconds.
	MaxRuntimeSeconds int64
}

// LocalRunner executes workflows on this machine with the runner binary (or the runner inside a
// container image) and keeps the in-flight and recent runs for the dashboard.
type LocalRunner struct {
	WorkDir        string
	RunnerBin      string
	Image          string
	ContainerTool  string
	DBURL          string
	ContainerDBURL string
	MaxRuntime     int64

	exec LocalExecFunc
	now  func() time.Time

	mu          sync.Mutex
	inflight    map[string]*localRun // by workflow path
	recent      map[string]*localRun // by run id
	recentOrder []string
	wg          sync.WaitGroup
}

type localRun struct {
	summary RunSummary
	results map[string]map[string]any
}

// Describe names the executor for the dashboard: the container image and tool, or the host binary.
func (l *LocalRunner) Describe() string {
	if l == nil {
		return ""
	}
	if l.Image != "" {
		tool := l.ContainerTool
		if tool == "" {
			tool = "container"
		}
		return fmt.Sprintf("%s image %s", tool, l.Image)
	}
	return "runner binary " + filepath.Base(l.RunnerBin)
}

// NewLocalRunner resolves the container tool and the in-container database URL.
func NewLocalRunner(opts LocalRunnerOptions) *LocalRunner {
	r := &LocalRunner{
		WorkDir:    opts.WorkDir,
		RunnerBin:  opts.RunnerBin,
		Image:      strings.TrimSpace(opts.Image),
		DBURL:      strings.TrimSpace(opts.DBURL),
		MaxRuntime: opts.MaxRuntimeSeconds,
		exec:       defaultLocalExec,
		now:        time.Now,
		inflight:   map[string]*localRun{},
		recent:     map[string]*localRun{},
	}
	if r.Image != "" {
		r.ContainerTool = strings.TrimSpace(opts.ContainerTool)
		if r.ContainerTool == "" {
			r.ContainerTool = detectContainerTool()
		}
		r.ContainerDBURL = strings.TrimSpace(opts.ContainerDBURL)
		if r.ContainerDBURL == "" && r.DBURL != "" {
			r.ContainerDBURL = containerDBURL(r.DBURL, r.ContainerTool)
		}
	}
	return r
}

// Mode is "container" or "direct".
func (r *LocalRunner) Mode() string {
	if r.Image != "" {
		return "container"
	}
	return "direct"
}

func detectContainerTool() string {
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman"
	}
	return "docker"
}

// containerDBURL points a loopback database host at the container host gateway.
func containerDBURL(dbURL, tool string) string {
	parsed, err := url.Parse(dbURL)
	if err != nil || parsed.Host == "" {
		return dbURL
	}
	host := parsed.Hostname()
	switch host {
	case "127.0.0.1", "localhost", "::1":
	default:
		return dbURL
	}
	gateway := "host.docker.internal"
	if strings.Contains(filepath.Base(tool), "podman") {
		gateway = "host.containers.internal"
	}
	if port := parsed.Port(); port != "" {
		parsed.Host = net.JoinHostPort(gateway, port)
	} else {
		parsed.Host = gateway
	}
	return parsed.String()
}

// command builds the process for one run.
func (r *LocalRunner) command(runID, workflowPath string) LocalCommand {
	if r.Image != "" {
		args := []string{"run", "--rm", "-e", "CADS_RUN_ID=" + runID}
		if r.ContainerDBURL != "" {
			args = append(args, "-e", "CADS_DB_URL="+r.ContainerDBURL)
		}
		args = append(args,
			"-v", filepath.Join(r.WorkDir, "workflows")+":"+containerAppRoot+"/workflows:ro",
			r.Image,
			containerRunnerBin, "--json-output", "--workflow", workflowPath,
		)
		return LocalCommand{Name: r.ContainerTool, Args: args, Env: os.Environ(), Dir: r.WorkDir}
	}
	env := withoutEnv(os.Environ(), EnvRunID, "CADS_DB_URL")
	env = append(env, EnvRunID+"="+runID)
	if r.DBURL != "" {
		env = append(env, "CADS_DB_URL="+r.DBURL)
	}
	return LocalCommand{
		Name: r.RunnerBin,
		Args: []string{"--json-output", "--workflow", workflowPath},
		Env:  env,
		Dir:  r.WorkDir,
	}
}

func withoutEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		drop := false
		for _, key := range keys {
			if strings.HasPrefix(entry, key+"=") {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, entry)
		}
	}
	return out
}

// Submit starts workflowPath in the background; one run per workflow may be in flight.
func (r *LocalRunner) Submit(workflowPath string) (*RunSummary, error) {
	normalized, err := ResolveLaunchWorkflow(r.WorkDir, workflowPath)
	if err != nil {
		return nil, err
	}
	metadata, digest, err := readWorkflowDocument(r.WorkDir, normalized)
	if err != nil {
		return nil, err
	}
	deadline, _ := resolveRunLimits(ArgoConfig{MaxRuntimeSeconds: r.MaxRuntime}, metadata.Limits)

	r.mu.Lock()
	if running, ok := r.inflight[normalized]; ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("workflow %s already has a local run in flight (%s)", normalized, running.summary.Name)
	}
	now := r.now().UTC()
	runID := ResolveRunID(nil, now)
	run := &localRun{summary: RunSummary{
		Name:            runID,
		WorkflowPath:    normalized,
		Phase:           "Running",
		CreatedAt:       &now,
		StartedAt:       &now,
		Image:           r.Image,
		DeadlineSeconds: deadline,
		WorkflowSHA256:  digest,
		Source:          RunSourceLocal,
		SubmittedFrom:   "local " + r.Mode(),
	}}
	r.inflight[normalized] = run
	summary := run.summary
	r.wg.Add(1)
	r.mu.Unlock()

	cmd := r.command(runID, normalized)
	go r.execute(run, cmd, time.Duration(deadline)*time.Second)
	return &summary, nil
}

func (r *LocalRunner) execute(run *localRun, cmd LocalCommand, deadline time.Duration) {
	defer r.wg.Done()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	stdout, stderr, execErr := r.exec(ctx, cmd)
	deadlineHit := errors.Is(ctx.Err(), context.DeadlineExceeded)
	cancel()

	finished := r.now().UTC()
	results, parseErr := extractRunResultsFromLogs(stdout)
	if parseErr != nil {
		results = nil
	}

	summary := run.summary
	summary.FinishedAt = &finished
	summary.DurationSeconds = computeDurationSeconds(summary.StartedAt, &finished, finished)
	summary.DeadlineExceeded = deadlineHit
	summary.DataWindow = dataWindowFromResults(results)

	status := runInfoStatus(results)
	switch {
	case execErr == nil && results != nil && (status == "" || status == "succeeded" || status == "skipped"):
		summary.Phase = "Succeeded"
		if status == "skipped" {
			summary.Message = "no new data"
		}
	case results != nil:
		summary.Phase = "Failed"
		summary.Message = runInfoError(results)
	default:
		summary.Phase = "Error"
	}
	if deadlineHit {
		summary.Phase = "Failed"
		summary.Message = fmt.Sprintf("local run exceeded its deadline of %s", deadline)
	}
	if summary.Message == "" && summary.Phase != "Succeeded" {
		switch {
		case execErr != nil:
			summary.Message = execErr.Error()
		case parseErr != nil:
			summary.Message = parseErr.Error()
		}
	}
	if summary.Phase != "Succeeded" {
		log.Printf("[local] run %s (%s) %s: %s\n%s", summary.Name, summary.WorkflowPath, strings.ToLower(summary.Phase), summary.Message, stderrTail(stderr, localStderrTailSize))
	} else {
		log.Printf("[local] run %s (%s) finished in %.1fs", summary.Name, summary.WorkflowPath, summary.DurationSeconds)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	run.summary = summary
	run.results = results
	if r.inflight[summary.WorkflowPath] == run {
		delete(r.inflight, summary.WorkflowPath)
	}
	r.recent[summary.Name] = run
	r.recentOrder = append(r.recentOrder, summary.Name)
	for len(r.recentOrder) > localRecentCap {
		delete(r.recent, r.recentOrder[0])
		r.recentOrder = r.recentOrder[1:]
	}
}

// Wait blocks until every started run has finished (tests and shutdown).
func (r *LocalRunner) Wait() {
	r.wg.Wait()
}

// InFlight lists the running local runs, newest first.
func (r *LocalRunner) InFlight() []RunSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	out := make([]RunSummary, 0, len(r.inflight))
	for _, run := range r.inflight {
		summary := run.summary
		summary.DurationSeconds = computeDurationSeconds(summary.StartedAt, nil, now)
		out = append(out, summary)
	}
	sortRunsNewestFirst(out)
	return out
}

// Recent lists the finished local runs kept in memory, newest first.
func (r *LocalRunner) Recent() []RunSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RunSummary, 0, len(r.recent))
	for _, run := range r.recent {
		out = append(out, run.summary)
	}
	sortRunsNewestFirst(out)
	return out
}

// lookup returns a local run by id; inFlight is true while it runs.
func (r *LocalRunner) lookup(name string) (summary RunSummary, results map[string]map[string]any, inFlight, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, run := range r.inflight {
		if run.summary.Name == name {
			summary = run.summary
			summary.DurationSeconds = computeDurationSeconds(summary.StartedAt, nil, r.now().UTC())
			return summary, nil, true, true
		}
	}
	if run, found := r.recent[name]; found {
		return run.summary, run.results, false, true
	}
	return RunSummary{}, nil, false, false
}

func sortRunsNewestFirst(runs []RunSummary) {
	sort.SliceStable(runs, func(i, j int) bool {
		return runSortTime(runs[i]).After(runSortTime(runs[j]))
	})
}

func runSortTime(run RunSummary) time.Time {
	switch {
	case run.CreatedAt != nil:
		return *run.CreatedAt
	case run.StartedAt != nil:
		return *run.StartedAt
	case run.FinishedAt != nil:
		return *run.FinishedAt
	}
	return time.Time{}
}

func runInfoStatus(results map[string]map[string]any) string {
	if info := results[workflowpkg.RunInfoStepName]; info != nil {
		if status, ok := info["status"].(string); ok {
			return strings.ToLower(strings.TrimSpace(status))
		}
	}
	return ""
}

func runInfoError(results map[string]map[string]any) string {
	if info := results[workflowpkg.RunInfoStepName]; info != nil {
		if message, ok := info["error"].(string); ok {
			return strings.TrimSpace(message)
		}
	}
	return ""
}

// dataWindowFromResults decodes `_run.data_window` (nil when absent).
func dataWindowFromResults(results map[string]map[string]any) *workflowpkg.DataWindowInfo {
	info := results[workflowpkg.RunInfoStepName]
	if info == nil {
		return nil
	}
	return decodeDataWindow(info["data_window"])
}

func decodeDataWindow(raw any) *workflowpkg.DataWindowInfo {
	if raw == nil {
		return nil
	}
	if window, ok := raw.(*workflowpkg.DataWindowInfo); ok {
		return window
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var window workflowpkg.DataWindowInfo
	if err := json.Unmarshal(data, &window); err != nil || window.Mode == "" {
		return nil
	}
	return &window
}

func stderrTail(stderr []byte, lines int) string {
	text := strings.TrimRight(string(stderr), "\n")
	if text == "" {
		return "(no stderr)"
	}
	parts := strings.Split(text, "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

func defaultLocalExec(ctx context.Context, c LocalCommand) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Env = c.Env
	cmd.Dir = c.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// phaseFromStoredStatus maps a <site>.runs status to a RunSummary phase.
func phaseFromStoredStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "skipped":
		return "Succeeded"
	case "failed", "cancelled", "canceled":
		return "Failed"
	case "running":
		return "Running"
	case "":
		return "Unknown"
	}
	return "Failed"
}

// runSourceForID tells local runs (run id local-*) from Argo runs.
func runSourceForID(runID string) string {
	if strings.HasPrefix(runID, "local-") {
		return RunSourceLocal
	}
	return RunSourceArgo
}
