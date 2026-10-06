package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveArgoConfigFlagsOverrideEnv(t *testing.T) {
	cfg, problems := ResolveArgoConfig(ArgoOptionInputs{
		ArgoServer:     "flag-server",
		Namespace:      "flag-namespace",
		ServiceAccount: "flag-account",
		Image:          "flag-image",
		Kubeconfig:     "/tmp/flag-kubeconfig",
	}, func(key string) string {
		switch key {
		case "ARGO_SERVER":
			return "env-server"
		case "ARGO_NAMESPACE":
			return "env-namespace"
		case "ARGO_SERVICE_ACCOUNT":
			return "env-account"
		case "CADS_WORKFLOW_IMAGE":
			return "env-image"
		case "KUBECONFIG":
			return "/tmp/env-kubeconfig"
		case "ARGO_TOKEN":
			return "Bearer flag-wins"
		default:
			return ""
		}
	})

	if len(problems) != 0 {
		t.Fatalf("ResolveArgoConfig() problems = %v, want none", problems)
	}
	if cfg.ArgoServer != "flag-server" || cfg.Namespace != "flag-namespace" || cfg.ServiceAccount != "flag-account" || cfg.Image != "flag-image" || cfg.Kubeconfig != "/tmp/flag-kubeconfig" {
		t.Fatalf("ResolveArgoConfig() cfg = %+v, want flag values", cfg)
	}
	if cfg.Token != "flag-wins" {
		t.Fatalf("ResolveArgoConfig() token = %q, want normalized env token", cfg.Token)
	}
}

func TestResolveArgoConfigPrefersEnvTokenOverKubeconfig(t *testing.T) {
	root := t.TempDir()
	kubeconfig := filepath.Join(root, "config")
	if err := os.WriteFile(kubeconfig, []byte(`
current-context: playground
contexts:
  - name: playground
    context:
      user: kube-user
users:
  - name: kube-user
    user:
      token: kube-token
`), 0o644); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	cfg, problems := ResolveArgoConfig(ArgoOptionInputs{Kubeconfig: kubeconfig}, func(key string) string {
		if key == "ARGO_TOKEN" {
			return "Bearer env-token"
		}
		return ""
	})

	if len(problems) != 0 {
		t.Fatalf("ResolveArgoConfig() problems = %v, want none", problems)
	}
	if cfg.Token != "env-token" {
		t.Fatalf("ResolveArgoConfig() token = %q, want env-token", cfg.Token)
	}
}

func TestResolveArgoConfigReportsInvalidKubeconfig(t *testing.T) {
	root := t.TempDir()
	kubeconfig := filepath.Join(root, "broken")
	if err := os.WriteFile(kubeconfig, []byte("not: [valid"), 0o644); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	_, problems := ResolveArgoConfig(ArgoOptionInputs{Kubeconfig: kubeconfig}, func(string) string { return "" })
	if len(problems) != 1 {
		t.Fatalf("ResolveArgoConfig() problems = %v, want one problem", problems)
	}
	if !strings.Contains(problems[0], "invalid kubeconfig") {
		t.Fatalf("ResolveArgoConfig() problem = %q, want invalid kubeconfig message", problems[0])
	}
}

func TestResolveArgoConfigRuntimeLimits(t *testing.T) {
	tokenOnly := func(extra map[string]string) EnvLookup {
		return func(key string) string {
			if key == "ARGO_TOKEN" {
				return "token"
			}
			return extra[key]
		}
	}

	cfg, problems := ResolveArgoConfig(ArgoOptionInputs{}, tokenOnly(nil))
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if cfg.MaxRuntimeSeconds != 900 || cfg.MaxRuntimeCeilingSeconds != 3600 {
		t.Fatalf("cfg = %+v, want default 900/3600", cfg)
	}
	if cfg.DefaultResources.Requests["cpu"] != "250m" || cfg.DefaultResources.Requests["memory"] != "256Mi" ||
		cfg.DefaultResources.Limits["cpu"] != "1" || cfg.DefaultResources.Limits["memory"] != "1Gi" {
		t.Fatalf("DefaultResources = %+v, want built-in defaults", cfg.DefaultResources)
	}

	cfg, problems = ResolveArgoConfig(ArgoOptionInputs{MaxRuntimeSeconds: 120}, tokenOnly(map[string]string{
		"CADS_MAX_RUNTIME_SECONDS":         "600",
		"CADS_MAX_RUNTIME_CEILING_SECONDS": "1800",
		"CADS_DEFAULT_CPU_REQUEST":         "100m",
		"CADS_DEFAULT_MEMORY_LIMIT":        "2Gi",
	}))
	if len(problems) != 0 || cfg.MaxRuntimeSeconds != 120 || cfg.MaxRuntimeCeilingSeconds != 1800 ||
		cfg.DefaultResources.Requests["cpu"] != "100m" || cfg.DefaultResources.Limits["memory"] != "2Gi" {
		t.Fatalf("cfg = %+v problems = %v, want flag 120, ceiling 1800, env resources", cfg, problems)
	}

	cfg, _ = ResolveArgoConfig(ArgoOptionInputs{}, tokenOnly(map[string]string{"CADS_MAX_RUNTIME_SECONDS": "600"}))
	if cfg.MaxRuntimeSeconds != 600 {
		t.Fatalf("MaxRuntimeSeconds = %d, want env 600", cfg.MaxRuntimeSeconds)
	}

	cfg, problems = ResolveArgoConfig(ArgoOptionInputs{}, tokenOnly(map[string]string{
		"CADS_MAX_RUNTIME_SECONDS":    "soon",
		"CADS_DEFAULT_MEMORY_REQUEST": "lots",
	}))
	if len(problems) != 2 || !strings.Contains(problems[0], "CADS_MAX_RUNTIME_SECONDS") || !strings.Contains(problems[1], "CADS_DEFAULT_MEMORY_REQUEST") {
		t.Fatalf("problems = %v, want two invalid value problems", problems)
	}
	if cfg.MaxRuntimeSeconds != 900 || cfg.DefaultResources.Requests["memory"] != "256Mi" {
		t.Fatalf("cfg = %+v, want defaults kept for invalid values", cfg)
	}
}
