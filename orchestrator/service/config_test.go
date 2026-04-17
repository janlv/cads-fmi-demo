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
