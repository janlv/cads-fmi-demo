# CADS FMI Co‑Sim Demo

Two simple Python models packaged as **FMUs (FMI 2.0 Co‑Simulation)** and orchestrated in sequence:

- **Producer.fmu** reads/creates a CSV time‑series, computes features (mean/std/min/max/rolling mean) and
  intentionally runs ~30s wall‑clock for demo visibility.
- **Consumer.fmu** starts automatically after Producer finishes; it receives Producer's outputs as start values,
  computes a simple health score and an anomaly flag.

Artifacts written to `data/producer_result.json` and `data/consumer_result.json`.

## TL;DR (Docker)

```bash
docker build -t cads-fmi-demo .
docker run --rm -it -v "$PWD/data:/app/data" cads-fmi-demo
```

or with Compose:

```bash
docker compose up --build
```

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
