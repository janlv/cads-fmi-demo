# CADS FMI Co‑Sim Demo

Two simple Python models packaged as **FMUs (FMI 2.0 Co‑Simulation)** and orchestrated in sequence:

- **Producer.fmu** reads/creates a CSV time‑series, computes features (mean/std/min/max/rolling mean) and
  intentionally runs ~30s wall‑clock for demo visibility.
- **Consumer.fmu** starts automatically after Producer finishes; it receives Producer's outputs as start values,
  computes a simple health score and an anomaly flag.

Artifacts written to `data/producer_result.json` and `data/consumer_result.json`.

## Platform resources

Platform-specific pythonfmu binaries are cached under `fmu_artifacts/cache/<profile>/` (ignored by git). Run the helper script before local FMU builds or `docker build`; it auto-detects your architecture (override with `--profile`) and bootstraps the cache via a minimal Docker image when needed. If pip hits TLS errors during bootstrap, the script automatically runs `scripts/export_company_certs.py` in the background to capture your trusted chain and retries. The `fmu_artifacts/` directory itself is generated on demand; cloning the repo starts without it.

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

```bash
# Populate fmu_artifacts/cache/<profile>/... for the host architecture (once per machine)
scripts/install_platform_resources.py

# Build container using the cached resources
docker build -t cads-fmi-demo .
docker run --rm -it -v "$PWD/data:/app/data" cads-fmi-demo
```

During the Docker build, the same bootstrap sequence runs inside the image after requirements are installed; if the cache is present it is copied in first, otherwise pythonfmu is rebuilt from source so the resulting FMUs match the container’s architecture. The cert-export retry ensures pip can reach its indexes even behind corporate TLS proxies.

or with Compose:

```bash
docker compose up --build
```

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
2. Build & test the orchestrator (rebuilds the FMUs for arm64 automatically):
   ```bash
   docker compose up --build orchestrator
   ```
   The run finishes when you see the Consumer summary and the container exits with code `0`.
3. Verify outputs:
   ```bash
   cat data/producer_result.json
   cat data/consumer_result.json
   ```
4. Repeat runs without rebuilding unless model code changed:
   ```bash
   docker compose up orchestrator
   ```
5. When finished, reclaim resources:
   ```bash
   colima stop
   docker context use default
   ```

### Local rebuilds outside Docker (optional)

If you want to rebuild or simulate FMUs on the host Python interpreter, stage the platform resources first (populates `fmu_artifacts/cache/<profile>/...`):

```bash
scripts/install_platform_resources.py  # auto-detect profile
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
python -m pythonfmu build -f fmusrc/producer_fmu.py -d fmu_artifacts/build
python -m pythonfmu build -f fmusrc/consumer_fmu.py -d fmu_artifacts/build
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

## Repo layout

```
cads-fmi-demo/
├── Dockerfile
├── docker-compose.yml
├── requirements.txt
├── .gitignore
├── fmu_artifacts/  (generated)
│   ├── cache/      # pythonfmu binaries cached by scripts/install_platform_resources.py
│   └── build/      # FMUs built locally or inside Docker (Producer.fmu, Consumer.fmu)
├── orchestrator/
│   └── run.py
├── fmusrc/
│   ├── producer_fmu.py
│   └── consumer_fmu.py
└── data/
    └── (created at runtime)
```

## Notes

- The Producer will generate `data/measurements.csv` automatically if missing.
- To use your own CSV, drop a file at `data/measurements.csv` with header: `timestamp,value`.
- For a faster demo, reduce `duration_sec` inside `fmusrc/producer_fmu.py`.
