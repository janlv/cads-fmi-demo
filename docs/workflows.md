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
- `create_fmu/storhy_fmi3/` contains the FMI 3.0 co-simulation FMUs used by
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
| M1 runner stress | `RunnerStressFmi3` | Cheylas Runner RUL Lock-step |
| M5 sediment | `SedimentExposureFmi3` (tidal or cycling profile, cleaning accounting) | Cheylas Sediment Erosion Events |
| M7 IoT indicators | `IoTIndicatorFmi3` | Cheylas Runner RUL Lock-step |
| M8 degradation/health/RUL | `RulFmi3` | Cheylas Runner RUL Lock-step, Cheylas Sediment Erosion Events |
| M9 battery degradation | `BatteryDegradationFmi3` | Alqueva Battery-EMS Co-simulation |
| M10 EMS | `EmsDispatchFmi3` | Alqueva Battery-EMS Co-simulation |
| M14 degradation cost vs market revenue | `DegradationCostFmi3` | Cheylas Runner RUL Lock-step, Alqueva Battery-EMS Co-simulation |

## Demonstrator Workflows

### Le Cheylas Power Station

| Workflow | YAML | Model chain |
| --- | --- | --- |
| Cheylas Runner RUL Lock-step (W1) | `workflows/demonstrators/cheylas/maintenance/runner_rul_lockstep.yaml` | cosim jacobi [`RunnerStressFmi3`, `IoTIndicatorFmi3` -> `RulFmi3`] -> `DegradationCostFmi3` |
| Cheylas Sediment Erosion Events (W2) | `workflows/demonstrators/cheylas/monitoring/sediment_erosion_events.yaml` | cosim gauss_seidel [`SedimentExposureFmi3` -> `RulFmi3`, event `high_exposure` pulses `sediment.cleaning_trigger`] |

### Alqueva Hydroelectric Power Station

| Workflow | YAML | Model chain |
| --- | --- | --- |
| Alqueva Battery-EMS Co-simulation (W3) | `workflows/demonstrators/alqueva/hybrid/battery_ems_cosim.yaml` | cosim gauss_seidel [`EmsDispatchFmi3` <-> `BatteryDegradationFmi3`] -> `DegradationCostFmi3` |

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

## Dashboard Display Proposal

The dashboard currently has a generic STOR-HY mock panel. The following table
defines workflow-specific values and plots that should replace or extend the
generic view when the mock workflows mature.

| Workflow | Summary values | Suggested plots |
| --- | --- | --- |
| Cheylas Runner RUL Lock-step | `rul.damage_index`, `rul.rul_days`, `rul.status_code`, `runner.energy_mwh`, `degradation_cost_eur`, `net_benefit_eur`, `recommendation_code` | Load and stress trend; vibration and condition indicator; damage and RUL trend; cost versus revenue card. |
| Cheylas Sediment Erosion Events | `sediment.sediment_exposure`, `sediment.cleaning_count`, `sediment.cumulative_cleaning_cost_eur`, `sediment.downtime_h`, `rul.damage_index`, `rul.rul_days` | Exposure sawtooth with cleaning events; concentration trend; damage and RUL trend. |
| Alqueva Battery-EMS Co-simulation | `battery.soh_percent`, `battery.rul_days`, `ems.revenue_eur`, `degradation_cost_eur`, `net_benefit_eur`, `benefit_cost_ratio`, `recommendation_code` | SoC and set-point trend; price versus power; SoH trend; cost versus revenue card. |

Good cross-workflow defaults are a compact summary card row, a model-chain strip,
a status/recommendation card, one primary time-series plot, and one benefit/risk
comparison view. Workflow-specific panels should still keep the raw JSON
available behind an expandable details control for debugging.

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
          fmu: fmu/models/EmsDispatchFmi3.fmu
          start_values: {max_power_mw: 20.0}
        - name: battery
          fmu: fmu/models/BatteryDegradationFmi3.fmu
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
| `status`, `error`, `failed_step` | succeeded, failed or cancelled (ARCH-COMP-017) |
| `started_at`, `finished_at`, `wall_seconds`, `simulated_seconds`, `ratio` | wall-clock time and simulated-to-wall ratio (ARCH-COMP-008); simulated time is in the FMU's own unit |
| `runner_version`, `workflow.path`, `workflow.sha256` | the runner build and the exact workflow definition executed (ARCH-COMP-012/013) |
| `steps[].fmus[]` | per FMU: repo path, sha256, FMI version, model name, version, GUID or instantiation token, generation tool, declared and used step, do_step calls (ARCH-COMP-013/018) |
| `steps[].events[]`, `communication_points`, `terminated_by`, `failed_at` | cosim diagnostics |

### Pattern coverage of the demo workflows

| Workflow | ARCH-COMP-003 pattern | Slide-6 candidate | Models |
|---|---|---|---|
| `workflows/demonstrators/cheylas/maintenance/runner_rul_lockstep.yaml` | parallel lock-step (jacobi) over a one-way DAG + one-way sequential tail | W1 | RunnerStressFmi3, IoTIndicatorFmi3 -> RulFmi3, then DegradationCostFmi3 |
| `workflows/demonstrators/cheylas/monitoring/sediment_erosion_events.yaml` | event-driven (gauss_seidel, one-way connection, event targets the model it reads) | W2 | SedimentExposureFmi3 -> RulFmi3 with a pulsed `cleaning_trigger` |
| `workflows/demonstrators/alqueva/hybrid/battery_ems_cosim.yaml` | sequential ping-pong (gauss_seidel, cyclic) + one-way sequential tail | W3 | EmsDispatchFmi3 <-> BatteryDegradationFmi3, then DegradationCostFmi3 |
| `workflows/archive/**` | one-way master-slave (sequential hand-over) | archived | FMI 2 replicas |

The FMI 3.0 models under `create_fmu/storhy_fmi3/` are stateful, deterministic
placeholders with time-compressed ageing so a 24 h run shows visible trends.
They are not validated engineering models.

## Demo Workflows Screen (dashboard navigation)

The dashboard is a single screen: a left column lists the workflows the demo
runs and, for each, the chain of models it couples (FMU names, a short label,
owners) with the coupling drawn between them; the right column shows the
selected workflow (site, partners with access, coupling, limits, latest run,
launch button) and the selected model (FMU identity from the last run, inputs,
outputs, parameters); the run history and results follow below. The short labels and the per-workflow model lists
come from `orchestrator/service/web/static/cads-model-matrix.json`
(`models[].name`, `demo_fmus`, `demo_workflows`), which also carries the demo
scope (`demo_scope.sites`) and the partner access lists per site. The file
still holds the slide numbering of the Task 3.3 matrix (M1 to M14, W1 to W7)
for traceability, but the dashboard never shows ids or model-family groupings.
The FMU coupling label shown per workflow is derived from the catalog
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
