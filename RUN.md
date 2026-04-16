# Run Pipelines

The repository now exposes separate execution paths for the local Minikube demo
and the hosted KAIZEN playground.

```bash
./run_local.sh workflows/python_chain.yaml
./run_remote.sh workflows/python_chain.yaml --image ghcr.io/org/cads-demo:dev --kubeconfig ~/Kaizen_CADS/kubeconfig
```

`run.sh` remains as a compatibility wrapper for `run_local.sh`.

## Local flow

`run_local.sh` submits a workflow YAML to the Argo controller inside the local
Minikube cluster. Run it after `./build.sh` so the image exists locally.

1. **Argument parsing** – Ensures the first positional argument is a workflow
   file relative to the repo root. Optional `--image` switches to a different
   container tag.
2. **Environment wiring** – Adds `./.local/go/bin` and `./.local/bin` to `PATH`
   so the locally installed `kubectl` and `argo` CLIs (from `prepare_local.sh`)
   can be used even without shell profile edits.
3. **Local cluster setup** – Ensures the Minikube profile is running, verifies
   the active kube context is `minikube`, syncs custom CAs into Minikube,
   ensures the local Argo controller exists, and preloads the selected image.
4. **Manifest generation** – Calls `scripts/generate_manifests.sh --workflow …`
   to render the Argo Workflow manifest (`deploy/argo/<name>-workflow.yaml`) and
   the PVC manifest (`deploy/storage/data-pvc.yaml`). The generator also plugs
   the selected image into the template.
5. **PVC apply** – `scripts/run_argo_workflow.sh` (invoked by `run.sh`) applies
   the PVC manifest so the workflow always mounts the `cads-data-pvc` claim in
   the `argo` namespace before submission.
6. **Workflow submission** – The wrapper submits the workflow via
   `argo submit`, names it after the workflow file (sanitized + `cads-` prefix),
   and tails progress with `argo watch`.
7. **Artifact collection** – Copies `/app/data` out of the shared PVC into
   `data/run-artifacts/<workflow>-<timestamp>`.

## Remote flow

`run_remote.sh` submits a hosted-Argo manifest directly to the KAIZEN Argo
server. Run it after `./build.sh --image ...` and
`./prepare_remote.sh --image ...`.

1. Resolves the Argo token from `ARGO_TOKEN` or `--kubeconfig`.
2. Sources the host CA helper so outbound TLS works behind corporate proxies.
3. Generates a remote workflow manifest through
   `scripts/generate_remote_workflow.sh`.
4. Submits it with `argo submit --argo-http1` against
   `argoworkflows.cads.kzslab.dev`.
5. Overrides the manifest name with `cads-<workflow>-<timestamp>` to avoid name
   collisions in the shared `playground` namespace.
6. Watches by default and fetches workflow status/logs automatically if the
   submission fails.

## Arguments

| Flag | Description |
|------|-------------|
| `<workflow.yaml>` | **Required.** Path to the workflow file under the repo root. |
| `--image <name:tag>` | Override the image tag used in the generated manifest. |
| `--kubeconfig <path>` | Remote only. Extract the Argo token from the supplied kubeconfig. |
| `-h`, `--help` | Show usage information. |

## Prerequisites

- Local flow: `./prepare_local.sh` and `./build.sh` completed successfully.
- Remote flow: `./build.sh --image ...` and `./prepare_remote.sh --image ...`
  completed successfully.

## Artifacts

Running the local script updates the manifests in:

- `deploy/argo/<workflow>-workflow.yaml`
- `deploy/storage/data-pvc.yaml`

Running the remote script updates the manifest in:

- `deploy/argo/<workflow>-remote-workflow.yaml`

Argo workflow pods in the local flow mount `/app/data` to the shared PVC, so
outputs persist between runs. Hosted remote runs do not use the local PVC path.

## Troubleshooting

- **“Current kubectl context is ...”** – `run_local.sh` only targets the local
  Minikube cluster. Switch to `minikube` before rerunning.
- **Remote Argo authentication fails** – Re-run `./prepare_remote.sh` and verify
  that `ARGO_TOKEN` or the supplied kubeconfig contains the playground bearer
  token.
- **PVC errors** – Delete the claim (`kubectl delete pvc cads-data-pvc -n argo`)
  and rerun `./run_local.sh …` to recreate it. Adjust storage class/size in
  `scripts/generate_manifests.sh` if your cluster requires custom settings.
