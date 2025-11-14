# CADS Architecture

This document walks through the CADS FMI demo architecture using plain language.
It explains what each part does, how they cooperate, and where your own models
and workflows fit in. For hands-on setup commands see the [README](README.md).

## Big picture

Think of the demo as three concentric layers:

1. **Models you bring** – ready-made FMUs exported from tools such as Simulink
   or the included Python FMU samples.
2. **Workflow descriptions** – small YAML files that describe which FMUs to run,
   in which order, and which results to capture.
3. **Execution platform** – a Go application (the CADS workflow runner) packaged
   into a container image and launched either via the CLI, Podman/Docker, or
   Argo Workflows on top of Minikube.

You provide the first two layers; the repo supplies the third.

## Key roles

- **Model authors** export FMI *Co-Simulation* FMUs (zip files with compiled
  simulation logic). Place them under `fmu/models/`.
- **Workflow designers** stitch those FMUs together in YAML files under
  `workflows/`. Each step chooses one FMU and names the values that should flow
  to the next step.
- **Operators** run the helper scripts (`prepare.sh`, `build.sh`, `run.sh`) to
  install prerequisites, build the container image, and submit workflows.

The roles can be fulfilled by the same person, but splitting them clarifies how
CADS separates model development from runtime orchestration.

## Runtime components

| Component | Location | Purpose |
|-----------|----------|---------|
| **FMU library (FMIL)** | Installed under `./.local` by `prepare.sh` or reused via `FMIL_HOME`. | Provides the low-level FMI API that the Go runner calls to load and execute FMUs. |
| **Workflow runner (CLI)** | `orchestrator/service/cmd/cads-workflow-runner` → `bin/` | Reads a YAML workflow and executes each FMU step sequentially. Used inside containers or locally. |
| **Workflow service (HTTP)** | `orchestrator/service/cmd/cads-workflow-service` → `bin/` | Wraps the runner with a simple API (`POST /run`) so Argo can trigger workflows via HTTP. |
| **Python FMU builder** | `create_fmu/` | Optional helper that uses `pythonfmu` to produce sample FMUs (Producer/Consumer) for demos. It patches pythonfmu so the compiled FMUs run without a Python interpreter at runtime. |
| **Container image** | Built from `Dockerfile` via `build.sh` | Bundles the runner, service, FMUs, workflows, and any certificates into a single image that Argo/Podman can run. |
| **Argo + Minikube** | Installed/configured by `prepare.sh` + `build.sh` | Provide a lightweight Kubernetes environment where workflows are submitted, executed, and observed. |
| **Persistent storage (PVC)** | Generated in `deploy/storage/` | Holds `/app/data` so workflow outputs survive across runs. |

## Data and control flow

1. **Provide FMUs** – Copy your `.fmu` files into `fmu/models/`. The Python
   helper can generate demo FMUs but is optional if you already have Simulink
   (or other) exports.
2. **Describe the workflow** – In `workflows/*.yaml`, list each step, its FMU,
   any starting values, and which outputs should be saved. The files are human
   readable; no code is required.
3. **Build the platform** – `prepare.sh` installs Go, Argo, Minikube, FMIL, and
   other prerequisites on the host. `build.sh` compiles the Go binaries, builds
   the container image, ensures Argo is present in Minikube, and loads the image
   into the cluster.
4. **Submit a run** – `run.sh workflows/example.yaml` renders the Argo manifest,
   applies the persistent volume claim, and submits the workflow. Argo spins up
   a pod that runs `cads-workflow-runner`, which loads each FMU via FMIL and
   streams outputs to `/app/data`.
5. **Collect results** – Workflow steps can write JSON snapshots (via the
   `result` field) and all intermediate files land on the shared PVC. Inspect
   them with `kubectl`, Podman bind mounts, or by copying from `data/` locally.

## Operational boundaries

- **Python vs. Go** – Python is only used during FMU creation. Once an FMU is
  built, the runtime uses native Go + FMIL, which keeps the execution
  environment small and predictable.
- **Local vs. cluster** – The same runner binary works on a laptop, inside a
  container, or within Argo. Scripts merely automate packaging and submission.
- **User responsibilities** – You maintain ownership of the FMU logic and
  workflow descriptions. CADS does not edit or interpret the math inside your
  FMUs; it simply orchestrates them.

## Why this architecture?

- **Separation of concerns** – Model experts focus on FMUs; operators focus on
  infrastructure. YAML workflows provide a thin contract between them.
- **Reproducibility** – Every run executes the same containerized runner with
  the same FMUs and workflows, so results can be traced and repeated.
- **Portability** – FMI is a vendor-neutral standard. By leaning on FMUs and
  FMIL, the demo remains compatible with tools such as Simulink, Dymola, or any
  exporter that outputs FMI Co-Simulation packages.

For installation details, quick-start commands, and troubleshooting tips, see
the [README](README.md) and the supporting docs listed there.
