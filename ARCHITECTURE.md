# CADS Architecture

This document explains how the CADS FMI demo is wired when you follow the
recommended route: running workflows inside Argo Workflows on a local Minikube
cluster. It is written for non-specialists—no Kubernetes background is needed.
For step-by-step commands, see the [README](README.md). CADS relies on the FMI
standard so the runtime can execute FMUs exported from tools like Simulink
without rewriting the simulation logic—FMIL, the FMI library, handles the low
level details.

## FMI and FMU basics

- **FMI (Functional Mock-up Interface)** is an open standard for exchanging
  simulation models across tools. It defines how simulators expose inputs,
  outputs, configuration (start/stop times, step sizes), and binary artifacts.
- **FMU (Functional Mock-up Unit)** is the packaged model produced by tools such
  as Simulink, Dymola, or the bundled Python examples. An FMU is simply a ZIP
  file containing compiled code, metadata, and optional resources. This demo uses
  the *Co-Simulation* flavor, which means each FMU carries its own solver so the
  CADS runtime only needs to feed inputs and advance time.
- **FMIL (FMI Library)** is the shared runtime from Modelon that loads FMUs and
  exposes them to Go through cgo. The workflow runner links against FMIL, so any
  FMU that follows the standard can be executed without rewriting the runner.

In practice: you export an FMU from your modeling tool, drop it into
`fmu/models/`, and CADS handles the rest via FMIL.

## Core platform terms

- **Container** – a lightweight package that bundles an application plus its
  libraries and files. Think of it as a self-contained folder that always runs
  the same way, no matter which computer hosts it. Pods consume containers; you
  build the image once and Kubernetes reuses it for every run.
- **Pod** – the smallest unit Kubernetes starts. It wraps one or more containers
  with storage, networking, and restart rules. In this demo each pod contains a
  single CADS container baked with your FMUs, and Argo creates one pod per
  workflow run.
- **Manifest** – a YAML file that tells Kubernetes or Argo what to create (pods,
  volumes, workflows).
- **Kubernetes (K8s)** – an orchestration platform that starts and supervises
  containers. It keeps applications running even if a container crashes by
  launching replacements automatically. In this demo the Kubernetes cluster is
  provided by Minikube, so “Kubernetes” and “Minikube” refer to the same local
  environment unless stated otherwise.
- **Argo Workflows** – a higher-level service that runs on top of Kubernetes.
  You feed Argo a workflow description, and it asks Kubernetes to run the needed
  containers in the right order.

Putting it together: you build the container image once (it already includes
the workflow runner plus your FMUs and workflow YAML files), Argo reads your
workflow and asks Kubernetes to run it, and Kubernetes launches pods that host
those containers. Each workflow run is therefore a pod executing that CADS
container image.

## Key ingredients

- **FMUs (`fmu/models/`)** – the simulation packages you supply, whether exported
  from Simulink or built via the Python helper. They are copied into the
  container image so the cluster can run them without extra uploads.
- **Workflow YAML (`workflows/*.yaml`)** – the recipe that lists which FMUs to
  execute, in what order, and which outputs to keep. The same file drives local
  tests and Argo runs.
- **Helper scripts (`prepare.sh`, `build.sh`, `run.sh`)** – automate everything
  from installing toolchains to submitting Argo workflows so you rarely touch
  Kubernetes or Docker commands directly.
- **FMIL** – installed under `./.local` (or another path via `FMIL_HOME`). This
  shared library understands the FMI standard and lets the Go runner load FMUs.
- **Go workflow runner (`bin/cads-workflow-runner`)** – compiled during
  `build.sh` and baked into the container. At runtime it reads the workflow
  YAML, invokes FMIL, and passes values between steps.
- **Container image** – produced from the `Dockerfile` by `build.sh`. It bundles
  the runner, FMUs, workflow files, and certificates so Argo can start pods with
  everything preloaded.
- **Persistent volume claim** – rendered into `deploy/storage/` and applied by
  `run.sh`. It backs `/app/data` in every workflow pod so outputs persist after
  the container exits.
- **Argo controller (`argo` namespace)** – installed/verified by `build.sh` in
  the Minikube cluster. It watches for workflow submissions and launches pods as
  needed.

## How the pieces work together

1. **You provide content** – Copy FMUs (e.g., Simulink exports) into
   `fmu/models/` and define workflows under `workflows/`. These files stay under
   version control and are later embedded in the container image.
2. **`prepare.sh` readies the host** – Installs Go, Podman/Docker, Minikube,
   Argo CLI, and FMIL into `./.local`, ensuring the necessary tools exist
   without touching system directories. It also starts a Minikube profile named
   `minikube`.
3. **`build.sh` wires the cluster**:
   - Compiles the Go runner/service with FMIL support.
   - Builds the container image and tags it (default `cads-fmi-demo:latest`).
   - Copies corporate certificates (if any) into the Minikube VM so image pulls
     succeed behind proxies.
   - Installs or verifies Argo Workflows in the cluster.
   - Pushes the freshly built image into Minikube so pods can start instantly.
4. **`run.sh` submits a workflow**:
   - Calls `scripts/generate_manifests.sh` to render an Argo Workflow manifest
     that references your chosen YAML file and image tag. This translation step
     keeps the CADS workflow YAML as the source of truth while wrapping it in
     the heavier Argo manifest (pod specs, volumes, service accounts) so
     Kubernetes understands how to schedule it.
   - Applies the PVC manifest so `/app/data` points to durable storage.
   - Uses the Argo CLI to submit the workflow to the in-cluster controller.
5. **Argo executes the workflow**:
   - Argo launches a Kubernetes pod that runs `/app/bin/cads-workflow-runner`
     inside the container image you built.
   - The runner reads the workflow YAML (already inside the image), loads FMUs
     via FMIL, and executes them sequentially, passing results between steps as
     defined in YAML.
   - Outputs and JSON snapshots land on `/app/data`, which is backed by the PVC.
6. **You inspect results** – Use `argo watch`, `kubectl logs`, or mount the PVC
   to review files under `data/`. `run.sh` also spins up a short-lived helper pod
   that copies `/app/data` from the PVC down to your local `data/run-artifacts/`
   folder for convenience. Nothing is stored inside the short-lived workflow pod;
   everything you care about sits on the shared volume until copied out.

The key idea: once `run.sh` hands the workflow to Argo, Kubernetes handles the
heavy lifting—starting pods, restarting on failure, and isolating each run.

## Data flow in plain terms

- **Inputs**: FMUs (`.fmu` files) and workflow YAML travel from your working
  copy into the container image. When you call `run.sh`, that image is what the
  cluster executes.
- **Runtime data**: During execution, FMUs exchange values through the Go runner
  (it keeps variable state in memory) and write requested outputs to `/app/data`.
- **Outputs**: Anything written to `/app/data` persists on the PVC. You can copy
  it back to your host via `kubectl cp`, run another helper pod to read it, or
  mount the same PVC from future workflows.

## Why Kubernetes + Argo?

- **Repeatable runs** – Each workflow becomes a declarative manifest that Argo
  can replay. Pod specs, storage, and images are versioned alongside your FMUs.
- **Isolation** – Every workflow runs in its own pod. If an FMU crashes, it
  affects only that pod, not your host environment.
- **Scalability** – Although Minikube is single-node, the same manifests work
  on multi-node clusters without modification.
- **Observability** – Argo exposes run status (`argo list`, `argo watch`) and
  Kubernetes supplies detailed logs/events if something fails.

In short, CADS packages your simulation logic and workflow instructions into a
container, hands it to Argo inside Minikube, and lets Kubernetes coordinate the
execution. You stay focused on FMUs and workflow YAML; the scripts and cluster
handle everything else.
