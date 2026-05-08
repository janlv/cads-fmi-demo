package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	workflowpkg "github.com/norceresearch/cads-fmi-demo/orchestrator/service/workflow"
)

type WorkflowSummary struct {
	Path      string           `json:"path"`
	Name      string           `json:"name"`
	StepCount int              `json:"stepCount"`
	Metadata  WorkflowMetadata `json:"metadata"`
}

type WorkflowMetadata struct {
	DisplayName  string   `json:"displayName,omitempty" yaml:"display_name"`
	SiteID       string   `json:"siteId,omitempty" yaml:"site_id"`
	Category     string   `json:"category,omitempty" yaml:"category"`
	ResultFamily string   `json:"resultFamily,omitempty" yaml:"result_family"`
	Description  string   `json:"description,omitempty" yaml:"description"`
	Tags         []string `json:"tags,omitempty" yaml:"tags"`
}

var ErrWorkflowOutsideDirectory = errors.New("workflow path must stay within workflows/")

type workflowCatalogFile struct {
	Metadata WorkflowMetadata `yaml:"metadata"`
	Steps    []struct{}       `yaml:"steps"`
}

// ListWorkflows returns the launchable workflows from the repository.
func ListWorkflows(root string) ([]WorkflowSummary, error) {
	workflowsRoot := filepath.Join(root, "workflows")

	seen := make(map[string]struct{})
	var files []string
	if err := filepath.WalkDir(workflowsRoot, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(file))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		if _, exists := seen[file]; exists {
			return nil
		}
		seen[file] = struct{}{}
		files = append(files, file)
		return nil
	}); err != nil {
		if os.IsNotExist(err) {
			return []WorkflowSummary{}, nil
		}
		return nil, fmt.Errorf("walk workflows: %w", err)
	}
	sort.Strings(files)

	workflows := make([]WorkflowSummary, 0, len(files))
	for _, file := range files {
		rel, err := resolveWorkflowReferenceFromRepoPath(root, file)
		if err != nil {
			return nil, fmt.Errorf("list workflow %s: %w", file, err)
		}

		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read workflow %s: %w", rel, err)
		}

		var doc workflowCatalogFile
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse workflow %s: %w", rel, err)
		}

		base := filepath.Base(rel)
		workflows = append(workflows, WorkflowSummary{
			Path:      rel,
			Name:      strings.TrimSuffix(base, filepath.Ext(base)),
			StepCount: len(doc.Steps),
			Metadata:  doc.Metadata,
		})
	}

	return workflows, nil
}

// ResolveLaunchWorkflow validates and resolves a repo workflow path intended for execution.
func ResolveLaunchWorkflow(root string, workflowPath string) (string, error) {
	rel, abs, err := resolveRepoWorkflowPath(root, workflowPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("workflow %s not found: %w", rel, err)
	}
	return rel, nil
}

func resolveRepoWorkflowPath(root string, workflowPath string) (string, string, error) {
	if workflowPath == "" {
		return "", "", fmt.Errorf("workflow path is required")
	}

	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve repo root: %w", err)
	}

	candidate := workflowPath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(resolvedRoot, candidate)
	}
	candidate = filepath.Clean(candidate)

	rel, err := filepath.Rel(resolvedRoot, candidate)
	if err != nil {
		return "", "", fmt.Errorf("resolve workflow path %q: %w", workflowPath, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("%w: workflow %q", workflowpkg.ErrPathEscapesRoot, workflowPath)
	}

	normalized, err := NormalizeWorkflowReference(filepath.ToSlash(rel))
	if err != nil {
		return "", "", err
	}

	return normalized, filepath.Join(resolvedRoot, filepath.FromSlash(normalized)), nil
}

func resolveWorkflowReferenceFromRepoPath(root string, absPath string) (string, error) {
	rel, _, err := resolveRepoWorkflowPath(root, absPath)
	return rel, err
}

// NormalizeWorkflowReference accepts only repo-local workflow paths under workflows/.
func NormalizeWorkflowReference(workflowPath string) (string, error) {
	trimmed := strings.TrimSpace(workflowPath)
	if trimmed == "" {
		return "", fmt.Errorf("workflow path is required")
	}

	slashed := strings.ReplaceAll(trimmed, "\\", "/")
	if strings.HasPrefix(slashed, "/") {
		return "", fmt.Errorf("workflow path must be relative to the repository")
	}

	cleaned := path.Clean(slashed)
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("workflow path is required")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: workflow %q", workflowpkg.ErrPathEscapesRoot, workflowPath)
	}
	if !strings.HasPrefix(cleaned, "workflows/") {
		return "", fmt.Errorf("%w: %s", ErrWorkflowOutsideDirectory, workflowPath)
	}

	ext := strings.ToLower(path.Ext(cleaned))
	if ext != ".yaml" && ext != ".yml" {
		return "", fmt.Errorf("workflow path must point to a YAML file: %s", workflowPath)
	}

	return cleaned, nil
}
