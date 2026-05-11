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
