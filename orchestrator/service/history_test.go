package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
)

// The production store must satisfy the service's read interface.
var _ DataStore = (*cadsdb.PG)(nil)

func TestChooseBin(t *testing.T) {
	base := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		span      time.Duration
		sampling  float64
		maxPoints int
		want      float64
	}{
		{"fits raw", 24 * time.Hour, 300, 1500, 0},
		{"exactly max", 1500 * 300 * time.Second, 300, 1500, 0},
		{"7d over 100 points", 7*24*time.Hour + time.Second, 300, 100, 6049},
		{"30d over 1500 points", 30 * 24 * time.Hour, 600, 1500, 1728},
		{"unknown sampling as 1 s", 2000 * time.Second, 0, 1000, 2},
		{"empty span", 0, 300, 100, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chooseBin(base, base.Add(tc.span), tc.sampling, tc.maxPoints); got != tc.want {
				t.Fatalf("chooseBin() = %v, want %v", got, tc.want)
			}
		})
	}
}

func historyFixture() *fakeDataStore {
	ts := func(s string) *time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			panic(err)
		}
		return &v
	}
	f := func(v float64) *float64 { return &v }
	window := func(from, to string, simStart float64) map[string]any {
		return map[string]any{"data_window": map[string]any{"from": from, "to": to, "sim_start_time": simStart}}
	}
	store := &fakeDataStore{channels: map[string][]cadsdb.ChannelInfo{
		"cheylas": {
			{Channel: "vibration_rms_mm_s", Unit: "mm/s", SamplingIntervalS: 300, From: ts("2026-05-20T00:00:00Z"), To: ts("2026-05-22T00:00:00Z")},
			{Channel: "temp_c", Unit: "degC", SamplingIntervalS: 60, From: ts("2026-05-20T00:00:00Z"), To: ts("2026-05-23T00:00:00Z")},
		},
	}}
	start := *ts("2026-05-20T00:00:00Z")
	for i := 0; i <= 576; i++ {
		store.rows = append(store.rows, cadsdb.Row{Time: start.Add(time.Duration(i) * 300 * time.Second), Channel: "vibration_rms_mm_s", Value: float64(i)})
	}
	store.rows = append(store.rows, cadsdb.Row{Time: start, Channel: "temp_c", Value: 20})
	wf := "workflows/cheylas_runner_health.yaml"
	store.runList = []cadsdb.StoredRun{
		{RunID: "r-started", WorkflowPath: wf, Status: "succeeded", StartedAt: ts("2026-05-21T22:00:00Z"), SimulatedSeconds: f(3600), Provenance: map[string]any{}},
		{RunID: "local-20260521-abc", WorkflowPath: "workflows/other.yaml", Status: "succeeded", StartedAt: ts("2026-05-21T20:30:00Z"),
			Provenance: map[string]any{"steps": []any{map[string]any{"fmus": []any{map[string]any{"inputs": []any{
				map[string]any{"from": "2026-05-21T18:00:00Z", "to": "2026-05-21T20:00:00Z"}}}}}}}},
		{RunID: "r-new", WorkflowPath: wf, Status: "succeeded", StartedAt: ts("2026-05-21T13:00:00Z"), FinishedAt: ts("2026-05-21T13:01:00Z"),
			Provenance: window("2026-05-21T06:00:00Z", "2026-05-21T12:00:00Z", 3600)},
		{RunID: "r-old", WorkflowPath: wf, Status: "succeeded", StartedAt: ts("2026-05-21T12:05:00Z"),
			Provenance: window("2026-05-21T06:00:00Z", "2026-05-21T12:00:00Z", 0)},
		{RunID: "r-failed", WorkflowPath: wf, Status: "failed", StartedAt: ts("2026-05-21T14:00:00Z"),
			Provenance: window("2026-05-21T08:00:00Z", "2026-05-21T14:00:00Z", 0)},
		{RunID: "r-outside", WorkflowPath: wf, Status: "succeeded", StartedAt: ts("2026-05-18T12:00:00Z"),
			Provenance: window("2026-05-18T00:00:00Z", "2026-05-18T06:00:00Z", 0)},
	}
	text := "ok"
	store.outputs = []cadsdb.OutputRow{
		{RunID: "r-new", Step: "runner_health", Name: "rul.rul_days", Value: f(412.3)},
		{RunID: "r-new", Step: "runner_health", Name: "rul.state", ValueText: &text},
		{RunID: "r-new", Step: "runner_health", Name: "rul.null"},
		{RunID: "r-old", Step: "runner_health", Name: "rul.rul_days", Value: f(1)},
	}
	store.series = []cadsdb.RunSeriesRow{
		{RunID: "r-new", Step: "runner_health", Signal: "rul.rul_days", T: 3600, Value: f(413)},
		{RunID: "r-new", Step: "runner_health", Signal: "rul.rul_days", T: 4500},
		{RunID: "r-new", Step: "runner_health", Signal: "x", T: 3600, Value: f(1)},
		{RunID: "r-old", Step: "runner_health", Signal: "rul.rul_days", T: 0, Value: f(9)},
	}
	return store
}

func getHistory(t *testing.T, server *Server, query string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sites/"+query, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v (%s)", query, err, rec.Body.String())
	}
	return rec.Code, body
}

func TestSiteHistoryEndpoint(t *testing.T) {
	store := historyFixture()
	server := &Server{Data: store}

	code, body := getHistory(t, server, "cheylas/history?channels=vibration_rms_mm_s&series=runner_health:rul.rul_days,other:y")
	if code != http.StatusOK || body["configured"] != true || body["site"] != "cheylas" || body["span"] != "24h" {
		t.Fatalf("history = %d %v", code, body)
	}
	// Anchored at the requested channel's newest sample, not temp_c's later one.
	if body["anchor"] != "2026-05-22T00:00:00Z" || body["from"] != "2026-05-21T00:00:00Z" || body["to"] != "2026-05-22T00:00:01Z" ||
		body["binSeconds"] != 0.0 || body["truncated"] != false {
		t.Fatalf("range = %v %v %v bin=%v", body["anchor"], body["from"], body["to"], body["binSeconds"])
	}
	fromMs := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).UnixMilli()
	if body["fromMs"] != float64(fromMs) || body["toMs"] != float64(fromMs+86_401_000) {
		t.Fatalf("fromMs/toMs = %v %v", body["fromMs"], body["toMs"])
	}
	measurements := body["measurements"].([]any)
	m := measurements[0].(map[string]any)
	points := m["points"].([]any)
	first := points[0].([]any)
	if len(measurements) != 1 || m["channel"] != "vibration_rms_mm_s" || m["unit"] != "mm/s" || len(points) != 289 ||
		first[0] != float64(fromMs) || first[1] != 288.0 {
		t.Fatalf("measurement = %v (%d points, first %v)", m["channel"], len(points), first)
	}

	runs := body["runs"].([]any)
	var ids []string
	for _, raw := range runs {
		ids = append(ids, raw.(map[string]any)["runId"].(string))
	}
	if strings.Join(ids, ",") != "r-new,local-20260521-abc,r-started" {
		t.Fatalf("runs = %v, want newest per window, in-range, succeeded, ordered by window", ids)
	}
	newest := runs[0].(map[string]any)
	if newest["source"] != "argo" || newest["anchorSource"] != cadsdb.WindowSourceDataWindow || newest["simStartTime"] != 3600.0 ||
		newest["windowFrom"] != "2026-05-21T06:00:00Z" || newest["windowTo"] != "2026-05-21T12:00:00Z" ||
		newest["windowFromMs"] != float64(time.Date(2026, 5, 21, 6, 0, 0, 0, time.UTC).UnixMilli()) ||
		newest["startedAt"] != "2026-05-21T13:00:00Z" || newest["finishedAt"] != "2026-05-21T13:01:00Z" {
		t.Fatalf("newest run = %v", newest)
	}
	if got := fmt.Sprint(newest["outputs"]); got != "map[runner_health:map[rul.rul_days:412.3]]" {
		t.Fatalf("outputs = %s, want numeric outputs only", got)
	}
	if got := fmt.Sprint(newest["series"]); got != "map[runner_health:map[rul.rul_days:map[t:[3600 4500] v:[413 <nil>]]]]" {
		t.Fatalf("series = %s, want only the requested pair with null values", got)
	}
	local := runs[1].(map[string]any)
	if local["source"] != "local" || local["anchorSource"] != cadsdb.WindowSourceInput || local["windowFrom"] != "2026-05-21T18:00:00Z" ||
		local["simStartTime"] != 0.0 || local["finishedAt"] != nil || len(local["outputs"].(map[string]any)) != 0 {
		t.Fatalf("input-window run = %v", local)
	}
	started := runs[2].(map[string]any)
	if started["anchorSource"] != cadsdb.WindowSourceStartedAt || started["windowFrom"] != "2026-05-21T22:00:00Z" || started["windowTo"] != "2026-05-21T23:00:00Z" {
		t.Fatalf("started_at run = %v", started)
	}

	// 7d over 100 points is binned; buckets align to from and average the samples.
	store.binCalls = nil
	code, body = getHistory(t, server, "cheylas/history?span=7d&max_points=100&channels=vibration_rms_mm_s")
	if code != http.StatusOK || body["binSeconds"] != 6049.0 || len(store.binCalls) != 1 || store.binCalls[0] != 6049 {
		t.Fatalf("7d history = %d bin=%v calls=%v", code, body["binSeconds"], store.binCalls)
	}
	points = body["measurements"].([]any)[0].(map[string]any)["points"].([]any)
	if len(points) == 0 || len(points) > 100 {
		t.Fatalf("binned points = %d", len(points))
	}
	for _, raw := range points {
		p := raw.([]any)
		if int64(p[0].(float64)-body["fromMs"].(float64))%6_049_000 != 0 {
			t.Fatalf("bucket %v not aligned to from %v", p[0], body["fromMs"])
		}
	}
	if len(body["runs"].([]any)) != 4 {
		t.Fatalf("7d runs = %d, want the 2026-05-18 run included", len(body["runs"].([]any)))
	}

	// No channels requested: anchored at the newest channel of the site, no measurements read.
	store.binCalls = nil
	code, body = getHistory(t, server, "cheylas/history?span=6h")
	if code != http.StatusOK || body["anchor"] != "2026-05-23T00:00:00Z" || len(body["measurements"].([]any)) != 0 || len(store.binCalls) != 0 {
		t.Fatalf("no-channel history = %d %v", code, body)
	}

	// Without measurements the newest run window end anchors the range.
	runsOnly := historyFixture()
	runsOnly.channels = nil
	code, body = getHistory(t, &Server{Data: runsOnly}, "cheylas/history?span=6h")
	if code != http.StatusOK || body["anchor"] != "2026-05-21T23:00:00Z" || len(body["runs"].([]any)) != 2 {
		t.Fatalf("runs-only history = %d %v", code, body)
	}

	// Nothing stored: configured, empty.
	code, body = getHistory(t, &Server{Data: &fakeDataStore{}}, "cheylas/history")
	if code != http.StatusOK || body["configured"] != true || body["anchor"] != nil || len(body["runs"].([]any)) != 0 || len(body["measurements"].([]any)) != 0 {
		t.Fatalf("empty history = %d %v", code, body)
	}

	for _, query := range []string{
		"cheylas/history?span=2h",
		"cheylas/history?channels=a;b",
		"cheylas/history?channels=" + seventeenChannels(),
		"cheylas/history?max_points=50",
		"cheylas/history?max_points=abc",
		"cheylas/history?series=nocolon",
		"La-Rance/history",
	} {
		if code, body := getHistory(t, server, query); code != http.StatusBadRequest || body["error"] == nil {
			t.Fatalf("%s = %d %v, want 400", query, code, body)
		}
	}

	code, body = getHistory(t, &Server{}, "cheylas/history?span=7d")
	if code != http.StatusOK || body["configured"] != false || body["site"] != "cheylas" || body["span"] != "7d" ||
		len(body["measurements"].([]any)) != 0 || len(body["runs"].([]any)) != 0 {
		t.Fatalf("history without db = %d %v", code, body)
	}

	failing := historyFixture()
	failing.histErr = errors.New("connection refused")
	code, body = getHistory(t, &Server{Data: failing}, "cheylas/history")
	if code != http.StatusBadGateway || !strings.Contains(fmt.Sprint(body["error"]), "read history of cheylas: connection refused") {
		t.Fatalf("history with failing db = %d %v", code, body)
	}
}

func seventeenChannels() string {
	names := make([]string, 17)
	for i := range names {
		names[i] = fmt.Sprintf("ch_%d", i)
	}
	return strings.Join(names, ",")
}
