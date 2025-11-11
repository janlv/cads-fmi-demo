# CADS FMI Workflow Demo

This repository showcases how CADS can separate **FMU creation** from the
**workflow runtime** while remaining friendly to off-the-shelf tooling. Bring
your own FMUs and declarative workflow definitions, then let the Go/FM IL runner
handle orchestration:

- `create_fmu/` contains everything needed to build the Python demo FMUs with
  `pythonfmu`. The resulting `.fmu` files live alongside the rest of the FMUs in
  `fmu/models/`.
- `workflows/` stores declarative pipeline definitions. Each step names an FMU
  (Python-built or external, such as the bundled Simulink `CITest.fmu`), a few
  optional overrides, and an output location.
- `orchestrator/service/internal/fmi` together with the Go CLIs
  (`cads-workflow-runner`, `cads-workflow-service`) load workflow files and
  execute FMUs directly through FMIL via cgo. Only FMI **Co-Simulation** FMUs
  are supported because that mirrors the target CADS architecture; Python now
  lives solely on the FMU-generation edge.

The remaining Docker/compose helpers are kept for parity with previous demos,
but the focus is now on workflow-driven execution using FMUs you provide (drop
them under `fmu/models/`) and YAML workflows you define under `workflows/`.

---

## Quick start

### Step 0 – Prepare the environment

Bootstrap the host (system packages, Go ≥ 1.22, FMIL) via:

```bash
./prepare.sh --platform linux               # default local scope (FMIL/.local, CLI tools under ./.local/bin)
# add --podman (default) or --docker to pick the Minikube driver explicitly
# or install under /usr/local (requires sudo):
./prepare.sh --platform linux --global
```

On macOS the script provisions the package list but leaves Go/FM IL setup manual.
See [PREPARE.md](PREPARE.md) for overrides or manual instructions.

Once preparation succeeds, run:

```bash
./build.sh            # installs FMIL under ./.local if missing and builds Go binaries
./build.sh --fmil-home "$HOME/fmil"   # optional: reuse an existing FMIL install
```

`build.sh` installs FMIL when needed, exports the CGO variables, and compiles the
Go workflow binaries into `bin/`. Details live in [DEV.md](DEV.md). The helper
scripts install the required CLIs (`argo`, `kubectl`, `minikube`) under
`./.local/bin` in local mode and automatically prepend that directory to `PATH`.
`prepare.sh` also boots a local Minikube cluster (driver `docker`) so `kubectl`
and Argo commands have a ready kubecontext; set `MINIKUBE_AUTO_START=false` if
you plan to target an existing cluster instead.

### Step 1 – Build the FMUs (Python edge)

Run the helper script to produce the demo FMUs (Python is only used here to
generate FMUs). If you have custom FMUs, drop them into `fmu/models/` instead:

```bash
./create_fmu/build_python_fmus.sh
```

The helper installs `pythonfmu`, then **patches its exporter CMake files** so
the generated FMU binaries link against `libpython`. This is required now that
the workflow runtime runs purely in Go/FM IL—no embedded Python interpreter is
available when the FMUs are loaded, so the exporter must provide those symbols
itself. The patch step runs automatically every time the virtualenv (or Docker
image) installs pythonfmu.

Most helper scripts display long-running sub-commands in a compact “tail
window”. Export full logs (one line per command output) by setting
`CADS_LOG_TAIL_LINES=0` before invoking the script.

External FMUs (for example `fmu/models/CITest.fmu` exported from Simulink) are
simply copied into `fmu/models/`.

### Step 2 – Define the workflow (YAML)

Edit or copy one of the YAML files under `workflows/` (e.g.,
`workflows/python_chain.yaml`). Each step references one of the FMUs under
`fmu/models/` (chain as many steps/FMUs as you need), declares outputs to
capture, and can pass values downstream via `start_from`. Use `start_values`
for literal inputs and `result` to persist outputs. The same YAML file is used
everywhere (CLI, container, Kubernetes, Argo), so no extra metadata is required.
Once the workflow matches your scenario, you are ready to run it in the container.

### Step 3 – Run the containerized workflow (Kubernetes + Argo)

Run `build.sh` first so the Go binaries and container image are up to date.
Then use `run.sh` in Argo mode—which automatically schedules pods on Kubernetes
(Argo sits on top of K8s, so you get both layers with a single command):

```bash
./build.sh
./run.sh workflows/python_chain.yaml    # defaults to --mode argo
```

`run.sh` validates that the Kubernetes client is configured before submitting a
job/workflow. The Minikube cluster started by `prepare.sh` exposes a ready
context, but if you disable it (or want to point somewhere else) ensure
`kubectl config current-context` succeeds or set `KUBECONFIG`. The first call to
`scripts/run_argo_workflow.sh` also verifies that the Argo Workflows CRD exists
and, if missing, applies the upstream install manifest (defaults to namespace
`argo`). Override this behavior with:

- `ARGO_NAMESPACE=<ns>` – submit workflows and install Argo into a different namespace.
- `ARGO_AUTO_INSTALL=false` – skip the automatic install and print the necessary `kubectl` commands instead.
- `ARGO_MANIFEST_URL=<url>` – point to a pinned/custom manifest (defaults to the
  release matching the bundled Argo CLI version).
- `MINIKUBE_EXTRA_CA_CERT=/path/to/proxy-ca.crt` – add an extra CA file (with optional
  `MINIKUBE_EXTRA_CA_NAME`) to Minikube’s trust store before workloads run. The helper
  also installs every `.crt` / `.pem` found under `scripts/certs/` by default; override
  that directory with `MINIKUBE_EXTRA_CA_CERTS_DIR`.

Under the hood `run.sh` generates the manifests, submits the workflow via the
Argo CLI, and Argo starts the pods on your cluster. Use `--mode k8s` if you want
to see the raw Kubernetes Job instead, or `--mode local` for a Podman/Docker
smoke test. In every case the same container image and workflow YAML are used.

Local Podman/Docker smoke test (optional):

```bash
podman build -t cads-fmi-demo .
podman run --rm -v "$(pwd)/data:/app/data" cads-fmi-demo \
    /app/bin/cads-workflow-runner --workflow workflows/citest.yaml
```

Inside the container:

- `/app/fmu/models/` holds the FMUs you built or provided.
- `/app/workflows/` (or mounted files) supply the YAML definitions.
- `/app/bin/cads-workflow-runner` executes the workflow; `/app/bin/cads-workflow-service`
  exposes the HTTP API. Both use the same FMIL-backed Go engine, so running in
  Podman, minikube, or Argo only swaps the scheduler—not the code.
- `./scripts/generate_manifests.sh` writes the rendered YAML to `deploy/k8s/` and
  `deploy/argo/` so you can inspect or tweak them before applying.

---

## Optional local smoke tests

Quick checks for local development:

```bash
# CLI runner
./cads-workflow-runner --workflow workflows/python_chain.yaml

# HTTP service
./cads-workflow-service --serve --addr :8080 &
curl -X POST localhost:8080/run \
     -H 'Content-Type: application/json' \
     -d '{"workflow":"workflows/python_chain.yaml"}'
```

The CLI is handy for verifying workflows on the host; the service exercises the
REST interface. Use `--json-output` when you only need the final payload.

---

## Workflow format

Each file in `workflows/` contains a minimal schema:

```yaml
steps:
  - name: producer
    fmu: fmu/models/Producer.fmu
    start_values:
      num_points: 100000
    outputs:
      - mean
      - std
    result: data/producer_result.json

  - name: consumer
    fmu: fmu/models/Consumer.fmu
    start_from:
      mean_in: producer.mean
      std_in: producer.std
    outputs:
      - health_score
      - anomaly
    result: data/consumer_result.json
```

All keys are optional except `name` and `fmu`:

- `outputs` – list of variables to capture. If omitted, the runner gathers every
  variable whose causality is `output` or `calculatedParameter` (falling back to
  `time` when nothing matches).
- `start_values` – literal start values to feed into the FMU.
- `start_from` – copy start values from previous steps using `step.variable`
  references. The runner validates that the upstream step recorded the
  requested variable.
- `result` – optional JSON target path. The runner persists the final snapshot
  there (directories are created automatically).
- `start_time`, `stop_time`, `step_size` – rarely needed overrides when an FMU
  lacks a `DefaultExperiment`. Otherwise the runner uses the FMU’s defaults.

All relative paths are resolved from the repository root, so FMUs and artifacts
can be checked in or mounted during container runs without tweaking the YAML.

---

## Repository layout

```
.
├── create_fmu/             # pythonfmu build helpers, cached exporters
├── fmu/
│   └── models/             # Python FMU sources + ready-to-run .fmu files
├── workflows/              # YAML workflows consumed by the runner/service
├── orchestrator/
│   └── service/            # Go workflow runner + HTTP service (FMIL via cgo)
│       ├── internal/fmi    # FMIL bindings shared by the binaries
│       └── cmd/            # cads-workflow-runner + cads-workflow-service CLIs
├── scripts/                # Shared helper utilities
└── deploy/                 # Generated K8s/Argo manifests (via scripts/generate_manifests.sh)
```

Future work focuses on richer deployment examples (multi-step Argo DAGs, Helm
charts, etc.) now that the workflow runtime is fully native Go + FMIL.***
