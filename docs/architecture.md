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
browser never talks directly to the Argo server. Instead:

1. the local service resolves auth and configuration
2. the service lists workflows and hosted runs
3. button clicks generate and submit hosted manifests
4. the UI renders recent run state and AECIS results

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
- **Results.** After every run (also failed ones) the runner writes the run
  record, scalar outputs and traces to `<site>.runs`, `run_outputs` and
  `run_series` under the run id `CADS_RUN_ID` (the Argo workflow name on the
  playground), and reports the outcome in `_run.results_sink`
  (ARCH-COMP-007, ARCH-DATA-010). The dashboard reads results from the
  database first and falls back to pod logs.
- **Hosted runs** receive `CADS_DB_URL` from the Kubernetes secret
  `storhy-cads-db` (key `url`, optional). Until that secret exists in the
  playground, hosted runs use the bundled fallback and write nothing.

Per-site schemas are the storage-side prerequisite for per-demonstrator
authorisation (ARCH-DATA-006); role-based grants per schema are the next step.

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
