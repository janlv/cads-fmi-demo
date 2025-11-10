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

Both binaries auto-detect the repository root; override with `--workdir /path/to/repo`
if you run them from a different directory.
