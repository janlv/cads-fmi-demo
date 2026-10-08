package cadsdb

import (
	"reflect"
	"testing"
)

func sampleResults() map[string]map[string]any {
	return map[string]map[string]any{
		"monitor": {
			"rul_days":  42.5,
			"alarm":     true,
			"label":     "ok",
			"nothing":   nil,
			"vector":    []float64{1, 2},
			"int_value": 3,
			"trace": map[string]any{
				"time": []float64{0, 1, 1, 2},
				"signals": map[string]any{
					"vib":    []any{1.0, 2.0, 2.5, nil},
					"vector": []any{[]any{1.0}, []any{2.0}, []any{3.0}, []any{4.0}},
				},
			},
		},
		"_synthetic_case": {"name": "case"},
		"_run": {
			"status":            "failed",
			"started_at":        "2026-10-08T10:00:00.5Z",
			"finished_at":       "2026-10-08T10:00:02Z",
			"wall_seconds":      1.5,
			"simulated_seconds": 2.0,
			"runner_version":    "v1",
			"workflow":          map[string]any{"path": "workflows/x.yaml", "sha256": "abc", "site": "cheylas"},
		},
	}
}

func TestBuildRunRecordShapesPayload(t *testing.T) {
	rec, err := BuildRunRecord("cads-x-1", sampleResults())
	if err != nil {
		t.Fatalf("BuildRunRecord() error = %v", err)
	}
	if rec.Status != "failed" || rec.WorkflowPath != "workflows/x.yaml" || rec.WorkflowSHA256 == nil || *rec.WorkflowSHA256 != "abc" ||
		rec.RunnerVersion == nil || *rec.RunnerVersion != "v1" || rec.StartedAt == nil || rec.FinishedAt == nil ||
		*rec.WallSeconds != 1.5 || *rec.SimulatedSeconds != 2 {
		t.Fatalf("run row = %+v", rec)
	}
	byName := map[string]OutputRow{}
	for _, row := range rec.Outputs {
		if row.Step != "monitor" {
			t.Fatalf("output from step %s: `_` steps must be skipped", row.Step)
		}
		byName[row.Name] = row
	}
	if len(rec.Outputs) != 5 {
		t.Fatalf("outputs = %+v, want 5 scalar rows (vector and trace skipped)", rec.Outputs)
	}
	if *byName["rul_days"].Value != 42.5 || *byName["alarm"].Value != 1 || *byName["label"].ValueText != "ok" ||
		byName["nothing"].Value != nil || byName["nothing"].ValueText != nil || *byName["int_value"].Value != 3 {
		t.Fatalf("outputs = %+v", byName)
	}
	// Duplicate t=1 keeps the last sample; vector signals are skipped.
	if len(rec.Series) != 3 {
		t.Fatalf("series = %+v, want 3 vib samples", rec.Series)
	}
	if rec.Series[1].T != 1 || *rec.Series[1].Value != 2.5 || rec.Series[2].Value != nil {
		t.Fatalf("series = %+v", rec.Series)
	}
	if _, err := BuildRunRecord("", sampleResults()); err == nil {
		t.Fatal("BuildRunRecord without run id must fail")
	}
}

func TestAssembleRunRebuildsResults(t *testing.T) {
	one, two, text := 1.0, 2.0, "ok"
	provenance := map[string]any{"status": "succeeded"}
	results := AssembleRun(provenance,
		[]OutputRow{{Step: "s", Name: "y", Value: &one}, {Step: "s", Name: "label", ValueText: &text}, {Step: "s", Name: "none"}},
		[]SeriesRow{
			{Step: "s", Signal: "a", T: 0, Value: &one},
			{Step: "s", Signal: "a", T: 1, Value: &two},
			{Step: "s", Signal: "b", T: 1, Value: &one},
			{Step: "s", Signal: "b", T: 2},
		})
	if !reflect.DeepEqual(results["_run"], provenance) {
		t.Fatalf("_run = %v", results["_run"])
	}
	step := results["s"]
	if step["y"] != 1.0 || step["label"] != "ok" || step["none"] != nil {
		t.Fatalf("outputs = %v", step)
	}
	if _, ok := step["none"]; !ok {
		t.Fatal("null output must be present")
	}
	want := map[string]any{
		"time": []any{0.0, 1.0, 2.0},
		"signals": map[string]any{
			"a": []any{1.0, 2.0, nil},
			"b": []any{nil, 1.0, nil},
		},
	}
	if !reflect.DeepEqual(step["trace"], want) {
		t.Fatalf("trace = %#v, want %#v", step["trace"], want)
	}
}

func TestBuildThenAssembleRoundTrip(t *testing.T) {
	rec, err := BuildRunRecord("r", sampleResults())
	if err != nil {
		t.Fatal(err)
	}
	results := AssembleRun(rec.Provenance, rec.Outputs, rec.Series)
	if results["monitor"]["rul_days"] != 42.5 || results["monitor"]["alarm"] != 1.0 || results["_run"]["status"] != "failed" {
		t.Fatalf("results = %v", results)
	}
	trace := results["monitor"]["trace"].(map[string]any)
	if !reflect.DeepEqual(trace["time"], []any{0.0, 1.0, 2.0}) {
		t.Fatalf("trace time = %v", trace["time"])
	}
}

func TestValidSite(t *testing.T) {
	for site, want := range map[string]bool{"cheylas": true, "alqueva": true, "la_rance": true, "la-rance": false, "Cheylas": false, "": false, "1abc": false} {
		if ValidSite(site) != want {
			t.Fatalf("ValidSite(%q) = %v, want %v", site, !want, want)
		}
	}
}
