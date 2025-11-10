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
./prepare.sh --platform linux
```

On macOS the script provisions the package list but leaves Go/FM IL setup manual.
See [PREPARE.md](PREPARE.md) for overrides or manual instructions.

Once preparation succeeds, run:

```bash
./build.sh            # installs FMIL under ./.fmil if missing and builds Go binaries
./build.sh --fmil-home "$HOME/fmil"   # optional: reuse an existing FMIL install
```

`build.sh` installs FMIL when needed, exports the CGO variables, and compiles the
Go workflow binaries into `bin/`. Details live in [DEV.md](DEV.md).

### Step 1 – Build the FMUs (Python edge)

Run the helper script to produce the demo FMUs (Python is only used here to
generate FMUs). If you have custom FMUs, drop them into `fmu/models/` instead:

```bash
./create_fmu/build_python_fmus.sh
```

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

### Step 3 – Run the containerized workflow (Kubernetes + Argo default)

`build.sh` has already produced the Go binaries and ensured FMIL is available.
The `Dockerfile` bundles everything—FMUs, workflows, FMIL libs, and the Go
runner/service—so the resulting image is ready for your Kubernetes stack:

- **Kubernetes/minikube (default)** – Tag/push the image (or build inside
  minikube via `eval "$(minikube docker-env)"`). Generate and apply the Job
  manifest with:

  ```bash
  ./scripts/run_k8s_workflow.sh --workflow workflows/python_chain.yaml --image cads-fmi-demo:latest
  ```

  ```yaml
  apiVersion: batch/v1
  kind: Job
  metadata:
    name: cads-workflow
  spec:
    template:
      spec:
        restartPolicy: Never
        containers:
          - name: runner
            image: ghcr.io/your-org/cads-fmi-demo:latest
            command: ["/app/bin/cads-workflow-runner"]
            args: ["--workflow", "workflows/python_chain.yaml"]
  ```

- **Argo Workflows** – Install Argo in the same cluster and reference this image
  from a `container` template. Submit directly via:

  ```bash
  ./scripts/run_argo_workflow.sh --workflow workflows/python_chain.yaml --image cads-fmi-demo:latest
  ```

  ```yaml
  apiVersion: argoproj.io/v1alpha1
  kind: Workflow
  metadata:
    name: cads-workflow
  spec:
    entrypoint: run-fmu
    templates:
      - name: run-fmu
        container:
          image: ghcr.io/your-org/cads-fmi-demo:latest
          command: ["/app/bin/cads-workflow-runner"]
          args: ["--workflow", "workflows/python_chain.yaml"]
  ```
- **Local Podman/Docker (optional smoke test)** – Use the commands below only if
  you want to verify the image before pushing it to Kubernetes/Argo.

Local Podman/PDocker smoke test (optional):

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
└── deploy/ (future)        # K8s/Argo manifests for the full CADS showcase
```

Future work focuses on the deployment side (Argo/minikube manifests, Podman/K8s
pipelines) now that the workflow runtime is fully native Go + FMIL.***
