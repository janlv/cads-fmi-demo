# Building local FMUs

Use this directory for everything related to producing FMUs from the Python
models that live under `create_fmu/`. The workflow runner never calls
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
python -m pythonfmu build -f ./producer_fmu.py -d ../fmu/models
python -m pythonfmu build -f ./consumer_fmu.py -d ../fmu/models
```

The commands drop `Producer.fmu` and `Consumer.fmu` directly into
`fmu/models/`, so the workflow YAMLs can locate them without any extra
configuration. The Python source now lives solely in this directory,
keeping `fmu/models/` reserved for FMU artifacts.

Cached exporter binaries from `scripts/install_platform_resources.py` now live in
`create_fmu/artifacts/`, keeping build-only state separate from the runtime
orchestrator.

## FMI 3.0 co-simulation models (`storhy_fmi3/`)

The STOR-HY co-simulation demo FMUs are exported as **FMI 3.0** with
[`pythonfmu3`](https://pypi.org/project/pythonfmu3/) (pinned in
`requirements.txt` next to `pythonfmu`, which still builds the FMI 2.0
replicas). They are **deterministic placeholders, not validated physics**:
the equations have a plausible shape so coupling is visible in the dashboard,
but parameters are illustrative and nothing here is calibrated against plant
data.

| FMU (`fmu/models/<Class>.fmu`) | stepSize [s] | coupled inputs | outputs |
|---|---|---|---|
| `EmsDispatchFmi3` | 900 | `soc_percent` | `power_setpoint_mw`, `price_eur_mwh`, `mode`, `revenue_eur` |
| `BatteryDegradationFmi3` | 300 | `power_setpoint_mw`, `ambient_temp_c` | `soc_percent`, `soh_percent`, `rul_days`, `cycle_count`, `power_actual_mw`, `cell_temp_c` |
| `RunnerStressFmi3` | 900 | `flow_demand_pu` | `load_pu`, `operating_mode`, `stress_amplitude_mpa`, `start_stop_count` |
| `IoTIndicatorFmi3` | 300 | `load_pu` | `vibration_rms_mm_s`, `condition_indicator` |
| `RulFmi3` | 900 | `stress_amplitude_mpa`, `condition_indicator` | `damage_index`, `rul_days`, `damage_rate_per_day`, `status_code` |
| `SedimentExposureFmi3` | 300 | `cleanings_done` | `tidal_head_m`, `sediment_concentration_g_l`, `sediment_exposure` |
| `CleaningDecisionFmi3` | 900 | `sediment_exposure`, `trigger` | `cleaning_count`, `cumulative_cost_eur`, `downtime_h`, `hours_since_cleaning`, `cleaning_active` |

Every FMU declares a `DefaultExperiment` (0 to 86400 s, the stepSize above),
one `independent` variable `time`, `causality="input"` variables with explicit
`start` values, and only `Float64`/`Int32`/`Boolean` scalars. Internal state
is kept between steps and outputs are evaluated at `t + dt` at the end of
`do_step`. The different step sizes are deliberate: the CADS master sub-steps
each FMU with its own declared step inside the communication step
(ARCH-COMP-002).

Acceleration and simplifications worth knowing before quoting numbers:

- `BatteryDegradationFmi3.aging_acceleration` (default 100) multiplies cycle
  and calendar fade so one simulated day shows visible state-of-health loss;
  `rul_days` divides it back out and reports the real-rate estimate.
- `RulFmi3` uses a Basquin S-N curve with Miner's rule and a conservative
  planning rate (never below the design rate or the worst smoothed rate seen),
  so its RUL estimate only falls.
- Prices, tides, operating schedules and vibration "noise" are synthetic
  deterministic functions of time, so every run is reproducible.

Layout:

- `storhy_fmi3_models.py`: stdlib-only model specs and state-update functions
  (importable and unit tested without pythonfmu3).
- `storhy_fmi3_common.py`: pythonfmu3 glue (variable registration,
  `DefaultExperiment`). pythonfmu3 finds the FMU class with a regex, so every
  model file keeps the literal line `class <Name>(Fmi3Slave):` and delegates
  to these functions instead of sharing a base class.
- `*_fmi3.py`: one thin FMU class per model.

Build (done by `build_python_fmus.sh` and the Dockerfile):

```bash
python patch_pythonfmu_export.py --package all      # link both exporters against libpython
(cd "$(python -c 'import pathlib, pythonfmu3; print(pathlib.Path(pythonfmu3.__file__).parent)')/pythonfmu-export" && sh build_unix.sh)
python -m pythonfmu3 build -f storhy_fmi3/battery_degradation_fmi3.py -d ../fmu/models \
    storhy_fmi3/storhy_fmi3_common.py storhy_fmi3/storhy_fmi3_models.py
```

The exporter rebuild matters: the pythonfmu3 wheel only ships an
`x86_64-linux` library that is not linked against libpython, and the
patched CMake names the output folder after the real CPU
(`aarch64-linux` on arm64). Tests: `python3 -m unittest discover -s create_fmu -p 'test_*.py'`
(the modelDescription check runs only when pythonfmu3 is installed).
