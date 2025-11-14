# Troubleshooting

This guide covers the most common issues encountered while running the CADS FMI
demo scripts. Work through the sections in order; each one lists the symptom,
probable cause, and recommended fix.

---

## `./prepare.sh` fails

### `prepare.sh currently supports Linux hosts only.`
- The script relies on Debian/Ubuntu tools (`apt`, rootless Podman). Run it on a
  Linux machine or follow `PREPARE.md` to reproduce the steps manually on your OS.

### `Required command 'sudo' not found` or `apt-get` omissions
- Ensure you are on a Debian/Ubuntu host with sudo privileges. The script needs
  `sudo apt-get install` to pull Podman and helper packages.

### `Unsupported architecture: ...`
- Only `amd64` and `arm64` are supported today. Add the required checks/tarballs
  in `prepare.sh` if you need another architecture.

### Minikube refuses to start
- Typical causes: no container runtime, stale profiles, or rootless Podman not
  configured.
  1. Verify `podman info` (or `docker info`) works.
  2. `minikube delete -p minikube && ./prepare.sh` to rebuild the profile.
  3. Check `/etc/subuid` / `/etc/subgid` entries for the current user; Podman
     rootless needs subordinate UID/GID ranges.

---

## `./build.sh` errors

### `Required command 'go' not found` or Go version mismatch
- Re-run `./prepare.sh` so `./.local/go` is reinstated on PATH. Alternatively,
  install Go ≥ 1.22 and set `PATH` accordingly before running `build.sh`.

### `FMIL already present` but build still fails
- Corrupted FMIL installation. Run `./clean.sh` to remove `.local`, then rerun
  `./prepare.sh` and `./build.sh`. Or pass `--fmil-home /path/to/fmil` to reuse
  a known-good install.

### `docker` / `podman` not found
- Install one container runtime (Podman preferred). `prepare.sh` installs
  Podman automatically via apt; confirm it’s on PATH before rerunning `build.sh`.

### Minikube image load failures
- The script falls back to streaming via `podman save`/`docker save`. If that
  also fails, run `minikube image load -p minikube cads-fmi-demo:latest` after
  the build or allow the cluster to pull the tag from a registry you control.

### `scripts/install_minikube_ca.sh` warnings
- Happens when `scripts/certs/` is empty or Minikube isn’t running. Ensure your
  Minikube profile is up (`minikube status -p minikube`) before building.

---

## `./run.sh` or Argo submission problems

### `kubectl cannot determine the current context.`
- Minikube isn’t running or you switched contexts. Start Minikube (`minikube start`)
  and run `kubectl config use-context minikube`.

### `Workflow file not found`
- Run `./run.sh` from the repo root and pass relative paths (e.g.,
  `workflows/python_chain.yaml`).

### `argo submit` fails because CRDs are missing
- Re-run `./build.sh`; it installs Argo via `scripts/ensure_argo_workflows.sh`.
  Alternatively, run that script manually to reinstall the controller.

### PVC-related errors
- Delete the PVC and rebuild it with `./run.sh`:
  ```bash
  kubectl delete pvc cads-data-pvc -n argo
  ./run.sh workflows/python_chain.yaml
  ```
  Adjust storage class/size inside `scripts/generate_manifests.sh` if your cluster
  requires custom settings.

---

## `./clean.sh` observations

- The script removes `.local`, cached artifacts, container images, and deletes
  the Minikube profile. Run it when you need to test the full flow from scratch.
- If it logs `minikube command not found`, rerun `./prepare.sh` to reinstall the
  CLI before running `clean.sh` again (or ignore if Minikube is already absent).

---

## General tips

- Always run scripts from the repo root; many paths are relative.
- Keep `./scripts/certs/` populated with your corporate CA certs when working
  behind TLS-inspecting proxies. `build.sh` syncs them into Minikube.
- When debugging deeper issues, check the log files under `minikube logs` and
  `kubectl get events -n argo`.
