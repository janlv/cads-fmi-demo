# STOR-HY Replica Models And Workflows

This catalog documents the demonstration workflow layer implemented in this
repository from the STOR-HY proposal context. The partner models are represented
by simple Python FMU replicas for now; the folder and signal layout is intended
to be stable enough that real partner FMUs can replace the replicas later.

Pozo/Pozu Figaredo has left the consortium and is not represented as a current
demonstrator in these workflows, models, or dashboard mappings.

## Layout

- `create_fmu/storhy_replicas/` contains the Python FMU replica classes and the
  shared model logic in `storhy_replica_common.py`.
- `create_fmu/storhy_models/` contains the FMI 3.0 co-simulation FMUs used by
  the current demonstrator workflows (one FMU per model family).
- `workflows/demonstrators/<site>/<category>/` contains the site-specific
  workflow YAML files. The demo is scoped to two demonstrators, Le Cheylas and
  Alqueva.
- `workflows/archive/` keeps the retired pre-matrix workflows (FMI 2 replica
  chains for all sites and the cross-site templates formerly under
  `workflows/common/`) with their original relative paths, for regression and
  reference. They are hidden from the dashboard catalog.
- Every STOR-HY YAML file has a `metadata` block with `display_name`,
  `site_id`, `category`, `result_family: storhy_mock`, `description`, and
  `tags`. The dashboard uses this metadata to filter workflows by demonstrator.
- Every STOR-HY YAML file also references a `synthetic_case` fixture under
  `data/storhy/synthetic/`. Hosted Argo pods read these files from the container
  image and include the case context in the JSON result payload.

## Replica FMU Models

These FMI 2.0 replicas are used only by the archived workflows under
`workflows/archive/` and by some runtime tests; the current demonstrator
workflows use the FMI 3.0 FMUs listed below. The replicas are deterministic,
low-order approximations. They expose a
common numeric input/output contract so YAML files can route values from one
model step into the next using `start_from`.

| Replica FMU | Role |
| --- | --- |
| `HydroCascadeDispatchReplica.fmu` | Cascade dispatch and reservoir-level operating envelope. |
| `StartSequenceWearReplica.fmu` | Start-stop wear, damage index, and remaining useful life. |
| `HSCFlexibilityReplica.fmu` | Hydraulic short-circuit flexibility and value potential. |
| `ConditionMonitoringReplica.fmu` | Sensor-derived health, risk, and confidence indicators. |
| `RunnerSedimentWearReplica.fmu` | Sediment exposure and runner wear progression. |
| `PredictiveMaintenanceReplica.fmu` | Maintenance prioritisation from risk, damage, and RUL. |
| `CorrosionBiofoulingReplica.fmu` | Saltwater corrosion and biofouling risk indicators. |
| `CleaningIntervalReplica.fmu` | Cleaning/coating interval decision support. |
| `BESSSizingReplica.fmu` | Battery sizing benefit estimate for tidal/hybrid use cases. |
| `HybridEMSReplica.fmu` | Hybrid PSP, PV, and battery energy-management logic. |
| `FastServiceControllerReplica.fmu` | Fast ancillary-service dispatch controller. |
| `MIVRegulationReplica.fmu` | Main inlet valve regulation envelope. |
| `MIVFatigueReplica.fmu` | Main inlet valve fatigue and availability impact. |
| `KPIAssessmentReplica.fmu` | Common KPI scoring and status classification. |
| `SustainabilityCBAReplica.fmu` | CO2, OPEX, and value-delta cost-benefit assessment. |

Common outputs include `score`, `confidence`, `risk_index`, `status_code`,
`recommendation_code`, `kpi_score`, `value_delta_eur`, `opex_delta_eur`,
`co2_delta_tonnes`, `rul_days`, `availability_delta_percent`,
`flexibility_delta_percent`, and selected physical indicators such as
`power_mw`, `reservoir_level_m`, `soc_percent`, `damage_index`,
`sediment_exposure`, `corrosion_index`, `biofouling_index`, and
`valve_opening_percent`.

## FMI 3.0 Model Families

One FMI 3.0 FMU stands in for each model family of the Task 3.3 matrix. All are
deterministic placeholders, not validated engineering models; see
`create_fmu/README.md` for variables and step sizes.

| Family | FMU (`fmu/models/<Class>.fmu`) | Used in |
| --- | --- | --- |
| M1 runner stress | `RunnerStress` | Cheylas Runner RUL Lock-step |
| M5 sediment | `SedimentExposure` (tidal or cycling profile, cleaning accounting) | Cheylas Sediment Erosion Events |
| M7 IoT indicators | `IoTIndicator` | Cheylas Runner RUL Lock-step |
| M8 degradation/health/RUL | `Rul` | Cheylas Runner RUL Lock-step, Cheylas Sediment Erosion Events |
| M9 battery degradation | `BatteryDegradation` | Alqueva Battery-EMS Co-simulation |
| M10 EMS | `EmsDispatch` | Alqueva Battery-EMS Co-simulation |
| M14 degradation cost vs market revenue | `DegradationCost` | Cheylas Runner RUL Lock-step, Alqueva Battery-EMS Co-simulation |

## Demonstrator Workflows

### Le Cheylas Power Station

| Workflow | YAML | Model chain |
| --- | --- | --- |
| Cheylas Runner RUL Lock-step (W1) | `workflows/demonstrators/cheylas/maintenance/runner_rul_lockstep.yaml` | cosim jacobi [`RunnerStress`, `IoTIndicator` -> `Rul`] -> `DegradationCost`; `iot.vibration_rms_measured_mm_s` from channel `cheylas.vibration_rms_mm_s` (database, else bundled CSV) |
| Cheylas Sediment Erosion Events (W2) | `workflows/demonstrators/cheylas/monitoring/sediment_erosion_events.yaml` | cosim gauss_seidel [`SedimentExposure` -> `Rul`, event `high_exposure` pulses `sediment.cleaning_trigger`]; `sediment.measured_concentration_g_l` from channel `cheylas.turbidity_g_l` (database, else bundled CSV) |

### Alqueva Hydroelectric Power Station

| Workflow | YAML | Model chain |
| --- | --- | --- |
| Alqueva Battery-EMS Co-simulation (W3) | `workflows/demonstrators/alqueva/hybrid/battery_ems_cosim.yaml` | cosim gauss_seidel [`EmsDispatch` <-> `BatteryDegradation`] -> `DegradationCost` |

### Archived workflows

The earlier FMI 2 replica workflows for VSMC, Le Cheylas, La Rance, Alqueva and
Vilarinho, the cross-site templates (condition monitoring, degradation cost
benefit, KPI assessment, sustainability CBA) and the La Rance FMI 3.0
sediment/cleaning event demo now live under `workflows/archive/` with their
original relative paths (for example
`workflows/archive/demonstrators/vsmc/dispatch/cascade_dispatch.yaml`). See
`workflows/archive/README.md`.

## Test Workflows

The legacy AECIS, acoustic-emission statistics, and Python chain smoke-test
workflows live under `workflows/tests/`. They are kept for local checks and
result parser tests, but they are not part of the dashboard's normal
demonstrator workflow catalog.

## Dashboard Display

Each demonstrator site shows its workflows in three views. **Plots** shows the
site assessment (status, recommendation and a condition table of selected
outputs such as `rul.rul_days`, `rul.damage_index` or `net_benefit_eur`)
above history charts of measured channels, run traces and end-of-run outputs,
with limit lines from the placeholder models' thresholds. **Workflow** shows
the selected workflow's port-level wiring diagram with the latest run's output
values and the Workflow and FMU cards; **Runs** shows the run history and the
run record. The charts, indicators and thresholds are declared per site in
`orchestrator/service/web/static/cads-model-matrix.json`
(`sites[].operator_view`, `sites[].plots.charts[]`); see
[`architecture.md`](architecture.md#dashboard-path) for the full description.

## Routing Pattern

Each workflow step writes a structured result and routes selected outputs to
later steps. A typical chain follows this pattern:

```yaml
synthetic_case: data/storhy/synthetic/cheylas_sediment_cycling.yaml
steps:
  - name: condition_monitoring
    fmu: fmu/models/ConditionMonitoringReplica.fmu
    outputs: [score, confidence, risk_index, damage_index, rul_days]
  - name: predictive_maintenance
    fmu: fmu/models/PredictiveMaintenanceReplica.fmu
    start_from:
      input_score: condition_monitoring.score
      input_risk_index: condition_monitoring.risk_index
      input_damage_index: condition_monitoring.damage_index
      input_rul_days: condition_monitoring.rul_days
```

This mirrors the intended final integration style: partner FMUs publish a small
set of typed outputs, downstream decision-support models consume those outputs,
and the dashboard presents the latest successful result for the selected site
and workflow.

`start_from` and `carry_over` look alike but answer different questions:

| Key | Reads from | Typical use |
| --- | --- | --- |
| `start_from: {input: step.variable}` | an **earlier step of the same run** | hand a final value down the chain (RUL into the cost model) |
| `carry_over: {input: step.variable}` | the **previous succeeded run** of the same workflow, from the database | continue cumulative state across assessments (damage index, exposure) |

`carry_over` only exists together with a `data_window` in mode
`since_previous_run`, and a target may not be set by both keys; see
[Assessment windows and state carry-over](#assessment-windows-and-state-carry-over-data_window-carry_over).

## Coupled Co-Simulation Workflows (`cosim`)

Sequential steps hand over final values only. A `cosim` step instead advances
several FMUs together with communication points, exchanging values over
declared connections at every point. This is the mechanism behind the FMU
interaction patterns required by D3.5 ARCH-COMP-003.

```yaml
steps:
  - name: battery_ems
    cosim:
      scheme: gauss_seidel          # gauss_seidel (sequential, ping-pong) | jacobi (parallel, lock-step)
      start_time: 0
      stop_time: 86400
      communication_step: 900       # master step H
      models:
        - name: ems
          fmu: fmu/models/EmsDispatch.fmu
          start_values: {max_power_mw: 20.0}
        - name: battery
          fmu: fmu/models/BatteryDegradation.fmu
          start_from: {initial_soc_percent: earlier_step.soc_percent}   # optional, as for sequential steps
      connections:
        - {from: battery.soc_percent, to: ems.soc_percent}
        - {from: ems.power_setpoint_mw, to: battery.power_setpoint_mw}
      events:                       # optional: condition-driven discrete signals
        - name: low_soc
          when: battery.soc_percent < 20      # model.variable <op> number; op in < <= > >= == !=
          set: ems.protection_request         # input or tunable parameter on the target model
          value: 1                            # driven while the condition holds (default 1)
          reset: 0                            # driven otherwise (default 0)
          mode: level                         # level (default) | pulse (one interval after a rising edge)
      outputs: [battery.soc_percent, ems.revenue_eur]      # default: every output of every model
      trace: {signals: [battery.soc_percent, ems.power_setpoint_mw], sample_every: 900}
  - name: kpi_assessment
    fmu: fmu/models/KPIAssessmentReplica.fmu
    start_from: {input_rul_days: battery_ems.battery.rul_days}   # cosim results use model.variable keys
```

Semantics:

- **Step size (ARCH-COMP-002).** Each FMU advances with the `stepSize` declared
  in its own `modelDescription.xml` DefaultExperiment and sub-steps inside one
  communication interval. A `step_size` on a cosim model is rejected. For
  sequential steps the YAML `step_size` is now only a fallback used when the
  FMU declares none; a warning is printed when both exist and differ.
- **`jacobi`** advances every model from the values exchanged at the start of
  the interval, then exchanges (parallel, lock-step).
- **`gauss_seidel`** advances models in listed order; each model receives the
  outputs of the models already advanced in the current interval (sequential,
  ping-pong when the connection graph has a cycle, one-way when it is acyclic).
- **Events** are evaluated by the master at every communication point on the
  latest exchanged values. Rising and falling edges are logged into `_run`, a
  boolean trace signal `events.<name>.active` and an output
  `events.<name>.count` are added automatically. Models are never skipped or
  stepped out of order, because FMI requires contiguous communication points;
  "event-driven" means the receiving model reacts to the pulsed input.
- **Initialization.** Connected initial values are propagated between
  `enterInitializationMode` and `exitInitializationMode`.
- **Results.** A cosim step produces flattened `model.variable` keys plus an
  optional `trace` block in the same shape as sequential steps, so later steps
  can reference `cosim_step.model.variable` through `start_from`.

### Run status, timing and provenance (`_run`)

Every runner result carries a reserved pseudo-step `_run`, also when a step
fails (the completed steps stay in the result, the failing step is named):

| Field | Meaning |
|---|---|
| `status`, `error`, `failed_step` | succeeded, failed, cancelled (ARCH-COMP-017), or `skipped` when a `data_window` assessment found no new data (runner exit code 0, nothing written to the database, every step listed as skipped) |
| `started_at`, `finished_at`, `wall_seconds`, `simulated_seconds`, `ratio` | wall-clock time and simulated-to-wall ratio (ARCH-COMP-008); simulated time is in the FMU's own unit |
| `runner_version`, `workflow.path`, `workflow.sha256` | the runner build and the exact workflow definition executed (ARCH-COMP-012/013) |
| `steps[].fmus[]` | per FMU: repo path, sha256, FMI version, model name, version, GUID or instantiation token, generation tool, declared and used step, do_step calls (ARCH-COMP-013/018) |
| `steps[].events[]`, `communication_points`, `terminated_by`, `failed_at` | cosim diagnostics |
| `steps[].fmus[].inputs[]` | the dataset that fed each input series (`source`: `db`, `db-fallback-csv`, `csv` or `s3`), with the database window `from`/`to` |
| `steps[].fmus[].carry_over` | only with `carry_over`: `source` (`previous_run` or `start_values`), `run_id` of the run it continued from, `refs` (target to `step.variable`) and the `values` used |
| `data_window` | only with `data_window`: `mode`, `resolved`, `source` (`db` or `fallback`), `site`, `channels`, `from`/`to` (half-open window, RFC 3339), `span_seconds`, `sim_start_time`/`sim_stop_time`, `latest_sample`, `backlog_seconds`, `previous_run_id`, `previous_to` |
| `results_sink` | outcome of writing the run to the database (`skipped` for a no-new-data run) |

### Pattern coverage of the demo workflows

| Workflow | ARCH-COMP-003 pattern | Slide-6 candidate | Models |
|---|---|---|---|
| `workflows/demonstrators/cheylas/maintenance/runner_rul_lockstep.yaml` | parallel lock-step (jacobi) over a one-way DAG + one-way sequential tail | W1 | RunnerStress, IoTIndicator -> Rul, then DegradationCost |
| `workflows/demonstrators/cheylas/monitoring/sediment_erosion_events.yaml` | event-driven (gauss_seidel, one-way connection, event targets the model it reads) | W2 | SedimentExposure -> Rul with a pulsed `cleaning_trigger` |
| `workflows/demonstrators/alqueva/hybrid/battery_ems_cosim.yaml` | sequential ping-pong (gauss_seidel, cyclic) + one-way sequential tail | W3 | EmsDispatch <-> BatteryDegradation, then DegradationCost |
| `workflows/archive/**` | one-way master-slave (sequential hand-over) | archived | FMI 2 replicas |

The FMI 3.0 models under `create_fmu/storhy_models/` are stateful, deterministic
placeholders with time-compressed ageing so a 24 h run shows visible trends.
They are not validated engineering models.

## Measurement Data And Ingestion

The Cheylas workflows read their measured channels (IoT vibration and
turbidity) from the STOR-HY measurement database instead of synthesising them
inside the FMUs (D3.5 ARCH-DATA-001/004/007). The batches shipped in this repo
are **synthetic placeholders** generated by
`scripts/dev/generate_synthetic_measurements.py` (fixed seed); they are not
plant data.

### Batch layout

```text
data/storhy/<site>/measurements/
  batch-2026-05-20/            # one ingestion batch
    metadata.yaml
    vibration_rms_mm_s.csv     # header time,value; RFC3339 UTC timestamps
    turbidity_g_l.csv
  batch-bad-example/           # deliberately malformed (no unit), tests rejection
  upcoming/                    # later days, never loaded at startup
    batch-2026-05-21/ ...      # replayed one day at a time by run_db.sh --next
  vibration_rms_mm_s.csv       # fallback: header time,<FMU variable>; time in s from batch start
  turbidity_g_l.csv
```

`metadata.yaml` describes the batch (ARCH-DATA-001 provenance, ARCH-DATA-007
per-demonstrator storage):

| Field | Meaning |
| --- | --- |
| `site` | demonstrator id; also the PostgreSQL schema the batch is loaded into |
| `batch` | batch identifier, stored with each channel |
| `source` | instrument or system the data came from |
| `channels[].channel` | channel name (lowercase identifier) |
| `channels[].unit` | unit, required and non-empty |
| `channels[].sampling_interval_s` | nominal cadence; the CSV must match within 1 % |
| `channels[].file` | CSV file in the batch directory |

```yaml
site: cheylas
batch: 2026-05-20-iot
source: NORCE AE/vibration edge box (synthetic placeholder)
channels:
  - {channel: vibration_rms_mm_s, unit: mm/s, sampling_interval_s: 300, file: vibration_rms_mm_s.csv}
  - {channel: turbidity_g_l, unit: g/l, sampling_interval_s: 300, file: turbidity_g_l.csv}
```

### Database schema

Each site gets its own schema (`cheylas`, `alqueva`), created idempotently by
both the ingestion script and the Go runtime:

- `<site>.channels(channel primary key, unit, source, sampling_interval_s, batch, ingested_at)`
- `<site>.measurements(time timestamptz, channel references channels, value, primary key (time, channel))`,
  a TimescaleDB hypertable on `time` when the extension is installed.

### Ingestion

```bash
scripts/commands/run_db.sh                     # local TimescaleDB container + ingest all batch-*/ (skips batch-bad-*)
export CADS_DB_URL=postgres://cads:cads@127.0.0.1:5432/storhy

# or ingest explicitly (needs psycopg[binary] and pyyaml):
python3 scripts/ingest_measurements.py --batch data/storhy/cheylas/measurements/batch-2026-05-20
python3 scripts/ingest_measurements.py --dry-run --batch <dir> [--batch <dir> ...] [--site <id>]

# near-realtime replay: load the next upcoming day and ask the dashboard to assess it
scripts/commands/run_db.sh --next --notify
scripts/commands/run_db.sh --batch data/storhy/cheylas/measurements/upcoming/batch-2026-05-21
```

`scripts/commands/run_db.sh` starts or reuses the `cads-timescaledb` container
(`docker.io/timescale/timescaledb:latest-pg16`, user/password/database
`cads/cads/storhy` on port 5432) with Podman or Docker, waits until it is
ready, and ingests with a venv at `.local/dbvenv`. `--reset` removes the
container and its data first. Further options:

| Option | Effect |
| --- | --- |
| `--batch DIR` | ingest only `DIR` instead of the startup batches (repeatable) |
| `--next` | per site, ingest only the oldest `measurements/upcoming/batch-<date>/` dated after the newest measurement already in the database, as if that day had just arrived |
| `--notify` | after loading, `POST <url>/api/sites/<site>/runs` for every site that got new rows, so the dashboard service runs that site's assessment workflows |
| `--notify-url URL` | service URL for `--notify` (implies `--notify`; default `$CADS_DASHBOARD_URL` or `http://127.0.0.1:8080`) |
| `--no-ingest` | only start the database |

`scripts/ingest_measurements.py --notify URL` does the notification itself
(default `$CADS_DASHBOARD_URL`, unset means off): one POST per site with new
rows after all batches are processed, never on `--dry-run`, for rejected
batches or when nothing new was inserted. A failed notification only logs a
warning and never changes the exit code. Ingestion and assessment stay
decoupled; the database is the only boundary, and the notified site run does
exactly what the dashboard's **Run assessment** button does.

The synthetic data generator writes more days on request:

```bash
python3 scripts/dev/generate_synthetic_measurements.py --days 8                 # batch-2026-05-20 + upcoming/ 05-21..05-27
python3 scripts/dev/generate_synthetic_measurements.py --start 2026-05-20 --days 4
```

Day 0 (`--start`, default 2026-05-20) keeps the original single-day output
(`batch-<date>/`, fallback CSVs and the bad example) byte for byte. Each later
day goes to `upcoming/batch-<date>/` with 288 samples from 00:05 to the next
midnight and a slow vibration drift, so repeated assessments show damage and
RUL trending. `run_db.sh` never loads `upcoming/` at startup.

The ingestion script validates a batch as a whole before writing anything
(ARCH-DATA-004): required metadata keys, non-empty units, existing files, a
`time,value` header, strictly increasing RFC3339 times, finite numeric values,
and a cadence consistent with `sampling_interval_s` within 1 %. Any failure
rejects the whole batch with a logged reason and exit code 2, for example:

```text
[error] batch data/storhy/cheylas/measurements/batch-bad-example rejected: channel vibration_rms_mm_s: missing or empty 'unit'
```

Accepted batches upsert their channels and insert measurements with
`on conflict do nothing`, so re-ingesting is idempotent. The summary lists
rows, new versus already present rows, and the time range per channel.

### Feeding a model from the database (`input_series.db`)

```yaml
models:
  - name: iot
    fmu: fmu/models/IoTIndicator.fmu
    input_series:
      db:
        site: cheylas                                             # schema
        channels: {vibration_rms_measured_mm_s: vibration_rms_mm_s}   # FMU input variable: channel
        window: {from: "2026-05-20T00:00:00Z", to: "2026-05-21T00:00:00Z"}
        fallback: {csv: data/storhy/cheylas/measurements/vibration_rms_mm_s.csv}
```

The window maps to simulation time: `window.from` is `t = 0` and samples are
fed at `t = time - window.from` seconds. When the workflow sets a
[`data_window`](#assessment-windows-and-state-carry-over-data_window-carry_over),
`window` must be omitted: the input inherits the resolved window, and time
becomes `t = sim_start + (time - from)` with `sim_start` the seconds since
00:00 UTC of `from`. With `CADS_DB_URL` set, the runtime
reads the channels from `<site>.measurements`. Without it (the default for
Playground runs and plain local runs), it feeds the repo-relative
`fallback.csv`, which holds the same values with time in seconds since the
batch start. The runner records which source fed each input in `_run`.

On the FMU side the measured inputs default to 0, and a value of 0 means "no
measurement": `IoTIndicator` then falls back to its synthetic vibration
signal and `SedimentExposure` to its profile concentration.

## Assessment windows and state carry-over (`data_window`, `carry_over`)

A fixed `window` assesses the same day on every run. For repeated, near-realtime
assessments a workflow instead declares a workflow-level `data_window`: the
runtime resolves the window against the newest data in the database, so every
run covers the measurements that arrived since the previous one, and
`carry_over` continues cumulative model state from that previous run. Both
Cheylas workflows use it; `workflows/tests/cheylas_runner_rul_fixed_window.yaml`
keeps the earlier fixed-window version of the runner workflow for regression.
The Alqueva Battery-EMS workflow has no `data_window`: its assessment is a
fixed-window run that never chains from the previous run and never reports
"Up to date".

```yaml
metadata:
  site_id: cheylas                              # required; every db input must use this site
data_window: {mode: since_previous_run, initial_span: 24h, max_span: 7d}   # or {mode: latest, span: 24h}
steps:
  - name: runner_health
    cosim:
      scheme: jacobi
      communication_step: 900                   # no start_time/stop_time: the window sets them
      models:
        - name: iot
          fmu: fmu/models/IoTIndicator.fmu
          input_series:
            db:                                 # no window: inherited from data_window
              site: cheylas
              channels: {vibration_rms_measured_mm_s: vibration_rms_mm_s}
              fallback: {csv: data/storhy/cheylas/measurements/vibration_rms_mm_s.csv}
        - name: rul
          fmu: fmu/models/Rul.fmu
          start_values: {initial_damage: 0.35}  # first run (and no-database fallback)
          carry_over:                           # later runs: previous run's outputs
            initial_damage: runner_health.rul.damage_index
            initial_planning_rate_per_day: runner_health.rul.planning_rate_per_day
      outputs: [rul.damage_index, rul.planning_rate_per_day, ...]   # carried keys must be outputs
```

Rules (checked before any FMU is loaded):

- `mode: since_previous_run` takes `initial_span` and `max_span`;
  `mode: latest` takes `span`. Durations are Go durations (`36h`, `90m`), a
  day suffix (`7d`) or bare seconds (`86400`), positive and a multiple of the
  alignment step: the largest cosim `communication_step`, else the largest
  `step_size`, else 1 s.
- The workflow needs `metadata.site_id` and at least one `input_series.db`
  input; every db input uses that site and omits its own `window`.
- Steps that read db inputs omit `start_time`/`stop_time` (a cosim block may
  not set just one of them). Other plain steps without times, such as the
  `degradation_cost` tail, also run over the window, so cost integrates the
  real span.
- `carry_over` (on a cosim model or a plain `fmu` step, not on a cosim step
  itself) needs mode `since_previous_run`, uses `step.variable` references
  like `start_from`, and may not target a variable that `start_from` also
  sets. Order of precedence: `start_values` literal, then `carry_over`, then
  `start_from`.

Resolution, per run:

- `latest` is the newest sample time common to all window channels (the
  minimum over the channels' newest samples). The window is half-open,
  `[from, to)` with `to = latest`: the bridge holds a sample until the next
  one, so the newest sample is the first value of the *next* assessment, not
  the last of this one. With the bundled `batch-2026-05-20` (00:00 to the next
  midnight) the first run is exactly `[2026-05-20T00:00Z, 2026-05-21T00:00Z)`,
  simulation time 0..86400, the same as the fixed-window workflow.
- `since_previous_run`: without a previous succeeded run, `from = latest -
  initial_span`. Otherwise `from` is the previous run's `to` and the span is
  the available data rounded down to whole alignment steps, capped at
  `max_span`. Data beyond the cap is reported as `backlog_seconds` and picked
  up by the next run (catch-up, never a gap). Less than one alignment step of
  new data means *no new data*.
- `latest`: always the newest `span` (`from = latest - span`); no new data when
  the newest sample is not after the previous run's `to`.
- Simulation time follows the time of day, because the placeholder models use
  it for duty cycles: `sim_start` is the seconds since 00:00 UTC of `from`,
  `sim_stop = sim_start + (to - from)`. A window starting at 06:00 runs
  21600..(21600 + span); trace times in the result are therefore time-of-day
  based, and the dashboard maps them to wall time with `_run.data_window`.
- Every channel must have a sample exactly at `from` (consecutive windows join
  on a shared sample). The bundled 300 s batches on the 900 s grid satisfy
  this; otherwise the run fails with `have no sample exactly at the window
  start`.
- The previous run is the newest `succeeded` run of the same workflow path and
  mode, ordered by `provenance->'data_window'->>'to'` in `<site>.runs`. The
  window and `carry_over` always use the same previous run, recorded as
  `_run.data_window.previous_run_id` and `steps[].fmus[].carry_over.run_id`.
- A carried key that is missing from the previous run's outputs, or not
  numeric, fails the run instead of silently resetting accumulated damage.

Outcomes:

- **Skipped.** With no new data the run finishes with `_run.status: skipped`
  and `results_sink.status: skipped`, all steps skipped, exit code 0, nothing
  written to the database, stdout still the JSON result. The dashboard shows
  it as "no new data" / "Up to date".
- **Fallback without a database.** Without `CADS_DB_URL` (plain local runs,
  hosted runs without the `storhy-cads-db` secret) the window is unresolved:
  `data_window.resolved: false`, `source: fallback`, simulation time 0..span
  (`initial_span` or `span`), the fallback CSVs feed the inputs (their
  `inputs[].from`/`to` stay empty), `carry_over` uses the literal start values
  (`source: start_values`), and a warning goes to stderr if a fallback CSV is
  shorter than the span. Fallback runs never chain.
- **Reset.** The assessment chain lives only in `<site>.runs`. To start over,
  for example after removing an output that `carry_over` reads, run
  `scripts/commands/run_db.sh --reset`, which drops the container and its data
  and re-ingests the startup batches; the next run is a first run again.

The RUL model carries two values: `initial_damage` and
`initial_planning_rate_per_day`, fed from its outputs `damage_index` and
`planning_rate_per_day`. A positive `initial_planning_rate_per_day` acts as a
floor on the conservative planning rate, so the RUL does not jump up at a run
boundary; the default 0 keeps the plain `max(design, smoothed)` rule. The
sediment workflow carries `initial_exposure`, `initial_hours_since_cleaning`
and the RUL `initial_damage`; it also sets the RUL `demo_acceleration` to 20
(model default 1) so accumulated damage grows visibly over a few replayed
days, while `damage_rate_per_day` and `rul_days` stay at real rates.

## Dashboard Navigation

The dashboard has a site tab per demonstrator in the demo scope, a site strip,
and the Plots, Workflow and Runs views; workflow tabs grow with the catalog.
[`architecture.md`](architecture.md#dashboard-path) describes them. The short
labels and per-workflow model lists come from
`orchestrator/service/web/static/cads-model-matrix.json` (`models[].name`,
`demo_fmus`, `demo_workflows`), which also carries the demo scope
(`demo_scope.sites`) and the partner access lists per site. The file still
holds the slide numbering of the Task 3.3 matrix (M1 to M14, W1 to W7) for
traceability, but the dashboard never shows ids or model-family groupings. The
FMU coupling label shown per workflow is derived from the catalog
(`cosim.scheme` and `patterns`), not from this file.

## Runtime Positioning

D3.5 Section 6 states that the co-simulation responsibilities do not prescribe
a particular scheduler, execution library or deployment topology. What partners
build against is the integration contract: an FMI Co-Simulation FMU, a YAML
workflow definition, and the result JSON with its `_run` provenance block. The
Go/FMIL runner in this repository is a replaceable reference implementation of
that contract, and the workflows above double as its acceptance tests. Argo
Workflows is used because it is already the scheduler on the Kaizen playground
and it provides run isolation, deadlines, resource limits, outcome states and
label-based provenance (ARCH-COMP-006/015/016/017/018).
