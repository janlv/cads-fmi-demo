package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
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
	// SHA256 is the hex digest of the workflow file as it exists in this checkout; hosted runs
	// carry the digest of the file they were submitted with, so the dashboard can flag drift.
	SHA256 string `json:"sha256"`
	// Problems lists catalog-level issues (for example invalid limits) that do not hide the workflow.
	Problems []string `json:"problems,omitempty"`
}

type WorkflowMetadata struct {
	DisplayName  string          `json:"displayName,omitempty" yaml:"display_name"`
	SiteID       string          `json:"siteId,omitempty" yaml:"site_id"`
	Category     string          `json:"category,omitempty" yaml:"category"`
	ResultFamily string          `json:"resultFamily,omitempty" yaml:"result_family"`
	Description  string          `json:"description,omitempty" yaml:"description"`
	Tags         []string        `json:"tags,omitempty" yaml:"tags"`
	Limits       *WorkflowLimits `json:"limits,omitempty" yaml:"limits"`
}

// WorkflowLimits are the per-workflow execution limits (ARCH-COMP-015/016). CPU and Memory are
// Kubernetes quantities used as both request and limit; MaxRuntimeSeconds becomes the Argo
// activeDeadlineSeconds (clamped to the dashboard ceiling).
type WorkflowLimits struct {
	MaxRuntimeSeconds int64  `json:"maxRuntimeSeconds,omitempty" yaml:"max_runtime_seconds"`
	CPU               string `json:"cpu,omitempty" yaml:"cpu"`
	Memory            string `json:"memory,omitempty" yaml:"memory"`
}

type WorkflowModel struct {
	Name        string               `json:"name"`
	Label       string               `json:"label,omitempty"`
	Kind        string               `json:"kind"`
	FMU         string               `json:"fmu,omitempty"`
	Inputs      []WorkflowModelInput `json:"inputs,omitempty"`
	Outputs     []string             `json:"outputs,omitempty"`
	Parameters  []string             `json:"parameters,omitempty"`
	InputSeries string               `json:"inputSeries,omitempty"`
	CoSim       *WorkflowCoSim       `json:"cosim,omitempty"`
	Problems    []string             `json:"problems,omitempty"`
}

// WorkflowCoSim describes a `cosim:` step for the dashboard coupling view.
type WorkflowCoSim struct {
	Scheme            string               `json:"scheme"`
	StartTime         *float64             `json:"startTime,omitempty"`
	StopTime          *float64             `json:"stopTime,omitempty"`
	CommunicationStep *float64             `json:"communicationStep,omitempty"`
	Patterns          []string             `json:"patterns,omitempty"`
	Models            []WorkflowModel      `json:"models"`
	Connections       []WorkflowConnection `json:"connections"`
	Events            []WorkflowEvent      `json:"events,omitempty"`
}

type WorkflowConnection struct {
	From         string `json:"from"`
	To           string `json:"to"`
	FromModel    string `json:"fromModel,omitempty"`
	FromVariable string `json:"fromVariable,omitempty"`
	ToModel      string `json:"toModel,omitempty"`
	ToVariable   string `json:"toVariable,omitempty"`
}

type WorkflowEvent struct {
	Name string `json:"name"`
	When string `json:"when"`
	Set  string `json:"set"`
	Mode string `json:"mode,omitempty"`
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
	CoSim       *workflowpkg.CoSimSpec      `yaml:"cosim"`
}

// workflowCatalogCoSimSeries re-reads per-model input_series of cosim steps, whose spec type is
// not exported by the workflow package.
type workflowCatalogCoSimSeries struct {
	Steps []struct {
		CoSim *struct {
			Models []struct {
				Name        string                      `yaml:"name"`
				InputSeries *workflowCatalogInputSeries `yaml:"input_series"`
			} `yaml:"models"`
		} `yaml:"cosim"`
	} `yaml:"steps"`
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

		doc, digest, err := loadWorkflowDocument(root, rel)
		if err != nil {
			return nil, err
		}

		base := filepath.Base(rel)
		workflows = append(workflows, WorkflowSummary{
			Path:      rel,
			Name:      strings.TrimSuffix(base, filepath.Ext(base)),
			StepCount: len(doc.Steps),
			Metadata:  doc.Metadata,
			Models:    workflowModelSummaries(doc.Steps, doc.coSimSeries),
			SHA256:    digest,
			Problems:  validateWorkflowLimits(doc.Metadata.Limits),
		})
	}

	return workflows, nil
}

type loadedWorkflowDocument struct {
	workflowCatalogFile
	coSimSeries map[string]map[string]*workflowCatalogInputSeries
}

// loadWorkflowDocument reads a repo-relative workflow file once and returns its parsed catalog
// view together with the hex SHA-256 of the exact bytes on disk.
func loadWorkflowDocument(root string, rel string) (*loadedWorkflowDocument, string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, "", fmt.Errorf("read workflow %s: %w", rel, err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	doc := &loadedWorkflowDocument{}
	if err := yaml.Unmarshal(data, &doc.workflowCatalogFile); err != nil {
		return nil, "", fmt.Errorf("parse workflow %s: %w", rel, err)
	}

	var series workflowCatalogCoSimSeries
	if err := yaml.Unmarshal(data, &series); err == nil {
		for index, step := range series.Steps {
			if step.CoSim == nil || index >= len(doc.Steps) {
				continue
			}
			for _, model := range step.CoSim.Models {
				if model.InputSeries == nil {
					continue
				}
				if doc.coSimSeries == nil {
					doc.coSimSeries = make(map[string]map[string]*workflowCatalogInputSeries)
				}
				stepName := strings.TrimSpace(doc.Steps[index].Name)
				if doc.coSimSeries[stepName] == nil {
					doc.coSimSeries[stepName] = make(map[string]*workflowCatalogInputSeries)
				}
				doc.coSimSeries[stepName][strings.TrimSpace(model.Name)] = model.InputSeries
			}
		}
	}
	return doc, digest, nil
}

// readWorkflowDocument returns the metadata and SHA-256 of a repo-relative workflow. Invalid
// limits are an error here because the result is used to build a submission.
func readWorkflowDocument(root string, rel string) (WorkflowMetadata, string, error) {
	doc, digest, err := loadWorkflowDocument(root, rel)
	if err != nil {
		return WorkflowMetadata{}, "", err
	}
	if problems := validateWorkflowLimits(doc.Metadata.Limits); len(problems) > 0 {
		return WorkflowMetadata{}, "", fmt.Errorf("workflow %s: %s", rel, strings.Join(problems, "; "))
	}
	return doc.Metadata, digest, nil
}

var (
	cpuQuantityPattern    = regexp.MustCompile(`^(?:[0-9]+m|[0-9]+(?:\.[0-9]+)?)$`)
	memoryQuantityPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:Ki|Mi|Gi|Ti|Pi|Ei|k|M|G|T|P|E)?$`)
)

func validCPUQuantity(value string) bool {
	return cpuQuantityPattern.MatchString(value) && !isZeroQuantity(value)
}

func validMemoryQuantity(value string) bool {
	return memoryQuantityPattern.MatchString(value) && !isZeroQuantity(value)
}

func isZeroQuantity(value string) bool {
	return strings.Trim(value, "0.mkKMGTPEi") == ""
}

func validateWorkflowLimits(limits *WorkflowLimits) []string {
	if limits == nil {
		return nil
	}
	var problems []string
	if limits.MaxRuntimeSeconds < 0 {
		problems = append(problems, fmt.Sprintf("limits.max_runtime_seconds must be positive (got %d)", limits.MaxRuntimeSeconds))
	}
	if cpu := strings.TrimSpace(limits.CPU); cpu != "" && !validCPUQuantity(cpu) {
		problems = append(problems, fmt.Sprintf("limits.cpu %q is not a CPU quantity such as 500m or 2", limits.CPU))
	}
	if memory := strings.TrimSpace(limits.Memory); memory != "" && !validMemoryQuantity(memory) {
		problems = append(problems, fmt.Sprintf("limits.memory %q is not a memory quantity such as 512Mi or 2Gi", limits.Memory))
	}
	return problems
}

func workflowModelSummaries(steps []workflowCatalogStep, coSimSeries map[string]map[string]*workflowCatalogInputSeries) []WorkflowModel {
	models := make([]WorkflowModel, 0, len(steps))
	for _, step := range steps {
		if step.CoSim != nil {
			models = append(models, workflowCoSimSummary(step, coSimSeries[strings.TrimSpace(step.Name)]))
			continue
		}
		model := WorkflowModel{
			Name:        strings.TrimSpace(step.Name),
			Label:       workflowModelLabel(step),
			Kind:        "fmu",
			FMU:         strings.TrimSpace(step.FMU),
			Outputs:     append([]string(nil), step.Outputs...),
			Parameters:  sortedMapKeys(step.StartValues),
			InputSeries: workflowInputSeriesLabel(step.InputSeries),
			Inputs:      workflowModelInputs(step.StartFrom, ""),
		}
		models = append(models, model)
	}
	return models
}

func workflowModelInputs(startFrom map[string]string, prefix string) []WorkflowModelInput {
	inputNames := make([]string, 0, len(startFrom))
	for name := range startFrom {
		inputNames = append(inputNames, name)
	}
	sort.Strings(inputNames)
	var inputs []WorkflowModelInput
	for _, name := range inputNames {
		source := strings.TrimSpace(startFrom[name])
		input := WorkflowModelInput{
			Name:   prefix + name,
			Source: source,
		}
		if sourceStep, sourceOutput, ok := strings.Cut(source, "."); ok {
			input.SourceStep = sourceStep
			input.SourceOutput = sourceOutput
		}
		inputs = append(inputs, input)
	}
	return inputs
}

// workflowCoSimSummary keeps a cosim step as ONE catalog entry so neighbour-arrow rendering keeps
// working: outputs are the flattened `model.var` keys and inputs the prefixed model start_from.
func workflowCoSimSummary(step workflowCatalogStep, series map[string]*workflowCatalogInputSeries) WorkflowModel {
	spec := step.CoSim
	name := strings.TrimSpace(step.Name)
	label := titleCaseWords(prettifyIdentifier(name))
	if label == "" {
		label = name
	}
	summary := WorkflowModel{
		Name:    name,
		Label:   label,
		Kind:    "cosim",
		Outputs: append([]string(nil), spec.Outputs...),
	}
	if strings.TrimSpace(step.FMU) != "" || len(step.StartFrom) > 0 || len(step.StartValues) > 0 || step.InputSeries != nil {
		summary.Problems = append(summary.Problems, fmt.Sprintf("step %s: cosim cannot be combined with fmu, start_values, start_from or input_series", name))
	}
	if err := workflowpkg.ValidateCoSim(name, spec); err != nil {
		summary.Problems = append(summary.Problems, err.Error())
	}

	cosim := &WorkflowCoSim{
		Scheme:            spec.Scheme,
		StartTime:         spec.StartTime,
		StopTime:          spec.StopTime,
		CommunicationStep: spec.CommunicationStep,
		Patterns:          workflowpkg.CoSimPatterns(spec),
		Models:            make([]WorkflowModel, 0, len(spec.Models)),
		Connections:       make([]WorkflowConnection, 0, len(spec.Connections)),
	}

	for _, member := range spec.Models {
		memberName := strings.TrimSpace(member.Name)
		model := WorkflowModel{
			Name:        memberName,
			Label:       workflowModelLabel(workflowCatalogStep{Name: memberName, FMU: member.FMU}),
			Kind:        "fmu",
			FMU:         strings.TrimSpace(member.FMU),
			Parameters:  sortedMapKeys(member.StartValues),
			InputSeries: workflowInputSeriesLabel(series[memberName]),
			Inputs:      workflowModelInputs(member.StartFrom, ""),
		}
		for _, output := range spec.Outputs {
			if modelName, variable, ok := strings.Cut(output, "."); ok && modelName == memberName {
				model.Outputs = append(model.Outputs, variable)
			}
		}
		for _, conn := range spec.Connections {
			if modelName, variable, ok := strings.Cut(conn.To, "."); ok && modelName == memberName {
				model.Inputs = append(model.Inputs, WorkflowModelInput{Name: variable, Source: conn.From})
			}
		}
		cosim.Models = append(cosim.Models, model)
		summary.Inputs = append(summary.Inputs, workflowModelInputs(member.StartFrom, memberName+".")...)
	}

	for _, conn := range spec.Connections {
		entry := WorkflowConnection{From: strings.TrimSpace(conn.From), To: strings.TrimSpace(conn.To)}
		entry.FromModel, entry.FromVariable, _ = strings.Cut(entry.From, ".")
		entry.ToModel, entry.ToVariable, _ = strings.Cut(entry.To, ".")
		cosim.Connections = append(cosim.Connections, entry)
	}
	for _, event := range spec.Events {
		mode := strings.ToLower(strings.TrimSpace(event.Mode))
		if mode == "" {
			mode = "level"
		}
		cosim.Events = append(cosim.Events, WorkflowEvent{
			Name: event.Name,
			When: strings.TrimSpace(event.When),
			Set:  strings.TrimSpace(event.Set),
			Mode: mode,
		})
	}
	summary.CoSim = cosim
	return summary
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

// titleCaseWords upper-cases the first letter of each word (step names are snake_case).
func titleCaseWords(value string) string {
	words := strings.Fields(value)
	for i, word := range words {
		runes := []rune(word)
		runes[0] = unicode.ToUpper(runes[0])
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}
