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
	"sort"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	defaultArgoServer          = "argoworkflows.cads.kzslab.dev"
	defaultArgoNamespace       = "playground"
	defaultArgoServiceAccount  = "playground-storhy-playground-pg-admin"
	defaultRemoteImage         = "ghcr.io/janlv/cads-fmi-demo:playground"
	defaultS3CredentialsSecret = "storhy-argo-artifacts-s3-credentials"
	defaultPollInterval        = 5 * time.Second
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
}

type ArgoConfig struct {
	ArgoServer     string
	Namespace      string
	ServiceAccount string
	Image          string
	Kubeconfig     string
	Token          string
}

type DashboardConfig struct {
	RemoteEnabled       bool     `json:"remoteEnabled"`
	ArgoServer          string   `json:"argoServer"`
	Namespace           string   `json:"namespace"`
	ServiceAccount      string   `json:"serviceAccount"`
	Image               string   `json:"image"`
	PollIntervalSeconds int      `json:"pollIntervalSeconds"`
	Problems            []string `json:"problems"`
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
}

type RunResults struct {
	RunName       string                    `json:"runName"`
	WorkflowPath  string                    `json:"workflowPath"`
	StepResults   map[string]map[string]any `json:"stepResults"`
	CollectedFrom string                    `json:"collectedFrom"`
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

func (c *ArgoRemoteClient) Config() DashboardConfig {
	return DashboardConfig{
		RemoteEnabled:       len(c.problems) == 0,
		ArgoServer:          c.config.ArgoServer,
		Namespace:           c.config.Namespace,
		ServiceAccount:      c.config.ServiceAccount,
		Image:               c.config.Image,
		PollIntervalSeconds: int(defaultPollInterval / time.Second),
		Problems:            append([]string(nil), c.problems...),
	}
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

	resourceName := generateRemoteWorkflowName(normalized, c.now())
	manifest, err := generateRemoteWorkflowManifest(resourceName, c.config.Namespace, c.config.ServiceAccount, c.config.Image, normalized)
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

func (c *ArgoRemoteClient) GetRunResults(ctx context.Context, name string) (*RunResults, error) {
	if err := c.ensureReady(); err != nil {
		return nil, err
	}

	run, err := c.GetRun(ctx, name)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(run.Phase, "Succeeded") {
		return nil, fmt.Errorf("%w: workflow phase is %s", ErrRunResultsUnavailable, run.Phase)
	}

	output, err := c.runArgo(ctx,
		c.withArgoConnectionArgs("logs", name, "--tail", "2000")...,
	)
	if err != nil {
		return nil, err
	}

	results, err := extractRunResultsFromLogs(output)
	if err != nil {
		return nil, err
	}

	return &RunResults{
		RunName:       run.Name,
		WorkflowPath:  run.WorkflowPath,
		StepResults:   results,
		CollectedFrom: "argo logs",
	}, nil
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
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	CreationTimestamp string `json:"creationTimestamp"`
}

type argoSpec struct {
	ServiceAccountName string         `json:"serviceAccountName"`
	Templates          []argoTemplate `json:"templates"`
}

type argoTemplate struct {
	Name      string         `json:"name"`
	Container *argoContainer `json:"container"`
}

type argoContainer struct {
	Image   string       `json:"image"`
	Command []string     `json:"command"`
	Args    []string     `json:"args"`
	Env     []argoEnvVar `json:"env,omitempty"`
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
	workflowPath, image := extractWorkflowInvocation(envelope.Spec.Templates)
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

	return &RunSummary{
		Name:            envelope.Metadata.Name,
		WorkflowPath:    normalizedPath,
		Phase:           envelope.Status.Phase,
		CreatedAt:       createdAt,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		DurationSeconds: computeDurationSeconds(startedAt, finishedAt, now),
		Progress:        envelope.Status.Progress,
		Message:         envelope.Status.Message,
		Image:           image,
		ServiceAccount:  envelope.Spec.ServiceAccountName,
	}, nil
}

func extractWorkflowInvocation(templates []argoTemplate) (string, string) {
	for _, template := range templates {
		if template.Container == nil {
			continue
		}
		workflowPath := extractWorkflowArgument(template.Container.Args)
		if workflowPath == "" {
			workflowPath = extractWorkflowArgument(template.Container.Command)
		}
		if workflowPath == "" {
			continue
		}
		return workflowPath, template.Container.Image
	}
	return "", ""
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

	if results, err := unmarshalRunResults(trimmed); err == nil {
		return results, nil
	}

	normalized := bytes.TrimSpace(stripLogLinePrefixes(trimmed))
	if len(normalized) > 0 {
		if results, err := unmarshalRunResults(normalized); err == nil {
			return results, nil
		}
		if results, err := extractBalancedJSONObject(normalized); err == nil {
			return results, nil
		}
		if results, err := extractJSONTail(normalized); err == nil {
			return results, nil
		}
	}

	if results, err := extractBalancedJSONObject(trimmed); err == nil {
		return results, nil
	}
	if results, err := extractJSONTail(trimmed); err == nil {
		return results, nil
	}

	return nil, fmt.Errorf("%w: no JSON result payload found in workflow logs", ErrRunResultsUnavailable)
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
					return results, nil
				}
				start = -1
			}
		}
	}

	return nil, fmt.Errorf("no balanced JSON object found")
}

func extractJSONTail(payload []byte) (map[string]map[string]any, error) {
	for idx := len(payload) - 1; idx >= 0; idx-- {
		if payload[idx] != '{' {
			continue
		}
		if results, err := unmarshalRunResults(bytes.TrimSpace(payload[idx:])); err == nil {
			return results, nil
		}
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
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		ServiceAccountName string `yaml:"serviceAccountName"`
		Entrypoint         string `yaml:"entrypoint"`
		Templates          []struct {
			Name      string `yaml:"name"`
			Container struct {
				Image           string       `yaml:"image"`
				ImagePullPolicy string       `yaml:"imagePullPolicy"`
				Command         []string     `yaml:"command"`
				Args            []string     `yaml:"args"`
				Env             []argoEnvVar `yaml:"env,omitempty"`
			} `yaml:"container"`
		} `yaml:"templates"`
	} `yaml:"spec"`
}

func generateRemoteWorkflowManifest(name string, namespace string, serviceAccount string, image string, workflowPath string) ([]byte, error) {
	manifest := hostedWorkflowManifest{
		APIVersion: "argoproj.io/v1alpha1",
		Kind:       "Workflow",
	}
	manifest.Metadata.Name = name
	manifest.Metadata.Namespace = namespace
	manifest.Spec.ServiceAccountName = serviceAccount
	manifest.Spec.Entrypoint = "run-workflow"

	var template struct {
		Name      string `yaml:"name"`
		Container struct {
			Image           string       `yaml:"image"`
			ImagePullPolicy string       `yaml:"imagePullPolicy"`
			Command         []string     `yaml:"command"`
			Args            []string     `yaml:"args"`
			Env             []argoEnvVar `yaml:"env,omitempty"`
		} `yaml:"container"`
	}
	template.Name = "run-workflow"
	template.Container.Image = image
	template.Container.ImagePullPolicy = "Always"
	template.Container.Command = []string{"/app/bin/cads-workflow-runner"}
	template.Container.Args = []string{"--json-output", "--workflow", workflowPath}
	template.Container.Env = buildRemoteWorkflowEnvVars(defaultS3CredentialsSecret)
	manifest.Spec.Templates = append(manifest.Spec.Templates, template)

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

func generateRemoteWorkflowName(workflowPath string, now time.Time) string {
	base := path.Base(workflowPath)
	base = strings.TrimSuffix(base, path.Ext(base))
	return fmt.Sprintf("cads-%s-%s", sanitizeResourceName(base), now.UTC().Format("20060102150405"))
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
