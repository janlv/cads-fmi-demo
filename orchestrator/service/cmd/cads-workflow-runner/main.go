package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	svc "github.com/norceresearch/cads-fmi-demo/orchestrator/service"
	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/workflow"
)

const (
	exitFailed    = 1
	exitCancelled = 130
)

func main() {
	var workflowPath string
	var jsonOutput bool
	var workdir string
	var showVersion bool

	flag.StringVar(&workflowPath, "workflow", "workflows/tests/python_chain.yaml", "Workflow YAML to execute")
	flag.BoolVar(&jsonOutput, "json-output", false, "Only emit the final JSON result on stdout")
	flag.StringVar(&workdir, "workdir", "", "Explicit repository root (optional)")
	flag.BoolVar(&showVersion, "version", false, "Print the runner version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println(svc.ResolvedVersion())
		return
	}
	if workflowPath == "" {
		log.Fatal("workflow path is required")
	}

	opts := []workflow.Option{workflow.WithRunnerVersion(svc.ResolvedVersion())}
	if !jsonOutput {
		opts = append(opts, workflow.WithLogger(func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		}))
	}

	runner, err := svc.NewRunner(workdir, opts...)
	if err != nil {
		log.Fatal(err)
	}

	// SIGTERM (Argo deadline / pod deletion) and SIGINT cancel the run; the results printed below
	// then carry status "cancelled" (ARCH-COMP-017).
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-signals
		fmt.Fprintln(os.Stderr, "[workflow] cancellation requested")
		runner.Cancel()
	}()

	if !jsonOutput {
		fmt.Printf("[workflow] Running %s\n", workflowPath)
	}

	results, runErr := runner.Run(workflowPath)

	// The result map is printed even on failure: it carries completed steps plus the `_run`
	// pseudo-step with status, failing step and provenance. stdout stays JSON-only in
	// --json-output mode; diagnostics go to stderr.
	if results != nil {
		enc := json.NewEncoder(os.Stdout)
		if !jsonOutput {
			if runErr == nil {
				fmt.Println("[workflow] Completed all steps.")
			}
			enc.SetIndent("", "  ")
		}
		if err := enc.Encode(results); err != nil {
			fmt.Fprintf(os.Stderr, "[workflow] encode results: %v\n", err)
		}
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "[workflow] failed: %v\n", runErr)
		if errors.Is(runErr, fmi.ErrCancelled) {
			os.Exit(exitCancelled)
		}
		os.Exit(exitFailed)
	}
}
