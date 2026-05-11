# Local Workflow Development With Minikube

Use this path when you are developing or debugging one workflow/model locally.
It starts a local Minikube cluster, installs Argo Workflows there, loads the
local image, and copies artifacts back into the repository.

This path is for developers. It is not needed for a dashboard user who connects
to the hosted Kaizen Playground.

This is the **Local Dev** path in [`user-paths.md`](user-paths.md).
It is intentionally CLI-only and does not start a dashboard. The browser
dashboard is only for the Kaizen Playground paths.

## Prepare Local Development

```bash
./run_local_dev.sh workflows/python_chain.yaml
```

The expanded form is:

```bash
./prepare.sh --with-local-minikube
scripts/commands/build.sh
scripts/commands/run_local.sh workflows/python_chain.yaml
```

Use this path when you need to test workflow YAML changes, model changes, Argo
manifest generation, local image loading, or artifact copy-back behavior before
publishing a workflow image for others. This path does not publish to GHCR and
does not start a dashboard.
