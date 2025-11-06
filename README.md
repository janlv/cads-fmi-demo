# CADS FMI Co‑Sim Demo

Two simple Python models packaged as **FMUs (FMI 2.0 Co‑Simulation)** and orchestrated in sequence:

- **Producer.fmu** reads/creates a CSV time‑series, computes features (mean/std/min/max/rolling mean) and
  intentionally runs ~30s wall‑clock for demo visibility.
- **Consumer.fmu** starts automatically after Producer finishes; it receives Producer's outputs as start values,
  computes a simple health score and an anomaly flag.

Artifacts written to `data/producer_result.json` and `data/consumer_result.json`.

## Quick Start

1. Prepare your host (run once per machine):
   ```bash
   ./prepare.sh              # auto-detects; use --platform linux|mac to override
   ```
   See the platform preparation guides below for background and manual steps.
2. Build the FMUs and container image:
   ```bash
   ./build.sh
   ```
3. Run the orchestrator (reuses the build unless model code changed):
   ```bash
   docker compose up orchestrator   # or: podman compose up orchestrator
   ```
4. Review the results:
   ```bash
   cat data/producer_result.json
   cat data/consumer_result.json
   ```
5. For repeat simulations, just rerun `docker compose up orchestrator`.

The helper script stages the pythonfmu cache automatically and retries with exported corporate TLS certificates if pip hits SSL issues.

Notes:
- The Producer will generate `data/measurements.csv` automatically if missing.
- To use your own CSV, drop a file at `data/measurements.csv` with header: `timestamp,value`.
- For a faster demo, reduce `duration_sec` inside `fmu/models/producer_fmu.py`.

## Preparing Linux hosts

Run the helper to install prerequisites automatically (explicit override shown for clarity; the script skips work when everything is already in place):

```bash
./prepare.sh --platform linux
```

The script installs the packages listed in `scripts/package-lists/linux-apt.txt`, runs `podman system migrate`, attempts to start the rootless Podman socket, and flags missing subordinate ID ranges. Use the manual checklist below if you prefer to run the commands yourself or need finer control.

### Package prerequisites

```bash
sudo apt-get update
xargs -a scripts/package-lists/linux-apt.txt sudo apt-get install -y
```

`podman-docker` exposes a Docker-compatible CLI shim so the repo’s scripts keep working unchanged. If the package is unavailable, add `alias docker=podman` and `alias docker-compose='podman compose'` to your shell session before running the commands below.

### Rootless containers

Rootless Podman needs subordinate UID/GID ranges so it can map the container users onto your host account. Add a unique range (coordinate with IT if centrally managed):

```bash
sudo usermod --add-subuids 10000000-10098999 "$USER"
sudo usermod --add-subgids 10000000-10098999 "$USER"
```

Verify there is no overlap by sorting `/etc/subuid` and `/etc/subgid`:

```bash
sudo sort -t: -k2n /etc/subuid
sudo sort -t: -k2n /etc/subgid
```

Finish the rootless setup:

```bash
podman system migrate
systemctl --user enable --now podman.socket   # compose talks to this socket
```

If you have access to the Docker daemon instead of Podman, install `docker.io docker-compose-plugin` and skip the rootless mapping steps; the rest of the workflow is identical. If you plan to run the FMUs directly on the host (see below), also install `python3-venv`.

The Quick Start commands above handle building and running the demo once the environment is prepared.

## Preparing macOS (Apple Silicon) hosts

Run the helper to install prerequisites automatically (explicit override shown for clarity; the script skips work when everything is already in place):

```bash
./prepare.sh --platform mac
```

The script installs the packages listed in `scripts/package-lists/macports.txt` and reminds you to start Colima and switch the Docker context. Use the manual checklist below if you prefer to run the commands yourself.

### Package prerequisites

```bash
sudo port install $(< scripts/package-lists/macports.txt)
colima start
docker context use colima
```

Optional sanity checks:

```bash
docker context show    # expect "colima"
docker ps              # connectivity check, expect header row output
```

The Quick Start commands above handle building and running the demo once Colima and the Docker CLI are configured.

Notes:
- The Docker image rebuilds `libpythonfmu-export.so` during `docker compose build`, so the FMUs generated inside the container are native `arm64` binaries.

## Optional: Run FMUs directly on the host

This path is useful when you need to debug or profile the FMU Python code without rebuilding containers. After staging platform resources you can build and execute the FMUs directly on the host interpreter:

```bash
scripts/install_platform_resources.py  # auto-detect profile
python3 -m venv .venv && source .venv/bin/activate  # requires python3-venv on Linux
pip install -r requirements.txt
python -m pythonfmu build -f fmu/models/producer_fmu.py -d fmu/artifacts/build
python -m pythonfmu build -f fmu/models/consumer_fmu.py -d fmu/artifacts/build
python orchestrator/run.py
```

## Implementation details

### Repo layout

```
cads-fmi-demo/
├── build.sh          # One-stop helper to refresh platform cache and rebuild Docker image/FMUs
├── Dockerfile         # Container image build for the co-simulation demo
├── docker-compose.yml # Compose stack to orchestrate Producer/Consumer runs
├── requirements.txt   # Python dependencies used for local builds/tests
├── scripts/           # Helper utilities for cache bootstrap, cert export, etc.
├── fmu/
│   ├── models/        # Python sources for each FMU (producer_fmu.py, consumer_fmu.py)
│   └── artifacts/     # Generated outputs
│       ├── cache/     # pythonfmu toolchain cached by scripts/
│       └── build/     # Built FMUs (Producer.fmu, Consumer.fmu) for current architecture
├── orchestrator/
│   └── run.py         # Coordinates FMU execution and writes summary JSON
└── data/              # Runtime data/setpoints/results; created on demand
    └── (created at runtime)
```

### build.sh in detail

- **What it does:**  
  1. calls `scripts/install_platform_resources.py` to bootstrap the pythonfmu toolchain/cache for your host;  
  2. runs `docker compose build` (default target: `orchestrator`), which copies the repo into the image and rebuilds the FMUs with architecture-specific binaries.
- **Args before `--docker`:** forwarded to the install script. Example: `./build.sh --profile apple`.
- **Args after `--docker`:** passed verbatim to `docker compose build`. Example: `./build.sh --docker --no-cache orchestrator`.

You can replicate the same steps manually:

```bash
scripts/install_platform_resources.py  # bootstrap host-side cache
docker compose build orchestrator       # copy sources, rebuild FMUs in-container
docker compose up orchestrator          # run the simulation (podman compose also works)
```

During the Docker build, the same bootstrap sequence runs inside the image after requirements are installed; if the cache is present it is copied in first, otherwise pythonfmu is rebuilt from source so the resulting FMUs match the container’s architecture. The cert-export retry ensures pip can reach its indexes even behind corporate TLS proxies.

### Platform resources

Platform-specific pythonfmu binaries are cached under `fmu/artifacts/cache/<profile>/` (ignored by git). Run the helper script before local FMU builds or `docker build`; it auto-detects your architecture (override with `--profile`) and bootstraps the cache via a minimal Docker image when needed. If pip hits TLS errors during bootstrap, the script automatically runs `scripts/export_company_certs.py` in the background to capture your trusted chain and retries. The `fmu/` directory is generated on demand; cloning the repo starts without the `artifacts/` subtree.

```bash
# Populate/refresh the cache (auto-detect profile; override with --profile linux|apple)
scripts/install_platform_resources.py [--profile linux|apple]
```

The Docker image runs the equivalent logic automatically based on the target architecture, so you only need this when developing locally or rebuilding FMUs on the host. For more visibility during bootstrapping, pass `--verbose` to stream the underlying `apt-get`, `pip`, and build output.
