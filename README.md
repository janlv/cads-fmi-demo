# CADS FMI Workflow Demo

This repository showcases how CADS can separate **FMU creation** from the
**workflow runtime** while remaining friendly to off-the-shelf tooling. Your
only inputs are FMI **Co-Simulation** FMUs (for example a Simulink-exported
`CITest.fmu`) and declarative workflow descriptions written as YAML files.
Drop FMUs under `fmu/models/`, author workflows under `workflows/`, and let the
Go/FMIL runner orchestrate the pipeline. For demonstration purposes this repo
also ships a native Python FMU builder so you can generate sample FMUs without
leaving the project.

The key directories are:

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
but the focus is now on workflow-driven execution using FMUs you supply and YAML
workflows you define.

---

## Quick start

### Step 0 – Prepare the environment

Provision the host (Debian/Ubuntu) with the tooling used by the demo:

```bash
./prepare.sh              # installs packages + CLIs under ./.local and starts Minikube
```

The script targets Linux hosts, installs Podman plus helper packages via `apt`,
downloads Go/Argo/kubectl/Minikube into `./.local/`, and starts a rootless
Minikube profile named `minikube`. Place any corporate TLS certificates under
`scripts/certs/` so future builds sync them into the cluster. See
[PREPARE.md](PREPARE.md) for manual steps if you need to adapt the flow.
Because this is a self-contained demo, Minikube is always started/reset by the
scripts so every run begins from a known-good cluster.

The helper scripts automatically prepend `./.local/bin` and `./.local/go/bin` to
`PATH` so the freshly installed CLIs are available during the rest of the
workflow.

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

Run `build.sh` to (re)build the image, sync CA certificates into Minikube,
ensure the Argo controller exists, and load the freshly built image into the
cluster. Then submit a workflow via `run.sh`, which automatically packages the
selected workflow YAML into a ConfigMap so edits take effect without rebuilding
the container:

```bash
./build.sh
./run.sh workflows/python_chain.yaml
```

Use `--image` on either script when you want a different tag and
`--fmil-home` on `build.sh` to reuse an existing FMIL installation. `run.sh`
verifies that the current kube-context is reachable, applies the PVC manifest
generated earlier, and submits the workflow through the Argo CLI. Workflow pods
store their `/app/data` contents inside the PVC (`cads-data-pvc` in the `argo`
namespace) so runs remain durable across executions.

For manual container testing you can still drive Podman or Docker yourself:

```bash
podman build -t cads-fmi-demo .
podman run --rm -v "$(pwd)/data:/app/data" cads-fmi-demo \
    /app/bin/cads-workflow-runner --workflow workflows/python_chain.yaml
```

Inside the container:

- `/app/fmu/models/` holds the FMUs you built or provided.
- `/app/workflows/` (or mounted files) supply the YAML definitions.
- `/app/bin/cads-workflow-runner` executes the workflow; `/app/bin/cads-workflow-service`
  exposes the HTTP API. Both use the same FMIL-backed Go engine, so running in
  Podman, Minikube, or Argo only swaps the scheduler—not the code.
- `./scripts/generate_manifests.sh` writes the rendered Argo workflow and PVC
  definitions to `deploy/argo/` and `deploy/storage/` so you can inspect or tweak
  them before applying.

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

## Documentation map

- `PREPARE.md` – manual breakdown of what `prepare.sh` does so you can adapt it
  to bespoke hosts or reuse portions (certificate handling, Minikube bootstrap,
  CLI installs).
- `BUILD.md` – detailed description of the container build, where FMUs and
  workflows land inside the image, and the optional flags exposed by `build.sh`.
- `RUN.md` – how to submit workflows once an image exists, covering Argo,
  Podman, and direct CLI invocation, plus the expected data directories.
- `TROUBLESHOOTING.md` – known failure modes (Minikube resets, FMIL linkage,
  PVC provisioning) with diagnostic steps and commands.
- `DEV.md` – contributor-focused notes: Go module layout, lint/test targets, and
  how to iterate on the orchestrator locally.
- `create_fmu/README.md` – specifics of the Python demo FMUs, exporter patches,
  and how to substitute your own models.
- `orchestrator/service/README.md` – details about the Go services and CLIs,
  configuration knobs, and runtime environment variables.
