# CADS FMI Dashboard Demo

This repository is a demo of a STOR-HY/CADS workflow runtime: FMI
co-simulation models (FMUs) chained by declarative YAML workflows, executed by
a Go/FMIL runner, fed with measurements from a database, and driven from a
browser dashboard. It is meant for demonstration and integration testing, not
for production analysis.

The models are simple placeholders and the data is synthetic. The models
imitate the shape of a CADS workflow, but they do not represent validated
engineering, forecasting, or operational models.

There are two ways to run it:

- **Local demo** (start here): a local TimescaleDB with synthetic
  measurements, and a dashboard service that runs the workflows on your own
  machine. No Kaizen access needed.
- **Kaizen Playground**: the same dashboard submitting the workflows to the
  hosted Kaizen Argo playground. Needs Kaizen credentials.

## What You Will See

The dashboard has a tab per demonstrator site, a strip with the site's status
and recommendation, and three views:

- **Plots** (default): the latest assessment, a condition table, a **Run
  assessment** button and history charts of measured channels and model
  outputs (6h to All, zoomable).
- **Workflow**: the wiring diagram of the selected workflow's FMUs with the
  latest values, plus Workflow and FMU cards.
- **Runs**: the run history and the record of each run.

A status pill under the logo shows whether runs and the database are
available.

## Quick Start: Local Demo

You need Git, Python 3, and Podman running (Podman Desktop, or
`podman machine start` on macOS). Tested on macOS with Podman.

1. Clone the repository and install the repo-local tools (Go, Argo CLI,
   kubectl under `.local/`):

   ```bash
   git clone https://github.com/janlv/cads-fmi-demo.git
   cd cads-fmi-demo
   ./prepare.sh
   ```

2. Start the local database and load the first day of synthetic Le Cheylas
   measurements:

   ```bash
   scripts/commands/run_db.sh
   export CADS_DB_URL=postgres://cads:cads@127.0.0.1:5432/storhy
   ```

3. Build the runner image and the dashboard service for this checkout (the
   first build takes several minutes):

   ```bash
   scripts/commands/build.sh --image cads-fmi-demo:dev
   ```

4. Start the dashboard service with local runs:

   ```bash
   bin/cads-workflow-service --serve --execution local --local-runner-image cads-fmi-demo:dev
   ```

5. Open `http://127.0.0.1:8080`, pick **Le Cheylas** and press **Run
   assessment**. Both Le Cheylas workflows run on the first day and the charts
   fill in.

6. In another terminal, replay the next day:

   ```bash
   scripts/commands/run_db.sh --next --notify
   ```

   The service assesses only the new day and continues the models' state from
   the previous run, and the trend in the Plots view grows by one day. Repeat
   for up to seven days; `scripts/commands/run_db.sh --reset` starts over.

The full walkthrough is in
[`docs/user-paths.md`](docs/user-paths.md#local-demo); problems such as a
taken port 5432 or a Podman clock that has drifted are in
[`docs/troubleshooting.md`](docs/troubleshooting.md).

## Running On The Kaizen Playground

The hosted path runs the same image and workflows on the Kaizen Argo
playground. It needs Kaizen credentials (`ARGO_TOKEN`, or a kubeconfig at
`.local/kaizen/kubeconfig`), handed over encrypted with `age`; see
[`docs/dashboard-setup.md`](docs/dashboard-setup.md#encrypted-kubeconfig-handoff-with-age-default).

```bash
# Dashboard against the published Playground image.
./run_playground.sh

# Build and publish a new image to GHCR, then start the dashboard.
./run_publish.sh

# Submit one workflow without the dashboard.
scripts/commands/run_remote.sh workflows/demonstrators/cheylas/maintenance/runner_rul_lockstep.yaml
```

Hosted runs read and write the database only once the `storhy-cads-db` secret
exists in the Playground; until then they use bundled data. What Kaizen needs
to provide for that is in [`docs/kaizen-handover.md`](docs/kaizen-handover.md).

Do not commit playground credentials, kubeconfigs, S3 keys, bearer tokens, or
registry tokens: this repository is public. Use local credential files,
environment variables, or a secret manager.

## Developer Paths

```bash
# Build and test one workflow in local Minikube + Argo, without a dashboard.
./run_local_dev.sh workflows/tests/python_chain.yaml

# Build the demo FMUs.
./create_fmu/build_python_fmus.sh
```

Contributor notes, including the Go and Python test commands, are in
[`docs/dev.md`](docs/dev.md).

## Simple Architecture

- **Dashboard in your browser**: choose a site and workflow, start runs, and
  inspect history and results.
- **Workflow definitions**: YAML files in `workflows/` that describe which
  FMUs to run and how they are connected.
- **Demo models**: FMUs built from Python placeholder code in `create_fmu/`.
  An FMU (Functional Mock-up Unit) is a packaged simulation model with a
  standard interface.
- **Runner**: Go code in `orchestrator/service/` that reads a workflow, runs
  the FMUs through FMIL (the FMI Library), and reports results.
- **Database**: PostgreSQL/TimescaleDB with one schema per site, holding
  measurements and run results.

The runner reads only the data that arrived since its previous run and
continues the models' state from it; see
[`docs/workflows.md`](docs/workflows.md#assessment-windows-and-state-carry-over-data_window-carry_over).
The runtime is the same locally and on the Playground; only who schedules the
runs changes. Details are in [`docs/architecture.md`](docs/architecture.md).

## Main Files

- `run_playground.sh`, `run_publish.sh`, `run_local_dev.sh`: the Playground
  and Minikube entry points.
- `scripts/commands/`: lower-level commands (build, database, dashboard,
  local and remote runs); every script supports `--help`.
- `workflows/`: workflow YAML definitions.
- `create_fmu/`: Python source for the demo FMUs.
- `data/storhy/<site>/measurements/`: synthetic measurement batches
  (`batch-*/`, plus `upcoming/` days for replay) and bundled fallback CSVs.
- `data/storhy/synthetic/`: synthetic operating cases used by the workflows.
- `orchestrator/service/`: the Go/FMIL runner and dashboard service.

## Documentation

- [`docs/user-paths.md`](docs/user-paths.md) – the local demo and the
  Playground and Minikube paths, step by step.
- [`docs/run.md`](docs/run.md) – run pipelines (local, hosted, dashboard) and
  the service flags.
- [`docs/workflows.md`](docs/workflows.md) – STOR-HY demonstrator workflows,
  models, measurement data and assessment windows.
- [`docs/architecture.md`](docs/architecture.md) – system structure, design
  intent and the dashboard reference.
- [`docs/kaizen-handover.md`](docs/kaizen-handover.md) – what Kaizen needs to
  provide to run the runtime against the STOR-HY database.
- [`docs/troubleshooting.md`](docs/troubleshooting.md) – common setup and
  runtime issues.
- [`docs/playground-dashboard.md`](docs/playground-dashboard.md) – dashboard
  against an existing published Playground image.
- [`docs/dashboard-setup.md`](docs/dashboard-setup.md) – dashboard setup and
  credential handling, including the `age` kubeconfig handoff;
  [`docs/age-sender.md`](docs/age-sender.md) is the sender side.
- [`docs/local-workflow-dev.md`](docs/local-workflow-dev.md) – local Minikube
  workflow development.
- [`docs/prepare.md`](docs/prepare.md) – environment preparation.
- [`docs/build.md`](docs/build.md) – image and binary build details.
- [`docs/workflow-publishing-approaches.md`](docs/workflow-publishing-approaches.md)
  – options for publishing workflows separately from the runtime image.
- [`docs/dev.md`](docs/dev.md) – contributor notes.
- [`create_fmu/README.md`](create_fmu/README.md) – FMU generation details.
- [`orchestrator/service/README.md`](orchestrator/service/README.md) – Go
  service and CLI details, routes and flags.
