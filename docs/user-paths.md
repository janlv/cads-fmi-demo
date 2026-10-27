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
- builds the workflow container image for `linux/amd64` by default
- publishes the image to GHCR
- replaces the configured Playground image tag with the full current repo image
- validates access to the Kaizen Playground
- starts the dashboard against the published image
- uses the image configured in `config/playground.env` unless `--image` is
  provided
- requires Podman or Docker
- requires GHCR package-write access
- requires Kaizen Playground credentials

The default platform can be overridden with `./run_publish.sh --platform ...`
or `CADS_PLAYGROUND_IMAGE_PLATFORM`, but ordinary Playground publishes should
keep the default.

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

This is intentionally a local CLI workflow path. The browser dashboard is
normally used with the Kaizen Playground paths, but it also works without
Kaizen: started with `--execution local`, the service runs the workflows on
this machine instead of submitting them to Argo, as in the near-realtime demo
below.

## Near-realtime assessment demo (local database)

**For showing the operator Plots view with a growing trend, without Kaizen.**

A local TimescaleDB stands in for the STOR-HY database, the dashboard service
runs the workflows itself, and new measurement days are replayed one at a
time. The models are deterministic placeholders and the data is synthetic.

```bash
scripts/commands/run_db.sh                              # database + the bundled first day
export CADS_DB_URL=postgres://cads:cads@127.0.0.1:5432/storhy

# a runner image that matches this checkout (Apple silicon: native arm64 build)
podman build --platform linux/arm64 -t cads-fmi-demo:dev-arm64 "$PWD"
(cd orchestrator/service && CGO_ENABLED=0 GOWORK=off go build -o ../../bin/cads-workflow-service ./cmd/cads-workflow-service)

bin/cads-workflow-service --serve --execution local --local-runner-image cads-fmi-demo:dev-arm64
```

Then:

1. Open `http://127.0.0.1:8080`, pick Le Cheylas; the site opens on the
   **Plots** view with the measured vibration and turbidity of the first day.
   The status pill in the header reads "Ready, local runs" (amber: workflows
   run on this machine, the hosted playground is not used).
2. Press **Run assessment**: both Cheylas workflows run locally on the newest
   data (status Submitting, Running, Updated), the runs appear in the **Runs**
   view with a "ran locally" badge, and the RUL, damage, condition and cost
   charts fill in. Pressing Run assessment again reports "Up to date".
3. Replay the next day and let the service assess it without clicking:

   ```bash
   scripts/commands/run_db.sh --next --notify
   ```

   Each call loads one `upcoming/batch-<date>/`; the new runs continue the
   damage and exposure of the previous ones. After a few days switch the span
   to **7d** to see the trend; pinch or Ctrl+scroll in any chart, or drag the
   slider under the last chart, to zoom all of them. The seven
   `upcoming/` days come from
   `python3 scripts/dev/generate_synthetic_measurements.py --days 8`; rerun it
   if they are missing, or pass a larger `--days` when `run_db.sh --next`
   reports no newer upcoming batch.
4. To start the chain over, run `scripts/commands/run_db.sh --reset`.

Le Cheylas is the site that shows the chain. The Alqueva Battery-EMS workflow
has no `data_window`: Run assessment reruns its fixed window every time, so it
never continues from the previous run and never reports "Up to date".

On Linux with FMIL installed the image is optional: build with
`scripts/commands/build.sh` and start the service with `--execution local`
only, which runs `bin/cads-workflow-runner` directly. All service flags are in
the table in [`run.md`](run.md#near-realtime-local-flow). Without
`CADS_DB_URL` the Plots view shows the latest finished run of each workflow
with a note, and the data-window runs fall back to the bundled CSVs and do not
chain. Run assessment is disabled when the service has neither Argo access nor
local runs (the status pill then shows red "Attention"). On the Playground
paths above, Run assessment submits the site's workflows to hosted Argo
instead; trends there need the `storhy-cads-db` secret (see
[`architecture.md`](architecture.md#database-backed-data-interface)).
