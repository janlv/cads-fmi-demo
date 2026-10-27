# Local Workflow Development With Minikube

Use this path when you are developing or debugging one workflow/model locally.
It starts a local Minikube cluster, installs Argo Workflows there, loads the
local image, and copies artifacts back into the repository.

This path is for developers. It is not needed for a dashboard user who connects
to the hosted Kaizen Playground.

This is the **Local Dev** path in [`user-paths.md`](user-paths.md).
It is intentionally CLI-only and does not start a dashboard. The browser
dashboard normally runs against the Kaizen Playground; to use it without
Kaizen, start the service with `--execution local` so it runs the workflows
on this machine (see the
[near-realtime demo](user-paths.md#near-realtime-assessment-demo-local-database)).

## Prepare Local Development

```bash
./run_local_dev.sh workflows/tests/python_chain.yaml
```

The expanded form is:

```bash
./prepare.sh --with-local-minikube
scripts/commands/build.sh
scripts/commands/run_local.sh workflows/tests/python_chain.yaml
```

Use this path when you need to test workflow YAML changes, model changes, Argo
manifest generation, local image loading, or artifact copy-back behavior before
publishing a workflow image for others. This path does not publish to GHCR and
does not start a dashboard.
