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

### Step 0 – Provide FMUs + workflows (bring yours or build the Python demo)

Start by deciding whether you will use externally prepared FMUs together with
their matching YAML workflows, or whether you want to generate the bundled
Python FMUs for a quick smoke test. To build the sample FMUs (Python is only
used here to generate them), run:

```bash
./create_fmu/build_python_fmus.sh
```

The helper installs `pythonfmu`, then **patches its exporter CMake files** so
the generated FMU binaries link against `libpython`. This is required now that
the workflow runtime runs purely in Go/FMIL—no embedded Python interpreter is
available when the FMUs are loaded, so the exporter must provide those symbols
itself. The patch step runs automatically every time the virtualenv (or Docker
image) installs pythonfmu.

If you are supplying FMUs from another toolchain (for example
`fmu/models/CITest.fmu` exported from Simulink), copy them into `fmu/models/`
and pair them with the YAML workflows you already authored under `workflows/`.
This way step 2 can either refine those existing workflows or reuse the sample
ones alongside your imported FMUs.

### Step 1 – Define the workflow (YAML)

Edit or copy one of the YAML files under `workflows/` (e.g.,
`workflows/python_chain.yaml`). Each step references one of the FMUs under
`fmu/models/` (chain as many steps/FMUs as you need), declares outputs to
capture, and can pass values downstream via `start_from`. Use `start_values`
for literal inputs and `result` to persist outputs. The same YAML file is used
everywhere (CLI, container, Kubernetes, Argo), so no extra metadata is required.
Once the workflow matches your scenario, you are ready to run it in the container.

### Step 2A – Local Minikube flow

Prepare the local toolchain and Minikube cluster, build the image, and submit
the workflow:

```bash
./prepare_local.sh
./build.sh
./run_local.sh workflows/python_chain.yaml
```

`prepare_local.sh` installs the Linux packages plus the Go/Argo/kubectl/Minikube
tooling under `./.local` and starts the `minikube` profile. `build.sh` is now a
pure build step: it stages pythonfmu resources, builds the Go binaries, and
builds the container image. `run_local.sh` handles the local-cluster wiring:
it verifies the Minikube context, syncs custom CAs into Minikube, ensures the
local Argo controller exists, loads the image into Minikube, submits the
workflow through Argo, and copies PVC-backed artifacts into `data/run-artifacts/`.

### Step 2B – Remote playground flow

Build the image with a registry tag, validate/publish it, then submit the
remote workflow:

```bash
./build.sh --image ghcr.io/org/cads-demo:demo123
./prepare_remote.sh --image ghcr.io/org/cads-demo:demo123 --kubeconfig ~/Kaizen_CADS/kubeconfig
./run_remote.sh workflows/python_chain.yaml --image ghcr.io/org/cads-demo:demo123 --kubeconfig ~/Kaizen_CADS/kubeconfig
```

`prepare_remote.sh` resolves the Argo token from `ARGO_TOKEN` or the supplied
kubeconfig, validates access against `https://argoworkflows.cads.kzslab.dev`,
and publishes the selected image tag through the local container engine.
`run_remote.sh` generates a PVC/configmap-free manifest and submits it directly
to the hosted Argo server with a unique workflow name.

For browser access to the KAIZEN Argo UI:

- Open `https://argoworkflows.cads.kzslab.dev/`
- Choose `Client Authentication`
- Paste `Bearer <token>`

The same bearer token comes from either the `playground-storhy-playground-pg-admin`
secret or the `playground-admin` entry in the kubeconfig. Remote demos depend on
Argo server access; direct Kubernetes API access is useful for secrets and raw
cluster inspection, but it is not required to submit or watch workflows.

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
- `./scripts/generate_remote_workflow.sh` emits a PVC/configmap-free workflow
  manifest (defaulting to `deploy/argo/<workflow>-remote-workflow.yaml`) for
  hosted Argo environments which only expose the demo image. The legacy
  `./scripts/prepare_ui_workflow.sh` name is still available as a compatibility
  wrapper and preserves the old `-ui-workflow.yaml` default output name.

Compatibility wrappers remain in place during the transition:

- `./prepare.sh` forwards to `./prepare_local.sh`
- `./run.sh` forwards to `./run_local.sh`
- `./scripts/prepare_ui_workflow.sh` forwards to `./scripts/generate_remote_workflow.sh`

### Using the playground TimescaleDB feed

The Argo playground cluster ships with a managed TimescaleDB instance that
continuously ingests synthetic points. Use `scripts/fetch_timescaledb_measurements.py`
to pull the most recent rows into `/app/data/measurements.csv` before running an
FMU workflow (the bundled Producer FMU already consumes this CSV).

1. Extract credentials from the playground namespace (replace the selector or
   secret name below with whatever the cluster administrators provided):

   ```bash
   kubectl -n playground get secret
   kubectl -n playground get secret playground-timescaledb-superuser \
       -o jsonpath='{.data.uri}' | base64 -d
   ```

   Export the resulting URI to `TIMESCALE_CONN` (or break it into the individual
   host/user/password environment variables the script understands).
   This step requires direct Kubernetes API access; the remote Argo workflow
   demo itself only requires Argo server access.

2. Run the helper inside the repo (the defaults match the demo data source):

   ```bash
   python scripts/fetch_timescaledb_measurements.py \
       --table public.measurements \
       --time-column ts \
       --value-column value \
       --limit 5000
   ```

   Adjust the table/column names if the playground populated them differently.
   The script creates `data/measurements.csv`, overwriting any previous file.

3. Chain the helper into a remote Argo workflow by switching the container to
   run a short shell wrapper. For example edit
   `deploy/argo/calculate_aecis-remote-workflow.yaml` so the template reads:

   ```yaml
   command: ["/bin/sh", "-c"]
   args:
     - |
       python scripts/fetch_timescaledb_measurements.py --limit 5000 && \
       /app/bin/cads-workflow-runner --workflow workflows/calculate_aecis.yaml
   env:
     - name: TIMESCALE_CONN
       valueFrom:
         secretKeyRef:
           name: playground-timescaledb-superuser
           key: uri
   ```

   Replace the secret name/key with whatever the playground exposes. Because
   `generate_remote_workflow.sh` copies the template verbatim, the edited
   manifest can be re-generated (or hand-tweaked) whenever you need a different
   workflow file.

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

- [`PREPARE.md`](PREPARE.md) – manual breakdown of what `prepare.sh` does so you can adapt it
  to bespoke hosts or reuse portions (certificate handling, Minikube bootstrap,
  CLI installs).
- [`BUILD.md`](BUILD.md) – detailed description of the container build, where FMUs and
  workflows land inside the image, and the optional flags exposed by `build.sh`.
- [`RUN.md`](RUN.md) – how to submit workflows once an image exists, covering Argo,
  Podman, and direct CLI invocation, plus the expected data directories.
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) – known failure modes (Minikube resets, FMIL linkage,
  PVC provisioning) with diagnostic steps and commands.
- [`ARCHITECTURE.md`](ARCHITECTURE.md) – plain-language overview of how the CADS demo is structured
  (also referenced as `arch.md` in some notes).
- [`DEV.md`](DEV.md) – contributor-focused notes: Go module layout, lint/test targets, and
  how to iterate on the orchestrator locally.
- [`create_fmu/README.md`](create_fmu/README.md) – specifics of the Python demo FMUs, exporter patches,
  and how to substitute your own models.
- [`orchestrator/service/README.md`](orchestrator/service/README.md) – details about the Go services and CLIs,
  configuration knobs, and runtime environment variables.
