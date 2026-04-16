package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerRejectsWorkflowTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatalf("create workflows dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "fmu"), 0o755); err != nil {
		t.Fatalf("create fmu dir: %v", err)
	}

	runner, err := NewRunner(root)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	server := &Server{Runner: runner}
	req := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(`{"workflow":"../../outside.yaml"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "path escapes repository root") {
		t.Fatalf("ServeHTTP() body = %q, want path confinement error", rec.Body.String())
	}
}
