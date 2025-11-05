# CADS FMI Co‑Sim Demo

Two simple Python models packaged as **FMUs (FMI 2.0 Co‑Simulation)** and orchestrated in sequence:

- **Producer.fmu** reads/creates a CSV time‑series, computes features (mean/std/min/max/rolling mean) and
  intentionally runs ~30s wall‑clock for demo visibility.
- **Consumer.fmu** starts automatically after Producer finishes; it receives Producer's outputs as start values,
  computes a simple health score and an anomaly flag.

Artifacts written to `data/producer_result.json` and `data/consumer_result.json`.

## Repo layout

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

## Platform resources

Platform-specific pythonfmu binaries are cached under `fmu/artifacts/cache/<profile>/` (ignored by git). Run the helper script before local FMU builds or `docker build`; it auto-detects your architecture (override with `--profile`) and bootstraps the cache via a minimal Docker image when needed. If pip hits TLS errors during bootstrap, the script automatically runs `scripts/export_company_certs.py` in the background to capture your trusted chain and retries. The `fmu/` directory is generated on demand; cloning the repo starts without the `artifacts/` subtree.

```bash
# Populate/refresh the cache (auto-detect profile; override with --profile linux|apple)
scripts/install_platform_resources.py [--profile linux|apple]
```

The Docker image runs the equivalent logic automatically based on the target architecture, so you only need this when developing locally or rebuilding FMUs on the host.

For more visibility during bootstrapping, pass `--verbose` to stream the underlying `apt-get`, `pip`, and build output:

```bash
scripts/install_platform_resources.py --verbose
```

## Quick Start (Docker)

1. Build the FMUs and image:
   ```bash
   ./build.sh
   ```
2. Run the orchestrator (no rebuild unless models changed):
   ```bash
   docker compose up orchestrator
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
docker compose up orchestrator          # run the simulation
```

During the Docker build, the same bootstrap sequence runs inside the image after requirements are installed; if the cache is present it is copied in first, otherwise pythonfmu is rebuilt from source so the resulting FMUs match the container’s architecture. The cert-export retry ensures pip can reach its indexes even behind corporate TLS proxies.

## Build & test on macOS (Apple Silicon)

The Docker image rebuilds `libpythonfmu-export.so` during `docker compose build`, so the FMUs generated inside the container are native `arm64` binaries. On Apple Silicon the recommended workflow is Docker CLI + [Colima](https://github.com/abiosoft/colima):

1. Install the CLI stack (Homebrew or MacPorts—sample commands below use MacPorts):
   ```bash
   sudo port install docker docker-compose colima py311-requests-unixsocket
   colima start
   docker context use colima
   ```
   Optional sanity checks:
   ```bash
   docker context show    # expect "colima"
   docker ps              # connectivity check, expect header row output
   ```
2. Build the FMUs and container image (rebuilds the exporter for arm64 automatically):
   ```bash
   ./build.sh
   ```
3. Run the orchestrator:
   ```bash
   docker compose up orchestrator
   ```
   The run finishes when you see the Consumer summary and the container exits with code `0`.
4. Verify outputs:
   ```bash
   cat data/producer_result.json
   cat data/consumer_result.json
   ```
5. Repeat runs without rebuilding unless model code changed:
   ```bash
   docker compose up orchestrator
   ```
5. When finished, reclaim resources:
   ```bash
   colima stop
   docker context use default
   ```

### Local rebuilds outside Docker (optional)

If you want to rebuild or simulate FMUs on the host Python interpreter, stage the platform resources first (populates `fmu/artifacts/cache/<profile>/...`):

```bash
scripts/install_platform_resources.py  # auto-detect profile
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
python -m pythonfmu build -f fmu/models/producer_fmu.py -d fmu/artifacts/build
python -m pythonfmu build -f fmu/models/consumer_fmu.py -d fmu/artifacts/build
python orchestrator/run.py
```

## Build & test on Linux

The container build also works on x86_64 Linux, including the OVH images described below.

1. Install Docker & Compose plugin (Debian/Ubuntu example):
   ```bash
   sudo apt-get update
   sudo apt-get install -y docker.io docker-compose-plugin
   sudo usermod -aG docker "$USER"  # re-login to pick up group
   ```
2. Clone & build:
   ```bash
   git clone https://github.com/janlv/cads-fmi-demo
   cd cads-fmi-demo
   docker compose up --build orchestrator
   ```
3. Inspect results:
   ```bash
   cat data/producer_result.json
   cat data/consumer_result.json
   ```
4. Subsequent test runs (no rebuild):
   ```bash
   docker compose up orchestrator
   ```

## OVH Ubuntu 22.04 quickstart

```bash
sudo apt-get update
sudo apt-get install -y docker.io docker-compose-plugin
sudo usermod -aG docker $USER  # re-login afterwards

git clone https://github.com/janlv/cads-fmi-demo
cd cads-fmi-demo
docker compose up --build orchestrator
```

## Notes

- The Producer will generate `data/measurements.csv` automatically if missing.
- To use your own CSV, drop a file at `data/measurements.csv` with header: `timestamp,value`.
- For a faster demo, reduce `duration_sec` inside `fmu/models/producer_fmu.py`.
