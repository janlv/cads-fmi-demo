# Developer Notes

This document captures optional manual steps and environment details that most
users do not need when following `prepare.sh` + `build.sh`. Use it when you want
to tinker with the tooling directly.

---

## Go workflow binaries

`build.sh` already compiles both executables and drops them in `bin/`. To build
them yourself (for example, while iterating on the code), run:

```bash
cd orchestrator/service
go build -o ../../bin/cads-workflow-runner ./cmd/cads-workflow-runner
go build -o ../../bin/cads-workflow-service ./cmd/cads-workflow-service
```

Make sure the FMIL environment variables are exported; the helper scripts do this
automatically, but if you are running the commands manually you need:

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

- `go test ./...` from `orchestrator/service` runs the unit tests (add more as
  the workflow runner evolves).
- Set `GODEBUG=cgocheck=2` while iterating on the FMIL bindings to surface cgo
  misuse early.
- When working with alternative FMIL installs, pass `--fmil-home` to `build.sh`
  or export `FMIL_HOME` before running the manual commands above.

---

## FMU build helper (Python)

The Python FMU generator script lives under `create_fmu/`:

```bash
./create_fmu/build_python_fmus.sh
```

It creates a virtualenv inside `create_fmu/.venv`, installs
`create_fmu/requirements.txt`, and runs `pythonfmu` to produce the demo
`Producer.fmu` and `Consumer.fmu` files under `fmu/models/`.

Tips:

- Delete `create_fmu/.venv` when upgrading Python or `requirements.txt`.
- The script is idempotent; re-run it anytime you change the Python FMU sources.
- For custom FMUs, drop the resulting `.fmu` files straight into `fmu/models/`
  and reference them from your workflows; no code changes required.

---

## Container image tweaks

- `docker compose build` (or `podman`) uses the local sources plus the binaries
  produced by `build.sh`. Rebuild whenever you touch the Go code or FMUs.
- To debug inside the container, start an interactive shell:

  ```bash
  docker run --rm -it cads-fmi-demo:latest bash
  ```

  The binaries live under `/app/bin/` and FMUs under `/app/fmu/models/`.

---

## Kubernetes / Argo manifests

- `scripts/generate_manifests.sh --workflow workflows/foo.yaml --image cads-fmi-demo:latest`
  renders the K8s Job and Argo Workflow YAML into `deploy/k8s/` and `deploy/argo/`.
- `scripts/run_k8s_workflow.sh --workflow workflows/foo.yaml` runs the generator
  and applies the job via `kubectl`. Tail logs with `kubectl logs -f job/...`.
- `scripts/run_argo_workflow.sh --workflow workflows/foo.yaml` renders and submits
  the workflow via `argo submit`, then follows it with `argo watch`.
- Customize the manifests (additional env vars, volumes, secrets) by editing the
  generated files before applying or by extending the generator script.
