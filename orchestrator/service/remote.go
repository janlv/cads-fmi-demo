package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	workflowpkg "github.com/norceresearch/cads-fmi-demo/orchestrator/service/workflow"
)

const (
	defaultArgoServer          = "argoworkflows.cads.kzslab.dev"
	defaultArgoNamespace       = "playground"
	defaultArgoServiceAccount  = "playground-storhy-playground-pg-admin"
	defaultRemoteImage         = "ghcr.io/janlv/cads-fmi-demo:playground"
	defaultS3CredentialsSecret = "storhy-argo-artifacts-s3-credentials"
	defaultPollInterval        = 5 * time.Second

	defaultMaxRuntimeSeconds        int64 = 900
	defaultMaxRuntimeCeilingSeconds int64 = 3600
	defaultCPURequest                     = "250m"
	defaultMemoryRequest                  = "256Mi"
	defaultCPULimit                       = "1"
	defaultMemoryLimit                    = "1Gi"

	labelManagedBy           = "app.kubernetes.io/managed-by"
	labelManagedByValue      = "cads-dashboard"
	labelWorkflow            = "cads.norceresearch.no/workflow"
	labelSite                = "cads.norceresearch.no/site"
	labelWorkflowSHA         = "cads.norceresearch.no/workflow-sha"
	annotationWorkflowPath   = "cads.norceresearch.no/workflow-path"
	annotationWorkflowSHA256 = "cads.norceresearch.no/workflow-sha256"
	annotationVersion        = "cads.norceresearch.no/dashboard-version"
	annotationSubmittedFrom  = "cads.norceresearch.no/submitted-from"
)

var (
	ErrRemoteUnavailable     = errors.New("remote playground is not configured")
	ErrRemoteRunNotFound     = errors.New("remote workflow not found")
	ErrRunResultsUnavailable = errors.New("workflow results are not available")
)

type EnvLookup func(string) string

type ArgoOptionInputs struct {
	ArgoServer     string
	Namespace      string
	ServiceAccount string
	Image          string
	Kubeconfig     string
	// MaxRuntimeSeconds is the default Argo activeDeadlineSeconds (flag --max-runtime-seconds);
	// zero means "use CADS_MAX_RUNTIME_SECONDS or the built-in default".
	MaxRuntimeSeconds int64
}

type ArgoConfig struct {
	ArgoServer     string
	Namespace      string
	ServiceAccount string
	Image          string
	Kubeconfig     string
	Token          string

	// MaxRuntimeSeconds is the deadline used when a workflow declares no limits.max_runtime_seconds.
	MaxRuntimeSeconds int64
	// MaxRuntimeCeilingSeconds caps any per-workflow deadline.
	MaxRuntimeCeilingSeconds int64
	// DefaultResources are the container requests/limits used when a workflow declares none.
	DefaultResources RunResources
}

// RunResources mirrors a Kubernetes container resources block (cpu/memory quantities).
type RunResources struct {
	Requests map[string]string `json:"requests,omitempty" yaml:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty" yaml:"limits,omitempty"`
}

type DashboardConfig struct {
	RemoteEnabled            bool          `json:"remoteEnabled"`
	ArgoServer               string        `json:"argoServer"`
	Namespace                string        `json:"namespace"`
	ServiceAccount           string        `json:"serviceAccount"`
	Image                    string        `json:"image"`
	PollIntervalSeconds      int           `json:"pollIntervalSeconds"`
	Problems                 []string      `json:"problems"`
	Version                  string        `json:"version"`
	MaxRuntimeSeconds        int64         `json:"maxRuntimeSeconds,omitempty"`
	MaxRuntimeCeilingSeconds int64         `json:"maxRuntimeCeilingSeconds,omitempty"`
	DefaultResources         *RunResources `json:"defaultResources,omitempty"`
}

type RunSummary struct {
	Name            string     `json:"name"`
	WorkflowPath    string     `json:"workflowPath"`
	Phase           string     `json:"phase"`
	CreatedAt       *time.Time `json:"createdAt,omitempty"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	DurationSeconds float64    `json:"durationSeconds"`
	Progress        string     `json:"progress,omitempty"`
	Message         string     `json:"message,omitempty"`
	Image           string     `json:"image,omitempty"`
	ServiceAccount  string     `json:"serviceAccount,omitempty"`

	// Provenance and limits recovered from the submitted manifest (ARCH-COMP-013/015/016/018).
	DeadlineSeconds  int64             `json:"deadlineSeconds,omitempty"`
	DeadlineExceeded bool              `json:"deadlineExceeded,omitempty"`
	Resources        *RunResources     `json:"resources,omitempty"`
	WorkflowSHA256   string            `json:"workflowSha256,omitempty"`
	DashboardVersion string            `json:"dashboardVersion,omitempty"`
	SubmittedFrom    string            `json:"submittedFrom,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
}

type RunResults struct {
	RunName       string                    `json:"runName"`
	WorkflowPath  string                    `json:"workflowPath"`
	StepResults   map[string]map[string]any `json:"stepResults"`
	CollectedFrom string                    `json:"collectedFrom"`
	// Phase is the Argo phase; Status the runner outcome from `_run.status` (else the lowercase phase).
	Phase      string `json:"phase,omitempty"`
	Status     string `json:"status,omitempty"`
	Error      string `json:"error,omitempty"`
	FailedStep string `json:"failedStep,omitempty"`
	// Partial is true when the run did not succeed, so StepResults may be incomplete or empty.
	Partial bool `json:"partial,omitempty"`
}

type RemoteClient interface {
	Config() DashboardConfig
	ListRuns(ctx context.Context, limit int) ([]RunSummary, error)
	GetRun(ctx context.Context, name string) (*RunSummary, error)
	GetRunResults(ctx context.Context, name string) (*RunResults, error)
	SubmitWorkflow(ctx context.Context, workflowPath string) (*RunSummary, error)
}

type execRunner func(ctx context.Context, command string, args ...string) ([]byte, error)

type ArgoRemoteClient struct {
	workDir  string
	argoCmd  string
	config   ArgoConfig
	problems []string
	exec     execRunner
	now      func() time.Time
	hostname func() (string, error)
}

func NewArgoRemoteClient(workDir string, input ArgoOptionInputs, lookup EnvLookup) *ArgoRemoteClient {
	if lookup == nil {
		lookup = os.Getenv
	}
	cfg, problems := ResolveArgoConfig(input, lookup)
	argoCmd, err := exec.LookPath("argo")
	if err != nil {
		problems = append(problems, "argo CLI not found on PATH")
	}

	return &ArgoRemoteClient{
		workDir:  workDir,
		argoCmd:  argoCmd,
		config:   cfg,
		problems: dedupeProblems(problems),
		exec:     defaultExecRunner,
		now:      time.Now,
		hostname: os.Hostname,
	}
}

func ResolveArgoConfig(input ArgoOptionInputs, lookup EnvLookup) (ArgoConfig, []string) {
	if lookup == nil {
		lookup = os.Getenv
	}

	cfg := ArgoConfig{
		ArgoServer:     pickString(input.ArgoServer, lookup("ARGO_SERVER"), defaultArgoServer),
		Namespace:      pickString(input.Namespace, lookup("ARGO_NAMESPACE"), defaultArgoNamespace),
		ServiceAccount: pickString(input.ServiceAccount, lookup("ARGO_SERVICE_ACCOUNT"), defaultArgoServiceAccount),
		Image:          pickString(input.Image, lookup("CADS_WORKFLOW_IMAGE"), defaultRemoteImage),
		Kubeconfig:     pickString(input.Kubeconfig, lookup("KUBECONFIG")),
	}

	var problems []string
	cfg.MaxRuntimeSeconds = defaultMaxRuntimeSeconds
	if input.MaxRuntimeSeconds < 0 {
		problems = append(problems, fmt.Sprintf("--max-runtime-seconds must be positive (got %d)", input.MaxRuntimeSeconds))
	} else if input.MaxRuntimeSeconds > 0 {
		cfg.MaxRuntimeSeconds = input.MaxRuntimeSeconds
	} else if value, problem := parsePositiveSecondsEnv(lookup, "CADS_MAX_RUNTIME_SECONDS"); problem != "" {
		problems = append(problems, problem)
	} else if value > 0 {
		cfg.MaxRuntimeSeconds = value
	}
	cfg.MaxRuntimeCeilingSeconds = defaultMaxRuntimeCeilingSeconds
	if value, problem := parsePositiveSecondsEnv(lookup, "CADS_MAX_RUNTIME_CEILING_SECONDS"); problem != "" {
		problems = append(problems, problem)
	} else if value > 0 {
		cfg.MaxRuntimeCeilingSeconds = value
	}
	if cfg.MaxRuntimeSeconds > cfg.MaxRuntimeCeilingSeconds {
		cfg.MaxRuntimeSeconds = cfg.MaxRuntimeCeilingSeconds
	}

	quantity := func(env string, fallback string, valid func(string) bool) string {
		value := strings.TrimSpace(lookup(env))
		if value == "" {
			return fallback
		}
		if !valid(value) {
			problems = append(problems, fmt.Sprintf("%s=%q is not a valid Kubernetes quantity", env, value))
			return fallback
		}
		return value
	}
	cfg.DefaultResources = RunResources{
		Requests: map[string]string{
			"cpu":    quantity("CADS_DEFAULT_CPU_REQUEST", defaultCPURequest, validCPUQuantity),
			"memory": quantity("CADS_DEFAULT_MEMORY_REQUEST", defaultMemoryRequest, validMemoryQuantity),
		},
		Limits: map[string]string{
			"cpu":    quantity("CADS_DEFAULT_CPU_LIMIT", defaultCPULimit, validCPUQuantity),
			"memory": quantity("CADS_DEFAULT_MEMORY_LIMIT", defaultMemoryLimit, validMemoryQuantity),
		},
	}

	if token := normalizeBearerToken(lookup("ARGO_TOKEN")); token != "" {
		cfg.Token = token
		return cfg, problems
	}

	if cfg.Kubeconfig == "" {
		problems = append(problems, "no Argo token configured; set ARGO_TOKEN or KUBECONFIG/--kubeconfig")
		return cfg, problems
	}

	token, err := extractBearerTokenFromKubeconfig(cfg.Kubeconfig)
	if err != nil {
		problems = append(problems, fmt.Sprintf("invalid kubeconfig: %v", err))
		return cfg, problems
	}
	cfg.Token = token
	return cfg, problems
}

func parsePositiveSecondsEnv(lookup EnvLookup, key string) (int64, string) {
	raw := strings.TrimSpace(lookup(key))
	if raw == "" {
		return 0, ""
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Sprintf("%s=%q must be a positive number of seconds", key, raw)
	}
	return value, ""
}

func (c *ArgoRemoteClient) Config() DashboardConfig {
	resources := c.config.DefaultResources
	return DashboardConfig{
		RemoteEnabled:            len(c.problems) == 0,
		ArgoServer:               c.config.ArgoServer,
		Namespace:                c.config.Namespace,
		ServiceAccount:           c.config.ServiceAccount,
		Image:                    c.config.Image,
		PollIntervalSeconds:      int(defaultPollInterval / time.Second),
		Problems:                 append([]string(nil), c.problems...),
		Version:                  ResolvedVersion(),
		MaxRuntimeSeconds:        c.config.MaxRuntimeSeconds,
		MaxRuntimeCeilingSeconds: c.config.MaxRuntimeCeilingSeconds,
		DefaultResources:         &resources,
	}
}

// resolveRunLimits picks the deadline and container resources for one submission: the
// workflow's own limits win over the dashboard defaults, and the deadline is clamped to the ceiling.
// A per-workflow cpu/memory value is used as both request and limit.
func resolveRunLimits(cfg ArgoConfig, limits *WorkflowLimits) (int64, RunResources) {
	deadline := cfg.MaxRuntimeSeconds
	if deadline <= 0 {
		deadline = defaultMaxRuntimeSeconds
	}
	if limits != nil && limits.MaxRuntimeSeconds > 0 {
		deadline = limits.MaxRuntimeSeconds
	}
	ceiling := cfg.MaxRuntimeCeilingSeconds
	if ceiling <= 0 {
		ceiling = defaultMaxRuntimeCeilingSeconds
	}
	if deadline > ceiling {
		deadline = ceiling
	}

	resources := RunResources{
		Requests: map[string]string{"cpu": defaultCPURequest, "memory": defaultMemoryRequest},
		Limits:   map[string]string{"cpu": defaultCPULimit, "memory": defaultMemoryLimit},
	}
	for key, value := range cfg.DefaultResources.Requests {
		resources.Requests[key] = value
	}
	for key, value := range cfg.DefaultResources.Limits {
		resources.Limits[key] = value
	}
	if limits != nil {
		if cpu := strings.TrimSpace(limits.CPU); cpu != "" {
			resources.Requests["cpu"] = cpu
			resources.Limits["cpu"] = cpu
		}
		if memory := strings.TrimSpace(limits.Memory); memory != "" {
			resources.Requests["memory"] = memory
			resources.Limits["memory"] = memory
		}
	}
	return deadline, resources
}

func (c *ArgoRemoteClient) ListRuns(ctx context.Context, limit int) ([]RunSummary, error) {
	if err := c.ensureReady(); err != nil {
		return nil, err
	}

	output, err := c.runArgo(ctx,
		c.withArgoConnectionArgs("list", "-o", "json")...,
	)
	if err != nil {
		return nil, err
	}

	runs, err := parseArgoWorkflowList(c.workDir, output, c.now())
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

func (c *ArgoRemoteClient) GetRun(ctx context.Context, name string) (*RunSummary, error) {
	if err := c.ensureReady(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("workflow name is required")
	}

	output, err := c.runArgo(ctx,
		c.withArgoConnectionArgs("get", name, "-o", "json")...,
	)
	if err != nil {
		return nil, err
	}

	run, err := parseArgoWorkflow(c.workDir, output, c.now())
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, ErrRemoteRunNotFound
	}
	return run, nil
}

func (c *ArgoRemoteClient) SubmitWorkflow(ctx context.Context, workflowPath string) (*RunSummary, error) {
	if err := c.ensureReady(); err != nil {
		return nil, err
	}

	normalized, err := ResolveLaunchWorkflow(c.workDir, workflowPath)
	if err != nil {
		return nil, err
	}

	metadata, digest, err := readWorkflowDocument(c.workDir, normalized)
	if err != nil {
		return nil, err
	}
	deadline, resources := resolveRunLimits(c.config, metadata.Limits)
	submittedFrom := ""
	if c.hostname != nil {
		if host, err := c.hostname(); err == nil {
			submittedFrom = host
		}
	}

	manifest, err := generateRemoteWorkflowManifest(remoteManifestSpec{
		Namespace:        c.config.Namespace,
		ServiceAccount:   c.config.ServiceAccount,
		Image:            c.config.Image,
		WorkflowPath:     normalized,
		WorkflowSHA256:   digest,
		SiteID:           metadata.SiteID,
		DeadlineSeconds:  deadline,
		Resources:        resources,
		DashboardVersion: ResolvedVersion(),
		SubmittedFrom:    submittedFrom,
	})
	if err != nil {
		return nil, err
	}

	file, err := os.CreateTemp("", "cads-argo-dashboard-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("create temp manifest: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)

	if _, err := file.Write(manifest); err != nil {
		file.Close()
		return nil, fmt.Errorf("write temp manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close temp manifest: %w", err)
	}

	output, err := c.runArgo(ctx,
		c.withArgoConnectionArgs("submit", tempPath, "-o", "json")...,
	)
	if err != nil {
		return nil, err
	}

	run, err := parseArgoWorkflow(c.workDir, output, c.now())
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, fmt.Errorf("submitted workflow was not recognized as a repo workflow")
	}
	return run, nil
}

// resultsPhaseAllowed reports whether logs are worth reading for a run in this Argo phase.
// Failed and errored runs are included so partial results and the `_run` failure record show up.
func resultsPhaseAllowed(phase string) bool {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "succeeded", "failed", "error":
		return true
	}
	return false
}

func (c *ArgoRemoteClient) GetRunResults(ctx context.Context, name string) (*RunResults, error) {
	if err := c.ensureReady(); err != nil {
		return nil, err
	}

	run, err := c.GetRun(ctx, name)
	if err != nil {
		return nil, err
	}
	if !resultsPhaseAllowed(run.Phase) {
		return nil, fmt.Errorf("%w: workflow phase is %s", ErrRunResultsUnavailable, run.Phase)
	}
	succeeded := strings.EqualFold(run.Phase, "Succeeded")

	response := &RunResults{
		RunName:       run.Name,
		WorkflowPath:  run.WorkflowPath,
		CollectedFrom: "argo logs",
		Phase:         run.Phase,
		Status:        strings.ToLower(run.Phase),
		Partial:       !succeeded,
	}

	output, err := c.runArgo(ctx,
		c.withArgoConnectionArgs("logs", name, "--tail", "2000")...,
	)
	var results map[string]map[string]any
	if err == nil {
		results, err = extractRunResultsFromLogs(output)
	}
	if err != nil {
		if succeeded {
			return nil, err
		}
		// Failed runs (deadline kills in particular) may have printed nothing parseable.
		response.StepResults = map[string]map[string]any{}
		response.Error = run.Message
		return response, nil
	}

	response.StepResults = results
	if info := results[workflowpkg.RunInfoStepName]; info != nil {
		if status, ok := info["status"].(string); ok && strings.TrimSpace(status) != "" {
			response.Status = status
			if !strings.EqualFold(status, "succeeded") {
				response.Partial = true
			}
		}
		if message, ok := info["error"].(string); ok && strings.TrimSpace(message) != "" {
			response.Error = message
		}
		if failedStep, ok := info["failed_step"].(string); ok {
			response.FailedStep = failedStep
		}
	}
	if response.Partial && response.Error == "" {
		response.Error = run.Message
	}
	return response, nil
}

func (c *ArgoRemoteClient) ensureReady() error {
	if len(c.problems) > 0 {
		return fmt.Errorf("%w: %s", ErrRemoteUnavailable, strings.Join(c.problems, "; "))
	}
	return nil
}

func (c *ArgoRemoteClient) withArgoConnectionArgs(args ...string) []string {
	out := make([]string, 0, len(args)+8)
	out = append(out, args...)
	out = append(out, "-n", c.config.Namespace, "-s", c.config.ArgoServer)
	if strings.TrimSpace(c.config.Kubeconfig) != "" {
		out = append(out, "--kubeconfig", c.config.Kubeconfig)
	} else {
		out = append(out, "--token", c.config.Token)
	}
	out = append(out, "--argo-http1")
	return out
}

func (c *ArgoRemoteClient) runArgo(ctx context.Context, args ...string) ([]byte, error) {
	output, err := c.exec(ctx, c.argoCmd, args...)
	if err != nil {
		argText := redactSecrets(strings.Join(args, " "), c.config.Token)
		errText := redactSecrets(err.Error(), c.config.Token)
		return nil, fmt.Errorf("argo %s: %s", argText, errText)
	}
	return output, nil
}

func defaultExecRunner(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text != "" {
			return nil, fmt.Errorf("%w: %s", err, text)
		}
		return nil, err
	}
	return output, nil
}

type kubeconfigDocument struct {
	CurrentContext string `yaml:"current-context"`
	Contexts       []struct {
		Name    string `yaml:"name"`
		Context struct {
			User string `yaml:"user"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Token string `yaml:"token"`
		} `yaml:"user"`
	} `yaml:"users"`
}

func extractBearerTokenFromKubeconfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	var doc kubeconfigDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}

	users := make(map[string]string, len(doc.Users))
	for _, user := range doc.Users {
		if token := normalizeBearerToken(user.User.Token); token != "" {
			users[user.Name] = token
		}
	}

	if doc.CurrentContext != "" {
		for _, ctx := range doc.Contexts {
			if ctx.Name != doc.CurrentContext {
				continue
			}
			if token := users[ctx.Context.User]; token != "" {
				return token, nil
			}
			break
		}
	}

	for _, user := range doc.Users {
		if token := normalizeBearerToken(user.User.Token); token != "" {
			return token, nil
		}
	}

	return "", fmt.Errorf("kubeconfig does not contain a bearer token")
}

func normalizeBearerToken(token string) string {
	trimmed := strings.TrimSpace(token)
	trimmed = strings.TrimPrefix(trimmed, "Bearer ")
	trimmed = strings.TrimPrefix(trimmed, "bearer ")
	return strings.TrimSpace(trimmed)
}

func redactSecrets(text string, secrets ...string) string {
	redacted := text
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		redacted = strings.ReplaceAll(redacted, secret, "<redacted>")
		redacted = strings.ReplaceAll(redacted, "Bearer "+secret, "Bearer <redacted>")
		redacted = strings.ReplaceAll(redacted, "bearer "+secret, "bearer <redacted>")
	}
	return redacted
}

func pickString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func dedupeProblems(problems []string) []string {
	seen := make(map[string]struct{}, len(problems))
	out := make([]string, 0, len(problems))
	for _, problem := range problems {
		problem = strings.TrimSpace(problem)
		if problem == "" {
			continue
		}
		if _, exists := seen[problem]; exists {
			continue
		}
		seen[problem] = struct{}{}
		out = append(out, problem)
	}
	return out
}

type argoWorkflowEnvelope struct {
	Metadata argoMetadata `json:"metadata"`
	Spec     argoSpec     `json:"spec"`
	Status   argoStatus   `json:"status"`
}

type argoMetadata struct {
	Name              string            `json:"name"`
	GenerateName      string            `json:"generateName"`
	Namespace         string            `json:"namespace"`
	CreationTimestamp string            `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels"`
	Annotations       map[string]string `json:"annotations"`
}

type argoSpec struct {
	ServiceAccountName    string         `json:"serviceAccountName"`
	ActiveDeadlineSeconds *int64         `json:"activeDeadlineSeconds"`
	Templates             []argoTemplate `json:"templates"`
}

type argoTemplate struct {
	Name      string         `json:"name"`
	Container *argoContainer `json:"container"`
}

type argoContainer struct {
	Image     string        `json:"image"`
	Command   []string      `json:"command"`
	Args      []string      `json:"args"`
	Env       []argoEnvVar  `json:"env,omitempty"`
	Resources *RunResources `json:"resources,omitempty"`
}

type argoEnvVar struct {
	Name      string            `json:"name" yaml:"name"`
	Value     string            `json:"value,omitempty" yaml:"value,omitempty"`
	ValueFrom *argoValueFromRef `json:"valueFrom,omitempty" yaml:"valueFrom,omitempty"`
}

type argoValueFromRef struct {
	SecretKeyRef *argoSecretKeyRef `json:"secretKeyRef,omitempty" yaml:"secretKeyRef,omitempty"`
}

type argoSecretKeyRef struct {
	Name string `json:"name" yaml:"name"`
	Key  string `json:"key" yaml:"key"`
}

type argoStatus struct {
	Phase      string `json:"phase"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	Progress   string `json:"progress"`
	Message    string `json:"message"`
	Nodes      map[string]struct {
		DisplayName string `json:"displayName"`
		Phase       string `json:"phase"`
		Message     string `json:"message"`
	} `json:"nodes"`
}

func parseArgoWorkflowList(root string, payload []byte, now time.Time) ([]RunSummary, error) {
	var envelopes []argoWorkflowEnvelope
	if err := json.Unmarshal(payload, &envelopes); err != nil {
		return nil, fmt.Errorf("parse argo workflow list: %w", err)
	}

	runs := make([]RunSummary, 0, len(envelopes))
	for _, envelope := range envelopes {
		run, err := normalizeArgoWorkflow(root, envelope, now)
		if err != nil {
			return nil, err
		}
		if run == nil {
			continue
		}
		runs = append(runs, *run)
	}

	sort.Slice(runs, func(i, j int) bool {
		left := time.Time{}
		right := time.Time{}
		if runs[i].CreatedAt != nil {
			left = *runs[i].CreatedAt
		}
		if runs[j].CreatedAt != nil {
			right = *runs[j].CreatedAt
		}
		return left.After(right)
	})

	return runs, nil
}

func parseArgoWorkflow(root string, payload []byte, now time.Time) (*RunSummary, error) {
	var envelope argoWorkflowEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("parse argo workflow: %w", err)
	}
	return normalizeArgoWorkflow(root, envelope, now)
}

func normalizeArgoWorkflow(root string, envelope argoWorkflowEnvelope, now time.Time) (*RunSummary, error) {
	workflowPath, image, resources := extractWorkflowInvocation(envelope.Spec.Templates)
	if workflowPath == "" {
		// Fallback for manifests whose container args are not recognisable (for example a
		// wrapper script): trust the provenance annotation written at submission time.
		workflowPath = strings.TrimSpace(envelope.Metadata.Annotations[annotationWorkflowPath])
	}
	if workflowPath == "" {
		return nil, nil
	}

	normalizedPath, err := NormalizeWorkflowReference(workflowPath)
	if err != nil {
		return nil, nil
	}

	createdAt, err := parseArgoTime(envelope.Metadata.CreationTimestamp)
	if err != nil {
		return nil, fmt.Errorf("parse creation timestamp for %s: %w", envelope.Metadata.Name, err)
	}
	startedAt, err := parseArgoTime(envelope.Status.StartedAt)
	if err != nil {
		return nil, fmt.Errorf("parse start timestamp for %s: %w", envelope.Metadata.Name, err)
	}
	finishedAt, err := parseArgoTime(envelope.Status.FinishedAt)
	if err != nil {
		return nil, fmt.Errorf("parse finish timestamp for %s: %w", envelope.Metadata.Name, err)
	}

	message := summarizeArgoStatusMessage(envelope.Status)
	var deadline int64
	if envelope.Spec.ActiveDeadlineSeconds != nil {
		deadline = *envelope.Spec.ActiveDeadlineSeconds
	}
	phase := strings.ToLower(envelope.Status.Phase)
	deadlineExceeded := (phase == "failed" || phase == "error") && strings.Contains(strings.ToLower(message), "deadline")
	if resources != nil && len(resources.Requests) == 0 && len(resources.Limits) == 0 {
		resources = nil
	}
	var labels map[string]string
	if len(envelope.Metadata.Labels) > 0 {
		labels = make(map[string]string, len(envelope.Metadata.Labels))
		for key, value := range envelope.Metadata.Labels {
			labels[key] = value
		}
	}
	annotations := envelope.Metadata.Annotations

	return &RunSummary{
		DeadlineSeconds:  deadline,
		DeadlineExceeded: deadlineExceeded,
		Resources:        resources,
		WorkflowSHA256:   strings.TrimSpace(annotations[annotationWorkflowSHA256]),
		DashboardVersion: strings.TrimSpace(annotations[annotationVersion]),
		SubmittedFrom:    strings.TrimSpace(annotations[annotationSubmittedFrom]),
		Labels:           labels,
		Name:             envelope.Metadata.Name,
		WorkflowPath:     normalizedPath,
		Phase:            envelope.Status.Phase,
		CreatedAt:        createdAt,
		StartedAt:        startedAt,
		FinishedAt:       finishedAt,
		DurationSeconds:  computeDurationSeconds(startedAt, finishedAt, now),
		Progress:         envelope.Status.Progress,
		Message:          message,
		Image:            image,
		ServiceAccount:   envelope.Spec.ServiceAccountName,
	}, nil
}

func summarizeArgoStatusMessage(status argoStatus) string {
	message := strings.TrimSpace(status.Message)
	if message != "" {
		return message
	}

	nodeNames := make([]string, 0, len(status.Nodes))
	for name := range status.Nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)
	for _, name := range nodeNames {
		node := status.Nodes[name]
		nodeMessage := strings.TrimSpace(node.Message)
		if nodeMessage == "" {
			continue
		}
		nodePhase := strings.TrimSpace(node.Phase)
		if nodePhase != "" {
			return nodePhase + ": " + nodeMessage
		}
		return nodeMessage
	}

	return ""
}

func extractWorkflowInvocation(templates []argoTemplate) (string, string, *RunResources) {
	var fallbackImage string
	var fallbackResources *RunResources
	for _, template := range templates {
		if template.Container == nil {
			continue
		}
		workflowPath := extractWorkflowArgument(template.Container.Args)
		if workflowPath == "" {
			workflowPath = extractWorkflowArgument(template.Container.Command)
		}
		if workflowPath == "" {
			if fallbackImage == "" {
				fallbackImage = template.Container.Image
				fallbackResources = template.Container.Resources
			}
			continue
		}
		return workflowPath, template.Container.Image, template.Container.Resources
	}
	return "", fallbackImage, fallbackResources
}

func extractWorkflowArgument(args []string) string {
	for i, arg := range args {
		if arg == "--workflow" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "--workflow=") {
			return strings.TrimPrefix(arg, "--workflow=")
		}
	}
	return ""
}

func parseArgoTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func extractRunResultsFromLogs(payload []byte) (map[string]map[string]any, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%w: workflow logs are empty", ErrRunResultsUnavailable)
	}

	// Several strategies may each find a JSON object; the runner's result payload carries a `_run`
	// record, so a candidate with `_run` wins over any earlier stray JSON in the logs.
	var fallback map[string]map[string]any
	consider := func(results map[string]map[string]any, err error) bool {
		if err != nil {
			return false
		}
		if hasRunInfo(results) {
			fallback = results
			return true
		}
		if fallback == nil {
			fallback = results
		}
		return false
	}

	if consider(unmarshalRunResults(trimmed)) {
		return fallback, nil
	}
	normalized := bytes.TrimSpace(stripLogLinePrefixes(trimmed))
	if len(normalized) > 0 {
		if consider(unmarshalRunResults(normalized)) ||
			consider(extractBalancedJSONObject(normalized)) ||
			consider(extractJSONTail(normalized)) {
			return fallback, nil
		}
	}
	if consider(extractBalancedJSONObject(trimmed)) || consider(extractJSONTail(trimmed)) {
		return fallback, nil
	}
	if fallback != nil {
		return fallback, nil
	}

	return nil, fmt.Errorf("%w: no JSON result payload found in workflow logs", ErrRunResultsUnavailable)
}

func hasRunInfo(results map[string]map[string]any) bool {
	_, ok := results[workflowpkg.RunInfoStepName]
	return ok
}

func stripLogLinePrefixes(payload []byte) []byte {
	lines := strings.Split(string(payload), "\n")
	for idx, line := range lines {
		lines[idx] = stripLogPrefix(line)
	}
	return []byte(strings.Join(lines, "\n"))
}

func stripLogPrefix(line string) string {
	prefixEnd := strings.Index(line, ": ")
	if prefixEnd <= 0 {
		return line
	}

	prefix := line[:prefixEnd]
	if strings.Contains(prefix, " ") || strings.Contains(prefix, "\t") {
		return line
	}
	return line[prefixEnd+2:]
}

func extractBalancedJSONObject(payload []byte) (map[string]map[string]any, error) {
	var first map[string]map[string]any
	start := -1
	depth := 0
	inString := false
	escaped := false

	for idx, b := range payload {
		if start < 0 {
			if b == '{' {
				start = idx
				depth = 1
				inString = false
				escaped = false
			}
			continue
		}

		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch b {
		case '"':
			inString = true
		case '{':
			depth += 1
		case '}':
			depth -= 1
			if depth == 0 {
				candidate := bytes.TrimSpace(payload[start : idx+1])
				if results, err := unmarshalRunResults(candidate); err == nil {
					if hasRunInfo(results) {
						return results, nil
					}
					if first == nil {
						first = results
					}
				}
				start = -1
			}
		}
	}

	if first != nil {
		return first, nil
	}
	return nil, fmt.Errorf("no balanced JSON object found")
}

func extractJSONTail(payload []byte) (map[string]map[string]any, error) {
	var first map[string]map[string]any
	runKey := []byte(`"` + workflowpkg.RunInfoStepName + `"`)
	for idx := len(payload) - 1; idx >= 0; idx-- {
		if payload[idx] != '{' {
			continue
		}
		candidate := bytes.TrimSpace(payload[idx:])
		// Once a fallback exists, only keep scanning for candidates that could hold `_run`.
		if first != nil && !bytes.Contains(candidate, runKey) {
			continue
		}
		if results, err := unmarshalRunResults(candidate); err == nil {
			if hasRunInfo(results) {
				return results, nil
			}
			if first == nil {
				first = results
				if !bytes.Contains(payload[:idx], runKey) {
					break
				}
			}
		}
	}
	if first != nil {
		return first, nil
	}
	return nil, fmt.Errorf("no JSON tail found")
}

func unmarshalRunResults(payload []byte) (map[string]map[string]any, error) {
	var results map[string]map[string]any
	if err := json.Unmarshal(payload, &results); err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("result payload is empty")
	}
	return results, nil
}

func computeDurationSeconds(startedAt *time.Time, finishedAt *time.Time, now time.Time) float64 {
	if startedAt == nil {
		return 0
	}
	end := now
	if finishedAt != nil {
		end = *finishedAt
	}
	if end.Before(*startedAt) {
		return 0
	}
	return end.Sub(*startedAt).Seconds()
}

type hostedWorkflowManifest struct {
	APIVersion string                 `yaml:"apiVersion"`
	Kind       string                 `yaml:"kind"`
	Metadata   hostedWorkflowMetadata `yaml:"metadata"`
	Spec       hostedWorkflowSpec     `yaml:"spec"`
}

type hostedWorkflowMetadata struct {
	Name         string            `yaml:"name,omitempty"`
	GenerateName string            `yaml:"generateName,omitempty"`
	Namespace    string            `yaml:"namespace"`
	Labels       map[string]string `yaml:"labels,omitempty"`
	Annotations  map[string]string `yaml:"annotations,omitempty"`
}

type hostedWorkflowSpec struct {
	ServiceAccountName    string                   `yaml:"serviceAccountName"`
	Entrypoint            string                   `yaml:"entrypoint"`
	ActiveDeadlineSeconds int64                    `yaml:"activeDeadlineSeconds,omitempty"`
	Templates             []hostedWorkflowTemplate `yaml:"templates"`
}

type hostedWorkflowTemplate struct {
	Name      string                  `yaml:"name"`
	Container hostedWorkflowContainer `yaml:"container"`
}

type hostedWorkflowContainer struct {
	Image           string        `yaml:"image"`
	ImagePullPolicy string        `yaml:"imagePullPolicy"`
	Command         []string      `yaml:"command"`
	Args            []string      `yaml:"args"`
	Env             []argoEnvVar  `yaml:"env,omitempty"`
	Resources       *RunResources `yaml:"resources,omitempty"`
}

// remoteManifestSpec is everything one hosted submission needs; limits are already resolved.
type remoteManifestSpec struct {
	Namespace        string
	ServiceAccount   string
	Image            string
	WorkflowPath     string
	WorkflowSHA256   string
	SiteID           string
	DeadlineSeconds  int64
	Resources        RunResources
	DashboardVersion string
	SubmittedFrom    string
}

func generateRemoteWorkflowManifest(spec remoteManifestSpec) ([]byte, error) {
	labels := map[string]string{
		labelManagedBy: labelManagedByValue,
		labelWorkflow:  sanitizeLabelValue(workflowBaseName(spec.WorkflowPath)),
	}
	if site := sanitizeLabelValue(spec.SiteID); site != "" {
		labels[labelSite] = site
	}
	if sha := strings.ToLower(strings.TrimSpace(spec.WorkflowSHA256)); sha != "" {
		if len(sha) > 12 {
			sha = sha[:12]
		}
		labels[labelWorkflowSHA] = sha
	}
	annotations := map[string]string{
		annotationWorkflowPath: spec.WorkflowPath,
	}
	if spec.WorkflowSHA256 != "" {
		annotations[annotationWorkflowSHA256] = spec.WorkflowSHA256
	}
	if spec.DashboardVersion != "" {
		annotations[annotationVersion] = spec.DashboardVersion
	}
	if spec.SubmittedFrom != "" {
		annotations[annotationSubmittedFrom] = spec.SubmittedFrom
	}

	var resources *RunResources
	if len(spec.Resources.Requests) > 0 || len(spec.Resources.Limits) > 0 {
		copied := spec.Resources
		resources = &copied
	}

	manifest := hostedWorkflowManifest{
		APIVersion: "argoproj.io/v1alpha1",
		Kind:       "Workflow",
		Metadata: hostedWorkflowMetadata{
			GenerateName: generateRemoteWorkflowGenerateName(spec.WorkflowPath),
			Namespace:    spec.Namespace,
			Labels:       labels,
			Annotations:  annotations,
		},
		Spec: hostedWorkflowSpec{
			ServiceAccountName:    spec.ServiceAccount,
			Entrypoint:            "run-workflow",
			ActiveDeadlineSeconds: spec.DeadlineSeconds,
			Templates: []hostedWorkflowTemplate{{
				Name: "run-workflow",
				Container: hostedWorkflowContainer{
					Image:           spec.Image,
					ImagePullPolicy: "Always",
					Command:         []string{"/app/bin/cads-workflow-runner"},
					Args:            []string{"--json-output", "--workflow", spec.WorkflowPath},
					Env:             buildRemoteWorkflowEnvVars(defaultS3CredentialsSecret),
					Resources:       resources,
				},
			}},
		},
	}

	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(manifest); err != nil {
		return nil, fmt.Errorf("marshal remote workflow manifest: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("finalize remote workflow manifest: %w", err)
	}
	return buffer.Bytes(), nil
}

func buildRemoteWorkflowEnvVars(secretName string) []argoEnvVar {
	secretRef := func(name string, key string) argoEnvVar {
		return argoEnvVar{
			Name: name,
			ValueFrom: &argoValueFromRef{
				SecretKeyRef: &argoSecretKeyRef{
					Name: secretName,
					Key:  key,
				},
			},
		}
	}

	return []argoEnvVar{
		secretRef("AWS_ACCESS_KEY_ID", "access_key_id"),
		secretRef("AWS_SECRET_ACCESS_KEY", "secret_access_key"),
		secretRef("AWS_REGION", "region"),
		secretRef("AWS_DEFAULT_REGION", "region"),
		secretRef("S3_BUCKET", "bucket_name"),
		secretRef("S3_ENDPOINT", "endpoint"),
	}
}

const maxGenerateNameBaseLength = 40

func workflowBaseName(workflowPath string) string {
	base := path.Base(workflowPath)
	return strings.TrimSuffix(base, path.Ext(base))
}

// generateRemoteWorkflowGenerateName returns `cads-<base>-`; Argo appends a random suffix, so
// concurrent submissions of the same workflow never collide.
func generateRemoteWorkflowGenerateName(workflowPath string) string {
	base := sanitizeResourceName(workflowBaseName(workflowPath))
	if len(base) > maxGenerateNameBaseLength {
		base = strings.TrimRight(base[:maxGenerateNameBaseLength], ".-")
	}
	if base == "" {
		base = "workflow"
	}
	return "cads-" + base + "-"
}

var labelValueInvalid = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sanitizeLabelValue maps free text onto a Kubernetes label value (<= 63 chars, alphanumeric at
// both ends, [-_.A-Za-z0-9] in between). Empty input stays empty.
func sanitizeLabelValue(value string) string {
	value = labelValueInvalid.ReplaceAllString(strings.TrimSpace(value), "-")
	if len(value) > 63 {
		value = value[:63]
	}
	return strings.Trim(value, "-_.")
}

func sanitizeResourceName(value string) string {
	value = strings.ToLower(value)

	var builder strings.Builder
	for _, r := range value {
		if ('a' <= r && r <= 'z') || ('0' <= r && r <= '9') || r == '.' || r == '-' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('-')
	}

	sanitized := builder.String()
	sanitized = strings.TrimLeftFunc(sanitized, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	sanitized = strings.TrimRightFunc(sanitized, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if sanitized == "" {
		return "workflow"
	}
	return sanitized
}
