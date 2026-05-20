# CADS FMI Dashboard Demo

This repository contains a mock STOR-HY/CADS dashboard prototype. It is meant
for demonstration and integration testing, not for production analysis.

The dashboard uses simple placeholder models and synthetic local data. Those
models imitate the shape of a CADS workflow, but they do not represent validated
engineering, forecasting, or operational models.

The repo is organized around three OS-independent user paths:

- **Playground Dashboard**: connect a local dashboard to an existing Kaizen
  Playground image. This is the default path for most users.
- **Publish to Playground**: build locally, publish the workflow image to GHCR,
  and connect the dashboard to the Playground.
- **Local Dev**: build and test one workflow/model quickly in local Minikube
  without Kaizen Playground, GHCR publishing, or a dashboard.

See [`docs/user-paths.md`](docs/user-paths.md) for the support matrix.

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

3. Set up Kaizen credentials using the default handoff. First, on the checkout
   that should host the dashboard, run:

   ```bash
   ./scripts/age_receive_kubeconfig.sh --send-target javi@osl-1012 --force
   ```

   Leave that command running. Then, on the checkout that already has the
   Kaizen credentials, run:

   ```bash
   ./scripts/age_send_kubeconfig.sh javi@osl-1012
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

The dashboard needs access to the hosted Kaizen Argo playground. The default
handoff securely transfers the Kaizen kubeconfig from a checkout that already
has it to the checkout that should run the dashboard.

If `prepare.sh` says `age` is missing, install it with your system package
manager and run `./prepare.sh` again.

### Default: Sender Pushes To Receiver

Use this when one machine already has the Kaizen kubeconfig and can SSH to the
machine that needs it. Replace `javi@osl-1012` with the SSH target for the
machine that will run the dashboard when needed.

Run the receiver first on the machine that needs the credential:

```bash
./scripts/age_receive_kubeconfig.sh --send-target javi@osl-1012 --force
```

Leave it running. It prepares the receiver and waits for the encrypted
kubeconfig.

Run the sender on the checkout that already has the Kaizen kubeconfig:

```bash
./scripts/age_send_kubeconfig.sh javi@osl-1012
```

When the receiver prints that the kubeconfig is ready, start the dashboard with
`./run_playground.sh`.

For most users, the commands above are enough. Use the detailed documents when
you need to troubleshoot, use a different credential source, or customize the
flow:

- [`docs/user-paths.md`](docs/user-paths.md) for the three supported user paths.
- [`docs/playground-dashboard.md`](docs/playground-dashboard.md) for the default
  Playground Dashboard path.
- [`docs/dashboard-setup.md`](docs/dashboard-setup.md) for the full dashboard setup.
- [`docs/local-workflow-dev.md`](docs/local-workflow-dev.md) for local Minikube workflow
  development.
- [`docs/troubleshooting.md`](docs/troubleshooting.md) for common failures.
- [`docs/prepare.md`](docs/prepare.md) for setup details and optional local Minikube.
- [`docs/run.md`](docs/run.md) for direct workflow submission without the dashboard.
- [`docs/workflow-publishing-approaches.md`](docs/workflow-publishing-approaches.md) for
  workflow/model publishing architecture options.
- [`docs/age-sender.md`](docs/age-sender.md) if you are sending the kubeconfig to someone
  else.

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
scripts/commands/run_remote.sh workflows/demonstrators/la_rance/maintenance/cleaning_interval.yaml

# Build demo FMUs.
./create_fmu/build_python_fmus.sh

# Default credential handoff: run this first on the receiver.
./scripts/age_receive_kubeconfig.sh --send-target receiver_user@receiver_host

# Default credential handoff: run this second on the sender.
./scripts/age_send_kubeconfig.sh receiver_user@receiver_host

# Alternate credential handoff: receiver fetches from sender over SSH.
./scripts/age_decrypt_kubeconfig.sh --get-from sender_user@sender_host
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
- `orchestrator/service/` contains the Go/FMIL runner and dashboard service.

## Documentation

- [`docs/age-sender.md`](docs/age-sender.md) – sender-side age encryption workflow for a
  Kaizen kubeconfig.
- [`docs/user-paths.md`](docs/user-paths.md) – OS-independent supported user paths and
  current support status.
- [`docs/playground-dashboard.md`](docs/playground-dashboard.md) – connect a local
  dashboard to an existing published Playground workflow image.
- [`docs/local-workflow-dev.md`](docs/local-workflow-dev.md) – local Minikube workflow
  development path.
- [`docs/dashboard-setup.md`](docs/dashboard-setup.md) – local dashboard setup and
  credential handling.
- [`docs/workflows.md`](docs/workflows.md) – STOR-HY demonstrator workflow and model
  mapping.
- [`docs/run.md`](docs/run.md) – direct workflow submission paths and runtime behavior.
- [`docs/build.md`](docs/build.md) – image and binary build details.
- [`docs/workflow-publishing-approaches.md`](docs/workflow-publishing-approaches.md) –
  options for separating workflow publishing from runtime/model publishing.
- [`docs/prepare.md`](docs/prepare.md) – local and remote environment preparation.
- [`docs/troubleshooting.md`](docs/troubleshooting.md) – common setup and runtime issues.
- [`docs/architecture.md`](docs/architecture.md) – system structure and design intent.
- [`docs/dev.md`](docs/dev.md) – contributor notes.
- [`create_fmu/README.md`](create_fmu/README.md) – FMU generation details.
- [`orchestrator/service/README.md`](orchestrator/service/README.md) – Go
  service and CLI details.

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
