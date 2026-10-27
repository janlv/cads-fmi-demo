package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	svc "github.com/norceresearch/cads-fmi-demo/orchestrator/service"
	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
)

func main() {
	var workflow string
	var serve bool
	var addr string
	var workdir string
	var argoServer string
	var argoNamespace string
	var argoServiceAccount string
	var remoteImage string
	var kubeconfig string
	var maxRuntimeSeconds int64
	var showVersion bool
	var dbURL string
	var execution string
	var runnerBin string
	var localImage string
	var localDBURL string

	flag.StringVar(&workflow, "workflow", "", "Run the workflow once and exit")
	flag.BoolVar(&serve, "serve", false, "Start the HTTP service")
	flag.StringVar(&addr, "addr", ":8080", "HTTP listen address (default :8080)")
	flag.StringVar(&workdir, "workdir", "", "Explicit repository root (optional)")
	flag.StringVar(&argoServer, "argo-server", "", "Hosted Argo server host (default ARGO_SERVER or argoworkflows.cads.kzslab.dev)")
	flag.StringVar(&argoNamespace, "argo-namespace", "", "Hosted Argo namespace (default ARGO_NAMESPACE or playground)")
	flag.StringVar(&argoServiceAccount, "argo-service-account", "", "Hosted Argo service account (default ARGO_SERVICE_ACCOUNT or playground-storhy-playground-pg-admin)")
	flag.StringVar(&remoteImage, "remote-image", "", "Hosted workflow image (default CADS_WORKFLOW_IMAGE or ghcr.io/janlv/cads-fmi-demo:playground)")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Optional kubeconfig used when ARGO_TOKEN is not set")
	flag.Int64Var(&maxRuntimeSeconds, "max-runtime-seconds", 0, "Default hosted run deadline in seconds when a workflow sets no limits.max_runtime_seconds (default CADS_MAX_RUNTIME_SECONDS or 900; capped by CADS_MAX_RUNTIME_CEILING_SECONDS, default 3600)")
	flag.StringVar(&dbURL, "db-url", "", "CADS database URL for input data status and stored run results (default CADS_DB_URL)")
	flag.StringVar(&execution, "execution", "auto", "Where dashboard runs execute: auto (Argo when configured, else local when a runner image or binary is available), argo, or local")
	flag.StringVar(&runnerBin, "runner-bin", "", "Runner binary for local runs (default <workdir>/bin/cads-workflow-runner)")
	flag.StringVar(&localImage, "local-runner-image", os.Getenv("CADS_LOCAL_RUNNER_IMAGE"), "Run local runs inside this container image (podman, else docker) instead of the runner binary (default CADS_LOCAL_RUNNER_IMAGE)")
	flag.StringVar(&localDBURL, "local-db-url", os.Getenv("CADS_LOCAL_DB_URL"), "Database URL inside the local runner container (default CADS_LOCAL_DB_URL, else the database URL with a loopback host pointed at the container host)")
	flag.BoolVar(&showVersion, "version", false, "Print the service version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println(svc.ResolvedVersion())
		return
	}

	runner, err := svc.NewRunner(workdir)
	if err != nil {
		log.Fatal(err)
	}

	if workflow != "" {
		results, runErr := runner.Run(workflow)
		if results != nil {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(results); err != nil {
				log.Fatal(err)
			}
		}
		if runErr != nil {
			log.Fatal(runErr)
		}
		if !serve {
			return
		}
	}

	if serve {
		remote := svc.NewArgoRemoteClient(runner.WorkDir, svc.ArgoOptionInputs{
			ArgoServer:     argoServer,
			Namespace:      argoNamespace,
			ServiceAccount: argoServiceAccount,
			Image:          remoteImage,
			Kubeconfig:     kubeconfig,

			MaxRuntimeSeconds: maxRuntimeSeconds,
		}, os.Getenv)
		server := &svc.Server{
			Runner: runner,
			Remote: remote,
		}
		if dbURL == "" {
			dbURL = cadsdb.URLFromEnv()
		}
		if dbURL != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			store, err := cadsdb.Open(ctx, dbURL)
			cancel()
			if err != nil {
				log.Printf("[service] CADS database unavailable, continuing without it: %v", err)
			} else {
				defer store.Close()
				server.Data = store
				remote.SetRunStore(store)
				fmt.Println("[service] CADS database configured")
			}
		}
		mode, err := resolveExecutionMode(execution, remote.Config().RemoteEnabled, localImage, &runnerBin, runner.WorkDir)
		if err != nil {
			log.Fatal(err)
		}
		server.ExecutionMode = mode
		if mode == svc.ExecutionLocal {
			server.Local = svc.NewLocalRunner(svc.LocalRunnerOptions{
				WorkDir:           runner.WorkDir,
				RunnerBin:         runnerBin,
				Image:             localImage,
				DBURL:             dbURL,
				ContainerDBURL:    localDBURL,
				MaxRuntimeSeconds: maxRuntimeSeconds,
			})
			if server.Local.Image != "" {
				fmt.Printf("[service] local execution in %s image %s\n", server.Local.ContainerTool, server.Local.Image)
			} else {
				fmt.Printf("[service] local execution with %s\n", runnerBin)
			}
		} else {
			fmt.Println("[service] execution through Argo")
		}
		fmt.Printf("[service] listening on %s (workdir %s, version %s)\n", addr, runner.WorkDir, svc.ResolvedVersion())
		log.Fatal(http.ListenAndServe(addr, server))
	}

	if workflow == "" && !serve {
		flag.Usage()
		os.Exit(1)
	}
}

// resolveExecutionMode picks argo or local for --execution and fills the default runner binary.
func resolveExecutionMode(execution string, remoteEnabled bool, image string, runnerBin *string, workDir string) (string, error) {
	if strings.TrimSpace(*runnerBin) == "" {
		*runnerBin = filepath.Join(workDir, "bin", "cads-workflow-runner")
	}
	localAvailable := strings.TrimSpace(image) != ""
	if !localAvailable {
		if info, err := os.Stat(*runnerBin); err == nil && !info.IsDir() {
			localAvailable = true
		}
	}
	switch strings.ToLower(strings.TrimSpace(execution)) {
	case "", "auto":
		if remoteEnabled {
			return svc.ExecutionArgo, nil
		}
		if localAvailable {
			return svc.ExecutionLocal, nil
		}
		return svc.ExecutionArgo, nil
	case "argo":
		return svc.ExecutionArgo, nil
	case "local":
		if !localAvailable {
			return "", fmt.Errorf("--execution local needs --local-runner-image or a runner binary at %s", *runnerBin)
		}
		return svc.ExecutionLocal, nil
	}
	return "", fmt.Errorf("--execution must be auto, argo or local (got %q)", execution)
}
