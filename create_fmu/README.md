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

One FMU per model family of the Task 3.3 matrix:

| Family | FMU (`fmu/models/<Class>.fmu`) | stepSize [s] | inputs | outputs |
|---|---|---|---|---|
| M10 | `EmsDispatchFmi3` | 900 | `soc_percent` | `power_setpoint_mw`, `price_eur_mwh`, `mode`, `revenue_eur` |
| M9 | `BatteryDegradationFmi3` | 300 | `power_setpoint_mw`, `ambient_temp_c` | `soc_percent`, `soh_percent`, `rul_days`, `cycle_count`, `power_actual_mw`, `cell_temp_c` |
| M1 | `RunnerStressFmi3` | 900 | `flow_demand_pu` | `load_pu`, `operating_mode`, `stress_amplitude_mpa`, `start_stop_count`, `energy_mwh` |
| M7 | `IoTIndicatorFmi3` | 300 | `load_pu` | `vibration_rms_mm_s`, `condition_indicator` |
| M8 | `RulFmi3` | 900 | `stress_amplitude_mpa`, `condition_indicator` | `damage_index`, `rul_days`, `damage_rate_per_day`, `status_code` |
| M5 | `SedimentExposureFmi3` | 300 | `cleaning_trigger` (Boolean) | `head_m`, `sediment_concentration_g_l`, `sediment_exposure`, `cleaning_count`, `cumulative_cleaning_cost_eur`, `downtime_h`, `hours_since_cleaning` |
| M14 | `DegradationCostFmi3` | 900 | `rul_days`, `damage_rate_per_day`, `soh_percent`, `revenue_eur`, `energy_mwh` | `degradation_cost_eur`, `degradation_cost_rate_eur_per_day`, `gross_revenue_eur`, `net_benefit_eur`, `benefit_cost_ratio`, `status_code`, `recommendation_code` |

`CleaningDecisionFmi3` was retired; its cleaning accounting (minimum interval,
cost, downtime, count) now lives in `SedimentExposureFmi3`.

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
  so its RUL estimate only falls. `demo_acceleration` (default 1) multiplies
  the accumulated damage only; `damage_rate_per_day` and `rul_days` stay at
  real rates.
- `RunnerStressFmi3.energy_mwh` integrates `load_pu * rated_power_mw`
  (default 240 MW) over turbine-mode intervals only.
- `SedimentExposureFmi3.profile_mode` selects the La Rance tidal profile (0)
  or the Cheylas pump/turbine cycling and dewatering profile (1, head around
  260 m, `cycles_per_day` sediment bursts). A true `cleaning_trigger` resets
  exposure to `post_cleaning_exposure` at most once per
  `min_cleaning_interval_h` and adds `cleaning_cost_eur` and
  `downtime_per_cleaning_h`.
- `DegradationCostFmi3` charges `asset_value_eur` times the consumed life:
  `damage_rate_per_day * dt / 86400` plus any capacity fade
  `(100 - soh_percent) / 100` not yet charged (relative to a new asset). Gross
  revenue is `revenue_eur + energy_mwh * price_eur_mwh`. `status_code` flags
  `rul_days` below `rul_warning_days` (1) or `rul_alarm_days` (2);
  `recommendation_code` is 2 (schedule maintenance) on alarm, 1 (reduce
  cycling) when `benefit_cost_ratio < 1`, else 0. `planning_horizon_days` is
  informative only. Because the battery fade is time-compressed, the Alqueva
  cost is deliberately exaggerated.
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
