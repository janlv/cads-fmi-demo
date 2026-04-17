package service

import (
	"context"
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
	            "image": "ghcr.io/janlv/cads-fmi-demo:latest",
	            "args": ["--workflow", "workflows/python_chain.yaml"]
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
	            "image": "ghcr.io/janlv/cads-fmi-demo:latest",
	            "args": ["--workflow=workflows/calculate_aecis.yaml"]
	          }
	        }
	      ]
	    },
	    "status": {
	      "phase": "Running",
	      "startedAt": "2026-04-16T16:48:05Z",
	      "progress": "0/1"
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
	if runs[0].WorkflowPath != "workflows/calculate_aecis.yaml" || runs[0].Phase != "Running" {
		t.Fatalf("runs[0] = %+v, want running calculate_aecis", runs[0])
	}
	if runs[0].DurationSeconds != 115 {
		t.Fatalf("running duration = %v, want 115", runs[0].DurationSeconds)
	}
	if runs[1].WorkflowPath != "workflows/python_chain.yaml" || runs[1].DurationSeconds != 30 {
		t.Fatalf("runs[1] = %+v, want succeeded python_chain with 30s duration", runs[1])
	}
}

func TestExtractRunResultsFromLogsParsesMixedRunnerOutput(t *testing.T) {
	logs := []byte(`[workflow] Running workflows/calculate_aecis.yaml
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

func TestArgoRemoteClientSubmitWorkflowBuildsConfiguredManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatalf("create workflows dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "fmu"), 0o755); err != nil {
		t.Fatalf("create fmu dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "python_chain.yaml"), []byte("steps:\n  - name: producer\n"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

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
		if container.Image != "ghcr.io/example/cads:test" || len(container.Args) != 3 || container.Args[0] != "--json-output" || container.Args[2] != "workflows/python_chain.yaml" {
			t.Fatalf("container = %+v, want configured image and workflow path", container)
		}
		if !strings.HasPrefix(manifest.Metadata.Name, "cads-python-chain-20260416170000") {
			t.Fatalf("manifest name = %q, want timestamped workflow name", manifest.Metadata.Name)
		}

		return []byte(`{
		  "metadata": {
		    "name": "cads-python-chain-20260416170000",
		    "creationTimestamp": "2026-04-16T17:00:00Z"
		  },
		  "spec": {
		    "serviceAccountName": "playground-admin",
		    "templates": [
		      {
		        "name": "run-workflow",
		        "container": {
		          "image": "ghcr.io/example/cads:test",
		          "args": ["--workflow", "workflows/python_chain.yaml"]
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

	run, err := client.SubmitWorkflow(context.Background(), "workflows/python_chain.yaml")
	if err != nil {
		t.Fatalf("SubmitWorkflow() error = %v", err)
	}
	if run.Name != "cads-python-chain-20260416170000" || run.WorkflowPath != "workflows/python_chain.yaml" {
		t.Fatalf("run = %+v, want normalized submitted run", run)
	}
}
