# Run Pipelines

This repository supports three active execution paths:

- `run_local.sh` for the local Minikube + Argo demo cluster
- `run_remote.sh` for direct submissions to the hosted Kaizen playground
- `run_dashboard.sh` for the browser UI that launches the same hosted runs

All three paths execute the same workflow YAML files through the same Go/FMIL
runtime. The difference is where Argo runs the workflow and how you interact
with it.

## Local flow

Use the local path when you want a self-contained Minikube loop and automatic
artifact copy-back into the repo.

```bash
./prepare_local.sh
./build.sh
./run_local.sh workflows/python_chain.yaml
```

`run_local.sh` is the supported local Minikube manifest path. It:

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
./build.sh
./prepare_remote.sh
./run_remote.sh workflows/python_chain.yaml
```

`prepare_remote.sh` publishes a hosted image tag and caches it for later reuse.
If you omit `--image`, it generates a fresh remote tag automatically and reuses
the most recent local build as the source image.

`run_remote.sh` then:

1. Resolves Argo auth from `ARGO_TOKEN`, `KUBECONFIG`, `--kubeconfig`, or the
   default `~/Kaizen_CADS/kubeconfig`.
2. Reuses the last image prepared by `prepare_remote.sh` when no explicit image
   is given.
3. Calls `scripts/generate_remote_workflow.sh` to emit the hosted manifest.
4. Submits the workflow to `argoworkflows.cads.kzslab.dev` with a unique run
   name in the `playground` namespace.

Hosted manifests automatically inject the playground S3 secret into standard AWS
environment variables so workflows can read S3-backed inputs without per-run
manifest edits.

## Dashboard flow

Use the dashboard when you want a browser UI for the same hosted playground.

```bash
./run_dashboard.sh
```

The dashboard serves a local web UI and launches the same hosted workflow image
that `run_remote.sh` uses. It supports:

- automatic remote image preparation when needed
- recent-run status and results
- AECIS trace visualization
- workflow launch buttons for files under `workflows/`

Useful variants:

```bash
./run_dashboard.sh --prepare-remote
./run_dashboard.sh --no-prepare-remote
./run_dashboard.sh --kubeconfig ~/Kaizen_CADS/kubeconfig
```

## S3 helpers

The repo also includes helper entrypoints for inspecting S3-backed inputs from
the playground:

```bash
./run_list_s3_objects.sh --prefix artifacts/
./run_inspect_s3_object.sh artifacts/my-file
./run_argo.sh logs <workflow-name>
```

These helpers are remote-only and reuse the same prepared hosted image and
Kaizen auth defaults as the main remote flow.

## Arguments

Common patterns:

- `<workflow.yaml>` is always a repo-relative workflow path such as
  `workflows/python_chain.yaml`
- `--image` overrides the image tag for build or submission
- `--kubeconfig` overrides the default Kaizen kubeconfig

Run `--help` on any surviving entrypoint for the current interface:

- `./prepare_local.sh --help`
- `./run_local.sh --help`
- `./build.sh --help`
- `./prepare_remote.sh --help`
- `./run_remote.sh --help`
- `./run_dashboard.sh --help`

## Troubleshooting shortcuts

- Local Minikube issues: rerun `./prepare_local.sh`
- Remote Argo auth issues: rerun `./prepare_remote.sh` or pass `--kubeconfig`
- Hosted image mismatch: rerun `./build.sh` followed by `./prepare_remote.sh`
- S3 inspection/listing issues: verify the remote helper is using the latest
  prepared image and inspect the logs with `./run_argo.sh logs <workflow-name>`
