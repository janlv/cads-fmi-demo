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

	flag.StringVar(&workflow, "workflow", "", "Run the workflow once and exit")
	flag.BoolVar(&serve, "serve", false, "Start the HTTP service")
	flag.StringVar(&addr, "addr", ":8080", "HTTP listen address (default :8080)")
	flag.StringVar(&workdir, "workdir", "", "Explicit repository root (optional)")
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
		server := &svc.Server{Runner: runner}
		fmt.Printf("[service] listening on %s (workdir %s)\n", addr, runner.WorkDir)
		log.Fatal(http.ListenAndServe(addr, server))
	}

	if workflow == "" && !serve {
		flag.Usage()
		os.Exit(1)
	}
}
