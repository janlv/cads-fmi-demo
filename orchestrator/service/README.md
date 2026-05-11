# Go-based workflow runner & service

The binaries in this directory execute CADS workflows directly through FMIL
using cgo. Python now lives only at the FMU-authoring edge (via `pythonfmu`);
every workflow run goes through Go + FMIL.

Both executables share the `internal/fmi` bindings and the `workflow` package:

- `cmd/cads-workflow-runner` – CLI equivalent of the old Python script.
- `cmd/cads-workflow-service` – HTTP API (`POST /run`) for Argo/minikube demos.

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
./cads-workflow-runner --workflow workflows/python_chain.yaml
./cads-workflow-service --workflow workflows/python_chain.yaml
```

## Serve HTTP

```bash
./cads-workflow-service --serve --addr :8080
curl -X POST localhost:8080/run \
     -H 'Content-Type: application/json' \
     -d '{"workflow":"workflows/python_chain.yaml"}'
```

When serving, the same binary now also exposes a browser dashboard at `/` plus
the JSON endpoints:

- `GET /api/config`
- `GET /api/workflows`
- `GET /api/runs?limit=20`
- `GET /api/runs/{name}`
- `POST /api/runs`

Remote Kaizen playground access is configured with flags or environment
variables:

```bash
export ARGO_TOKEN=...
./cads-workflow-service --serve --addr :8080
./cads-workflow-service --serve --addr :8080 --kubeconfig ~/Kaizen_CADS/kubeconfig
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

The dashboard is still a frontend for the hosted Kaizen path, so the auth model
has two parts:

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
