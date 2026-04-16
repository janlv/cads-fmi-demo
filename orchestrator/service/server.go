package service

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/workflow"
)

type Server struct {
	Runner *Runner
}

type runRequest struct {
	Workflow string `json:"workflow"`
}

type runResponse struct {
	Workflow string                    `json:"workflow"`
	Results  map[string]map[string]any `json:"results"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/run" {
		http.NotFound(w, r)
		return
	}

	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}
	if req.Workflow == "" {
		http.Error(w, "workflow is required", http.StatusBadRequest)
		return
	}

	results, err := s.Runner.Run(req.Workflow)
	if err != nil {
		log.Printf("workflow %s failed: %v", req.Workflow, err)
		status := http.StatusInternalServerError
		if errors.Is(err, workflow.ErrPathEscapesRoot) {
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(runResponse{Workflow: req.Workflow, Results: results}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
