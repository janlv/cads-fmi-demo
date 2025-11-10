package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/workflow"
)

// Runner executes workflows directly via the Go FMIL bindings.
type Runner struct {
	WorkDir string
	exec    *workflow.Executor
}

func NewRunner(workDir string, opts ...workflow.Option) (*Runner, error) {
	resolved, err := ResolveWorkDir(workDir)
	if err != nil {
		return nil, err
	}
	exec, err := workflow.NewExecutor(resolved, opts...)
	if err != nil {
		return nil, err
	}
	return &Runner{WorkDir: resolved, exec: exec}, nil
}

// Run executes the workflow and returns its results.
func (r *Runner) Run(workflowPath string) (map[string]map[string]any, error) {
	return r.exec.Run(workflowPath)
}

// ResolveWorkDir figures out the repository root when not provided.
func ResolveWorkDir(explicit string) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if isRepoRoot(abs) {
			return abs, nil
		}
		return "", fmt.Errorf("%s does not look like the repo root", abs)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if isRepoRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("unable to locate repository root (looked for workflows/ directory)")
}

func isRepoRoot(path string) bool {
	wf := filepath.Join(path, "workflows")
	fmu := filepath.Join(path, "fmu")
	if _, err := os.Stat(wf); err != nil {
		return false
	}
	if _, err := os.Stat(fmu); err != nil {
		return false
	}
	return true
}
