# Troubleshooting

This guide covers the most common issues encountered while running the CADS FMI
demo scripts. Work through the sections in order; each one lists the symptom,
probable cause, and recommended fix.

---

## Start with the dashboard status pill

The traffic-light pill under the logo is the first diagnostic. Hover it for a
one-line summary, click it for the details (execution mode, hosted playground
state and problems, database state, run-refresh errors, dashboard version).

### Why is Run assessment (or Launch workflow) disabled?
- A red "Attention" pill saying "No way to run workflows" means the service
  has neither Argo access nor local runs. Fix the Kaizen credentials (see
  [Playground or hosted Argo problems](#playground-or-hosted-argo-problems)),
  or start the service with `--execution local` and a runner image or binary
  (see [`run.md`](run.md#local-demo)).
- **Launch workflow** submits to the hosted playground only, so it stays
  disabled without Argo access even when local runs work.
- An amber pill is usable: "Ready, no database" means the Plots view shows only
  the latest finished run; "Ready, local runs" means workflows run on this
  machine.

---

## Host preparation fails

### `age` is missing
- Install `age`, then rerun `./prepare.sh`. Debian/Ubuntu hosts can normally let
  `prepare.sh` install it through `apt`; macOS users can run `brew install age`
  or `sudo port install age`.

### `Required command 'sudo' not found` or `apt-get` omissions
- Ensure you are on a Debian/Ubuntu host with sudo privileges. The script needs
  `sudo apt-get install` to pull Podman and helper packages.

### `Unsupported architecture: ...`
- Only `amd64` and `arm64` are supported today. Add the required checks/tarballs
  in `scripts/lib/tooling.sh` if you need another architecture.

### Minikube refuses to start
- Typical causes: no container runtime, stale profiles, or rootless Podman not
  configured.
  1. Verify `podman info` (or `docker info`) works.
  2. Run `minikube delete -p minikube && ./prepare.sh --with-local-minikube`
     to rebuild the profile.
  3. Check `/etc/subuid` / `/etc/subgid` entries for the current user; Podman
     rootless needs subordinate UID/GID ranges.

---

## Build errors

### `Required command 'go' not found` or Go version mismatch
- Re-run `./prepare.sh` so `./.local/go` is reinstated on PATH. Alternatively,
  install Go >= 1.22 and set `PATH` accordingly before running
  `scripts/commands/build.sh`.

### `FMIL already present` but build still fails
- Corrupted FMIL installation. Run `scripts/commands/clean.sh` to remove
  `.local`, then rerun `./prepare.sh` and `scripts/commands/build.sh`. Or pass
  `--fmil-home /path/to/fmil` to reuse a known-good install.

### `docker` / `podman` not found
- Install one container runtime (Podman preferred). On Debian/Ubuntu,
  `prepare.sh` can install Podman automatically via apt; confirm it’s on PATH
  before rerunning `scripts/commands/build.sh`.
- If you are only connecting to existing Playground workflows, use
  `./run_playground.sh`; that path does not need Podman or Docker.

### `No running container runtime found` or `podman info` fails on macOS
- The Publish to Playground and Local Dev paths need Podman or Docker already
  running. The Playground Dashboard path does not. The Podman Desktop app can
  be open while its engine is still stopped. For Podman:
  ```bash
  ./prepare.sh
  ```
- `podman info` must work from the same shell as `./run_publish.sh` or
  `./run_local_dev.sh`. If
  `podman machine start` claims success but `podman info` still reports
  connection refused, the Podman VM/socket is stale; restart or recreate the
  Podman machine before rerunning the repo scripts.

### `Release file ... is not valid yet` during the image build
- The Podman VM clock on macOS has drifted behind (it can lag days after the
  laptop sleeps), so apt rejects Debian's package lists.
  `./run_local_demo.sh` corrects this itself; for other builds compare
  `podman machine ssh date -u` with `date -u`, then set the VM clock and
  rebuild:
  ```bash
  podman machine ssh "sudo date -u -s @$(date +%s)"
  ```
- The same drift shifts the `started_at` stamps of local runs.

### `COPY scripts/certs/` fails or `SSL certificate problem` during the image build
- Build with `scripts/commands/build.sh`, not a plain `podman build`. The
  script stages company CA certificates under `scripts/certs/` (exported from
  the host when the network intercepts TLS) before building; a plain build in
  a network that intercepts TLS fails on the Go, pip or FMIL downloads.

### Podman pull fails with `x509: certificate signed by unknown authority`
- The TLS error happens inside the Podman VM before bootstrap containers start.
  Put your corporate CA `.crt`/`.pem` files under `scripts/certs/`, then run:
  ```bash
  scripts/install_podman_ca.sh --cert-dir scripts/certs
  podman pull docker.io/library/python:3.11-slim
  ```
- If your certs are in another directory, pass it explicitly:
  ```bash
  scripts/install_podman_ca.sh --cert-dir /path/to/certs
  ```

---

## Local Dev or local Argo submission problems

### `Current kubectl context is ...`
- The local command path only targets the local Minikube flow. Start Minikube
  if needed and run `kubectl config use-context minikube`.

### `Workflow file not found`
- Run `./run_local_dev.sh` from the repo root and pass relative paths (e.g.,
  `workflows/tests/python_chain.yaml`).

### `argo submit` fails because CRDs are missing
- Re-run `./run_local_dev.sh`; it installs Argo via
  `scripts/ensure_argo_workflows.sh` before submitting. Alternatively, run that
  script manually to reinstall the controller.

### PVC-related errors
- Delete the PVC and rebuild it with `./run_local_dev.sh`:
  ```bash
  kubectl delete pvc cads-data-pvc -n argo
  ./run_local_dev.sh workflows/tests/python_chain.yaml
  ```
  Adjust storage class/size inside `scripts/generate_manifests.sh` if your cluster
  requires custom settings.

---

## Playground or hosted Argo problems

### `Unable to resolve an Argo token`
- Pass `--kubeconfig /path/to/kubeconfig` or export `ARGO_TOKEN`.
- The browser UI expects `Bearer <token>`, but the CLI scripts normalize that
  prefix automatically if you reused the same value in `ARGO_TOKEN`.

### Remote submission fails before the workflow starts
- Re-run `./run_playground.sh --kubeconfig ...` to validate Argo access.
- If publishing, confirm the image tag in `config/playground.env` is the same
  tag you built and published with `./run_publish.sh`.

### GHCR push fails with `403 Forbidden`
- Run `./run_publish.sh` again to refresh the local container-runtime login for
  `ghcr.io`.
- If using environment tokens, set both `GHCR_USERNAME` and `GHCR_TOKEN`; the
  token must have package write access for the target image owner.

---

## Database, Plots and assessment runs

### Run assessment says "Up to date" or a run shows "no new data" (`skipped`)
- A workflow with `data_window` assesses only data that arrived after its
  previous succeeded run. With nothing new (less than one 15-minute
  communication step for the Cheylas workflows) the runner finishes with
  `_run.status: skipped`, exit code 0, and writes nothing.
- Load more data: `./run_local_demo.sh --next` ingests the next
  `upcoming/batch-<date>/`. If it says no upcoming batch is newer, generate
  more days with `python3 scripts/dev/generate_synthetic_measurements.py --days N`.
- If `data/storhy/cheylas/measurements/upcoming/` is missing, recreate the
  seven demo days with
  `python3 scripts/dev/generate_synthetic_measurements.py --days 8`.
- The Alqueva workflow has no `data_window`; it reruns its fixed window and
  never reports "Up to date".

### `carry_over[...] missing variable ... of previous run ...`
- The previous run of that workflow does not have the output that
  `carry_over` reads, typically after editing a workflow's `outputs` or the
  model. Add the variable to the step's `outputs`, then reset the assessment
  chain with `./run_local_demo.sh --reset` so the next run starts from
  the literal `start_values` again. The runner fails on purpose instead of
  silently resetting accumulated damage.

### `have no sample exactly at the window start`
- Consecutive `data_window` assessments join on a shared sample: every channel
  needs a sample exactly at the window's `from` (the previous run's `to`).
  Batches whose timestamps are off the 300 s grid, or channels with gaps at
  that instant, break this. Ingest batches on the common grid (the generator's
  are), or reset the chain with `./run_local_demo.sh --reset`.

### Local runs fail to start or cannot reach the database
- On macOS the demo FMUs are Linux builds: start the service with
  `--local-runner-image IMAGE` and make sure the image exists locally
  (`podman images`) and matches the checkout's FMUs and runner.
- Inside the container `127.0.0.1` is the container itself. The service
  rewrites a loopback host to `host.containers.internal` (podman) or
  `host.docker.internal` (docker) by default; on a non-default network pass
  `--local-db-url` (or `CADS_LOCAL_DB_URL`) with a host name the container
  can reach.
- Docker on Linux does not define `host.docker.internal`, and `run_db.sh`
  publishes the database on `127.0.0.1` only, so the runner container cannot
  reach it. Use Podman, or on Linux run the runner binary instead of an image
  (next point).
- Without the image, `--execution local` needs `bin/cads-workflow-runner`
  built against FMIL on the host (`scripts/commands/build.sh`); the service
  refuses to start with `--execution local needs --local-runner-image or a
  runner binary` otherwise. Use `--execution argo` instead.

### `run_db.sh` cannot start the database: port 5432 already in use
- Another PostgreSQL runs on the default port. Pick another one and use the
  URL the script prints:
  ```bash
  CADS_DB_PORT=55432 ./run_local_demo.sh
  ```
  Pass the same `CADS_DB_PORT` to later `./run_local_demo.sh --next` calls.

### Plots show a note instead of history
- "No CADS database configured" means the service has no `CADS_DB_URL`
  (or `--db-url`). The Plots view then shows only the latest finished run of
  each workflow. Start the demo with `./run_local_demo.sh`, which sets it;
  when starting the service by hand, export the URL printed by
  `scripts/commands/run_db.sh` and restart the service.

---

## Cleanup observations

- The script removes `.local`, cached artifacts, container images, and deletes
  the Minikube profile. Run it when you need to test the full flow from scratch.
- If it logs `minikube command not found`, rerun `./prepare.sh` to reinstall the
  CLI before running `scripts/commands/clean.sh` again (or ignore if Minikube
  is already absent).

---

## `kubectl` TLS errors outside Minikube

### `tls: failed to verify certificate: x509: certificate signed by unknown authority`
- Corporate TLS proxies re-sign outbound traffic, so external clusters (e.g., the Kaizen playground) require your corporate CA when running `kubectl` directly from the host.
- Run `source scripts/host_ca_env.sh` once per shell. The script targets `scripts/certs/` and `.local/` under the repo by default, but you can point it at another folder with `source scripts/host_ca_env.sh "$PWD"` or `CADS_HOST_CA_ROOT=/path/to/dir source scripts/host_ca_env.sh`. If the chosen cert directory already contains `.crt/.pem` files, the script concatenates them and exports `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, and `GIT_SSL_CAINFO`. When the folder is empty, it attempts to run `scripts/export_company_certs.py` automatically to harvest the certs from your OS trust store before exporting the bundle.
- Run your command normally afterwards, e.g.:
  ```bash
  KUBECONFIG=.local/kaizen/kubeconfig kubectl get pods
  ```
  Any `kubectl`, `helm`, or `curl` invocation in that shell will reuse the injected CA bundle, eliminating the x509 error.

---

## General tips

- Always run scripts from the repo root; many paths are relative.
- Keep `./scripts/certs/` populated with your corporate CA certs when working
  behind TLS-inspecting proxies. `scripts/commands/run_local.sh` syncs them
  into Minikube, and the Playground commands source the host CA helper before
  remote Argo calls.
- When debugging deeper issues, check the log files under `minikube logs` and
  `kubectl get events -n argo`.
