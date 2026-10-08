package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
	workflowpkg "github.com/norceresearch/cads-fmi-demo/orchestrator/service/workflow"
)

// EnvRunID names the run id used for the results sink; hosted manifests set it to the Argo
// workflow name so the dashboard can find the stored run again.
const EnvRunID = "CADS_RUN_ID"

// Results-sink outcomes recorded in `_run.results_sink`.
const (
	SinkWritten = "written"
	SinkFailed  = "failed"
	SinkSkipped = "skipped"
)

// RunWriter is the write side of the CADS Data interface used by the results sink.
type RunWriter interface {
	WriteRun(ctx context.Context, site, runID string, results map[string]map[string]any) (*cadsdb.WriteSummary, error)
}

// ResolveRunID returns CADS_RUN_ID or a generated `local-<yyyymmddHHMMSS>-<rand>` id.
func ResolveRunID(lookup EnvLookup, now time.Time) string {
	if lookup != nil {
		if id := strings.TrimSpace(lookup(EnvRunID)); id != "" {
			return id
		}
	}
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return "local-" + now.UTC().Format("20060102150405")
	}
	return "local-" + now.UTC().Format("20060102150405") + "-" + hex.EncodeToString(suffix)
}

// RunSite returns `_run.workflow.site` of a result map.
func RunSite(results map[string]map[string]any) string {
	info := results[workflowpkg.RunInfoStepName]
	if info == nil {
		return ""
	}
	switch wf := info["workflow"].(type) {
	case map[string]any:
		site, _ := wf["site"].(string)
		return strings.TrimSpace(site)
	case workflowpkg.WorkflowInfo:
		return strings.TrimSpace(wf.Site)
	}
	return ""
}

// SinkResults writes results to the database and records the outcome in `_run.results_sink`
// (ARCH-DATA-010). It never fails the run: the returned error is for logging only.
func SinkResults(ctx context.Context, writer RunWriter, runID string, results map[string]map[string]any) error {
	if results == nil {
		return fmt.Errorf("no results to write")
	}
	info := results[workflowpkg.RunInfoStepName]
	if info == nil {
		info = map[string]any{}
		results[workflowpkg.RunInfoStepName] = info
	}
	site := RunSite(results)
	switch {
	case site == "":
		err := fmt.Errorf("workflow has no metadata.site_id")
		info["results_sink"] = map[string]any{"status": SinkSkipped, "error": err.Error()}
		return err
	case !cadsdb.ValidSite(site):
		err := fmt.Errorf("site %q is not a database schema identifier", site)
		info["results_sink"] = map[string]any{"status": SinkSkipped, "site": site, "error": err.Error()}
		return err
	case writer == nil:
		err := fmt.Errorf("database is not available")
		info["results_sink"] = map[string]any{"status": SinkFailed, "error": err.Error()}
		return err
	}
	summary, err := writer.WriteRun(ctx, site, runID, results)
	if err != nil {
		info["results_sink"] = map[string]any{"status": SinkFailed, "error": err.Error()}
		return err
	}
	info["results_sink"] = summary.SinkRecord()
	return nil
}
