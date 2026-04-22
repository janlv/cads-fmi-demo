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

### Step 2 – Choose an execution path

The repo supports three ways to run the same workflow definition:

- **Local path (`run_local.sh`)** – runs the workflow in a local Minikube +
  Argo setup on your machine. Use this when you want an isolated dev loop and
  local artifact collection under `data/run-artifacts/`.
- **Remote path (`run_remote.sh`)** – submits one workflow run directly to the
  shared Kaizen Argo playground. Use this when you want to validate the same
  image/workflow in the hosted environment without the browser dashboard.
- **Dashboard path (`run_dashboard.sh`)** – starts a local browser UI that lists
  repo workflows, launches them into the remote Kaizen playground, and shows
  recent run status plus a live duration plot. Use this when you want an
  operator-style view of the remote environment.

The runtime itself stays the same across all three paths: the workflow YAML is
still executed by the same Go/FMIL code. What changes is where Argo runs it and
how you interact with it.

### Step 2A – Local path (Minikube + local Argo)

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

Use the local path when:

- you want to iterate without publishing an image to a remote registry
- you want workflow outputs copied back into the repo automatically
- you want a local demo cluster you control end to end

### Step 2B – Remote path (hosted Kaizen playground)

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
For `ghcr.io/...` images it also tries to log the container engine into GHCR
automatically before pushing, using this order:

- `GHCR_TOKEN`
- `GITHUB_TOKEN`
- a valid `gh auth login -h github.com -s write:packages` session

The GHCR username is resolved from `GHCR_USERNAME`, then `GITHUB_ACTOR`, then
the authenticated `gh` user, with the image owner as a final fallback.
`run_remote.sh` generates a PVC/configmap-free manifest and submits it directly
to the hosted Argo server with a unique workflow name. Hosted manifests now
also expose the playground S3 secret `storhy-argo-artifacts-s3-credentials`
inside the workflow pod as standard environment variables so workflows can read
bucket-backed inputs without per-run manifest edits:

- `AWS_ACCESS_KEY_ID`
- `AWS_SECRET_ACCESS_KEY`
- `AWS_REGION`
- `AWS_DEFAULT_REGION`
- `S3_BUCKET`
- `S3_ENDPOINT`

This publish step is indirect deployment:

1. `build.sh` creates a container image from the current repo contents.
2. `prepare_remote.sh` pushes that image to `ghcr.io` or another registry tag.
3. `run_remote.sh` submits an Argo workflow that references that image.
4. The Kaizen playground pulls that image when it starts the workflow pod.

That is why a new workflow file in this repo currently implies a new image tag
if you want the playground to run it. The hosted workflow does not read your
local filesystem directly; it runs the image that was published earlier.

Remote auth is split across two separate systems:

- Kaizen/Argo auth: `ARGO_TOKEN` or `--kubeconfig` is used to talk to the
  workflow server and submit or inspect runs.
- GitHub/GHCR auth: `GHCR_TOKEN`, `GITHUB_TOKEN`, or `gh auth login` is only
  used to push a newly built image to the registry.

Practical rule:

- If the image already exists in the registry, Kaizen credentials are enough.
- If you need the playground to run a newly built image, you also need GHCR
  push credentials.

GHCR usually uses the same GitHub account identity as the repo, but not always
the same credential. For example, pushing commits over SSH does not authenticate
`podman` or `docker` to `ghcr.io`. For the registry push, the GitHub credential
must include package-write access such as `write:packages`.

Use the remote path when:

- you want one direct CLI submission into the shared Kaizen playground
- you already have a published image tag that the playground can pull
- you do not need the browser dashboard for launch/monitoring

### Step 2C – Dashboard path (local browser UI for the remote playground)

The simplest dashboard command is now:

```bash
./run_dashboard.sh
```

By default, the launcher automatically prepares a remote image when needed and
reuses the last prepared image when the git tree is clean and unchanged.
If the repo has changed, it will generate a new remote tag, run the build and
remote preparation flow, stop an older dashboard session already listening on
the selected port, and then start the dashboard.

If you want to force a fresh remote image anyway:

```bash
./run_dashboard.sh --prepare-remote
```

If you want to skip all automatic remote preparation and just start the local
dashboard process immediately:

```bash
./run_dashboard.sh --no-prepare-remote
```

If you do want to pin an explicit remote tag, you still can:

```bash
IMAGE=ghcr.io/org/cads-demo:demo123
./run_dashboard.sh --image "$IMAGE"
```

Then open `http://localhost:8080/`. The dashboard serves one button per
workflow under `workflows/`, lists recent hosted runs that point at repo
workflows, and polls the Kaizen playground every 5 seconds for live duration
updates.

The dashboard is a control surface for the **remote** path, not a separate
runtime. It still launches the configured container image in the hosted
playground. You can either prepare that image separately with
`./build.sh` + `./prepare_remote.sh`, or let `./run_dashboard.sh` handle
that automatically.

Remote preparation needs two different kinds of credentials:

- Argo credentials to talk to the Kaizen workflow server
- GHCR credentials to push a newly built image tag

Authentication for the dashboard follows this order:

- `ARGO_TOKEN` if it is already set in the shell
- `KUBECONFIG` if it is already set in the shell
- an explicit `--kubeconfig ...` passed to `run_dashboard.sh`
- `~/Kaizen_CADS/kubeconfig` automatically, if it exists

If remote preparation needs to push to GHCR, it will also try to authenticate
`podman` or `docker` automatically using `GHCR_TOKEN`, `GITHUB_TOKEN`, or a
valid `gh auth login -h github.com -s write:packages` session. The simplest one-time setup is:

```bash
gh auth login -h github.com -s write:packages
```

After that, `./run_dashboard.sh` can usually build, publish, and launch
without a separate manual `podman login ghcr.io` step.

This is the same split as the CLI remote path:

- dashboard launch and run monitoring use Kaizen credentials
- automatic image preparation uses GHCR credentials if a new image must be pushed

If `./run_dashboard.sh` is only launching workflows from an image that is
already published, then the dashboard only needs the Kaizen side.

Example with an explicit token:

```bash
export ARGO_TOKEN=...
./run_dashboard.sh
```

Example with an explicit kubeconfig:

```bash
./run_dashboard.sh --kubeconfig ~/Kaizen_CADS/kubeconfig
```

Example that forces a new remote image build/publish before launch:

```bash
./run_dashboard.sh --prepare-remote --kubeconfig ~/Kaizen_CADS/kubeconfig
```

Use the dashboard path when:

- you want to launch remote workflows from a browser instead of the CLI
- you want a live view of recent playground runs tied to repo workflows
- you want a simple operator UI, but still keep all secrets and Argo access on
  the local machine rather than in frontend code

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
- `input_series` – point a time-series FMU input at either a repo-local CSV or
  an S3 object. Exactly one source must be provided.
- `result` – optional JSON target path. The runner persists the final snapshot
  there (directories are created automatically).
- `start_time`, `stop_time`, `step_size` – rarely needed overrides when an FMU
  lacks a `DefaultExperiment`. Otherwise the runner uses the FMU’s defaults.
- `trace` – optional sampled inputs/outputs captured during the FMU run.

Example local CSV input:

```yaml
steps:
  - name: calculate_aecis
    fmu: fmu/models/CalculateAECIs.fmu
    input_series:
      csv: data/calculate_aecis_synthetic.csv
```

Example S3-backed input for the hosted playground:

```yaml
steps:
  - name: calculate_aecis
    fmu: fmu/models/CalculateAECIs.fmu
    input_series:
      s3:
        key: acoustic/demo/latest.csv
```

For S3-backed inputs, the workflow describes the data location while the
credentials stay outside the YAML. Locally, the runner reads the usual AWS env
vars. In the hosted Kaizen path, `run_remote.sh` and the dashboard-generated
manifests automatically project the playground secret
`storhy-argo-artifacts-s3-credentials` into those env vars.

If you want to inspect what is available in the bucket before writing a
workflow, use the helper script:

```bash
python3 scripts/list_s3_objects.py --long
python3 scripts/list_s3_objects.py --prefix acoustic/
```

By default, `scripts/list_s3_objects.py` auto-loads missing bucket and
credential values from the Kaizen playground secret
`storhy-argo-artifacts-s3-credentials` in namespace `playground`, using
`$KUBECONFIG` or `~/Kaizen_CADS/kubeconfig` when available.

The script also supports:

- `--flat`, `--delimiter ''`, and `--limit` for different listing modes
- `--path-style` for S3-compatible object stores
- `--secret-name`, `--secret-namespace`, and `--kubeconfig` to change the
  Kubernetes secret lookup
- `--no-k8s-secret` if you want to rely only on explicit CLI args or env vars

If your laptop cannot read the playground secret directly via the Kubernetes
API, use the in-cluster listing workflow instead:

```bash
./run_list_s3_objects.sh
./run_list_s3_objects.sh --prefix acoustic/ --limit 500
```

This submits a small Argo workflow to the playground, injects the same S3
secret that remote CADS workflows use, and prints the bucket prefixes/keys in
the workflow logs. The checked-in reference manifest lives at
`deploy/argo/list_s3_objects.yaml`.

To inspect one concrete object and print its metadata plus a small content
preview from inside the playground, use:

```bash
./run_inspect_s3_object.sh artifacts/my-file
./run_inspect_s3_object.sh artifacts/my-file --bytes 8192
```

The inspector prints the bucket name, key, size, content type, timestamp, and
either a UTF-8 text preview or a base64 preview for binary content.

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
