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
