# Environment Preparation

`prepare.sh` keeps the demo self-contained on Debian/Ubuntu hosts. It installs
Podman and a few helper packages, downloads the Go toolchain alongside the
required CLIs (Argo, kubectl, Minikube) into `./.local/`, and starts a rootless
Minikube profile named `minikube`. Use it before running `build.sh`.

```
./prepare.sh              # install everything and start Minikube
```

## What the script does

1. Installs the package list in `scripts/package-lists/linux-apt.txt` using
   `sudo apt-get`. The list currently contains Podman and a handful of helper
   tools used by Minikube.
2. Downloads Go `1.22.2` and extracts it to `./.local/go`. All repo scripts
   prepend `./.local/go/bin` to `PATH`, so no shell config changes are required.
3. Fetches the Argo CLI (`v3.5.6`), `kubectl` (`v1.30.0`), and Minikube
   (`v1.33.1`) directly from their upstream release URLs and installs them to
   `./.local/bin`.
4. Starts Minikube with the Podman driver when available (falls back to Docker
   otherwise) so the demo always runs against a clean local cluster.

Corporate TLS inspection can break image pulls. Drop any required certificate
bundles under `scripts/certs/` (`.crt`/`.pem` files) and `build.sh` will sync
them into the Minikube VM before loading the container image.

## Manual alternatives

When running on non-Debian hosts or locked-down machines, reproduce the steps
manually:

- Install Podman + dependencies (or Docker if you prefer that driver).
- Install Go ≥ 1.22 and ensure `go` is on your `PATH`.
- Download the Argo CLI, `kubectl`, and Minikube binaries and place them on
  your `PATH`.
- Start Minikube and keep the context active (`kubectl config current-context`
  should return the desired cluster).

Once those prerequisites exist, `build.sh` and `run.sh` can execute without
touching system packages.
