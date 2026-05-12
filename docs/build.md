# Build Pipeline

`scripts/commands/build.sh` is now a pure build step. It is idempotent and safe to rerun whenever
you touch FMUs, Go code, or the Dockerfile. If the repo-local Go toolchain is
missing, the script bootstraps it under `./.local/go` automatically; all other
cluster/registry preparation remains outside the build command.

```bash
scripts/commands/build.sh
scripts/commands/build.sh --image ghcr.io/org/cads-demo:dev --fmil-home "$HOME/fmil"
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
   Docker/Podman container when the cache is missing.
4. **Go binaries** – Builds `cads-workflow-runner` and `cads-workflow-service`
   with cgo enabled (FMIL headers/libraries are wired via `CGO_*` exports). The
   binaries land in `bin/`.
5. **Container image** – Prefers a running Podman runtime but falls back to a
   running Docker runtime; whichever tool is reachable builds the Dockerfile
   into the requested tag (`cads-fmi-demo:latest` by default). The build context
   is the repo root.

## Host Paths

### Linux and macOS

Both Linux and macOS use the same host preparation path:

```bash
./prepare.sh
```

Use the Local Dev path when you want the self-contained local loop:

```bash
./run_local_dev.sh workflows/tests/python_chain.yaml
```

On Debian/Ubuntu, `prepare.sh` can install Podman plus helper packages with
`apt`. On macOS, install/start Podman or Docker outside the repo first. If
`podman info` fails, fix Podman before rerunning the dashboard. Having Podman
Desktop open is not sufficient unless its Podman engine/machine is running.

Local cluster preparation is wrapped by `run_local_dev.sh`, while Playground
image publication is wrapped by `run_publish.sh`.

## Arguments

| Flag | Description |
|------|-------------|
| `--image <name:tag>` | Override the image tag used for the build. |
| `--fmil-home <path>` | Reuse an existing fmilib installation instead of writing to `./.local`. |
| `--platform <os/arch>` | Build the container image for a specific platform, for example `linux/amd64`. |
| `-h`, `--help` | Display usage. |

## Outputs

- `bin/cads-workflow-runner` and `bin/cads-workflow-service`
- Container image tagged as the requested name (default `cads-fmi-demo:latest`)
- No cluster-side changes; local Argo/controller setup happens in
  `scripts/commands/run_local.sh`, and remote image publication happens in
  `scripts/commands/prepare_remote.sh`

## Troubleshooting

- **Missing toolchains** – Re-run `./prepare.sh` to regenerate the local
  Go/CLI installs. The build command only reuses what already exists under
  `./.local/`.
- **FMIL path issues** – Pass `--fmil-home` pointing to the desired install or
  export `FMIL_HOME` before invoking the script.
- **Container runtime missing or stopped** – Install/start Podman or Docker.
  The build command needs one of them reachable locally even for remote-only workflows.
  On macOS, verify `podman machine start` and `podman info` in the same shell.
