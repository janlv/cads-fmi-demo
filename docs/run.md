# Run Pipelines

This repository has three user-path entrypoints:

- `run_playground.sh` for the default Playground dashboard path
- `run_publish.sh` for build/publish/update of the Playground image
- `run_local_dev.sh` for CLI-only local Minikube workflow/model development

The lower-level execution scripts remain available:

- `scripts/commands/run_local.sh` for the local Minikube + Argo demo cluster
- `scripts/commands/run_remote.sh` for direct submissions to the hosted Kaizen playground
- `scripts/commands/run_dashboard.sh` for the browser UI that launches the same hosted runs

All three paths execute the same workflow YAML files through the same Go/FMIL
runtime. The difference is where Argo runs the workflow and how you interact
with it.

See [`user-paths.md`](user-paths.md) for the OS-independent user paths:
Playground Dashboard, Publish to Playground, and CLI-only Local Dev.

## Local flow

Use the local path when you want a self-contained Minikube loop and automatic
artifact copy-back into the repo. This path does not start a dashboard.

```bash
./run_local_dev.sh workflows/tests/python_chain.yaml
```

The wrapper runs the same prepare/build/local submission sequence on Linux and
macOS.

`scripts/commands/run_local.sh` is the supported local Minikube manifest path. It:

1. Verifies the requested workflow file exists.
2. Ensures the local Minikube cluster and in-cluster Argo controller are ready.
3. Loads the selected image into Minikube.
4. Delegates manifest rendering and submission to:
   - `scripts/run_argo_workflow.sh`
   - `scripts/generate_manifests.sh`
5. Copies `/app/data` from the shared PVC into `data/run-artifacts/`.

The local path still relies on generated manifests under `deploy/`:

- `deploy/argo/<workflow>-workflow.yaml`
- `deploy/storage/data-pvc.yaml`

## Remote flow

Use the remote path when you want one CLI submission into the hosted Kaizen
playground.

```bash
./run_publish.sh
```

`scripts/commands/prepare_remote.sh` publishes a hosted image tag and caches it for later reuse.
If you omit `--image`, it generates a fresh remote tag automatically and reuses
the most recent local build as the source image.

`scripts/commands/run_remote.sh` then:

1. Resolves Argo auth from `ARGO_TOKEN`, `KUBECONFIG`, `--kubeconfig`, or the
   default `.local/kaizen/kubeconfig`.
2. Reuses the last image prepared by `scripts/commands/prepare_remote.sh` when
   no explicit image is given.
3. Calls `scripts/generate_remote_workflow.sh` to emit the hosted manifest.
4. Submits the workflow to `argoworkflows.cads.kzslab.dev` with a unique run
   name in the `playground` namespace.

Hosted manifests automatically inject the playground S3 secret into standard AWS
environment variables so workflows can read S3-backed inputs without per-run
manifest edits.

## Dashboard flow

Use the dashboard when you want a browser UI for the same hosted playground.

```bash
./run_playground.sh
```

The dashboard serves a local web UI and launches the same hosted workflow image
that `scripts/commands/run_remote.sh` uses. It supports:

- automatic remote image preparation when needed
- recent-run status and results
- AECIS trace visualization
- workflow launch buttons for files under `workflows/`

Useful variants:

```bash
./run_playground.sh
./run_publish.sh
./run_playground.sh --kubeconfig .local/kaizen/kubeconfig
```

Each site has three views. **Plots** (the default) shows the assessment line,
the condition table, history charts from the database, span buttons (6h to
All) and **Run assessment**, which runs every workflow of the site on the
newest data; on the Playground it submits those workflows to hosted Argo.
**Workflow** shows the per-workflow wiring diagram and the Workflow and FMU
cards, with a **Launch workflow** button for a single hosted run. **Runs**
shows the run history and the run record. The status pill in the header tells
whether runs and the database are available.

The dashboard remembers the last view; `?view=plots`, `?view=workflow` or
`?view=runs` in the URL opens a view directly. The full description is in
[`architecture.md`](architecture.md#dashboard-path).

## Near-realtime local flow

With a local database the service can run the workflows itself and the Plots
view becomes a near-realtime loop: ingest a day, assess it, plot the trend.

```bash
scripts/commands/run_db.sh
export CADS_DB_URL=postgres://cads:cads@127.0.0.1:5432/storhy
scripts/commands/build.sh --image cads-fmi-demo:dev   # runner image + bin/cads-workflow-service
bin/cads-workflow-service --serve --execution local --local-runner-image cads-fmi-demo:dev
# browser: Le Cheylas -> Plots -> Run assessment
scripts/commands/run_db.sh --next --notify      # next synthetic day, assessed automatically
```

Service options for local execution (`bin/cads-workflow-service --help` lists
all flags):

| Flag | Meaning |
| --- | --- |
| `--execution auto\|argo\|local` | `auto` (default) uses Argo when hosted access is configured, else local runs when `--local-runner-image` is set or the runner binary exists, else Argo; `local` fails at startup when neither the image nor the binary exists |
| `--runner-bin PATH` | runner binary for local runs (default `<workdir>/bin/cads-workflow-runner`) |
| `--local-runner-image IMAGE` | run the runner inside this image with `workflows/` mounted (env `CADS_LOCAL_RUNNER_IMAGE`); needed on macOS, where the demo FMUs are Linux builds |
| `--local-db-url URL` | database URL inside that container (env `CADS_LOCAL_DB_URL`; default the service URL with a `127.0.0.1`/`localhost` host rewritten to `host.containers.internal` for podman or `host.docker.internal` for docker) |
| `--db-url URL` | CADS database for input data status, stored runs and history (default `CADS_DB_URL`) |
| `--max-runtime-seconds N` | deadline of workflows without `limits.max_runtime_seconds` (default `CADS_MAX_RUNTIME_SECONDS` or 900) |

Workflows with a `data_window` assess only the data since their previous run
and continue its state; with nothing new, Run assessment reports "Up to date"
and a run that starts anyway finishes as `skipped` ("no new data"). The
Alqueva workflow has no `data_window`, so it reruns its fixed window every
time. See
[`user-paths.md`](user-paths.md#near-realtime-assessment-demo-local-database)
for the step-by-step demo and
[`workflows.md`](workflows.md#assessment-windows-and-state-carry-over-data_window-carry_over)
for the window rules.

## S3 helpers

The repo also includes helper entrypoints for inspecting S3-backed inputs from
the playground:

```bash
scripts/commands/run_list_s3_objects.sh --prefix artifacts/
scripts/commands/run_inspect_s3_object.sh artifacts/my-file
scripts/commands/run_argo.sh logs <workflow-name>
```

These helpers are remote-only and reuse the same prepared hosted image and
Kaizen auth defaults as the main remote flow.

## Arguments

Common patterns:

- `<workflow.yaml>` is always a repo-relative workflow path such as
  `workflows/tests/python_chain.yaml`
- `--image` overrides the image tag for build or submission
- `--kubeconfig` overrides the default Kaizen kubeconfig

Run `--help` on any entrypoint for the current interface:

- `./prepare.sh --help`
- `./run_playground.sh --help`
- `./run_publish.sh --help`
- `./run_local_dev.sh --help`
- `scripts/commands/prepare_local.sh --help`
- `scripts/commands/run_local.sh --help`
- `scripts/commands/build.sh --help`
- `scripts/commands/prepare_remote.sh --help`
- `scripts/commands/run_remote.sh --help`
- `scripts/commands/run_dashboard.sh --help`

## Troubleshooting shortcuts

- Local Minikube issues: rerun `./prepare.sh --with-local-minikube`
- Remote Argo auth issues: rerun `./run_playground.sh` or pass `--kubeconfig`
- Hosted image mismatch: rerun `./run_publish.sh`
- S3 inspection/listing issues: verify the remote helper is using the latest
  prepared image and inspect the logs with `scripts/commands/run_argo.sh logs <workflow-name>`
