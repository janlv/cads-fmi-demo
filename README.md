# CADS FMI Dashboard Demo

This repository contains a mock STOR-HY/CADS dashboard prototype. It is meant
for demonstration and integration testing, not for production analysis.

The dashboard uses simple placeholder models and synthetic local data. Those
models imitate the shape of a CADS workflow, but they do not represent validated
engineering, forecasting, or operational models.

The repo is organized around three OS-independent user paths, plus a local
database demo:

- **Playground Dashboard**: connect a local dashboard to an existing Kaizen
  Playground image. This is the default path for most users.
- **Publish to Playground**: build locally, publish the workflow image to GHCR,
  and connect the dashboard to the Playground.
- **Local Dev**: build and test one workflow/model quickly in local Minikube
  without Kaizen Playground, GHCR publishing, or a dashboard.
- **Near-realtime demo (local database)**: a local TimescaleDB with synthetic
  measurements and a dashboard service that runs the workflows on this machine
  (`--execution local`), without Kaizen.

See [`docs/user-paths.md`](docs/user-paths.md) for the support matrix.

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
available. With a local database the near-realtime loop runs without Kaizen:

1. `scripts/commands/run_db.sh --next --notify` ingests one more synthetic day;
2. the service assesses only the new data and continues the models' state;
3. the trend in the Plots view grows by one day.

See [`docs/user-paths.md`](docs/user-paths.md#near-realtime-assessment-demo-local-database).

## Bring Up The Dashboard

Run these commands in order on the checkout that should host the dashboard.

1. Clone the repository:

   ```bash
   git clone https://github.com/janlv/cads-fmi-demo.git
   cd cads-fmi-demo
   ```

2. Prepare the host:

   ```bash
   ./prepare.sh
   ```

   Podman or Docker is only needed for the **Publish to Playground** and
   **Local Dev** paths. It is not needed for the default
   **Playground Dashboard** path.

3. Set up Kaizen credentials using the default non-SSH handoff. First,
   on the checkout that should host the dashboard, run:

   ```bash
   ./scripts/age_create_identity.sh --copy
   ```

   Send the copied public key, which starts with `age1...`, to the person who
   already has the Kaizen credentials. They encrypt their kubeconfig for that
   key:

   ```bash
   ./scripts/age_encrypt_kubeconfig.sh \
       --recipient age1_receiver_public_key_here \
       --input .local/kaizen/kubeconfig
   ```

   They send back the encrypted `.age` file. Save it locally and decrypt it:

   ```bash
   ./scripts/age_decrypt_kubeconfig.sh /path/to/kubeconfig.age
   ```

4. Start the dashboard against the configured Playground image:

   ```bash
   ./run_playground.sh
   ```

5. Open:

   ```text
   http://localhost:8080/
   ```

The default image tag lives in `config/playground.env`. If you need to test a
different published image, pass it explicitly:

```bash
./run_playground.sh --image ghcr.io/org/cads-fmi-demo:tag
```

## Kaizen Credentials

The dashboard needs access to the hosted Kaizen Argo playground, through
`ARGO_TOKEN` or a kubeconfig at `.local/kaizen/kubeconfig`. The default handoff
is the `age` public-key exchange shown in step 3 above: the receiver sends only
the public `age1...` key, the sender returns an encrypted `.age` file, and
nobody shares SSH passwords or the plaintext credential. If `prepare.sh` says
`age` is missing, install it and run `./prepare.sh` again. Optional SSH-based
variants (`age_send_kubeconfig.sh`, `age_receive_kubeconfig.sh`,
`age_decrypt_kubeconfig.sh --get-from`) and the credential lookup order are in
[`docs/dashboard-setup.md`](docs/dashboard-setup.md#encrypted-kubeconfig-handoff-with-age-default);
the sender side is in [`docs/age-sender.md`](docs/age-sender.md).

## Credential Policy

Do not commit playground credentials, kubeconfigs, S3 keys, bearer tokens, or
registry tokens. This repository currently reports as public on GitHub, and
even private repositories are not safe storage for shared playground
credentials. Use local credential files, environment variables, or a secret
manager.

## Simple Architecture

The system has four main pieces:

- **Dashboard in your browser**: a local web page where you choose a demo
  workflow, start new runs, and inspect recent runs.
- **Workflow definitions**: YAML files in `workflows/` that describe what steps
  to run.
- **Demo models**: FMUs built from Python placeholder code in `create_fmu/`.
  An FMU, or Functional Mock-up Unit, is a packaged simulation model with a
  standard interface. These demo FMUs process synthetic files from
  `data/storhy/synthetic/`.
- **Runner service**: Go code in `orchestrator/service/` that reads a workflow,
  runs the FMUs through FMIL, and reports results. FMIL, or FMI Library, is the
  software library this project uses to load and call FMUs.

For hosted workflow launches, the dashboard tells the Kaizen Argo playground
which published container image to run. GHCR, GitHub Container Registry, is the
place where those workflow images are stored so the Playground can pull them.
For the current demo, the GHCR image is a bundled artifact: it contains the
runner, dependencies, mock models/FMUs, and workflow YAML files.

The service can also run the workflows itself (`--execution local`) instead
of submitting them to Argo, and with a database configured measured data
reaches the models from it; see [`docs/architecture.md`](docs/architecture.md#database-backed-data-interface)
and [`docs/workflows.md`](docs/workflows.md#assessment-windows-and-state-carry-over-data_window-carry_over).

## Useful Commands

```bash
# User path 1: connect to the configured Playground image.
./run_playground.sh

# User path 2: build/publish to Playground, then start the dashboard.
# Builds the hosted Playground platform by default.
./run_publish.sh

# User path 3: build/test one workflow locally with Minikube.
./run_local_dev.sh workflows/tests/python_chain.yaml

# Submit one workflow directly to the hosted playground.
scripts/commands/run_remote.sh workflows/demonstrators/cheylas/maintenance/runner_rul_lockstep.yaml

# Near-realtime demo: local TimescaleDB with the bundled first day ...
scripts/commands/run_db.sh
export CADS_DB_URL=postgres://cads:cads@127.0.0.1:5432/storhy
# ... a dashboard service that runs workflows on this machine (build the
# service and the image first, see docs/user-paths.md) ...
bin/cads-workflow-service --serve --execution local --local-runner-image cads-fmi-demo:dev-arm64
# ... and the next synthetic day, assessed automatically.
scripts/commands/run_db.sh --next --notify

# Build demo FMUs.
./create_fmu/build_python_fmus.sh

# Default credential handoff: receiver creates a public age key.
./scripts/age_create_identity.sh --copy

# Default credential handoff: sender encrypts for that public key.
./scripts/age_encrypt_kubeconfig.sh \
    --recipient age1_receiver_public_key_here \
    --input .local/kaizen/kubeconfig

# Default credential handoff: receiver decrypts the encrypted file.
./scripts/age_decrypt_kubeconfig.sh /path/to/kubeconfig.age

# Optional SSH credential handoff: receiver waits for sender push.
./scripts/age_receive_kubeconfig.sh --send-target USER@HOST --force

# Optional SSH credential handoff: sender pushes to a receiver they can SSH into.
./scripts/age_send_kubeconfig.sh USER@HOST

# Optional SSH credential handoff: receiver fetches from sender over SSH.
./scripts/age_decrypt_kubeconfig.sh --get-from USER@HOST
```

## Main Files

- `run_playground.sh` starts the local dashboard against the configured
  Playground image.
- `run_publish.sh` builds/publishes the bundled Playground image, then starts
  the dashboard. It builds the hosted Playground target platform by default;
  override with `--platform` only when the target cluster architecture changes.
- `run_local_dev.sh` runs one workflow in local Minikube without a dashboard.
- `scripts/commands/` contains lower-level build, local, remote, dashboard, and
  inspection commands for expert use.
- `workflows/` contains workflow YAML definitions.
- `docs/workflows.md` documents the STOR-HY demonstrator workflows and replica model
  catalog.
- `create_fmu/storhy_replicas/` contains Python source for the demo replica
  FMUs.
- `data/storhy/synthetic/` contains synthetic operating cases used by the demo
  workflows.
- `data/storhy/<site>/measurements/` contains the synthetic measurement
  batches (`batch-*/`, plus `upcoming/` days for replay) and the bundled
  fallback CSVs.
- `scripts/commands/run_db.sh` starts a local TimescaleDB and ingests the
  measurement batches; `scripts/ingest_measurements.py` validates and loads one
  batch and can notify the dashboard service.
- `scripts/tests/` holds the Python tests for the data generator and the
  ingest notification.
- `orchestrator/service/` contains the Go/FMIL runner and dashboard service.

## Documentation

- [`docs/user-paths.md`](docs/user-paths.md) – OS-independent supported user paths,
  current support status and the near-realtime demo.
- [`docs/playground-dashboard.md`](docs/playground-dashboard.md) – connect a local
  dashboard to an existing published Playground workflow image (the default path).
- [`docs/dashboard-setup.md`](docs/dashboard-setup.md) – full local dashboard setup
  and credential handling, including the `age` kubeconfig handoff.
- [`docs/age-sender.md`](docs/age-sender.md) – sender-side age encryption workflow for a
  Kaizen kubeconfig.
- [`docs/local-workflow-dev.md`](docs/local-workflow-dev.md) – local Minikube workflow
  development path.
- [`docs/run.md`](docs/run.md) – run pipelines (local, hosted, dashboard,
  near-realtime local flow) and the service flags.
- [`docs/workflows.md`](docs/workflows.md) – STOR-HY demonstrator workflow and model
  mapping, measurement data and assessment windows.
- [`docs/architecture.md`](docs/architecture.md) – system structure, design intent and
  the dashboard reference.
- [`docs/troubleshooting.md`](docs/troubleshooting.md) – common setup and runtime issues.
- [`docs/prepare.md`](docs/prepare.md) – local and remote environment preparation,
  including optional local Minikube.
- [`docs/build.md`](docs/build.md) – image and binary build details.
- [`docs/workflow-publishing-approaches.md`](docs/workflow-publishing-approaches.md) –
  options for separating workflow publishing from runtime/model publishing.
- [`docs/dev.md`](docs/dev.md) – contributor notes.
- [`create_fmu/README.md`](create_fmu/README.md) – FMU generation details.
- [`orchestrator/service/README.md`](orchestrator/service/README.md) – Go
  service and CLI details, routes and flags.

## Future Publishing Layouts

The current demo uses the simplest layout: workflows, models, runner code, and
dependencies are bundled into one GHCR image. That keeps the dashboard demo easy
to run and reduces the number of Playground-side objects to manage.

For partner-facing or production-like use, other layouts may be better. The
main candidate is to keep runtime/model artifacts in GHCR while publishing
approved workflow definitions separately to the Playground, for example as Argo
WorkflowTemplates. That would let a workflow developer update one workflow
without rebuilding the runtime/model image.

See [`docs/workflow-publishing-approaches.md`](docs/workflow-publishing-approaches.md)
for the detailed comparison of alternatives and tradeoffs.
