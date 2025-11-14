# Run Pipeline

`run.sh` submits a workflow YAML to the Argo controller inside the local
Minikube cluster. The script focuses entirely on the Argo mode showcased by the
demo. Run it after `./build.sh` so the image, PVC manifest, and controller are
ready.

```bash
./run.sh workflows/python_chain.yaml
./run.sh workflows/python_chain.yaml --image ghcr.io/org/cads-demo:dev
```

## What the script does

1. **Argument parsing** – Ensures the first positional argument is a workflow
   file relative to the repo root. Optional `--image` switches to a different
   container tag.
2. **Environment wiring** – Adds `./.local/go/bin` and `./.local/bin` to `PATH`
   so the locally installed `kubectl` and `argo` CLIs (from `prepare.sh`) can be
   used even without shell profile edits.
3. **Kubernetes sanity check** – Verifies that `kubectl config current-context`
   succeeds; this fails fast if Minikube isn’t running or `$KUBECONFIG` points
   elsewhere.
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

## Arguments

| Flag | Description |
|------|-------------|
| `<workflow.yaml>` | **Required.** Path to the workflow file under the repo root. |
| `--image <name:tag>` | Override the image tag used in the generated manifest. |
| `-h`, `--help` | Show usage information. |

## Prerequisites

- `./prepare.sh` and `./build.sh` completed successfully.
- Minikube is running (`minikube status -p minikube` shows `Running`) and the
  kube context is active.
- The Argo controller exists in the `argo` namespace (handled automatically by
  `build.sh`, but `kubectl get pods -n argo` is a quick check).

## Artifacts

Running the script updates the manifests in:

- `deploy/argo/<workflow>-workflow.yaml`
- `deploy/storage/data-pvc.yaml`

Argo workflow pods mount `/app/data` to the shared PVC, so outputs persist
between runs. Inspect them with `kubectl exec` or by attaching another helper
pod to the claim.

## Troubleshooting

- **“kubectl cannot determine the current context”** – Ensure Minikube is
  running (`minikube start`) and the `minikube` context is your current
  selection (`kubectl config use-context minikube` if needed).
- **Argo submission fails** – Verify the controller exists with
  `kubectl get pods -n argo`. If it is missing, rerun `./build.sh` or execute
  `scripts/ensure_argo_workflows.sh`.
- **PVC errors** – Delete the claim (`kubectl delete pvc cads-data-pvc -n argo`)
  and rerun `./run.sh …` to recreate it. Adjust storage class/size in
  `scripts/generate_manifests.sh` if your cluster requires custom settings.
