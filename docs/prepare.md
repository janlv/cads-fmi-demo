# Environment Preparation

The repository has one main host preparation path:

```bash
./prepare.sh
./run_publish.sh
```

`prepare.sh` auto-detects Linux vs macOS but keeps the workflow the same on both
systems. It installs repo-local Go, Argo CLI, and kubectl under `./.local/` and
checks that `age` is available for encrypted kubeconfig exchange.

## Host preparation

The default path is intentionally lean. It prepares the hosted dashboard and
remote workflow client tooling without requiring a container runtime or starting
a local cluster:

```bash
./prepare.sh
```

When you also want a fully local Minikube demo loop, add one option on either
Linux or macOS:

```bash
./prepare.sh --with-local-minikube
```

On Debian/Ubuntu, `prepare.sh` can install the minimal packages listed in
`scripts/package-lists/linux-dashboard-apt.txt` using `sudo apt-get`. When
`--with-local-minikube` or `--require-container-runtime` is used, it installs
the broader local/build package list in `scripts/package-lists/linux-apt.txt`.
On macOS, install small host tools such as `age`, Git, and Podman/Docker with
Homebrew, MacPorts, or your normal desktop installer.

1. Installs/checks small host prerequisites where the OS supports it.
2. Downloads the configured Go version and extracts it to `./.local/go`. All repo scripts
   prepend `./.local/go/bin` to `PATH`, so no shell config changes are required.
3. Fetches the configured Argo CLI and `kubectl` versions directly from
   their upstream release URLs and installs them to `./.local/bin`.
4. Verifies Podman/Docker only when you pass `--require-container-runtime` or
   `--with-local-minikube`.
5. Installs and starts Minikube only when `--with-local-minikube` is supplied.

The default tool versions are defined in `config/tool-versions.env`. Update that
file when the project intentionally moves to a newer Go, Argo, kubectl,
Minikube, or FMIL reference.

For the normal **Playground** path, continue with
`./run_playground.sh`. Build/publish work is the separate
**Publish to Playground** path and requires Podman or Docker.

If your network performs TLS inspection, Podman image pulls may fail before the
bootstrap container can start. Place corporate CA `.crt`/`.pem` files under
`scripts/certs/`, then run:

```bash
scripts/install_podman_ca.sh --cert-dir scripts/certs
podman pull docker.io/library/python:3.11-slim
```

## Remote preparation

If you need to publish freshly built hosted images, use `./run_publish.sh`.
It wraps GHCR authentication, remote preparation, and dashboard launch. The
lower-level commands live under `scripts/commands/` for expert use.

`scripts/commands/prepare_remote.sh` is the minimal hosted-playground preflight. It prepares a
hosted image tag for the Kaizen playground and can auto-generate the remote tag
when `--image` is omitted.

1. Ensures Go, Argo CLI, and kubectl are available under `./.local/`.
2. Resolves the Argo bearer token from `ARGO_TOKEN` or the supplied kubeconfig.
3. Sources the corporate CA helper (`scripts/host_ca_env.sh`) before networked
   Argo calls so TLS-inspecting proxies do not break authentication.
4. Validates access through the Argo server with `argo list ... --argo-http1`.
5. Resolves the source image from the latest local build when needed, retags it
   to the hosted image name, and publishes it through the locally available
   container engine via `scripts/publish_image.sh`.

Corporate TLS inspection can break image pulls. Drop any required certificate
bundles under `scripts/certs/` (`.crt`/`.pem` files) and `scripts/commands/build.sh` will sync
them into the Minikube VM before loading the container image. Host-side tools
(`kubectl`, `helm`, etc.) can reuse the same bundle by running
`source scripts/host_ca_env.sh` (optionally pass another target directory, e.g.,
`source scripts/host_ca_env.sh "$PWD"` or set `CADS_HOST_CA_ROOT=/path`). The helper
also attempts to harvest certificates automatically via `scripts/export_company_certs.py`
when the folder is empty.

## Manual alternatives

When running on non-Debian hosts or locked-down machines, reproduce the steps
manually:

- Install Podman + dependencies (or Docker if you prefer that driver) for local
  Minikube work, or only the Argo/kubectl CLIs for remote-only usage.
- Install Go ≥ 1.22 and ensure `go` is on your `PATH`.
- Download the Argo CLI, `kubectl`, and Minikube binaries and place them on
  your `PATH` for local work, or at least Argo + kubectl for remote work.
- Start Minikube and keep the context active (`kubectl config current-context`
  should return `minikube`) if you are using the local flow.
- For remote usage, make sure you have either `ARGO_TOKEN` set or a kubeconfig
  that contains the playground token.

Once those prerequisites exist, `scripts/commands/build.sh`,
`scripts/commands/run_local.sh`, and `scripts/commands/run_remote.sh` can
execute without touching system packages.
