package service

import (
	"path/filepath"
	"testing"
)

// The Cheylas workflows use a relative data_window: their cosim steps omit start/stop times,
// which the catalog must accept, and the window is exposed in the summary.
func TestListWorkflowsAcceptsDataWindowWorkflows(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := ListWorkflows(root)
	if err != nil {
		t.Fatalf("ListWorkflows() error = %v", err)
	}
	found := 0
	for _, wf := range workflows {
		if wf.Metadata.SiteID != "cheylas" {
			continue
		}
		found++
		if wf.DataWindow == nil || wf.DataWindow.Mode != "since_previous_run" || wf.DataWindow.InitialSpan != "24h" || wf.DataWindow.MaxSpan != "7d" {
			t.Fatalf("%s data window = %+v", wf.Path, wf.DataWindow)
		}
		if len(wf.Problems) > 0 {
			t.Fatalf("%s problems = %v", wf.Path, wf.Problems)
		}
		for _, model := range wf.Models {
			if len(model.Problems) > 0 {
				t.Fatalf("%s model %s problems = %v", wf.Path, model.Name, model.Problems)
			}
			if model.CoSim != nil && (model.CoSim.StartTime != nil || model.CoSim.StopTime != nil) {
				t.Fatalf("%s cosim times = %v..%v, want omitted", wf.Path, model.CoSim.StartTime, model.CoSim.StopTime)
			}
		}
	}
	if found != 2 {
		t.Fatalf("found %d Cheylas workflows, want 2", found)
	}
	for _, wf := range workflows {
		if wf.Metadata.SiteID != "cheylas" && wf.DataWindow != nil {
			t.Fatalf("%s unexpectedly has a data window", wf.Path)
		}
	}
}
