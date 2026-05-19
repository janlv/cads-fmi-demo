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
	"unicode"

	"gopkg.in/yaml.v3"

	workflowpkg "github.com/norceresearch/cads-fmi-demo/orchestrator/service/workflow"
)

type WorkflowSummary struct {
	Path      string           `json:"path"`
	Name      string           `json:"name"`
	StepCount int              `json:"stepCount"`
	Metadata  WorkflowMetadata `json:"metadata"`
	Models    []WorkflowModel  `json:"models,omitempty"`
}

type WorkflowMetadata struct {
	DisplayName  string   `json:"displayName,omitempty" yaml:"display_name"`
	SiteID       string   `json:"siteId,omitempty" yaml:"site_id"`
	Category     string   `json:"category,omitempty" yaml:"category"`
	ResultFamily string   `json:"resultFamily,omitempty" yaml:"result_family"`
	Description  string   `json:"description,omitempty" yaml:"description"`
	Tags         []string `json:"tags,omitempty" yaml:"tags"`
}

type WorkflowModel struct {
	Name        string               `json:"name"`
	Label       string               `json:"label,omitempty"`
	FMU         string               `json:"fmu,omitempty"`
	Inputs      []WorkflowModelInput `json:"inputs,omitempty"`
	Outputs     []string             `json:"outputs,omitempty"`
	Parameters  []string             `json:"parameters,omitempty"`
	InputSeries string               `json:"inputSeries,omitempty"`
}

type WorkflowModelInput struct {
	Name         string `json:"name"`
	Source       string `json:"source"`
	SourceStep   string `json:"sourceStep,omitempty"`
	SourceOutput string `json:"sourceOutput,omitempty"`
}

var ErrWorkflowOutsideDirectory = errors.New("workflow path must stay within workflows/")

type workflowCatalogFile struct {
	Metadata WorkflowMetadata      `yaml:"metadata"`
	Steps    []workflowCatalogStep `yaml:"steps"`
}

type workflowCatalogStep struct {
	Name        string                      `yaml:"name"`
	FMU         string                      `yaml:"fmu"`
	Outputs     []string                    `yaml:"outputs"`
	StartFrom   map[string]string           `yaml:"start_from"`
	StartValues map[string]any              `yaml:"start_values"`
	InputSeries *workflowCatalogInputSeries `yaml:"input_series"`
}

type workflowCatalogInputSeries struct {
	CSV string `yaml:"csv"`
	S3  *struct {
		Bucket string `yaml:"bucket"`
		Key    string `yaml:"key"`
	} `yaml:"s3"`
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
			if entry.Name() == "tests" && file != workflowsRoot {
				return filepath.SkipDir
			}
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
			Models:    workflowModelSummaries(doc.Steps),
		})
	}

	return workflows, nil
}

func workflowModelSummaries(steps []workflowCatalogStep) []WorkflowModel {
	models := make([]WorkflowModel, 0, len(steps))
	for _, step := range steps {
		model := WorkflowModel{
			Name:        strings.TrimSpace(step.Name),
			Label:       workflowModelLabel(step),
			FMU:         strings.TrimSpace(step.FMU),
			Outputs:     append([]string(nil), step.Outputs...),
			Parameters:  sortedMapKeys(step.StartValues),
			InputSeries: workflowInputSeriesLabel(step.InputSeries),
		}

		inputNames := make([]string, 0, len(step.StartFrom))
		for name := range step.StartFrom {
			inputNames = append(inputNames, name)
		}
		sort.Strings(inputNames)
		for _, name := range inputNames {
			source := strings.TrimSpace(step.StartFrom[name])
			input := WorkflowModelInput{
				Name:   name,
				Source: source,
			}
			if sourceStep, sourceOutput, ok := strings.Cut(source, "."); ok {
				input.SourceStep = sourceStep
				input.SourceOutput = sourceOutput
			}
			model.Inputs = append(model.Inputs, input)
		}

		models = append(models, model)
	}
	return models
}

func workflowModelLabel(step workflowCatalogStep) string {
	base := strings.TrimSpace(step.FMU)
	if base != "" {
		base = path.Base(strings.ReplaceAll(base, "\\", "/"))
		base = strings.TrimSuffix(base, path.Ext(base))
		base = strings.TrimSuffix(base, "Replica")
	}
	if base == "" {
		base = strings.TrimSpace(step.Name)
	}
	label := prettifyIdentifier(base)
	if label == "" {
		return step.Name
	}
	return label
}

func workflowInputSeriesLabel(series *workflowCatalogInputSeries) string {
	if series == nil {
		return ""
	}
	if strings.TrimSpace(series.CSV) != "" {
		return strings.TrimSpace(series.CSV)
	}
	if series.S3 != nil && strings.TrimSpace(series.S3.Key) != "" {
		bucket := strings.TrimSpace(series.S3.Bucket)
		key := strings.TrimSpace(series.S3.Key)
		if bucket != "" {
			return "s3://" + bucket + "/" + key
		}
		return key
	}
	return ""
}

func sortedMapKeys(values map[string]any) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func prettifyIdentifier(value string) string {
	normalized := strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(value))
	if normalized == "" {
		return ""
	}

	runes := []rune(normalized)
	var builder strings.Builder
	for index, current := range runes {
		if index > 0 && current != ' ' {
			prev := runes[index-1]
			var next rune
			if index+1 < len(runes) {
				next = runes[index+1]
			}
			if prev != ' ' && shouldSplitIdentifier(prev, current, next) {
				builder.WriteRune(' ')
			}
		}
		builder.WriteRune(current)
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func shouldSplitIdentifier(prev rune, current rune, next rune) bool {
	if !unicode.IsUpper(current) {
		return false
	}
	if unicode.IsLower(prev) || unicode.IsDigit(prev) {
		return true
	}
	return unicode.IsUpper(prev) && next != 0 && unicode.IsLower(next)
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
