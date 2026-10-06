package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseArgoWorkflowListFiltersAndNormalizesRepoRuns(t *testing.T) {
	now := time.Date(2026, 4, 16, 16, 50, 0, 0, time.UTC)
	payload := []byte(`[
	  {
	    "metadata": {
	      "name": "cads-python-chain-20260416164333",
	      "creationTimestamp": "2026-04-16T16:43:34Z"
	    },
	    "spec": {
	      "serviceAccountName": "playground-admin",
	      "templates": [
	        {
	          "name": "run-workflow",
	          "container": {
	            "image": "ghcr.io/janlv/cads-fmi-demo:playground",
	            "args": ["--workflow", "workflows/tests/python_chain.yaml"]
	          }
	        }
	      ]
	    },
	    "status": {
	      "phase": "Succeeded",
	      "startedAt": "2026-04-16T16:43:40Z",
	      "finishedAt": "2026-04-16T16:44:10Z",
	      "progress": "1/1"
	    }
	  },
	  {
	    "metadata": {
	      "name": "cads-calculate-aecis-20260416164800",
	      "creationTimestamp": "2026-04-16T16:48:00Z"
	    },
	    "spec": {
	      "serviceAccountName": "playground-admin",
	      "templates": [
	        {
	          "name": "run-workflow",
	          "container": {
	            "image": "ghcr.io/janlv/cads-fmi-demo:playground",
	            "args": ["--workflow=workflows/tests/calculate_aecis.yaml"]
	          }
	        }
	      ]
	    },
	    "status": {
	      "phase": "Running",
	      "startedAt": "2026-04-16T16:48:05Z",
	      "progress": "0/1",
	      "nodes": {
	        "cads-calculate-aecis-20260416164800": {
	          "phase": "Pending",
	          "message": "ImagePullBackOff: failed to pull image"
	        }
	      }
	    }
	  },
	  {
	    "metadata": {
	      "name": "hello-world",
	      "creationTimestamp": "2026-04-16T16:30:00Z"
	    },
	    "spec": {
	      "templates": [
	        {
	          "name": "run-workflow",
	          "container": {
	            "image": "argoproj/argosay:v2",
	            "args": ["echo", "hello"]
	          }
	        }
	      ]
	    },
	    "status": {
	      "phase": "Succeeded",
	      "startedAt": "2026-04-16T16:30:02Z",
	      "finishedAt": "2026-04-16T16:30:05Z",
	      "progress": "1/1"
	    }
	  }
	]`)

	runs, err := parseArgoWorkflowList(".", payload, now)
	if err != nil {
		t.Fatalf("parseArgoWorkflowList() error = %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2 repo runs", len(runs))
	}
	if runs[0].WorkflowPath != "workflows/tests/calculate_aecis.yaml" || runs[0].Phase != "Running" {
		t.Fatalf("runs[0] = %+v, want running calculate_aecis", runs[0])
	}
	if runs[0].DurationSeconds != 115 {
		t.Fatalf("running duration = %v, want 115", runs[0].DurationSeconds)
	}
	if runs[0].Message != "Pending: ImagePullBackOff: failed to pull image" {
		t.Fatalf("runs[0].Message = %q, want node status message", runs[0].Message)
	}
	if runs[1].WorkflowPath != "workflows/tests/python_chain.yaml" || runs[1].DurationSeconds != 30 {
		t.Fatalf("runs[1] = %+v, want succeeded python_chain with 30s duration", runs[1])
	}
}

func TestExtractRunResultsFromLogsParsesMixedRunnerOutput(t *testing.T) {
	logs := []byte(`[workflow] Running workflows/tests/calculate_aecis.yaml
[workflow] Completed all steps.
{
  "calculate_aecis": {
    "CIvector": [2.46, 2.47, 0.19, -0.1, 2.7],
    "time": 10
  }
}
`)

	results, err := extractRunResultsFromLogs(logs)
	if err != nil {
		t.Fatalf("extractRunResultsFromLogs() error = %v", err)
	}

	step := results["calculate_aecis"]
	if step == nil {
		t.Fatalf("results = %+v, want calculate_aecis step", results)
	}
	vector, ok := step["CIvector"].([]any)
	if !ok || len(vector) != 5 {
		t.Fatalf("CIvector = %#v, want five values", step["CIvector"])
	}
	if got := step["time"]; got != float64(10) {
		t.Fatalf("time = %#v, want 10", got)
	}
}

func TestExtractRunResultsFromLogsParsesPrefixedArgoLogs(t *testing.T) {
	logs := []byte(`cads-calculate-aecis-20260417105312: [INFO][FMILIB] XML specifies FMI standard version 3.0
cads-calculate-aecis-20260417105312: {"calculate_aecis":{"CIvector":0,"time":10}}
cads-calculate-aecis-20260417105312: time="2026-04-17T10:53:17.517Z" level=info msg="sub-process exited" argo=true error="<nil>"
`)

	results, err := extractRunResultsFromLogs(logs)
	if err != nil {
		t.Fatalf("extractRunResultsFromLogs() error = %v", err)
	}

	step := results["calculate_aecis"]
	if step == nil {
		t.Fatalf("results = %+v, want calculate_aecis step", results)
	}
	if got := step["CIvector"]; got != float64(0) {
		t.Fatalf("CIvector = %#v, want 0", got)
	}
	if got := step["time"]; got != float64(10) {
		t.Fatalf("time = %#v, want 10", got)
	}
}

func TestExtractRunResultsFromLogsParsesNestedTracePayload(t *testing.T) {
	logs := []byte(`cads-calculate-aecis-20260421073538: [INFO][FMILIB] XML specifies FMI standard version 3.0
cads-calculate-aecis-20260421073538: {"calculate_aecis":{"CIvector":[0,0,0,0,0],"time":30,"trace":{"signals":{"CIvector":[[2.48,2.48,0,null,null],[0,0,0,0,0]],"rawsig":[2.48,2.51],"time_1":[0,0.05]},"time":[0,0.05]}}}
cads-calculate-aecis-20260421073538: time="2026-04-21T07:36:03.545Z" level=info msg="sub-process exited" argo=true error="<nil>"
`)

	results, err := extractRunResultsFromLogs(logs)
	if err != nil {
		t.Fatalf("extractRunResultsFromLogs() error = %v", err)
	}

	step := results["calculate_aecis"]
	if step == nil {
		t.Fatalf("results = %+v, want calculate_aecis step", results)
	}
	trace, ok := step["trace"].(map[string]any)
	if !ok {
		t.Fatalf("trace = %#v, want nested trace object", step["trace"])
	}
	signals, ok := trace["signals"].(map[string]any)
	if !ok {
		t.Fatalf("signals = %#v, want nested signals map", trace["signals"])
	}
	rawsig, ok := signals["rawsig"].([]any)
	if !ok || len(rawsig) != 2 {
		t.Fatalf("rawsig = %#v, want two traced raw samples", signals["rawsig"])
	}
}

func TestArgoRemoteClientSubmitWorkflowBuildsConfiguredManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatalf("create workflows dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "fmu"), 0o755); err != nil {
		t.Fatalf("create fmu dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "workflows", "tests"), 0o755); err != nil {
		t.Fatalf("create test workflows dir: %v", err)
	}
	workflowYAML := []byte("metadata:\n  site_id: La Rance\nsteps:\n  - name: producer\n")
	if err := os.WriteFile(filepath.Join(root, "workflows", "tests", "python_chain.yaml"), workflowYAML, 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	sum := sha256.Sum256(workflowYAML)
	wantSHA := hex.EncodeToString(sum[:])

	client := NewArgoRemoteClient(root, ArgoOptionInputs{
		ArgoServer:     "argoworkflows.cads.kzslab.dev",
		Namespace:      "playground",
		ServiceAccount: "playground-admin",
		Image:          "ghcr.io/example/cads:test",
	}, func(key string) string {
		if key == "ARGO_TOKEN" {
			return "submit-token"
		}
		return ""
	})
	client.argoCmd = "argo"
	client.problems = nil
	client.now = func() time.Time {
		return time.Date(2026, 4, 16, 17, 0, 0, 0, time.UTC)
	}
	client.hostname = func() (string, error) { return "dev-laptop", nil }
	client.exec = func(_ context.Context, command string, args ...string) ([]byte, error) {
		if command != "argo" {
			t.Fatalf("command = %q, want argo", command)
		}
		if len(args) < 2 || args[0] != "submit" {
			t.Fatalf("args = %v, want submit invocation", args)
		}

		manifestData, err := os.ReadFile(args[1])
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}

		var manifest hostedWorkflowManifest
		if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
			t.Fatalf("unmarshal manifest: %v", err)
		}

		if manifest.Metadata.Namespace != "playground" || manifest.Spec.ServiceAccountName != "playground-admin" {
			t.Fatalf("manifest = %+v, want configured namespace and service account", manifest)
		}
		container := manifest.Spec.Templates[0].Container
		if container.Image != "ghcr.io/example/cads:test" || len(container.Args) != 3 || container.Args[0] != "--json-output" || container.Args[2] != "workflows/tests/python_chain.yaml" {
			t.Fatalf("container = %+v, want configured image and workflow path", container)
		}
		envByName := make(map[string]argoEnvVar, len(container.Env))
		for _, env := range container.Env {
			envByName[env.Name] = env
		}
		if envByName["AWS_ACCESS_KEY_ID"].ValueFrom == nil || envByName["AWS_ACCESS_KEY_ID"].ValueFrom.SecretKeyRef == nil {
			t.Fatalf("container.Env = %+v, want AWS_ACCESS_KEY_ID secret ref", container.Env)
		}
		if envByName["AWS_ACCESS_KEY_ID"].ValueFrom.SecretKeyRef.Name != defaultS3CredentialsSecret || envByName["AWS_ACCESS_KEY_ID"].ValueFrom.SecretKeyRef.Key != "access_key_id" {
			t.Fatalf("AWS_ACCESS_KEY_ID env = %+v, want default S3 secret mapping", envByName["AWS_ACCESS_KEY_ID"])
		}
		if envByName["S3_BUCKET"].ValueFrom == nil || envByName["S3_BUCKET"].ValueFrom.SecretKeyRef == nil || envByName["S3_BUCKET"].ValueFrom.SecretKeyRef.Key != "bucket_name" {
			t.Fatalf("S3_BUCKET env = %+v, want bucket_name secret ref", envByName["S3_BUCKET"])
		}
		if envByName["S3_ENDPOINT"].ValueFrom == nil || envByName["S3_ENDPOINT"].ValueFrom.SecretKeyRef == nil || envByName["S3_ENDPOINT"].ValueFrom.SecretKeyRef.Key != "endpoint" {
			t.Fatalf("S3_ENDPOINT env = %+v, want endpoint secret ref", envByName["S3_ENDPOINT"])
		}
		if manifest.Metadata.Name != "" || manifest.Metadata.GenerateName != "cads-python-chain-" {
			t.Fatalf("manifest metadata = %+v, want generateName cads-python-chain- and no name", manifest.Metadata)
		}
		if manifest.Spec.ActiveDeadlineSeconds != defaultMaxRuntimeSeconds {
			t.Fatalf("activeDeadlineSeconds = %d, want default %d", manifest.Spec.ActiveDeadlineSeconds, defaultMaxRuntimeSeconds)
		}
		if container.Resources == nil ||
			container.Resources.Requests["cpu"] != "250m" || container.Resources.Requests["memory"] != "256Mi" ||
			container.Resources.Limits["cpu"] != "1" || container.Resources.Limits["memory"] != "1Gi" {
			t.Fatalf("container.Resources = %+v, want default requests/limits", container.Resources)
		}
		labels := manifest.Metadata.Labels
		if labels[labelManagedBy] != "cads-dashboard" || labels[labelWorkflow] != "python_chain" ||
			labels[labelSite] != "La-Rance" || labels[labelWorkflowSHA] != wantSHA[:12] {
			t.Fatalf("labels = %+v, want provenance labels", labels)
		}
		annotations := manifest.Metadata.Annotations
		if annotations[annotationWorkflowPath] != "workflows/tests/python_chain.yaml" ||
			annotations[annotationWorkflowSHA256] != wantSHA ||
			annotations[annotationVersion] == "" ||
			annotations[annotationSubmittedFrom] != "dev-laptop" {
			t.Fatalf("annotations = %+v, want provenance annotations", annotations)
		}

		return []byte(`{
		  "metadata": {
		    "name": "cads-python-chain-x7k2p",
		    "generateName": "cads-python-chain-",
		    "creationTimestamp": "2026-04-16T17:00:00Z"
		  },
		  "spec": {
		    "serviceAccountName": "playground-admin",
		    "templates": [
		      {
		        "name": "run-workflow",
		        "container": {
		          "image": "ghcr.io/example/cads:test",
		          "args": ["--workflow", "workflows/tests/python_chain.yaml"]
		        }
		      }
		    ]
		  },
		  "status": {
		    "phase": "Running",
		    "startedAt": "2026-04-16T17:00:01Z",
		    "progress": "0/1"
		  }
		}`), nil
	}

	run, err := client.SubmitWorkflow(context.Background(), "workflows/tests/python_chain.yaml")
	if err != nil {
		t.Fatalf("SubmitWorkflow() error = %v", err)
	}
	if run.Name != "cads-python-chain-x7k2p" || run.WorkflowPath != "workflows/tests/python_chain.yaml" {
		t.Fatalf("run = %+v, want normalized submitted run", run)
	}
}

func TestArgoRemoteClientListRunsUsesKubeconfigWhenAvailable(t *testing.T) {
	client := NewArgoRemoteClient(t.TempDir(), ArgoOptionInputs{
		ArgoServer: "argoworkflows.cads.kzslab.dev",
		Namespace:  "playground",
		Kubeconfig: "/tmp/kubeconfig",
	}, func(key string) string {
		if key == "ARGO_TOKEN" {
			return "env-token"
		}
		return ""
	})
	client.argoCmd = "argo"
	client.problems = nil
	client.now = func() time.Time {
		return time.Date(2026, 4, 16, 17, 0, 0, 0, time.UTC)
	}
	client.exec = func(_ context.Context, command string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--kubeconfig /tmp/kubeconfig") {
			t.Fatalf("args = %v, want kubeconfig auth", args)
		}
		if strings.Contains(joined, "--token") || strings.Contains(joined, "env-token") {
			t.Fatalf("args = %v, did not want token auth when kubeconfig is available", args)
		}
		return []byte(`[]`), nil
	}

	if _, err := client.ListRuns(context.Background(), 20); err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
}

func TestArgoRemoteClientRunArgoRedactsTokenFromErrors(t *testing.T) {
	client := &ArgoRemoteClient{
		argoCmd: "argo",
		config:  ArgoConfig{Token: "super-secret-token"},
		exec: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return nil, errors.New("command failed with super-secret-token")
		},
	}

	_, err := client.runArgo(context.Background(), "list", "--token", "super-secret-token")
	if err == nil {
		t.Fatal("runArgo() error = nil, want error")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Fatalf("runArgo() error leaked token: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("runArgo() error = %v, want redaction marker", err)
	}
}

func TestResolveRunLimitsUsesWorkflowLimitsAndCeiling(t *testing.T) {
	cfg := ArgoConfig{
		MaxRuntimeSeconds:        900,
		MaxRuntimeCeilingSeconds: 3600,
		DefaultResources: RunResources{
			Requests: map[string]string{"cpu": "250m", "memory": "256Mi"},
			Limits:   map[string]string{"cpu": "1", "memory": "1Gi"},
		},
	}

	deadline, resources := resolveRunLimits(cfg, nil)
	if deadline != 900 || resources.Requests["cpu"] != "250m" || resources.Limits["memory"] != "1Gi" {
		t.Fatalf("defaults = %d %+v, want 900 and default resources", deadline, resources)
	}

	deadline, resources = resolveRunLimits(cfg, &WorkflowLimits{MaxRuntimeSeconds: 300, CPU: "500m", Memory: "512Mi"})
	if deadline != 300 {
		t.Fatalf("deadline = %d, want per-workflow 300", deadline)
	}
	if resources.Requests["cpu"] != "500m" || resources.Limits["cpu"] != "500m" ||
		resources.Requests["memory"] != "512Mi" || resources.Limits["memory"] != "512Mi" {
		t.Fatalf("resources = %+v, want per-workflow cpu/memory as request and limit", resources)
	}
	if cfg.DefaultResources.Requests["cpu"] != "250m" {
		t.Fatalf("resolveRunLimits mutated the config defaults: %+v", cfg.DefaultResources)
	}

	deadline, _ = resolveRunLimits(cfg, &WorkflowLimits{MaxRuntimeSeconds: 7200})
	if deadline != 3600 {
		t.Fatalf("deadline = %d, want clamped to ceiling 3600", deadline)
	}
}

func TestSubmitWorkflowAppliesPerWorkflowLimits(t *testing.T) {
	root := writeDashboardRepoFixture(t)
	if err := os.WriteFile(filepath.Join(root, "workflows", "tests", "limited.yaml"), []byte(`
metadata:
  limits: {max_runtime_seconds: 120, cpu: "2", memory: 2Gi}
steps:
  - name: producer
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "tests", "bad_limits.yaml"), []byte(`
metadata:
  limits: {cpu: lots}
steps:
  - name: producer
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	client := NewArgoRemoteClient(root, ArgoOptionInputs{}, func(key string) string {
		if key == "ARGO_TOKEN" {
			return "token"
		}
		return ""
	})
	client.argoCmd = "argo"
	client.problems = nil
	var manifest hostedWorkflowManifest
	client.exec = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		data, err := os.ReadFile(args[1])
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			t.Fatalf("unmarshal manifest: %v", err)
		}
		return []byte(`{"metadata":{"name":"cads-limited-abcde","creationTimestamp":"2026-04-16T17:00:00Z"},
		  "spec":{"activeDeadlineSeconds":120,"templates":[{"name":"run-workflow","container":{"args":["--workflow","workflows/tests/limited.yaml"]}}]},
		  "status":{"phase":"Pending"}}`), nil
	}

	run, err := client.SubmitWorkflow(context.Background(), "workflows/tests/limited.yaml")
	if err != nil {
		t.Fatalf("SubmitWorkflow() error = %v", err)
	}
	if manifest.Spec.ActiveDeadlineSeconds != 120 {
		t.Fatalf("activeDeadlineSeconds = %d, want 120", manifest.Spec.ActiveDeadlineSeconds)
	}
	res := manifest.Spec.Templates[0].Container.Resources
	if res == nil || res.Requests["cpu"] != "2" || res.Limits["memory"] != "2Gi" || res.Requests["memory"] != "2Gi" {
		t.Fatalf("resources = %+v, want per-workflow cpu 2 / memory 2Gi", res)
	}
	if run.DeadlineSeconds != 120 {
		t.Fatalf("run.DeadlineSeconds = %d, want 120", run.DeadlineSeconds)
	}

	if _, err := client.SubmitWorkflow(context.Background(), "workflows/tests/bad_limits.yaml"); err == nil || !strings.Contains(err.Error(), "limits.cpu") {
		t.Fatalf("SubmitWorkflow(bad limits) error = %v, want limits.cpu rejection", err)
	}
}

func TestNormalizeArgoWorkflowReadsProvenance(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	payload := []byte(`{
	  "metadata": {
	    "name": "cads-cleaning-interval-q8z4m",
	    "creationTimestamp": "2026-10-06T11:00:00Z",
	    "labels": {
	      "app.kubernetes.io/managed-by": "cads-dashboard",
	      "cads.norceresearch.no/workflow": "cleaning_interval",
	      "cads.norceresearch.no/site": "la_rance",
	      "cads.norceresearch.no/workflow-sha": "0123456789ab"
	    },
	    "annotations": {
	      "cads.norceresearch.no/workflow-path": "workflows/demonstrators/la_rance/maintenance/cleaning_interval.yaml",
	      "cads.norceresearch.no/workflow-sha256": "0123456789abcdef",
	      "cads.norceresearch.no/dashboard-version": "v1.2.3-dirty",
	      "cads.norceresearch.no/submitted-from": "dev-laptop"
	    }
	  },
	  "spec": {
	    "activeDeadlineSeconds": 300,
	    "templates": [{
	      "name": "run-workflow",
	      "container": {
	        "image": "ghcr.io/example/cads:test",
	        "args": ["--json-output", "--workflow", "workflows/demonstrators/la_rance/maintenance/cleaning_interval.yaml"],
	        "resources": {"requests": {"cpu": "250m", "memory": "256Mi"}, "limits": {"cpu": "1", "memory": "1Gi"}}
	      }
	    }]
	  },
	  "status": {
	    "phase": "Failed",
	    "startedAt": "2026-10-06T11:00:01Z",
	    "finishedAt": "2026-10-06T11:05:01Z",
	    "message": "Step exceeded its deadline"
	  }
	}`)

	run, err := parseArgoWorkflow(".", payload, now)
	if err != nil || run == nil {
		t.Fatalf("parseArgoWorkflow() = %+v, %v", run, err)
	}
	if run.DeadlineSeconds != 300 || !run.DeadlineExceeded {
		t.Fatalf("deadline = %d exceeded=%v, want 300 and exceeded", run.DeadlineSeconds, run.DeadlineExceeded)
	}
	if run.Resources == nil || run.Resources.Requests["cpu"] != "250m" || run.Resources.Limits["memory"] != "1Gi" {
		t.Fatalf("resources = %+v, want container resources", run.Resources)
	}
	if run.WorkflowSHA256 != "0123456789abcdef" || run.DashboardVersion != "v1.2.3-dirty" || run.SubmittedFrom != "dev-laptop" {
		t.Fatalf("provenance = %+v, want annotations", run)
	}
	if run.Labels[labelSite] != "la_rance" || run.Labels[labelWorkflowSHA] != "0123456789ab" {
		t.Fatalf("labels = %+v, want provenance labels", run.Labels)
	}

	// A succeeded run whose message mentions a deadline is not a deadline kill.
	succeeded := strings.Replace(string(payload), `"phase": "Failed"`, `"phase": "Succeeded"`, 1)
	run, _ = parseArgoWorkflow(".", []byte(succeeded), now)
	if run == nil || run.DeadlineExceeded {
		t.Fatalf("succeeded run = %+v, want deadlineExceeded false", run)
	}

	// Without recognisable container args the workflow-path annotation identifies the run.
	wrapped := strings.Replace(string(payload), `"args": ["--json-output", "--workflow", "workflows/demonstrators/la_rance/maintenance/cleaning_interval.yaml"],`, `"args": ["run.sh"],`, 1)
	run, err = parseArgoWorkflow(".", []byte(wrapped), now)
	if err != nil || run == nil || run.WorkflowPath != "workflows/demonstrators/la_rance/maintenance/cleaning_interval.yaml" {
		t.Fatalf("annotation fallback run = %+v, %v; want workflow path from annotation", run, err)
	}
}

func newResultsTestClient(t *testing.T, phase string, message string, logs string, logsErr error) *ArgoRemoteClient {
	t.Helper()
	client := &ArgoRemoteClient{
		workDir: ".",
		argoCmd: "argo",
		config:  ArgoConfig{Namespace: "playground", ArgoServer: "argo", Token: "token"},
		now:     func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) },
	}
	client.exec = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "get":
			return []byte(`{"metadata":{"name":"cads-run-abc12","creationTimestamp":"2026-10-06T11:00:00Z"},
			  "spec":{"templates":[{"name":"run-workflow","container":{"args":["--workflow","workflows/tests/cosim_fail.yaml"]}}]},
			  "status":{"phase":"` + phase + `","message":"` + message + `"}}`), nil
		case "logs":
			if logsErr != nil {
				return nil, logsErr
			}
			return []byte(logs), nil
		}
		t.Fatalf("unexpected argo call %v", args)
		return nil, nil
	}
	return client
}

func TestGetRunResultsFailedRunWithRunInfo(t *testing.T) {
	logs := `cads-run-abc12: [workflow] Running workflows/tests/cosim_fail.yaml
cads-run-abc12: {"first":{"x":1},"_run":{"status":"failed","error":"variable ems.missing not found","failed_step":"coupled","steps":[]}}
cads-run-abc12: time="2026-10-06T11:00:05Z" level=info msg="sub-process exited" argo=true error="exit status 1"
`
	client := newResultsTestClient(t, "Failed", "Error (exit code 1)", logs, nil)
	results, err := client.GetRunResults(context.Background(), "cads-run-abc12")
	if err != nil {
		t.Fatalf("GetRunResults() error = %v", err)
	}
	if results.Phase != "Failed" || results.Status != "failed" || !results.Partial ||
		results.Error != "variable ems.missing not found" || results.FailedStep != "coupled" {
		t.Fatalf("results = %+v, want failed partial results from _run", results)
	}
	if results.StepResults["first"]["x"] != float64(1) {
		t.Fatalf("StepResults = %+v, want partial step values", results.StepResults)
	}
}

func TestGetRunResultsFailedRunWithoutLogs(t *testing.T) {
	for name, tc := range map[string]struct {
		logs string
		err  error
	}{
		"no json":     {logs: "cads-run-abc12: killed\n"},
		"logs failed": {err: errors.New("pod deleted")},
	} {
		t.Run(name, func(t *testing.T) {
			client := newResultsTestClient(t, "Failed", "Step exceeded its deadline", tc.logs, tc.err)
			results, err := client.GetRunResults(context.Background(), "cads-run-abc12")
			if err != nil {
				t.Fatalf("GetRunResults() error = %v", err)
			}
			if !results.Partial || results.Error != "Step exceeded its deadline" || results.Status != "failed" ||
				results.StepResults == nil || len(results.StepResults) != 0 {
				t.Fatalf("results = %+v, want empty partial result with run message", results)
			}
		})
	}
}

func TestGetRunResultsRunningIsUnavailable(t *testing.T) {
	client := newResultsTestClient(t, "Running", "", "", nil)
	if _, err := client.GetRunResults(context.Background(), "cads-run-abc12"); !errors.Is(err, ErrRunResultsUnavailable) {
		t.Fatalf("GetRunResults() error = %v, want ErrRunResultsUnavailable", err)
	}
	for _, phase := range []string{"Succeeded", "failed", "Error"} {
		if !resultsPhaseAllowed(phase) {
			t.Fatalf("resultsPhaseAllowed(%q) = false", phase)
		}
	}
	for _, phase := range []string{"Running", "Pending", ""} {
		if resultsPhaseAllowed(phase) {
			t.Fatalf("resultsPhaseAllowed(%q) = true", phase)
		}
	}
}

func TestGetRunResultsSucceededStatusFromRunInfo(t *testing.T) {
	client := newResultsTestClient(t, "Succeeded", "", `{"step":{"y":2},"_run":{"status":"succeeded"}}`, nil)
	results, err := client.GetRunResults(context.Background(), "cads-run-abc12")
	if err != nil {
		t.Fatalf("GetRunResults() error = %v", err)
	}
	if results.Status != "succeeded" || results.Partial || results.Error != "" {
		t.Fatalf("results = %+v, want succeeded non-partial", results)
	}
}

func TestExtractRunResultsPrefersRunInfoCandidate(t *testing.T) {
	logs := []byte(`pod: {"stray":{"debug":1}}
pod: [workflow] Running
pod: {"real":{"value":3},"_run":{"status":"succeeded"}}
pod: {"later_stray":{"debug":2}}
`)
	results, err := extractRunResultsFromLogs(logs)
	if err != nil {
		t.Fatalf("extractRunResultsFromLogs() error = %v", err)
	}
	if _, ok := results["_run"]; !ok || results["real"]["value"] != float64(3) {
		t.Fatalf("results = %+v, want candidate with _run", results)
	}

	balanced, err := extractBalancedJSONObject([]byte(`{"a":{"x":1}} noise {"b":{"y":2},"_run":{"status":"failed"}}`))
	if err != nil || balanced["_run"] == nil {
		t.Fatalf("extractBalancedJSONObject() = %+v, %v; want _run candidate", balanced, err)
	}
	tail, err := extractJSONTail([]byte(`noise {"a":{"x":1}} {"b":{"y":2},"_run":{"status":"failed"}}`))
	if err != nil || tail["_run"] == nil {
		t.Fatalf("extractJSONTail() = %+v, %v; want _run candidate", tail, err)
	}
}
