# Go-based workflow runner & service

The binaries in this directory execute CADS workflows directly through FMIL
using cgo. Python now lives only at the FMU-authoring edge (via `pythonfmu`);
every workflow run goes through Go + FMIL.

Both executables share the `internal/fmi` bindings and the `workflow` package:

- `cmd/cads-workflow-runner` – CLI equivalent of the old Python script.
- `cmd/cads-workflow-service` – HTTP API and browser dashboard; it submits
  runs to Argo or executes them locally. It is built with `CGO_ENABLED=0` and
  needs no FMIL (the `internal/fmi` stub stands in).

## Prerequisites

1. Go 1.22+ (`go version`).
2. FMIL (https://github.com/modelon-community/fmi-library) installed somewhere
   on disk. Point Go at it:

   ```bash
   export FMIL_HOME=$HOME/fmil
   export LD_LIBRARY_PATH=$FMIL_HOME/lib:$LD_LIBRARY_PATH
   export PKG_CONFIG_PATH=$FMIL_HOME/lib/pkgconfig:$PKG_CONFIG_PATH
   export CGO_ENABLED=1
   export CGO_CFLAGS="-I$FMIL_HOME/include"
   export CGO_CXXFLAGS="-I$FMIL_HOME/include"
   export CGO_LDFLAGS="-L$FMIL_HOME/lib"
   export GOWORK=off
   ```

## Build

```bash
cd orchestrator/service
go build ./cmd/cads-workflow-runner
go build ./cmd/cads-workflow-service
```

Set `GOWORK=off` if you normally use a Go workspace.

## Run once

```bash
./cads-workflow-runner --workflow workflows/tests/python_chain.yaml
./cads-workflow-service --workflow workflows/tests/python_chain.yaml
```

## Serve HTTP

```bash
./cads-workflow-service --serve --addr :8080
curl -X POST localhost:8080/run \
     -H 'Content-Type: application/json' \
     -d '{"workflow":"workflows/tests/python_chain.yaml"}'
```

When serving, the same binary also exposes the browser dashboard and the JSON
endpoints (the dashboard itself is described in
[`docs/architecture.md`](../../docs/architecture.md#dashboard-path)):

| Route | Purpose |
| --- | --- |
| `GET /`, `GET /static/*` | dashboard page and embedded assets |
| `GET /api/config` | `remoteEnabled`, `databaseConfigured`, `localRunsEnabled`, `executionMode`, `localRunner`, `problems`, `version`, Argo settings |
| `GET /api/data?site=` | ingested channel status of a site (`configured: false` without a database) |
| `GET /api/workflows` | workflow catalog from the YAML `metadata` blocks |
| `GET /api/runs?limit=20` | Argo, local and stored runs merged, newest first |
| `POST /api/runs` | start one workflow (`{"workflow": "workflows/..."}`) |
| `GET /api/runs/{name}` | one run |
| `GET /api/runs/{name}/results` | its step results (database first, else pod logs or local memory) |
| `POST /api/sites/{site}/runs` | start every workflow of a site; answers `{site, runs[], errors[], upToDate[]}` with 200, 502 when all failed, 404 when the site has no workflows |
| `GET /api/sites/{site}/history` | Plots data (`span`, `channels`, `series`, `max_points`) |
| `POST /run` | run a workflow in-process and return the result JSON |

Flags (`./cads-workflow-service --help`):

| Flag | Meaning |
| --- | --- |
| `--serve`, `--addr :8080` | start the HTTP service on this address |
| `--workflow PATH` | run one workflow once and print the result (with `--serve`, then keep serving) |
| `--workdir DIR` | explicit repository root |
| `--version` | print the service version and exit |
| `--argo-server`, `--argo-namespace`, `--argo-service-account` | hosted Argo target (defaults `ARGO_SERVER`/`argoworkflows.cads.kzslab.dev`, `ARGO_NAMESPACE`/`playground`, `ARGO_SERVICE_ACCOUNT`) |
| `--remote-image IMAGE` | hosted workflow image (default `CADS_WORKFLOW_IMAGE`) |
| `--kubeconfig PATH` | kubeconfig used when `ARGO_TOKEN` is not set |
| `--max-runtime-seconds N` | deadline of workflows without `limits.max_runtime_seconds` (default `CADS_MAX_RUNTIME_SECONDS` or 900, capped by `CADS_MAX_RUNTIME_CEILING_SECONDS`) |
| `--db-url URL` | CADS database (default `CADS_DB_URL`) |
| `--execution auto\|argo\|local` | where dashboard runs execute; see [`docs/run.md`](../../docs/run.md#local-demo) |
| `--runner-bin PATH` | runner binary for local runs (default `<workdir>/bin/cads-workflow-runner`) |
| `--local-runner-image IMAGE` | run local runs inside this image (env `CADS_LOCAL_RUNNER_IMAGE`) |
| `--local-db-url URL` | database URL inside that container (env `CADS_LOCAL_DB_URL`) |

With `--execution local` (or `auto` without hosted Argo access but with a
runner image or binary) the service runs each workflow on this machine, either
through the runner binary or inside `--local-runner-image` with `workflows/`
mounted, and gives the runs ids `local-...`. Their results come from memory or
the database, and they appear in the run list next to Argo and stored runs.

Remote Kaizen playground access is configured with flags or environment
variables:

```bash
export ARGO_TOKEN=...
./cads-workflow-service --serve --addr :8080
./cads-workflow-service --serve --addr :8080 --kubeconfig .local/kaizen/kubeconfig
```

From the repo root you can also use the convenience launcher:

```bash
./run_playground.sh
```

By default, `run_playground.sh` connects the local dashboard to the configured
Playground image without building or publishing. Before starting the new
service, it also stops an older dashboard session already listening on the
selected port.

When you need to publish a new bundled `ghcr.io/...` image, use
`./run_publish.sh`. It tries to authenticate `podman` or `docker` to GHCR
using `GHCR_TOKEN`, `GITHUB_TOKEN`, or a valid
`gh auth login -h github.com -s write:packages` session.

For the hosted path the auth model has two parts:

- Kaizen/Argo auth for listing runs and submitting workflows
- GitHub/GHCR auth only when a new image has to be pushed before launch

If the selected image is already published, the dashboard only needs the Kaizen
side. If the launcher needs to build and publish a fresh image, it also needs
GHCR push credentials.

Hosted workflow submissions also project the Kaizen S3 secret
`storhy-argo-artifacts-s3-credentials` into the runner pod as standard env vars
(`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`,
`AWS_DEFAULT_REGION`, `S3_BUCKET`, `S3_ENDPOINT`). That lets workflow YAML use
`input_series.s3` without per-run manifest edits.

If you want to force a fresh remote image build/publish before launch:

```bash
./run_publish.sh
```

If you want to skip automatic remote preparation entirely:

```bash
./run_playground.sh
```

You can still pass `--image ghcr.io/org/cads-demo:demo123` if you want to pin
an explicit tag.

Both binaries auto-detect the repository root; override with `--workdir /path/to/repo`
if you run them from a different directory.
