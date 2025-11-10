package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	svc "github.com/norceresearch/cads-fmi-demo/orchestrator/service"
	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/workflow"
)

func main() {
	var workflowPath string
	var jsonOutput bool
	var workdir string

	flag.StringVar(&workflowPath, "workflow", "workflows/python_chain.yaml", "Workflow YAML to execute")
	flag.BoolVar(&jsonOutput, "json-output", false, "Only emit the final JSON result")
	flag.StringVar(&workdir, "workdir", "", "Explicit repository root (optional)")
	flag.Parse()

	if workflowPath == "" {
		log.Fatal("workflow path is required")
	}

	var opts []workflow.Option
	if !jsonOutput {
		opts = append(opts, workflow.WithLogger(func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		}))
	}

	runner, err := svc.NewRunner(workdir, opts...)
	if err != nil {
		log.Fatal(err)
	}

	if !jsonOutput {
		fmt.Printf("[workflow] Running %s\n", workflowPath)
	}

	results, err := runner.Run(workflowPath)
	if err != nil {
		log.Fatal(err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		if err := enc.Encode(results); err != nil {
			log.Fatal(err)
		}
		return
	}

	fmt.Println("[workflow] Completed all steps.")

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		log.Fatal(err)
	}
}
