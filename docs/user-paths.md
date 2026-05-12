# User Paths

The repo is organized around three OS-independent user paths. Use the same
path command on macOS and Linux.

## 1. Playground Dashboard

**Default for most users.**

Connect a local dashboard to an existing Kaizen Playground environment where a
workflow image has already been published by someone else.

```bash
./run_playground.sh
```

This path:

- uses your Kaizen kubeconfig or `ARGO_TOKEN`
- can inspect existing Playground runs
- can launch new runs with the image configured in `config/playground.env`
- does not build FMUs
- does not build or publish a container image
- does not use Minikube
- does not require Podman or Docker

To test another already published image, use the advanced override:

```bash
./run_playground.sh --image ghcr.io/org/cads-fmi-demo:tag
```

## 2. Publish To Playground

**For developers or release owners updating the Playground image.**

Build the workflows locally, publish the workflow image to GHCR, prepare the
Kaizen Playground to use that image, and start the dashboard against it.

```bash
./run_publish.sh
```

This path:

- builds the local Go/FMIL workflow binaries
- builds the workflow container image
- publishes the image to GHCR
- replaces the configured Playground image tag with the full current repo image
- validates access to the Kaizen Playground
- starts the dashboard against the published image
- uses the image configured in `config/playground.env` unless `--image` is
  provided
- requires Podman or Docker
- requires GHCR package-write access
- requires Kaizen Playground credentials

Use this only when you intentionally want to update what the Playground runs.
`./run_publish.sh` does not publish one workflow file in isolation. It builds
and pushes the full repository image, including all workflow YAML files, FMUs,
and runner code present in the checkout. For the common replacement workflow
flow, develop and test one workflow locally with
`./run_local_dev.sh workflows/my_workflow.yaml`, commit or keep that replacement
in the repo, then run `./run_publish.sh` to update the configured Playground
image tag.

## 3. Local Dev

**For workflow/model developers testing one workflow at a time without Kaizen
Playground.**

Build and run workflows against a local Minikube + Argo environment.

```bash
./run_local_dev.sh workflows/tests/python_chain.yaml
```

This path:

- starts or reuses local Minikube
- installs Argo Workflows locally
- builds the local workflow image
- loads the image into Minikube
- submits workflows with `scripts/commands/run_local.sh`
- copies artifacts back into the repository
- does not start a dashboard
- does not publish to GHCR
- does not require Kaizen Playground credentials

This is intentionally a local CLI workflow path. The browser dashboard is only
for the Kaizen Playground paths.
