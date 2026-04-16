# Build Pipeline

`build.sh` is now a pure build step. It is idempotent and safe to rerun whenever
you touch FMUs, Go code, or the Dockerfile. If the repo-local Go toolchain is
missing, the script bootstraps it under `./.local/go` automatically; all other
cluster/registry preparation remains outside `build.sh`.

```bash
./build.sh
./build.sh --image ghcr.io/org/cads-demo:dev --fmil-home "$HOME/fmil"
```

## High-level workflow

1. **Environment detection** – Prepends `./.local/go/bin` and `./.local/bin` to
   `PATH`, bootstraps Go locally when missing, selects the FMIL installation
   (default `./.local` unless you pass `--fmil-home`), and ensures `$FMIL_HOME`
   points to the resolved path.
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

Local cluster preparation now lives in `run_local.sh`, while remote image
publication lives in `prepare_remote.sh`.

## Arguments

| Flag | Description |
|------|-------------|
| `--image <name:tag>` | Override the image tag used for the build. |
| `--fmil-home <path>` | Reuse an existing fmilib installation instead of writing to `./.local`. |
| `-h`, `--help` | Display usage. |

## Outputs

- `bin/cads-workflow-runner` and `bin/cads-workflow-service`
- Container image tagged as the requested name (default `cads-fmi-demo:latest`)
- No cluster-side changes; local Argo/controller setup happens in `run_local.sh`,
  and remote image publication happens in `prepare_remote.sh`

## Troubleshooting

- **Missing toolchains** – Re-run `./prepare_local.sh` or `./prepare_remote.sh`
  to regenerate the local Go/CLI installs. `build.sh` only reuses what already
  exists under `./.local/`.
- **FMIL path issues** – Pass `--fmil-home` pointing to the desired install or
  export `FMIL_HOME` before invoking the script.
- **Container runtime missing** – Install Podman or Docker. `build.sh` needs one
  of them available locally even for remote-only workflows.
