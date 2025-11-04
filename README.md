# CADS FMI Co‑Sim Demo

Two simple Python models packaged as **FMUs (FMI 2.0 Co‑Simulation)** and orchestrated in sequence:

- **Producer.fmu** reads/creates a CSV time‑series, computes features (mean/std/min/max/rolling mean) and
  intentionally runs ~30s wall‑clock for demo visibility.
- **Consumer.fmu** starts automatically after Producer finishes; it receives Producer's outputs as start values,
  computes a simple health score and an anomaly flag.

Artifacts written to `data/producer_result.json` and `data/consumer_result.json`.

## Quick Start (Docker)

```bash
docker build -t cads-fmi-demo .
docker run --rm -it -v "$PWD/data:/app/data" cads-fmi-demo
```

or with Compose:

```bash
docker compose up --build
```

## Testing with Docker on macOS (CLI only)

1. Install the command-line Docker stack and a lightweight VM runtime using MacPorts, plus the socket adapter that Compose needs:
   ```bash
   sudo port install docker docker-compose colima py311-requests-unixsocket
   colima start
   docker context use colima
   ```
   `colima start` launches the Docker daemon inside a small VM and exposes the Docker socket for the CLI tools. Switching to the `colima` Docker context points the CLI at that socket (rerun `docker context use colima` in new shells).
   ```bash
   docker context show    # should print "colima"
   docker ps              # sanity-check that the CLI can talk to Colima
   ```
2. Back in this repository, build the image and run the orchestrator end to end:
   ```bash
   docker compose up --build orchestrator
   ```
   The `data/` directory is bind-mounted automatically, so results land on the host.
   ```bash
   docker-compose up --build orchestrator    # legacy Compose v1; install requests-unixsocket if you prefer this CLI
   ```
   If `docker-compose` reports `Not supported URL scheme http+docker`, install the adapter into the same Python runtime:
   ```bash
   python3.11 -m pip install --user requests-unixsocket
   ```
   Older Compose releases do not understand Docker contexts. Export the Colima socket path so `docker-compose` can talk to the VM:
   ```bash
   export DOCKER_HOST=$(docker context inspect colima --format '{{ (index .Endpoints "docker").Host }}')
   docker-compose up --build orchestrator
   ```
   Run the `export` again in new shells or add it to your session script.
3. Inspect the generated artifacts to confirm the run completed:
   ```bash
   cat data/producer_result.json
   cat data/consumer_result.json
   ```
   Each file contains the computed metrics and health score from the FMUs.
4. For repeat runs, use `docker-compose run --rm orchestrator` (or `docker compose run --rm orchestrator`) to trigger the pipeline without forcing a rebuild. Add `--build` whenever you change dependencies or FMU source code.
5. When you are finished, stop the Colima VM so it releases resources:
   ```bash
   colima stop
   ```
   If you switched Docker contexts earlier, return to the default with `docker context use default`. Unset any session variables you exported (`unset DOCKER_HOST`) if you plan to talk to a different Docker daemon.
   Check `colima status` if you need to confirm the VM state.

## Local Dev (no Docker)

```bash
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt

# Build FMUs
python -m pythonfmu export fmusrc/producer_fmu.py Producer dist/Producer.fmu
python -m pythonfmu export fmusrc/consumer_fmu.py Consumer dist/Consumer.fmu

# Run orchestrator
python orchestrator/run.py
```

## OVH Ubuntu 22.04 quickstart

```bash
sudo apt-get update
sudo apt-get install -y docker.io docker-compose-plugin
sudo usermod -aG docker $USER  # re-login afterwards

git clone https://github.com/janlv/cads-fmi-demo
cd cads-fmi-demo
docker build -t cads-fmi-demo .
docker run --rm -it -v "$PWD/data:/app/data" cads-fmi-demo
```

## Repo layout

```
cads-fmi-demo/
├── Dockerfile
├── docker-compose.yml
├── requirements.txt
├── .gitignore
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
