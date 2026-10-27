# CADS Architecture

This repository demonstrates a CADS-style workflow runtime built around FMUs,
declarative workflow YAML, and Argo execution in either a local Minikube
cluster or the hosted Kaizen playground.

The core idea is stable across all supported paths:

1. FMUs live under `fmu/models/`.
2. Workflow definitions live under `workflows/`.
3. The Go/FMIL runtime loads those workflows and executes the FMUs.
4. Argo schedules the runtime either locally or in the hosted playground.

## Runtime model

- **FMI / FMU**: the repo executes FMI Co-Simulation FMUs produced by tools such
  as Simulink or the bundled Python demo builder.
- **FMIL**: the low-level shared library used by the Go runner to load and step
  FMUs.
- **Go runner**: `cads-workflow-runner` reads a workflow YAML file, resolves
  inputs/outputs, executes each step, and writes results.
- **Go service**: `cads-workflow-service` exposes the same execution layer
  through an HTTP API and serves the dashboard UI.

The runtime is identical across local, remote, and dashboard-triggered flows.
Only the scheduler and operator surface change.

## Supported execution paths

### Local Minikube path

The local path is the supported in-repo Kubernetes development flow:

- `./run_local_dev.sh` is the user-facing entrypoint
- `scripts/commands/prepare_local.sh` installs local tooling and boots Minikube
- `scripts/commands/build.sh` builds the container image and Go binaries
- `scripts/commands/run_local.sh` drives local Argo submission

`scripts/commands/run_local.sh` still depends on:

- `scripts/run_argo_workflow.sh`
- `scripts/generate_manifests.sh`

These scripts render the local Argo workflow and PVC manifests into `deploy/`
and submit the workflow into the in-cluster Argo controller. This is an active
supported path, not a legacy leftover.

### Hosted Kaizen path

The hosted path targets `argoworkflows.cads.kzslab.dev`:

- `./run_publish.sh` is the user-facing entrypoint for publishing the bundled
  image and starting the dashboard
- `scripts/commands/prepare_remote.sh` validates Argo access and publishes a
  hosted image tag
- `scripts/commands/run_remote.sh` generates a hosted manifest and submits it
  to the playground

The hosted workflow runs the same CADS image, but without the local PVC/configmap
assumptions used by the Minikube flow.

### Dashboard path

`./run_playground.sh` starts a local browser UI that talks to the hosted Kaizen
playground through the local Go service without building or publishing. The
same service can instead run workflows on this machine (see *Local execution*
below). The browser never talks directly to the Argo server or the database.
Instead:

1. the local service resolves auth, execution mode and configuration
2. the service lists workflows and runs (Argo, local and stored runs merged)
3. button clicks submit hosted manifests or start local runs
4. the UI renders run state, results and the history charts

This section is the reference description of the dashboard; the other guides
link here.

**Header.** "STOR-HY Demonstrator Dashboard" with a one-paragraph CADS lede,
the logo at the right and a traffic-light status pill under it:

| Pill | Meaning |
| --- | --- |
| grey, pulsing | loading the configuration |
| green "Ready" | runs are possible and the database is connected |
| amber "Ready, no database" / "Ready, local runs" | usable with limits: no database (Plots show only the latest finished run), or local runs with the hosted playground unused |
| red "Attention" | no way to run workflows (no Argo access and no local runs), or refreshing the run list fails |

Hovering the pill shows a one-line summary; clicking it opens a details panel
with the execution mode (for local runs the runner description from
`/api/config` `localRunner`), the hosted playground state and its `problems`,
the database state, run-refresh errors and the dashboard version.

**Site strip.** Under the demonstrator tabs (one per site in
`demo_scope.sites`) a strip shows the site name, operator | location, partner
chips, a **Status** badge with its description and the workflows that raised
it, and the **Recommendation**. It stays visible in every view.

**Views.** A view row switches between **Plots** (the default), **Workflow**
and **Runs**. `?view=plots|workflow|runs` deep-links a view; otherwise the last
choice is remembered in `localStorage` (a remembered legacy `details` value
maps to Workflow).

- **Plots** holds the assessment line (last assessment, up-to-date text,
  errors), the condition table (two columns of indicator / source / value from
  `sites[].operator_view.indicators` in `web/static/cads-model-matrix.json`),
  then a toolbar with the **Run assessment** button (runs every workflow of
  the site through `POST /api/sites/{site}/runs`), the span buttons 6h, 24h,
  7d, 30d and All, a status text and a zoom hint, then the history charts.
  All charts share an identical time axis and synchronised zoom (pinch or
  Ctrl+scroll inside a chart, or the slider under the last chart; plain
  scrolling moves the page), and each chart header shows the latest value of
  every series. The charts are declared per site under
  `sites[].plots.charts[]` (`id`, `title`, `unit`, optional `thresholds` with
  `value`, `label` and `severity` alarm|warning|reference, drawn as dashed
  limit lines taken from the placeholder models' own thresholds, the axis
  extended to include them, and `series` of kind `measurement` (a database
  channel), `trace` (a run trace signal of `workflow`/`step`) or `output` (one
  end-of-run value per run)). The data comes from
  `GET /api/sites/{site}/history`, see below. **Run assessment** is disabled
  when the service has neither Argo access nor local runs.
- **Workflow** holds the workflow tabs (from the catalog), a header row
  (title, coupling badges, one-sentence summary and an outlined **Launch
  workflow** button that submits only that workflow to the hosted playground;
  it needs Argo access), the port-level wiring diagram and its badge legend as
  caption. Each FMU box lists input ports, output ports with the latest run's
  values, and folded parameters; co-simulation connections are solid wires,
  `start_from` hand-overs dashed; measured-series inputs, event targets and
  carried-over state are colour-coded; outputs exchanged only inside the step
  are shown dashed; hovering a port highlights its wires; the chain scrolls
  inside its panel. Below it a **Workflow** card (Data in, Latest run with the
  assessed window and a "run record ▸" link that opens the Runs view, Steps ·
  max runtime) sits beside an **FMU** card for the FMU picked in the chain
  (file name as title, what it models, owners, role in this workflow, FMI
  identity line, inputs, outputs, parameters).
- **Runs** holds the workflow tabs, the history rail (runs of the selected
  workflow; local runs carry a "ran locally" badge) and the run record: run
  name, collected-from badge (database or pod logs), CSV export, timing strip,
  an "Assessed data" block (window, previous run, backlog) with the "Carried
  over" values, the FMU step chain, the synthetic case and scalar output
  cards. Clicking a run in the rail pins it in the record ("Selected run"
  header, "from history" pill, **show latest** button); clicking it again or
  **show latest** returns to the newest run.

**Site runs.** `POST /api/sites/{site}/runs` starts every workflow whose
`metadata.site_id` matches and answers
`{site, runs[], errors[], upToDate[{workflow, reason, coveredTo, latestSample}]}`:
200 when anything started or is up to date, 502 when everything failed, 404
when the site has no workflows. In local mode the service first resolves each
`data_window` workflow's next window and lists it under `upToDate` instead of
starting a run when there is no new data (the dashboard shows "Up to date").
In Argo mode there is no pre-check: the workflows are submitted and the
runner's `skipped` status is authoritative.

**Data status.** `GET /api/data?site=` reports `configured` and, with a
database, the site's ingested channels (`channel`, `unit`, `source`,
`samplingIntervalS`, `points`, `from`, `to`, `ingestedAt`, `batch`), which the
Workflow card's "Data in" line uses.

**Local execution.** The service can execute runs itself instead of submitting
them to Argo (`cads-workflow-service --execution auto|argo|local`; flag table
in [`run.md`](run.md#local-demo)). In local mode it starts the
runner binary (`--runner-bin`, default `bin/cads-workflow-runner`) as a child
process per run with `CADS_RUN_ID` and the database URL, or runs it inside a
container image (`--local-runner-image IMAGE`, env `CADS_LOCAL_RUNNER_IMAGE`)
with `workflows/` mounted; the image route is the one to use on macOS, where
the demo FMUs (Linux builds) cannot load natively. Inside the container the
database URL comes from `--local-db-url` (env `CADS_LOCAL_DB_URL`), by default
the service's URL with a `127.0.0.1`/`localhost` host rewritten to
`host.containers.internal` (podman) or `host.docker.internal` (docker). Local
runs get ids `local-...`, appear in the run list merged with Argo and stored
runs, and their results come from memory or the database. `/api/config`
reports `remoteEnabled`, `databaseConfigured`, `localRunsEnabled`,
`executionMode`, `localRunner` (for example `podman image cads-fmi-demo:dev`
or `runner binary cads-workflow-runner`), `problems` and `version`.

## S3-backed workflow inputs

Workflows can declare time-series input data from either:

- a repo-local CSV file
- an S3 object

For S3-backed hosted runs, the generated manifests automatically project the
playground S3 secret into standard AWS-style environment variables inside the
workflow pod. That keeps credentials out of workflow YAML while allowing the
runner to fetch remote inputs at runtime.

The repo also includes helper workflows and scripts for:

- listing S3 bucket prefixes/keys
- inspecting one S3 object from inside the playground

These helpers exercise the same hosted-image pattern as the main CADS remote
workflows.

## Database-backed Data interface

Measured data reaches models through the STOR-HY database, mirroring the D3.5
data path (bucket, ingestion, per-demonstrator database partition, co-simulation,
results back to the database):

- **Ingestion.** `scripts/ingest_measurements.py` validates a measurement batch
  (`metadata.yaml` with site, batch, source, and per-channel unit, sampling
  interval and file; RFC3339 CSVs) and loads it into the per-site schema
  `<site>.channels` / `<site>.measurements` (a TimescaleDB hypertable when the
  extension exists). A batch that fails validation is rejected as a whole with
  the reason logged (ARCH-DATA-001/004/007). `scripts/commands/run_db.sh`
  starts a local TimescaleDB and ingests every bundled batch.
- **Inputs.** A step or co-simulation model declares `input_series.db` (site,
  FMU variable to channel map, time window, optional bundled fallback). With
  `CADS_DB_URL` set the runner queries the window and feeds it to the FMU; the
  provenance records `source: db` and the dataset. Without a database the
  bundled fallback CSV is used and recorded as `db-fallback-csv`, which the
  dashboard flags.
- **Relative windows and the assessment chain.** A workflow-level
  `data_window` (`since_previous_run` or `latest`) resolves the window from the
  newest samples and the previous succeeded run (found through
  `runs.provenance->'data_window'`, no extra tables); `carry_over` seeds start
  values from that run's outputs, and a run with no new data finishes as
  `skipped` without writing anything. Rules in
  [`workflows.md`](workflows.md#assessment-windows-and-state-carry-over-data_window-carry_over).
- **Results.** After every run (also failed ones) the runner writes the run
  record, scalar outputs and traces to `<site>.runs`, `run_outputs` and
  `run_series` under the run id `CADS_RUN_ID` (the Argo workflow name on the
  playground), and reports the outcome in `_run.results_sink`
  (ARCH-COMP-007, ARCH-DATA-010). The dashboard reads results from the
  database first and falls back to pod logs.
- **History.** `GET /api/sites/{site}/history` serves the Plots view from the
  database: `span` (`6h`, `24h` default, `7d`, `30d`, `all`), `channels`
  (comma-separated or repeated, at most 16), `series` (`step:signal`, at most
  32) and `max_points` (100..5000, default 1500). The range ends at the newest
  sample of the requested channels (`anchor`; else the newest run window), and
  measurements are averaged into `binSeconds` buckets when the raw samples
  would exceed `max_points`. The response carries `configured`, `site`,
  `span`, `anchor`, `from`/`to` (also as `fromMs`/`toMs`), `binSeconds`,
  `truncated`, `measurements[]` (`channel`, `unit`, `points` as
  `[epochMs, value]`) and `runs[]`: the succeeded runs whose assessed window
  overlaps the range, newest per workflow and window start, at most 200, each
  with `runId`, `workflowPath`, `source` (`argo` or `local`), `windowFromMs`,
  `windowToMs`, `simStartTime`, `anchorSource`, `outputs` and the requested
  `series` (simulation seconds; wall time is `windowFromMs + (t -
  simStartTime) * 1000`). Without a database it returns
  `{"configured": false}` and the Plots view falls back to the latest finished
  run of each workflow with a note; invalid queries get 400, database errors
  502.
- **Local execution.** A service started with the database URL and local
  execution runs **Run assessment** through the runner binary or image (see
  the dashboard path above), so the whole loop (ingest, assess, plot) works
  without Argo.
- **Hosted runs** receive `CADS_DB_URL` from the Kubernetes secret
  `storhy-cads-db` (key `url`, optional). Until that secret exists in the
  playground, hosted runs use the bundled fallback and write nothing; a
  `data_window` workflow then runs its unresolved fallback window
  (`data_window.resolved: false`) and does not chain from one run to the next.

Per-site schemas are the storage-side prerequisite for per-demonstrator
authorisation (ARCH-DATA-006); role-based grants per schema are the next step.
The runner only runs schema DDL when a site table is missing, so a runtime
role with DML grants on tables created in advance is enough; grants and the
hosted setup are in [`kaizen-handover.md`](kaizen-handover.md).

## Main repo boundaries

- `create_fmu/`: Python-side FMU generation only
- `fmu/models/`: active FMUs used by workflows
- `workflows/`: declarative workflow definitions
- `orchestrator/service/`: Go runtime, HTTP service, and dashboard assets
- `scripts/`: build, submission, and helper tooling
- `archive/FMI_surya/`: historical Simulink-side reference assets, preserved for
  reference but not used by the active runtime path

## Operational split

The supported commands now map cleanly to responsibilities:

- **Playground dashboard**: `./run_playground.sh`
- **Publish to Playground**: `./run_publish.sh`
- **Local Dev**: `./run_local_dev.sh`
- **Lower-level commands**: `scripts/commands/`
- **Hosted inspection helpers**: `scripts/commands/run_argo.sh`,
  `scripts/commands/run_list_s3_objects.sh`,
  `scripts/commands/run_inspect_s3_object.sh`

Deprecated compatibility wrappers and compose-era parity are no longer part of
the supported architecture.
