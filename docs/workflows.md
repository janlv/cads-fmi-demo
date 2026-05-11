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

The existing `workflows/ae_event_statistics.yaml` demo is also mapped to La
Rance in the dashboard as an edge-computed acoustic-emission statistics view.

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

The existing `workflows/calculate_aecis.yaml` demo remains available in the
dashboard as the current AECIS trend plot workflow.

## Common Workflow Templates

| Workflow | YAML | Model chain |
| --- | --- | --- |
| CADS Condition Monitoring | `workflows/common/condition_monitoring/cads_condition_monitoring.yaml` | `ConditionMonitoringReplica` -> `PredictiveMaintenanceReplica` |
| Degradation Cost Benefit | `workflows/common/decision_support/degradation_cost_benefit.yaml` | `ConditionMonitoringReplica` -> `PredictiveMaintenanceReplica` -> `SustainabilityCBAReplica` |
| Demo KPI Assessment | `workflows/common/kpi/demo_kpi_assessment.yaml` | `KPIAssessmentReplica` |
| Sustainability CBA | `workflows/common/sustainability/sustainability_cba.yaml` | `KPIAssessmentReplica` -> `SustainabilityCBAReplica` |

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
      input_score: {step: condition_monitoring, output: score}
      input_risk_index: {step: condition_monitoring, output: risk_index}
      input_damage_index: {step: condition_monitoring, output: damage_index}
      input_rul_days: {step: condition_monitoring, output: rul_days}
```

This mirrors the intended final integration style: partner FMUs publish a small
set of typed outputs, downstream decision-support models consume those outputs,
and the dashboard presents the latest successful result for the selected site
and workflow.
