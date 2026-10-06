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
- `workflows/demonstrators/<site>/<category>/` contains site-specific workflow
  YAML files.
- `workflows/common/<category>/` contains cross-site workflow templates.
- Every STOR-HY YAML file has a `metadata` block with `display_name`,
  `site_id`, `category`, `result_family: storhy_mock`, `description`, and
  `tags`. The dashboard uses this metadata to filter workflows by demonstrator.
- Every STOR-HY YAML file also references a `synthetic_case` fixture under
  `data/storhy/synthetic/`. Hosted Argo pods read these files from the container
  image and include the case context in the JSON result payload.

## Replica FMU Models

The current replicas are deterministic, low-order approximations. They expose a
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

## Demonstrator Workflows

### VSMC Dams

Site id: `vsmc`

| Workflow | YAML | Model chain |
| --- | --- | --- |
| Cascade Dispatch | `workflows/demonstrators/vsmc/dispatch/cascade_dispatch.yaml` | `HydroCascadeDispatchReplica` -> `StartSequenceWearReplica` -> `KPIAssessmentReplica` |
| HSC Flexibility | `workflows/demonstrators/vsmc/dispatch/hsc_flexibility.yaml` | `HSCFlexibilityReplica` -> `SustainabilityCBAReplica` -> `KPIAssessmentReplica` |
| Soft Start Wear | `workflows/demonstrators/vsmc/maintenance/soft_start_wear.yaml` | `StartSequenceWearReplica` -> `PredictiveMaintenanceReplica` -> `KPIAssessmentReplica` |

### Le Cheylas Power Station

Site id: `cheylas`

| Workflow | YAML | Model chain |
| --- | --- | --- |
| Fast Dewatering Cycling | `workflows/demonstrators/cheylas/control/fast_dewatering.yaml` | `StartSequenceWearReplica` -> `PredictiveMaintenanceReplica` -> `KPIAssessmentReplica` |
| Sediment Runner Wear | `workflows/demonstrators/cheylas/monitoring/sediment_runner_wear.yaml` | `ConditionMonitoringReplica` -> `RunnerSedimentWearReplica` -> `PredictiveMaintenanceReplica` -> `KPIAssessmentReplica` |
| Predictive Maintenance | `workflows/demonstrators/cheylas/maintenance/predictive_maintenance.yaml` | `ConditionMonitoringReplica` -> `PredictiveMaintenanceReplica` -> `KPIAssessmentReplica` |

### La Rance Tidal Power Station

Site id: `la-rance`

| Workflow | YAML | Model chain |
| --- | --- | --- |
| Corrosion Biofouling | `workflows/demonstrators/la_rance/harsh_fluid/corrosion_biofouling.yaml` | `CorrosionBiofoulingReplica` -> `KPIAssessmentReplica` |
| Cleaning Interval | `workflows/demonstrators/la_rance/maintenance/cleaning_interval.yaml` | `CorrosionBiofoulingReplica` -> `CleaningIntervalReplica` -> `KPIAssessmentReplica` |
| Tidal BESS Sizing | `workflows/demonstrators/la_rance/hybrid/bess_sizing.yaml` | `BESSSizingReplica` -> `SustainabilityCBAReplica` -> `KPIAssessmentReplica` |

### Alqueva Hydroelectric Power Station

Site id: `alqueva`

| Workflow | YAML | Model chain |
| --- | --- | --- |
| Hybrid EMS | `workflows/demonstrators/alqueva/hybrid/hybrid_ems.yaml` | `HybridEMSReplica` -> `PredictiveMaintenanceReplica` -> `KPIAssessmentReplica` |
| Fast Service Controller | `workflows/demonstrators/alqueva/control/fast_service_controller.yaml` | `FastServiceControllerReplica` -> `PredictiveMaintenanceReplica` -> `KPIAssessmentReplica` |
| Runner Fatigue | `workflows/demonstrators/alqueva/maintenance/runner_fatigue.yaml` | `ConditionMonitoringReplica` -> `StartSequenceWearReplica` -> `PredictiveMaintenanceReplica` -> `KPIAssessmentReplica` |

### Vilarinho Das Furnas Dam

Site id: `vilarinho`

| Workflow | YAML | Model chain |
| --- | --- | --- |
| MIV Regulation | `workflows/demonstrators/vilarinho/control/miv_regulation.yaml` | `MIVRegulationReplica` -> `MIVFatigueReplica` -> `KPIAssessmentReplica` |
| MIV Fatigue Monitoring | `workflows/demonstrators/vilarinho/monitoring/miv_fatigue.yaml` | `ConditionMonitoringReplica` -> `MIVFatigueReplica` -> `PredictiveMaintenanceReplica` -> `KPIAssessmentReplica` |
| HSC MIV Comparison | `workflows/demonstrators/vilarinho/control/hsc_miv_comparison.yaml` | `MIVRegulationReplica` -> `HSCFlexibilityReplica` -> `SustainabilityCBAReplica` -> `KPIAssessmentReplica` |

## Common Workflow Templates

| Workflow | YAML | Model chain |
| --- | --- | --- |
| CADS Condition Monitoring | `workflows/common/condition_monitoring/cads_condition_monitoring.yaml` | `ConditionMonitoringReplica` -> `PredictiveMaintenanceReplica` |
| Degradation Cost Benefit | `workflows/common/decision_support/degradation_cost_benefit.yaml` | `ConditionMonitoringReplica` -> `PredictiveMaintenanceReplica` -> `SustainabilityCBAReplica` |
| Demo KPI Assessment | `workflows/common/kpi/demo_kpi_assessment.yaml` | `KPIAssessmentReplica` |
| Sustainability CBA | `workflows/common/sustainability/sustainability_cba.yaml` | `KPIAssessmentReplica` -> `SustainabilityCBAReplica` |

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
| VSMC Cascade Dispatch | `score`, `risk_index`, `power_mw`, `reservoir_level_m`, `flexibility_delta_percent`, `value_delta_eur`, `rul_days` | Power and reservoir level over time; flexibility versus risk; start-sequence damage and RUL trend. |
| VSMC HSC Flexibility | `kpi_score`, `flexibility_delta_percent`, `power_mw`, `value_delta_eur`, `co2_delta_tonnes`, `risk_index` | HSC power/flexibility trend; KPI score versus risk; value and CO2 benefit bars. |
| VSMC Soft Start Wear | `score`, `damage_index`, `rul_days`, `availability_delta_percent`, `risk_index`, `recommendation_code` | Damage and RUL over time; risk/score trend; maintenance recommendation card. |
| Cheylas Fast Dewatering Cycling | `score`, `damage_index`, `rul_days`, `risk_index`, `availability_delta_percent` | Cycling wear trend; RUL forecast; risk band with status threshold markers. |
| Cheylas Sediment Runner Wear | `score`, `sediment_exposure`, `damage_index`, `rul_days`, `risk_index`, `confidence` | Sediment exposure and damage trend; RUL trend; risk versus confidence. |
| Cheylas Predictive Maintenance | `score`, `risk_index`, `damage_index`, `rul_days`, `status_code`, `recommendation_code` | Risk and RUL trend; damage index trend; status/recommendation panel. |
| La Rance Corrosion Biofouling | `score`, `corrosion_index`, `biofouling_index`, `risk_index`, `confidence` | Corrosion and biofouling trend; risk score trend; corrosion/biofouling range bars. |
| La Rance Cleaning Interval | `score`, `corrosion_index`, `biofouling_index`, `risk_index`, `opex_delta_eur`, `recommendation_code` | Biofouling/corrosion trend; OPEX impact bar; cleaning recommendation panel. |
| La Rance Tidal BESS Sizing | `kpi_score`, `soc_percent`, `power_mw`, `value_delta_eur`, `co2_delta_tonnes`, `risk_index` | BESS state-of-charge and power trend; value and CO2 benefit bars; KPI/risk trend. |
| Alqueva Hybrid EMS | `score`, `soc_percent`, `power_mw`, `flexibility_delta_percent`, `value_delta_eur`, `co2_delta_tonnes`, `risk_index` | SOC and power trend; flexibility and value trend; risk and maintenance score. |
| Alqueva Fast Service Controller | `score`, `power_mw`, `flexibility_delta_percent`, `availability_delta_percent`, `risk_index` | Fast-service power response; flexibility and availability trend; risk score trend. |
| Alqueva Runner Fatigue | `score`, `damage_index`, `rul_days`, `availability_delta_percent`, `risk_index`, `recommendation_code` | Fatigue damage and RUL trend; availability impact; maintenance recommendation panel. |
| Vilarinho MIV Regulation | `score`, `valve_opening_percent`, `power_mw`, `risk_index`, `damage_index`, `availability_delta_percent` | Valve opening and power trend; fatigue/risk trend; availability impact card. |
| Vilarinho MIV Fatigue Monitoring | `score`, `valve_opening_percent`, `damage_index`, `rul_days`, `risk_index`, `confidence` | Valve opening and fatigue damage trend; RUL trend; risk versus confidence. |
| Vilarinho HSC MIV Comparison | `kpi_score`, `valve_opening_percent`, `power_mw`, `flexibility_delta_percent`, `value_delta_eur`, `co2_delta_tonnes` | MIV regulation versus HSC flexibility comparison; KPI/risk trend; value and CO2 benefit bars. |
| CADS Condition Monitoring | `score`, `confidence`, `risk_index`, `damage_index`, `rul_days`, `recommendation_code` | Health score and risk trend; damage/RUL trend; recommendation card. |
| Degradation Cost Benefit | `score`, `risk_index`, `rul_days`, `value_delta_eur`, `opex_delta_eur`, `co2_delta_tonnes` | Risk and RUL trend; value/OPEX/CO2 benefit bars; KPI score trend. |
| Demo KPI Assessment | `kpi_score`, `score`, `risk_index`, `status_code`, `recommendation_code`, `confidence` | KPI score and risk trend; status threshold gauge; recommendation card. |
| Sustainability CBA | `kpi_score`, `value_delta_eur`, `opex_delta_eur`, `co2_delta_tonnes`, `availability_delta_percent`, `risk_index` | Value and OPEX bars; CO2 benefit trend; KPI/risk trend. |

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
| `workflows/demonstrators/alqueva/hybrid/battery_ems_cosim.yaml` | sequential ping-pong (gauss_seidel, cyclic) + one-way FMI 2 tail | W3 / W6 | EmsDispatchFmi3 <-> BatteryDegradationFmi3, then KPIAssessmentReplica |
| `workflows/demonstrators/cheylas/maintenance/runner_rul_lockstep.yaml` | parallel lock-step (jacobi) over a one-way DAG + FMI 2 tail | W1 | RunnerStressFmi3, IoTIndicatorFmi3 -> RulFmi3, then PredictiveMaintenanceReplica |
| `workflows/demonstrators/la_rance/maintenance/sediment_cleaning_events.yaml` | event-driven | W2 | SedimentExposureFmi3 <-> CleaningDecisionFmi3 with a pulsed `trigger` |
| all 19 existing demonstrator and common workflows | one-way master-slave (sequential hand-over) | W1 to W6 placeholders | FMI 2 replicas |

The FMI 3.0 models under `create_fmu/storhy_fmi3/` are stateful, deterministic
placeholders with time-compressed ageing so a 24 h run shows visible trends.
They are not validated engineering models.

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
