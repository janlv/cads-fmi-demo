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

See [`USER_PATHS.md`](USER_PATHS.md) for the support matrix.

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

## Start The Dashboard

These steps are for a non-expert user who wants to bring up the dashboard on
their own machine.

Clone the repository:

```bash
git clone https://github.com/janlv/cads-fmi-demo.git
cd cads-fmi-demo
```

Prepare the host:

```bash
./prepare.sh
```

If `prepare.sh` says `age` is missing, install it and run `./prepare.sh` again:

```bash
# macOS with Homebrew
brew install age

# Debian/Ubuntu
sudo apt-get update
sudo apt-get install -y age
```

`age` is used only to exchange the Kaizen kubeconfig safely. The kubeconfig is
the credential that lets the dashboard talk to the hosted playground.

Podman or Docker is only needed for the **Publish to Playground** and
**Local Dev** paths. It is not needed for the default
**Playground Dashboard** path.

Create your local age identity and send the printed public key to the person
who has the Kaizen kubeconfig:

```bash
./scripts/age_create_identity.sh
```

Then send the public key to the credential sender using one of these options.

Open a prefilled email draft:

```bash
./scripts/age_create_identity.sh --mailto sender@example.com
```

Copy the key to your clipboard and paste it into your preferred message channel:

```bash
./scripts/age_create_identity.sh --copy
```

Send the key directly to the sender's machine over SSH:

```bash
./scripts/age_create_identity.sh --send-to sender_user@sender_host
```

The SSH command sends only the public key. It uses the normal `ssh` password
prompt when needed and saves the key on the remote host as
`~/.config/cads/age-recipient.txt`, which the sender can pass to
`age_encrypt_kubeconfig.sh --recipient-file`.

The public key starts with `age1...` and is safe to share. The private key stays
on the dashboard machine in `~/.config/age/key.txt` by default and must not be
shared. The script also stores the public key at
`~/.config/cads/age-recipient.txt`; the decrypt helper uses that file when
fetching credentials from a remote host. If that public key file is missing but
the private key exists, the decrypt helper recreates it locally.

When you receive the encrypted kubeconfig file, decrypt it:

```bash
./scripts/age_decrypt_kubeconfig.sh ~/Downloads/kubeconfig.age
```

If the sender has the plaintext kubeconfig on their machine and you have SSH
access, encrypt it remotely and decrypt it locally:

```bash
./scripts/age_decrypt_kubeconfig.sh --get-from sender_user@sender_host
```

This uses the stored public key, asks `ssh` for the remote password if needed,
and runs `age` on the sender host. The remote kubeconfig path defaults to
`.local/kaizen/kubeconfig` relative to the sender's login directory. Use
`--remote-path /path/to/kubeconfig` if the sender stores it somewhere else.

This writes the dashboard kubeconfig to `.local/kaizen/kubeconfig` in this
checkout.

Start the dashboard against the configured Playground image:

```bash
./run_playground.sh
```

Open:

```text
http://localhost:8080/
```

The default image tag lives in `config/playground.env`. If you need to test a
different published image, pass it explicitly:

```bash
./run_playground.sh --image ghcr.io/org/cads-fmi-demo:tag
```

For most users, the commands above are enough. Use the detailed documents when
you need to troubleshoot or customize the flow:

- [`USER_PATHS.md`](USER_PATHS.md) for the three supported user paths.
- [`PLAYGROUND_DASHBOARD.md`](PLAYGROUND_DASHBOARD.md) for the default
  Playground Dashboard path.
- [`DASHBOARD_SETUP.md`](DASHBOARD_SETUP.md) for the full dashboard setup.
- [`LOCAL_WORKFLOW_DEV.md`](LOCAL_WORKFLOW_DEV.md) for local Minikube workflow
  development.
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) for common failures.
- [`PREPARE.md`](PREPARE.md) for setup details and optional local Minikube.
- [`RUN.md`](RUN.md) for direct workflow submission without the dashboard.
- [`WORKFLOW_PUBLISHING_APPROACHES.md`](WORKFLOW_PUBLISHING_APPROACHES.md) for
  workflow/model publishing architecture options.
- [`AGE_SENDER.md`](AGE_SENDER.md) if you are sending the kubeconfig to someone
  else.

## Credential Policy

Do not commit playground credentials, kubeconfigs, S3 keys, bearer tokens, or
registry tokens. This repository currently reports as public on GitHub, and
even private repositories are not safe storage for shared playground
credentials. Use environment variables, `.local/kaizen/kubeconfig`, or a secret
manager.

## Useful Commands

```bash
# User path 1: connect to the configured Playground image.
./run_playground.sh

# User path 2: build/publish to Playground, then start the dashboard.
./run_publish.sh

# User path 3: build/test one workflow locally with Minikube.
./run_local_dev.sh workflows/python_chain.yaml

# Submit one workflow directly to the hosted playground.
scripts/commands/run_remote.sh workflows/demonstrators/la_rance/maintenance/cleaning_interval.yaml

# Build demo FMUs.
./create_fmu/build_python_fmus.sh

# Receiver: create an age identity and print the public recipient key.
./scripts/age_create_identity.sh

# Receiver: open a prefilled email draft for sending the public key.
./scripts/age_create_identity.sh --mailto sender@example.com

# Receiver: send the public key to a remote SSH account.
./scripts/age_create_identity.sh --send-to sender_user@sender_host

# Receiver: decrypt the encrypted kubeconfig into .local/kaizen/kubeconfig.
./scripts/age_decrypt_kubeconfig.sh ~/Downloads/kubeconfig.age

# Receiver: encrypt the remote kubeconfig over SSH and decrypt it locally.
./scripts/age_decrypt_kubeconfig.sh --get-from sender_user@sender_host
```

## Main Files

- `run_playground.sh` starts the local dashboard against the configured
  Playground image.
- `run_publish.sh` builds/publishes the bundled image, then starts the
  dashboard.
- `run_local_dev.sh` runs one workflow in local Minikube without a dashboard.
- `scripts/commands/` contains lower-level build, local, remote, dashboard, and
  inspection commands for expert use.
- `workflows/` contains workflow YAML definitions.
- `workflows.md` documents the STOR-HY demonstrator workflows and replica model
  catalog.
- `create_fmu/storhy_replicas/` contains Python source for the demo replica
  FMUs.
- `data/storhy/synthetic/` contains synthetic operating cases used by the demo
  workflows.
- `orchestrator/service/` contains the Go/FMIL runner and dashboard service.

## Documentation

- [`AGE_SENDER.md`](AGE_SENDER.md) – sender-side age encryption workflow for a
  Kaizen kubeconfig.
- [`USER_PATHS.md`](USER_PATHS.md) – OS-independent supported user paths and
  current support status.
- [`PLAYGROUND_DASHBOARD.md`](PLAYGROUND_DASHBOARD.md) – connect a local
  dashboard to an existing published Playground workflow image.
- [`LOCAL_WORKFLOW_DEV.md`](LOCAL_WORKFLOW_DEV.md) – local Minikube workflow
  development path.
- [`DASHBOARD_SETUP.md`](DASHBOARD_SETUP.md) – local dashboard setup and
  credential handling.
- [`workflows.md`](workflows.md) – STOR-HY demonstrator workflow and model
  mapping.
- [`RUN.md`](RUN.md) – direct workflow submission paths and runtime behavior.
- [`BUILD.md`](BUILD.md) – image and binary build details.
- [`WORKFLOW_PUBLISHING_APPROACHES.md`](WORKFLOW_PUBLISHING_APPROACHES.md) –
  options for separating workflow publishing from runtime/model publishing.
- [`PREPARE.md`](PREPARE.md) – local and remote environment preparation.
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) – common setup and runtime issues.
- [`ARCHITECTURE.md`](ARCHITECTURE.md) – system structure and design intent.
- [`DEV.md`](DEV.md) – contributor notes.
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

See [`WORKFLOW_PUBLISHING_APPROACHES.md`](WORKFLOW_PUBLISHING_APPROACHES.md)
for the detailed comparison of alternatives and tradeoffs.
