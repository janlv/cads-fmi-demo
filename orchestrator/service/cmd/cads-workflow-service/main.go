package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	svc "github.com/norceresearch/cads-fmi-demo/orchestrator/service"
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

	flag.StringVar(&workflow, "workflow", "", "Run the workflow once and exit")
	flag.BoolVar(&serve, "serve", false, "Start the HTTP service")
	flag.StringVar(&addr, "addr", ":8080", "HTTP listen address (default :8080)")
	flag.StringVar(&workdir, "workdir", "", "Explicit repository root (optional)")
	flag.StringVar(&argoServer, "argo-server", "", "Hosted Argo server host (default ARGO_SERVER or argoworkflows.cads.kzslab.dev)")
	flag.StringVar(&argoNamespace, "argo-namespace", "", "Hosted Argo namespace (default ARGO_NAMESPACE or playground)")
	flag.StringVar(&argoServiceAccount, "argo-service-account", "", "Hosted Argo service account (default ARGO_SERVICE_ACCOUNT or playground-storhy-playground-pg-admin)")
	flag.StringVar(&remoteImage, "remote-image", "", "Hosted workflow image (default CADS_WORKFLOW_IMAGE or ghcr.io/janlv/cads-fmi-demo:playground)")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Optional kubeconfig used when ARGO_TOKEN is not set")
	flag.Parse()

	runner, err := svc.NewRunner(workdir)
	if err != nil {
		log.Fatal(err)
	}

	if workflow != "" {
		results, err := runner.Run(workflow)
		if err != nil {
			log.Fatal(err)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			log.Fatal(err)
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
		}, os.Getenv)
		server := &svc.Server{
			Runner: runner,
			Remote: remote,
		}
		fmt.Printf("[service] listening on %s (workdir %s)\n", addr, runner.WorkDir)
		log.Fatal(http.ListenAndServe(addr, server))
	}

	if workflow == "" && !serve {
		flag.Usage()
		os.Exit(1)
	}
}
