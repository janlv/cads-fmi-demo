# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

A demo/prototype of a CADS-style workflow runtime for the STOR-HY project: FMI
co-simulation models (FMUs) chained by declarative YAML, executed by a Go/FMIL
runner, scheduled by Argo either in local Minikube or the hosted Kaizen
playground, and driven from a local browser dashboard. The models under
`create_fmu/storhy_replicas/` are deterministic placeholders, not validated
engineering models — keep that framing in user-facing text and docs.

`README.md` is the user-facing entry point; `docs/` holds the detailed guides
(`architecture.md`, `run.md`, `build.md`, `dev.md`, `workflows.md`,
`user-paths.md`, `troubleshooting.md`). Prefer updating those over duplicating
their content elsewhere.

## Toolchain lives in `.local/`, not on the system

`go`, `argo`, `kubectl`, and `minikube` are installed repo-locally by
`prepare.sh` and are **not** on the default PATH. Scripts handle this via
`cads_setup_local_path`; for manual commands do it yourself:

```bash
export PATH="$PWD/.local/go/bin:$PWD/.local/bin:$PATH"
```

Tool versions are pinned in `config/tool-versions.env`; the default playground
image tag in `config/playground.env`. `.local/state/*.env` caches the last
built/published image so later commands can reuse it.

## Commands

### Go service and runner

```bash
cd orchestrator/service

# Tests — the dashboard/service half is CGO-free, so this needs no FMIL:
CGO_ENABLED=0 GOWORK=off go test ./...
CGO_ENABLED=0 GOWORK=off go test ./workflow -run TestName   # single test

# Build both binaries (needs FMIL for the runner; see docs/dev.md for the
# full CGO_* exports, or just run scripts/commands/build.sh)
scripts/commands/build.sh                    # from repo root; also builds the image
```

`scripts/commands/build.sh` builds `cads-workflow-runner` with `CGO_ENABLED=1`
against FMIL and `cads-workflow-service` with `CGO_ENABLED=0`. Keep that split:
the service must stay cgo-free so the dashboard runs without FMIL, which is why
`internal/fmi` has both `fmi.go` (`//go:build cgo`) and `fmi_stub.go`
(`//go:build !cgo`). Any new FMIL-facing API needs a matching stub.

### Python (FMU authoring side only)

```bash
python3 -m unittest discover -s create_fmu -p 'test_*.py'   # stdlib only, no venv needed
./create_fmu/build_python_fmus.sh                           # builds FMUs into fmu/models/
```

### The three user paths

```bash
./run_playground.sh                                       # dashboard against the published Playground image
./run_publish.sh                                          # build + push to GHCR + prepare Playground + dashboard
./run_local_dev.sh workflows/tests/python_chain.yaml      # Minikube + Argo, no dashboard
scripts/commands/run_remote.sh <workflow.yaml>            # one hosted submission, no dashboard
scripts/commands/clean.sh                                 # wipe .local/, bin/, images, Minikube profile
```

Lower-level steps live in `scripts/commands/`; every script supports `--help`.

## Architecture

### Execution layers

1. **Workflow YAML** (`workflows/`) — declarative step list; see the schema in
   `workflowStep` at [workflow.go:202](orchestrator/service/workflow/workflow.go#L202).
2. **`workflow.Executor`** — resolves paths (rejecting anything escaping the
   repo root), wires `start_from` step-to-step values, downloads `input_series.s3`
   inputs, and calls FMIL per step.
3. **`internal/fmi`** — cgo bridge over `runner_bridge.cpp`/FMIL that loads and
   steps one FMU.
4. **Argo** — schedules the same container image locally (PVC/configmap-backed
   manifests from `scripts/generate_manifests.sh`) or hosted
   (PVC-free manifests from `scripts/generate_remote_workflow.sh`).

The runtime is identical across all paths; only the scheduler and operator
surface change.

### The runner→dashboard result contract

`cads-workflow-runner` prints the whole `map[step]map[output]value` result as
JSON on stdout. The dashboard does **not** read a database or artifact store: it
shells out to `argo` and recovers results by parsing that JSON back out of pod
logs (`extractRunResultsFromLogs` in `remote.go`). So anything that pollutes
runner stdout, or changes the result shape, breaks run-result display.

### Repo root discovery

Both binaries walk up from the cwd looking for a directory containing both
`workflows/` and `fmu/` (`ResolveWorkDir` in `runner.go`). All workflow, FMU,
and result paths are repo-relative, and the same relative layout exists inside
the container image at `/app`, which is why hosted manifests can reference
`workflows/...` paths directly.

### Dashboard

Vanilla HTML/CSS/JS in `orchestrator/service/web/`, embedded with `go:embed`
(`ui_assets.go`) — no npm, no build step, so editing `web/static/app.js`
requires only a service rebuild. The browser never talks to Argo directly; it
uses `/api/config`, `/api/workflows`, `/api/runs[/{name}[/results]]` served by
`server.go`, plus `POST /run` for local in-process execution.

Workflows appear in the UI from their YAML `metadata` block (`display_name`,
`site_id`, `category`, `result_family`, `description`, `tags`) — see
`ListWorkflows` in `workflows.go`. A new workflow for an existing site only
needs a matching `site_id`; a new *demonstrator site* also needs an entry in the
hardcoded `DEMONSTRATORS` array in `app.js` (labels, map coordinates, facts).

### Auth model

Kaizen/Argo credentials (`ARGO_TOKEN`, or a bearer token extracted from
`.local/kaizen/kubeconfig`) are needed to list and submit runs; GHCR
credentials (`GHCR_TOKEN`/`GITHUB_TOKEN`/`gh auth`) are needed only when
publishing a new image. Hosted manifests project the playground S3 secret into
`AWS_*`/`S3_*` env vars so `input_series.s3` works without per-run edits.

## Conventions

- **Shell portability** (see `AGENTS.md`): scripts must run on Linux Bash and
  macOS Bash 3.2. Under `set -u`, guard array expansion with
  `if ((${#arr[@]} > 0)); then` before `"${arr[@]}"`. Keep SSH remote snippets
  `/bin/sh`-compatible and prefer `sh -s -- arg...` over positional `sh -c`.
- Scripts source `scripts/lib/{logging,runtime,tooling}.sh` and use the
  `log_step`/`log_info`/`log_warn`/`log_ok`/`log_error` helpers rather than bare
  `echo`; use `log_stream_cmd`/`run_with_logged_output` for long subcommands.
- Never commit kubeconfigs, S3 keys, bearer tokens, registry tokens, or the
  corporate CA certs in `certs/` and `scripts/certs/` — the repo is public.
  `*.age`, `kubeconfig`, `.local/`, `bin/`, `deploy/`, `*.fmu`, and most of
  `data/` are gitignored; generated manifests and FMUs are build outputs, not
  source.
- Commit subjects are short imperative one-liners, occasionally with a
  `fix:`/`docs:`/`chore:` prefix.
- There is no CI; verify changes locally with the Go and Python test commands
  above.
- When using sub-agents, pick a model suited to the task the agent will
  execute. Do not use heavy models for simple tasks (file lookups, greps,
  small edits); reserve the heavier models for work that needs deeper
  reasoning. This keeps limited resources from being spent on
  over-complicated models.
