# Troubleshooting

This guide covers the most common issues encountered while running the CADS FMI
demo scripts. Work through the sections in order; each one lists the symptom,
probable cause, and recommended fix.

---

## Host preparation fails

### `age` is missing
- Install `age`, then rerun `./prepare.sh`. Debian/Ubuntu hosts can normally let
  `prepare.sh` install it through `apt`; macOS users can run `brew install age`
  or `sudo port install age`.

### `Required command 'sudo' not found` or `apt-get` omissions
- Ensure you are on a Debian/Ubuntu host with sudo privileges. The script needs
  `sudo apt-get install` to pull Podman and helper packages.

### `Unsupported architecture: ...`
- Only `amd64` and `arm64` are supported today. Add the required checks/tarballs
  in `scripts/lib/tooling.sh` if you need another architecture.

### Minikube refuses to start
- Typical causes: no container runtime, stale profiles, or rootless Podman not
  configured.
  1. Verify `podman info` (or `docker info`) works.
  2. Run `minikube delete -p minikube && ./prepare.sh --with-local-minikube`
     to rebuild the profile.
  3. Check `/etc/subuid` / `/etc/subgid` entries for the current user; Podman
     rootless needs subordinate UID/GID ranges.

---

## Build errors

### `Required command 'go' not found` or Go version mismatch
- Re-run `./prepare.sh` so `./.local/go` is reinstated on PATH. Alternatively,
  install Go >= 1.22 and set `PATH` accordingly before running
  `scripts/commands/build.sh`.

### `FMIL already present` but build still fails
- Corrupted FMIL installation. Run `scripts/commands/clean.sh` to remove
  `.local`, then rerun `./prepare.sh` and `scripts/commands/build.sh`. Or pass
  `--fmil-home /path/to/fmil` to reuse a known-good install.

### `docker` / `podman` not found
- Install one container runtime (Podman preferred). On Debian/Ubuntu,
  `prepare.sh` can install Podman automatically via apt; confirm it’s on PATH
  before rerunning `scripts/commands/build.sh`.
- If you are only connecting to existing Playground workflows, use
  `./run_playground.sh`; that path does not need Podman or Docker.

### `No running container runtime found` or `podman info` fails on macOS
- The Publish to Playground and Local Dev paths need Podman or Docker already
  running. The Playground Dashboard path does not. The Podman Desktop app can
  be open while its engine is still stopped. For Podman:
  ```bash
  ./prepare.sh
  ```
- `podman info` must work from the same shell as `./run_publish.sh` or
  `./run_local_dev.sh`. If
  `podman machine start` claims success but `podman info` still reports
  connection refused, the Podman VM/socket is stale; restart or recreate the
  Podman machine before rerunning the repo scripts.

### Podman pull fails with `x509: certificate signed by unknown authority`
- The TLS error happens inside the Podman VM before bootstrap containers start.
  Put your corporate CA `.crt`/`.pem` files under `scripts/certs/`, then run:
  ```bash
  scripts/install_podman_ca.sh --cert-dir scripts/certs
  podman pull docker.io/library/python:3.11-slim
  ```
- If your certs are in another directory, pass it explicitly:
  ```bash
  scripts/install_podman_ca.sh --cert-dir /path/to/certs
  ```

---

## Local Dev or local Argo submission problems

### `Current kubectl context is ...`
- The local command path only targets the local Minikube flow. Start Minikube
  if needed and run `kubectl config use-context minikube`.

### `Workflow file not found`
- Run `./run_local_dev.sh` from the repo root and pass relative paths (e.g.,
  `workflows/python_chain.yaml`).

### `argo submit` fails because CRDs are missing
- Re-run `./run_local_dev.sh`; it installs Argo via
  `scripts/ensure_argo_workflows.sh` before submitting. Alternatively, run that
  script manually to reinstall the controller.

### PVC-related errors
- Delete the PVC and rebuild it with `./run_local_dev.sh`:
  ```bash
  kubectl delete pvc cads-data-pvc -n argo
  ./run_local_dev.sh workflows/python_chain.yaml
  ```
  Adjust storage class/size inside `scripts/generate_manifests.sh` if your cluster
  requires custom settings.

---

## Playground or hosted Argo problems

### `Unable to resolve an Argo token`
- Pass `--kubeconfig /path/to/kubeconfig` or export `ARGO_TOKEN`.
- The browser UI expects `Bearer <token>`, but the CLI scripts normalize that
  prefix automatically if you reused the same value in `ARGO_TOKEN`.

### Remote submission fails before the workflow starts
- Re-run `./run_playground.sh --kubeconfig ...` to validate Argo access.
- If publishing, confirm the image tag in `config/playground.env` is the same
  tag you built and published with `./run_publish.sh`.

### GHCR push fails with `403 Forbidden`
- Run `./run_publish.sh` again to refresh the local container-runtime login for
  `ghcr.io`.
- If using environment tokens, set both `GHCR_USERNAME` and `GHCR_TOKEN`; the
  token must have package write access for the target image owner.

---

## Cleanup observations

- The script removes `.local`, cached artifacts, container images, and deletes
  the Minikube profile. Run it when you need to test the full flow from scratch.
- If it logs `minikube command not found`, rerun `./prepare.sh` to reinstall the
  CLI before running `scripts/commands/clean.sh` again (or ignore if Minikube
  is already absent).

---

## `kubectl` TLS errors outside Minikube

### `tls: failed to verify certificate: x509: certificate signed by unknown authority`
- Corporate TLS proxies re-sign outbound traffic, so external clusters (e.g., the Kaizen playground) require your corporate CA when running `kubectl` directly from the host.
- Run `source scripts/host_ca_env.sh` once per shell. The script targets `scripts/certs/` and `.local/` under the repo by default, but you can point it at another folder (e.g., `Kaizen_CADS`) with `source scripts/host_ca_env.sh "$PWD"` or `CADS_HOST_CA_ROOT=/path/to/dir source scripts/host_ca_env.sh`. If the chosen cert directory already contains `.crt/.pem` files, the script concatenates them and exports `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, and `GIT_SSL_CAINFO`. When the folder is empty, it attempts to run `scripts/export_company_certs.py` automatically to harvest the certs from your OS trust store before exporting the bundle.
- Run your command normally afterwards, e.g.:
  ```bash
  cd ~/Kaizen_CADS
  KUBECONFIG=./kubeconfig kubectl get pods
  ```
  Any `kubectl`, `helm`, or `curl` invocation in that shell will reuse the injected CA bundle, eliminating the x509 error.

---

## General tips

- Always run scripts from the repo root; many paths are relative.
- Keep `./scripts/certs/` populated with your corporate CA certs when working
  behind TLS-inspecting proxies. `scripts/commands/run_local.sh` syncs them
  into Minikube, and the Playground commands source the host CA helper before
  remote Argo calls.
- When debugging deeper issues, check the log files under `minikube logs` and
  `kubectl get events -n argo`.
