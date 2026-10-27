# Developer Notes

This document captures optional manual steps and environment details that most
users do not need when following the root entrypoints. Use it when you want to
tinker with the tooling directly.

---

## Go workflow binaries

`scripts/commands/build.sh` already compiles both executables and drops them in
`bin/`. To build them yourself (for example, while iterating on the code), run:

```bash
cd orchestrator/service
go build -o ../../bin/cads-workflow-runner ./cmd/cads-workflow-runner
go build -o ../../bin/cads-workflow-service ./cmd/cads-workflow-service
```

The service is cgo-free and needs no FMIL, so it can also be built on its own
(for example on macOS for the local demo):

```bash
cd orchestrator/service
CGO_ENABLED=0 GOWORK=off go build -o ../../bin/cads-workflow-service ./cmd/cads-workflow-service
```

For the runner, make sure the FMIL environment variables are exported; the
helper scripts do this automatically, but if you are running the commands
manually you need:

```bash
export FMIL_HOME=/path/to/fmil
export LD_LIBRARY_PATH="$FMIL_HOME/lib:$LD_LIBRARY_PATH"
export PKG_CONFIG_PATH="$FMIL_HOME/lib/pkgconfig:$PKG_CONFIG_PATH"
export CGO_ENABLED=1
export CGO_CFLAGS="-I$FMIL_HOME/include"
export CGO_CXXFLAGS="-I$FMIL_HOME/include"
export CGO_LDFLAGS="-L$FMIL_HOME/lib"
export GOWORK=off
```

Useful extras:

- `CGO_ENABLED=0 GOWORK=off go test ./...` from `orchestrator/service` runs
  the unit tests without FMIL (add more as the workflow runner evolves). With
  `scripts/commands/run_db.sh` running and `CADS_DB_URL` exported,
  `go test -tags dbintegration ./internal/cadsdb/` also exercises the database
  reads and writes.
- Python tests need only the standard library:

  ```bash
  python3 -m unittest discover -s create_fmu -p 'test_*.py'     # FMU model logic
  python3 -m unittest discover -s scripts/tests -p 'test_*.py'  # synthetic data generator, ingest --notify
  ```
- Set `GODEBUG=cgocheck=2` while iterating on the FMIL bindings to surface cgo
  misuse early.
- When working with alternative FMIL installs, pass `--fmil-home` to
  `scripts/commands/build.sh` or export `FMIL_HOME` before running the manual
  commands above.

---

## FMU build helper (Python)

The Python FMU generator script lives under `create_fmu/`:

```bash
./create_fmu/build_python_fmus.sh
```

It creates a virtualenv inside `create_fmu/.venv`, installs
`create_fmu/requirements.txt`, and runs `pythonfmu` to produce the demo
`Producer.fmu` and `Consumer.fmu` files under `fmu/models/`.

Because the runtime FMU executor is pure Go/FM IL (no embedded Python
interpreter), the FMUs must include exporter binaries that link against
`libpython`. Upstream `pythonfmu` builds its exporter as a regular CPython
extension, so we patch the installed copy **immediately after pip install**
(`create_fmu/patch_pythonfmu_export.py`). The script rewrites
`pythonfmu-export/CMakeLists.txt` to request the `Development.Embed` component
and link `Python3::Python`, then rebuilds via `build_unix.sh`. The helper script
invokes the patch before every FMU build, and the Docker/installer workflows do
the same so all environments stay consistent.

Tips:

- Delete `create_fmu/.venv` when upgrading Python or `requirements.txt`.
- The script is idempotent; re-run it anytime you change the Python FMU sources.
- For custom FMUs, drop the resulting `.fmu` files straight into `fmu/models/`
  and reference them from your workflows; no code changes required.

---

## Container image tweaks

- `scripts/commands/build.sh` compiles the Go binaries and builds the container
  image (preferring Podman but falling back to Docker). Re-run it whenever you
  change the Go code, FMUs, or Dockerfile contents.
- `scripts/commands/run_local.sh` handles Minikube CA sync, Argo controller
  installation, image preload, and PVC-backed local submission.
- `scripts/commands/prepare_remote.sh` validates hosted-Argo access and
  publishes the selected image tag; `scripts/commands/run_remote.sh` submits
  the hosted workflow manifest.
- Override `--image` to push/tag alternative names and `--fmil-home` to reuse an
  existing FMIL installation rather than installing under `./.local/`.
- To debug inside the container, start an interactive shell:

  ```bash
  docker run --rm -it cads-fmi-demo:latest bash
  ```

  The binaries live under `/app/bin/` and FMUs under `/app/fmu/models/`.

---

## Kubernetes / Argo manifests

- `scripts/generate_manifests.sh --workflow workflows/foo.yaml --image cads-fmi-demo:latest`
  renders the Argo Workflow and data PVC manifests into `deploy/argo/` and
  `deploy/storage/`.
- `scripts/run_argo_workflow.sh --workflow workflows/foo.yaml` renders and submits
  the workflow via `argo submit`, creates the PVC if needed, and shows progress
  with `argo watch`.
- `scripts/generate_remote_workflow.sh workflows/foo.yaml --image ghcr.io/...`
  renders the hosted-Argo manifest into `deploy/argo/`.
- Customize the manifests (additional env vars, volumes, secrets) by editing the
  generated files before applying or by extending the generator script.
- `scripts/commands/run_local.sh workflows/foo.yaml` validates the Minikube
  context, applies the PVC manifest, and delegates to
  `scripts/run_argo_workflow.sh`.
- `scripts/commands/run_remote.sh workflows/foo.yaml --image ghcr.io/... --kubeconfig ...`
  generates the hosted manifest and submits it through the remote Argo server.
