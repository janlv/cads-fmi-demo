# Environment Preparation

The repository now exposes separate local and remote preparation paths:

```bash
./prepare_local.sh
./prepare_remote.sh --image ghcr.io/org/cads-demo:demo123 --kubeconfig ~/Kaizen_CADS/kubeconfig
```

## Local preparation

`prepare_local.sh` keeps the local Minikube demo self-contained on Debian/Ubuntu
hosts. It installs Podman and a few helper packages, downloads the Go toolchain
plus the required CLIs (Argo, kubectl, Minikube) into `./.local/`, and starts a
rootless Minikube profile named `minikube`.

1. Installs the package list in `scripts/package-lists/linux-apt.txt` using
   `sudo apt-get`. The list currently contains Podman and helper tools used by
   Minikube.
2. Downloads Go `1.22.2` and extracts it to `./.local/go`. All repo scripts
   prepend `./.local/go/bin` to `PATH`, so no shell config changes are required.
3. Fetches the Argo CLI (`v3.5.6`), `kubectl` (`v1.30.0`), and Minikube
   (`v1.33.1`) directly from their upstream release URLs and installs them to
   `./.local/bin`.
4. Starts Minikube with the Podman driver when available (falls back to Docker
   otherwise) so the demo always runs against a clean local cluster.

## Remote preparation

`prepare_remote.sh` is the minimal hosted-playground preflight. It prepares a
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
bundles under `scripts/certs/` (`.crt`/`.pem` files) and `build.sh` will sync
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

Once those prerequisites exist, `build.sh`, `run_local.sh`, and `run_remote.sh`
can execute without touching system packages.
