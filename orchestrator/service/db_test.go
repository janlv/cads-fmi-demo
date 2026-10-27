package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
)

type fakeDataStore struct {
	runs     map[string]map[string]map[string]any // site/runID -> results
	channels map[string][]cadsdb.ChannelInfo
	readErr  error
	reads    []string
	writes   []string
	written  map[string]map[string]any

	// History reads (in-memory, any site).
	rows     []cadsdb.Row
	runList  []cadsdb.StoredRun
	outputs  []cadsdb.OutputRow
	series   []cadsdb.RunSeriesRow
	binCalls []float64
	histErr  error

	// Window reads (data_window pre-check).
	latest   map[string]time.Time
	previous map[string]*cadsdb.PreviousRun // workflow path -> previous run
}

func (f *fakeDataStore) ReadSeries(_ context.Context, _ string, _ []string, _, _ time.Time) ([]cadsdb.Row, error) {
	return nil, nil
}

func (f *fakeDataStore) LatestSamples(_ context.Context, _ string, channels []string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	for _, channel := range channels {
		if at, ok := f.latest[channel]; ok {
			out[channel] = at
		}
	}
	return out, nil
}

func (f *fakeDataStore) LatestSucceededRun(_ context.Context, _ string, workflowPath, _ string) (*cadsdb.PreviousRun, error) {
	return f.previous[workflowPath], nil
}

func (f *fakeDataStore) ReadRun(_ context.Context, site, runID string) (map[string]map[string]any, error) {
	f.reads = append(f.reads, site+"/"+runID)
	if f.readErr != nil {
		return nil, f.readErr
	}
	results, ok := f.runs[site+"/"+runID]
	if !ok {
		return nil, cadsdb.ErrRunNotFound
	}
	return results, nil
}

func (f *fakeDataStore) ChannelStatus(_ context.Context, site string) ([]cadsdb.ChannelInfo, error) {
	if f.histErr != nil {
		return nil, f.histErr
	}
	return f.channels[site], nil
}

// ReadSeriesBinned filters rows to channels and [from, to); bin > 0 averages per bucket aligned
// to from (ordered by bucket, then channel) like the PostgreSQL date_bin query.
func (f *fakeDataStore) ReadSeriesBinned(_ context.Context, _ string, channels []string, from, to time.Time, bin float64) ([]cadsdb.Row, error) {
	f.binCalls = append(f.binCalls, bin)
	if f.histErr != nil {
		return nil, f.histErr
	}
	wanted := map[string]bool{}
	for _, c := range channels {
		wanted[c] = true
	}
	var raw []cadsdb.Row
	for _, row := range f.rows {
		if wanted[row.Channel] && !row.Time.Before(from) && row.Time.Before(to) {
			raw = append(raw, row)
		}
	}
	if bin <= 0 {
		sort.SliceStable(raw, func(i, j int) bool { return raw[i].Time.Before(raw[j].Time) })
		return raw, nil
	}
	type key struct {
		bucket  int64
		channel string
	}
	sums, counts := map[key]float64{}, map[key]int{}
	var keys []key
	width := time.Duration(bin * float64(time.Second))
	for _, row := range raw {
		k := key{int64(row.Time.Sub(from) / width), row.Channel}
		if counts[k] == 0 {
			keys = append(keys, k)
		}
		sums[k] += row.Value
		counts[k]++
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].bucket != keys[j].bucket {
			return keys[i].bucket < keys[j].bucket
		}
		return keys[i].channel < keys[j].channel
	})
	out := make([]cadsdb.Row, len(keys))
	for i, k := range keys {
		out[i] = cadsdb.Row{Time: from.Add(time.Duration(k.bucket) * width), Channel: k.channel, Value: sums[k] / float64(counts[k])}
	}
	return out, nil
}

func (f *fakeDataStore) ListRuns(_ context.Context, _ string, limit int) ([]cadsdb.StoredRun, error) {
	if f.histErr != nil {
		return nil, f.histErr
	}
	out := append([]cadsdb.StoredRun{}, f.runList...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeDataStore) ReadRunOutputs(_ context.Context, _ string, runIDs []string) ([]cadsdb.OutputRow, error) {
	if f.histErr != nil {
		return nil, f.histErr
	}
	ids := stringSet(runIDs)
	var out []cadsdb.OutputRow
	for _, row := range f.outputs {
		if ids[row.RunID] {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeDataStore) ReadRunSeries(_ context.Context, _ string, runIDs, steps, signals []string) ([]cadsdb.RunSeriesRow, error) {
	if f.histErr != nil {
		return nil, f.histErr
	}
	ids, stepSet, signalSet := stringSet(runIDs), stringSet(steps), stringSet(signals)
	var out []cadsdb.RunSeriesRow
	for _, row := range f.series {
		if ids[row.RunID] && stepSet[row.Step] && signalSet[row.Signal] {
			out = append(out, row)
		}
	}
	return out, nil
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

func (f *fakeDataStore) WriteRun(_ context.Context, site, runID string, results map[string]map[string]any) (*cadsdb.WriteSummary, error) {
	f.writes = append(f.writes, site+"/"+runID)
	if f.readErr != nil {
		return nil, f.readErr
	}
	rec, err := cadsdb.BuildRunRecord(runID, results)
	if err != nil {
		return nil, err
	}
	return &cadsdb.WriteSummary{Site: site, RunID: runID, Outputs: len(rec.Outputs), SeriesPoints: len(rec.Series)}, nil
}

func TestSinkResultsRecordsOutcome(t *testing.T) {
	newResults := func(site string) map[string]map[string]any {
		return map[string]map[string]any{
			"s":    {"y": 1.0, "trace": map[string]any{"time": []any{0.0, 1.0}, "signals": map[string]any{"y": []any{0.0, 1.0}}}},
			"_run": {"status": "failed", "workflow": map[string]any{"path": "workflows/x.yaml", "site": site}},
		}
	}

	store := &fakeDataStore{}
	results := newResults("cheylas")
	if err := SinkResults(context.Background(), store, "cads-x-1", results); err != nil {
		t.Fatalf("SinkResults() error = %v", err)
	}
	sink := results["_run"]["results_sink"].(map[string]any)
	if sink["status"] != SinkWritten || sink["site"] != "cheylas" || sink["run_id"] != "cads-x-1" || sink["outputs"] != 1 || sink["series_points"] != 2 {
		t.Fatalf("results_sink = %v", sink)
	}
	// The stdout payload must stay JSON-encodable.
	if _, err := json.Marshal(results); err != nil {
		t.Fatal(err)
	}

	failing := &fakeDataStore{readErr: errors.New("connection refused")}
	results = newResults("cheylas")
	if err := SinkResults(context.Background(), failing, "cads-x-1", results); err == nil {
		t.Fatal("SinkResults() with failing store must return the error")
	}
	sink = results["_run"]["results_sink"].(map[string]any)
	if sink["status"] != SinkFailed || !strings.Contains(sink["error"].(string), "connection refused") {
		t.Fatalf("results_sink = %v, want failed with error", sink)
	}
	if results["s"]["y"] != 1.0 || results["_run"]["status"] != "failed" {
		t.Fatalf("results changed by failing sink: %v", results)
	}

	for _, site := range []string{"", "la-rance"} {
		results = newResults(site)
		if err := SinkResults(context.Background(), store, "id", results); err == nil {
			t.Fatalf("site %q: want skip error", site)
		}
		if sink := results["_run"]["results_sink"].(map[string]any); sink["status"] != SinkSkipped {
			t.Fatalf("site %q: results_sink = %v, want skipped", site, sink)
		}
	}
	if len(store.writes) != 1 {
		t.Fatalf("writes = %v, want only the valid site written", store.writes)
	}
}

func TestResolveRunID(t *testing.T) {
	now := time.Date(2026, 10, 8, 9, 30, 5, 0, time.UTC)
	if id := ResolveRunID(func(string) string { return " cads-abc " }, now); id != "cads-abc" {
		t.Fatalf("ResolveRunID(env) = %q", id)
	}
	id := ResolveRunID(func(string) string { return "" }, now)
	if !strings.HasPrefix(id, "local-20261008093005-") || len(id) != len("local-20261008093005-")+6 {
		t.Fatalf("ResolveRunID(fallback) = %q", id)
	}
}

func writeSiteWorkflow(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "workflows", "tests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cosim_fail.yaml"), []byte("metadata:\n  site_id: cheylas\nsteps:\n  - name: s\n    fmu: fmu/a.fmu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGetRunResultsPrefersDatabase(t *testing.T) {
	root := t.TempDir()
	writeSiteWorkflow(t, root)
	client := newResultsTestClient(t, "Failed", "Error (exit code 1)", `{"from_logs":{"x":1},"_run":{"status":"failed"}}`, nil)
	client.workDir = root
	store := &fakeDataStore{runs: map[string]map[string]map[string]any{
		"cheylas/cads-run-abc12": {
			"s":    {"y": 2.0},
			"_run": {"status": "failed", "error": "boom", "failed_step": "s"},
		},
	}}
	client.SetRunStore(store)

	results, err := client.GetRunResults(context.Background(), "cads-run-abc12")
	if err != nil {
		t.Fatalf("GetRunResults() error = %v", err)
	}
	if results.CollectedFrom != "database" || results.StepResults["s"]["y"] != 2.0 || results.Status != "failed" ||
		!results.Partial || results.Error != "boom" || results.FailedStep != "s" {
		t.Fatalf("results = %+v, want database results with _run status", results)
	}
	if len(store.reads) != 1 || store.reads[0] != "cheylas/cads-run-abc12" {
		t.Fatalf("reads = %v", store.reads)
	}

	// Not in the database (or database error): fall back to the logs.
	for _, readErr := range []error{nil, errors.New("db down")} {
		store := &fakeDataStore{readErr: readErr}
		client.SetRunStore(store)
		results, err = client.GetRunResults(context.Background(), "cads-run-abc12")
		if err != nil {
			t.Fatalf("GetRunResults() error = %v", err)
		}
		if results.CollectedFrom != "argo logs" || results.StepResults["from_logs"]["x"] != float64(1) {
			t.Fatalf("results = %+v, want log fallback", results)
		}
	}

	// Running runs never reach the database.
	running := newResultsTestClient(t, "Running", "", "", nil)
	running.workDir = root
	runningStore := &fakeDataStore{}
	running.SetRunStore(runningStore)
	if _, err := running.GetRunResults(context.Background(), "cads-run-abc12"); !errors.Is(err, ErrRunResultsUnavailable) || len(runningStore.reads) != 0 {
		t.Fatalf("running GetRunResults() error = %v, reads = %v", err, runningStore.reads)
	}
}

func TestDataEndpointAndConfig(t *testing.T) {
	ingested := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	from := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	to := from.Add(48 * time.Hour)
	store := &fakeDataStore{channels: map[string][]cadsdb.ChannelInfo{
		"cheylas": {{Channel: "vibration_rms_mm_s", Unit: "mm/s", Source: "synthetic", SamplingIntervalS: 600, Points: 289, From: &from, To: &to, IngestedAt: ingested, Batch: "b1"}},
	}}
	get := func(server *Server, path string) (*httptest.ResponseRecorder, map[string]any) {
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec, body
	}

	with := &Server{Data: store}
	rec, body := get(with, "/api/data?site=cheylas")
	if rec.Code != http.StatusOK || body["configured"] != true || body["site"] != "cheylas" {
		t.Fatalf("GET /api/data = %d %v", rec.Code, body)
	}
	channels := body["channels"].([]any)
	channel := channels[0].(map[string]any)
	if len(channels) != 1 || channel["channel"] != "vibration_rms_mm_s" || channel["unit"] != "mm/s" || channel["points"] != 289.0 ||
		channel["samplingIntervalS"] != 600.0 || channel["from"] != "2026-05-20T00:00:00Z" || channel["to"] != "2026-05-22T00:00:00Z" ||
		channel["ingestedAt"] != "2026-10-01T08:00:00Z" || channel["batch"] != "b1" || channel["source"] != "synthetic" {
		t.Fatalf("channel = %v", channel)
	}
	rec, body = get(with, "/api/data?site=alqueva")
	if rec.Code != http.StatusOK || body["configured"] != true || len(body["channels"].([]any)) != 0 {
		t.Fatalf("GET /api/data?site=alqueva = %d %v, want empty list", rec.Code, body)
	}
	if rec, _ := get(with, "/api/data?site=../x"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid site status = %d", rec.Code)
	}
	_, cfg := get(with, "/api/config")
	if cfg["databaseConfigured"] != true {
		t.Fatalf("config = %v, want databaseConfigured", cfg)
	}

	without := &Server{}
	rec, body = get(without, "/api/data?site=cheylas")
	if rec.Code != http.StatusOK || body["configured"] != false || body["channels"] == nil || len(body["channels"].([]any)) != 0 {
		t.Fatalf("GET /api/data without db = %d %v", rec.Code, body)
	}
	_, cfg = get(without, "/api/config")
	if v, ok := cfg["databaseConfigured"]; !ok || v != false {
		t.Fatalf("config = %v, want databaseConfigured false", cfg)
	}
}
