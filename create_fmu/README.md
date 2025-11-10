# Building local FMUs

Use this directory for everything related to producing FMUs from the Python
models that live under `fmu/models/`. The workflow runner never calls
`pythonfmu` itself; instead, it consumes whatever `.fmu` files you place in
`fmu/models/`.

## Prerequisites

Either run the helper script:

```bash
./build_python_fmus.sh
```

or perform the steps manually:

```bash
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
python -m pythonfmu build -f ../fmu/models/producer_fmu.py -d ../fmu/models
python -m pythonfmu build -f ../fmu/models/consumer_fmu.py -d ../fmu/models
```

The commands drop `Producer.fmu` and `Consumer.fmu` directly into
`fmu/models/`, alongside their Python sources, so the workflow YAMLs can locate
them without any extra configuration.

Cached exporter binaries from `scripts/install_platform_resources.py` now live in
`create_fmu/artifacts/`, keeping build-only state separate from the runtime
orchestrator.
