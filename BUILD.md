# Build Pipeline

`build.sh` assembles everything needed to run the CADS FMI demo inside Argo on
Minikube. It assumes you already ran `./prepare.sh` on a Debian/Ubuntu host so
the local toolchain (`./.local/go`, `./.local/bin/argo`, …) and the Minikube
profile exist. The script is idempotent and safe to rerun whenever you touch
FMUs, Go code, or the Dockerfile.

```bash
./build.sh
./build.sh --image ghcr.io/org/cads-demo:dev --fmil-home "$HOME/fmil"
```

## High-level workflow

1. **Environment detection** – Prepends `./.local/go/bin` and `./.local/bin` to
   `PATH`, selects the FMIL installation (default `./.local` unless you pass
   `--fmil-home`), and ensures `$FMIL_HOME` points to the resolved path.
2. **FMIL toolchain** – If `${FMIL_HOME}/include/FMI` or the fmilib shared
   library is missing, invokes `scripts/install_fmil.sh --prefix "$FMIL_HOME"` to
   clone, build, and install fmilib.
3. **pythonfmu resources** – Runs `scripts/install_platform_resources.py`
   (streamed via `log_stream_cmd`). This script bootstraps the cached
   `pythonfmu` resources for the active architecture by spinning up a temporary
   Docker image when the cache is missing.
4. **Go binaries** – Builds `cads-workflow-runner` and `cads-workflow-service`
   with cgo enabled (FMIL headers/libraries are wired via `CGO_*` exports). The
   binaries land in `bin/`.
5. **Container image** – Prefers Podman but falls back to Docker; whichever tool
   is found builds the Dockerfile into the requested tag (`cads-fmi-demo:latest`
   by default). The build context is the repo root.
6. **Minikube CA sync** – Calls `scripts/install_minikube_ca.sh`. Every `.crt`
   or `.pem` under `scripts/certs/` is copied into the Minikube VM so corporate
   MITM certificates are trusted before Argo pulls the image.
7. **Argo controller** – Executes `scripts/ensure_argo_workflows.sh` to install
   (or verify) the Argo Workflows CRD and controller in the `argo` namespace.
8. **Image preload** – Attempts `minikube image load -p minikube <tag>`. If the
   direct load fails, streams the image from Podman/Docker into Minikube. When
   everything fails, Argo will still pull the tag, but it may hit the registry.

## Arguments

| Flag | Description |
|------|-------------|
| `--image <name:tag>` | Override the image tag used for the build and the subsequent Minikube preload. |
| `--fmil-home <path>` | Reuse an existing fmilib installation instead of writing to `./.local`. |
| `-h`, `--help` | Display usage. |

## Outputs

- `bin/cads-workflow-runner` and `bin/cads-workflow-service`
- Container image tagged as the requested name (default `cads-fmi-demo:latest`)
- Argo workflow controller installed/verified in Minikube’s `argo` namespace
- `deploy/argo/<workflow>-workflow.yaml` and `deploy/storage/data-pvc.yaml`
  refreshed the next time `run.sh` is executed (build generates none itself, but
  it ensures the PVC/image prerequisites for submission are in place)

## Troubleshooting

- **Missing toolchains** – Re-run `./prepare.sh` to regenerate the local Go/CLI
  installs. `build.sh` only reuses what already exists under `./.local/`.
- **FMIL path issues** – Pass `--fmil-home` pointing to the desired install or
  export `FMIL_HOME` before invoking the script.
- **Minikube errors** – Ensure `minikube status -p minikube` reports `Running`.
  Use `./clean.sh` followed by `./prepare.sh` to rebuild a broken profile from
  scratch.
