# CADS FMI Dashboard Demo

This repository contains a STOR-HY/CADS workflow dashboard prototype. The local
dashboard lets a user choose a demonstrator site, launch a workflow into the
Kaizen Argo playground, and inspect recent runs and structured outputs.

The workflows are declared as YAML files under `workflows/` and executed by the
Go/FMIL runner against FMUs in `fmu/models/`. Python is used only to build the
demo FMUs, including the STOR-HY replica models.

## Start The Dashboard

Clone the repository:

```bash
git clone https://github.com/janlv/cads-fmi-demo.git
cd cads-fmi-demo
```

Install `age` if it is not already available:

```bash
sudo apt install age
# macOS: brew install age
```

Create your local age identity and send the printed public recipient key to the
person who has the Kaizen kubeconfig:

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
in `~/.config/age/key.txt` by default and must not be shared.

When you receive the encrypted kubeconfig file, decrypt it:

```bash
./scripts/age_decrypt_kubeconfig.sh ~/Downloads/kubeconfig.age
```

This writes the dashboard kubeconfig to `~/Kaizen_CADS/kubeconfig`.

Start the dashboard:

```bash
./run_dashboard.sh
```

Open:

```text
http://localhost:8080/
```

For a fresh checkout, build the bundled demo FMUs if needed:

```bash
./create_fmu/build_python_fmus.sh
```

If the dashboard needs to publish a new workflow image, authenticate GHCR
locally first:

```bash
gh auth login -h github.com -s write:packages
```

See [`DASHBOARD_SETUP.md`](DASHBOARD_SETUP.md) for the full local dashboard
setup, other credential options, and troubleshooting steps. If you are the
person sending the encrypted kubeconfig, use [`AGE_SENDER.md`](AGE_SENDER.md).

## Credential Policy

Do not commit playground credentials, kubeconfigs, S3 keys, bearer tokens, or
registry tokens. This repository currently reports as public on GitHub, and
even private repositories are not safe storage for shared playground
credentials. Use environment variables, `~/Kaizen_CADS/kubeconfig`, or a secret
manager.

## Useful Commands

```bash
# Start the local dashboard for the remote playground.
./run_dashboard.sh

# Force a fresh remote image build and publish before starting.
./run_dashboard.sh --prepare-remote

# Start the dashboard without remote image preparation.
./run_dashboard.sh --no-prepare-remote

# Submit one workflow directly to the hosted playground.
./run_remote.sh workflows/demonstrators/la_rance/maintenance/cleaning_interval.yaml

# Build demo FMUs.
./create_fmu/build_python_fmus.sh

# Receiver: create an age identity and print the public recipient key.
./scripts/age_create_identity.sh

# Receiver: open a prefilled email draft for sending the public key.
./scripts/age_create_identity.sh --mailto sender@example.com

# Receiver: send the public key to a remote SSH account.
./scripts/age_create_identity.sh --send-to sender_user@sender_host

# Receiver: decrypt the encrypted kubeconfig into ~/Kaizen_CADS/kubeconfig.
./scripts/age_decrypt_kubeconfig.sh ~/Downloads/kubeconfig.age
```

## Main Files

- `run_dashboard.sh` starts the local dashboard.
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
- [`DASHBOARD_SETUP.md`](DASHBOARD_SETUP.md) – local dashboard setup and
  credential handling.
- [`workflows.md`](workflows.md) – STOR-HY demonstrator workflow and model
  mapping.
- [`RUN.md`](RUN.md) – direct workflow submission paths and runtime behavior.
- [`BUILD.md`](BUILD.md) – image and binary build details.
- [`PREPARE.md`](PREPARE.md) – local and remote environment preparation.
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) – common setup and runtime issues.
- [`ARCHITECTURE.md`](ARCHITECTURE.md) – system structure and design intent.
- [`DEV.md`](DEV.md) – contributor notes.
- [`create_fmu/README.md`](create_fmu/README.md) – FMU generation details.
- [`orchestrator/service/README.md`](orchestrator/service/README.md) – Go
  service and CLI details.
