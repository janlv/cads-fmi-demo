# STOR-HY Candidate Workflows

Source reviewed: `/home/AD.NORCERESEARCH.NO/javi/Downloads/STOR-HY_Part B_IA_v8_20240116.pdf`

This catalog translates the STOR-HY proposal into candidate CADS dashboard workflows. The workflows are intended as implementation targets for Python/FMUs, Argo workflow definitions, and dashboard result panels. They are grouped into shared cross-site workflows and demonstrator-specific workflows.

## Design Principles

- Keep workflows demonstrator-oriented: selecting a site should reveal only the workflows that are relevant to that demonstrator.
- Keep outputs comparable: each workflow should return a compact summary, KPI status, time-series traces where relevant, and dashboard-ready plots.
- Prefer Python FMUs for early prototypes: this matches the current dashboard and avoids MATLAB/Simulink licensing constraints.
- Separate operational decision support from project reporting: real-time monitoring workflows should not be overloaded with WP7 cost, LCA, or social-impact summaries.
- Preserve CADS as the integration layer: each workflow should be able to consume SCADA, edge sensor summaries, simulation outputs, or synthetic/demo data.

## Common Result Shape

Each workflow should return a result object with these sections where applicable:

- `site`: demonstrator id, operator, country, and workflow category.
- `inputs`: files, sensor streams, SCADA tags, assumptions, and time range.
- `summary`: headline metrics for dashboard cards.
- `kpis`: proposal KPI baselines, targets, and current assessed values.
- `traces`: time series for trend plots.
- `diagnostics`: invalid rows, missing signals, model confidence, warnings.
- `recommendations`: concise operational or maintenance actions.
- `artifacts`: optional CSV, JSON, plot, or report paths.

## Cross-Site Workflows

### `cads_condition_monitoring`

Purpose: Convert SCADA and edge-computed sensor data into condition indicators, health indicators, and remaining useful life estimates.

Relevant proposal basis:
- WP3 condition monitoring strategy.
- IoT edge-compute sensors.
- CADS cyber-physical clone.
- Predictive maintenance strategies.

Candidate inputs:
- SCADA operating state, power, flow, head, valve openings, start/stop events.
- Edge summaries from vibration, acoustic emission, pressure, strain, turbidity, and temperature sensors.
- Site metadata and component configuration.

Dashboard outputs:
- Health indicator cards by component.
- Condition indicator trends.
- RUL estimate with confidence band.
- Sensor completeness and data-quality panel.

Relevant demonstrators:
- Cheylas
- Alqueva
- Vilarinho
- VSMC
- Pozu Figaredo

### `degradation_cost_benefit`

Purpose: Estimate the cost of degradation caused by a candidate operation and compare it with expected market revenue or service value.

Relevant proposal basis:
- WP3 informed decisions.
- WP4 ageing-aware control.
- CADS decision support for cost of wear versus operation benefit.

Candidate inputs:
- Candidate operation sequence.
- Market prices or ancillary-service remuneration.
- Degradation model outputs.
- Maintenance cost assumptions.

Dashboard outputs:
- Revenue versus degradation-cost balance.
- Recommendation: proceed, avoid, or review manually.
- Sensitivity to price, fatigue rate, and maintenance interval assumptions.

Relevant demonstrators:
- All sites, with different degradation models.

### `demo_kpi_assessment`

Purpose: Compare measured or simulated campaign data against STOR-HY demonstrator KPIs.

Relevant proposal basis:
- WP2 STOR-HY matrix.
- WP6 demonstrator validation.
- Multi-criteria assessment program.

Candidate inputs:
- Workflow results from site-specific workflows.
- KPI baseline and target table.
- Measurement campaign metadata.

Dashboard outputs:
- KPI status matrix.
- Baseline, current, and target values.
- Evidence links to source workflow runs.
- Site-level summary for reporting.

Relevant demonstrators:
- All sites.

### `sustainability_cba`

Purpose: Combine technical KPI gains with cost, environmental, and social-impact indicators for WP7-style reporting.

Relevant proposal basis:
- WP7 life-cycle assessment.
- Cost-benefit analysis.
- Circularity and social acceptance assessment.

Candidate inputs:
- Technical KPI assessment.
- Implementation and operational costs.
- CO2, circularity, safety, and local impact indicators.

Dashboard outputs:
- Cost-benefit summary.
- Environmental impact summary.
- Social and circularity notes.
- Exportable reporting table.

Relevant demonstrators:
- All sites.

## Demonstrator Workflows

## VSMC: Vouglans - Saut Mortier - Coiselet

Site context:
- EDF demonstrator.
- Three reservoirs in cascade.
- Low-head tandem pumping from Coiselet to Saut Mortier.
- Existing Vouglans pump turbine plus planned Saut Mortier variable-speed pump turbine.
- Main goals: cascade optimization, seasonal storage gain, start stress reduction, HSC flexibility, and environmental constraints such as hydropeaking.

### `vsmc_cascade_dispatch`

Purpose: Optimize cascade operation across Vouglans, Saut Mortier, and Coiselet.

Candidate inputs:
- Reservoir levels and volumes.
- Inflows and forecasts.
- Market prices and ancillary-service signals.
- Unit availability and operating constraints.
- Environmental and recreational constraints.

Dashboard outputs:
- Recommended pumping and generation schedule.
- Expected stored volume gain.
- Market value and CO2 reduction estimate.
- Hydropeaking constraint status.
- Unit dispatch timeline.

Implementation notes:
- Start with a deterministic Python optimizer.
- Later integrate reduced-order variable-speed pump-turbine model.
- Keep the result format compatible with `demo_kpi_assessment`.

### `vsmc_soft_start_wear`

Purpose: Compare pump start modes and quantify electrical/mechanical stress reduction.

Candidate inputs:
- Start event logs.
- Electrical current traces or summaries.
- Soft-starter assumptions.
- Mechanical stress model parameters.

Dashboard outputs:
- Peak current versus baseline.
- Estimated wear-and-tear reduction.
- Start event classification.
- Maintenance implication summary.

### `vsmc_hsc_flexibility`

Purpose: Evaluate hydraulic short-circuit operation for balancing power and low-demand flexibility.

Candidate inputs:
- Unit operating envelopes.
- HSC feasible zones.
- Grid service request profiles.
- Reservoir constraints.

Dashboard outputs:
- Available balancing capacity.
- Pump-mode flexibility window.
- CO2 displacement estimate.
- Operational risk flags.

## Cheylas

Site context:
- EDF demonstrator.
- Two 250 MW pump turbines.
- High-head PSP with sediment-laden water and high cycling.
- Main goals: market adaptation, shorter operating windows, fast dewatering, sediment monitoring, runner fatigue and RUL.

### `cheylas_fast_dewatering`

Purpose: Evaluate fast mode changes and shorter operating windows against market opportunities and fatigue impact.

Candidate inputs:
- Unit schedule and start/stop events.
- Market opportunity windows.
- Fast-dewatering sequence data.
- Vibration, pressure, strain, and temperature summaries.

Dashboard outputs:
- Current versus target operating-window duration.
- Start/stop reduction.
- Fatigue impact estimate.
- Market-adaptation score.
- Recommended operating mode.

### `cheylas_sediment_runner_wear`

Purpose: Estimate runner erosion, abrasion, and fatigue risk from turbidity and operating state.

Candidate inputs:
- Turbidity sensor data.
- Flow/head/power operating state.
- Vibration and pressure indicators.
- Runner model or empirical wear coefficients.

Dashboard outputs:
- Sediment exposure index.
- Runner wear indicator.
- RUL trend.
- Alarm periods and data gaps.
- Suggested inspection priority.

### `cheylas_predictive_maintenance`

Purpose: Combine virtual sensors, measured stress, and operating history into maintenance recommendations.

Candidate inputs:
- Condition indicators from `cads_condition_monitoring`.
- Runner stress estimates.
- Fatigue accumulation.
- Maintenance history and thresholds.

Dashboard outputs:
- Component risk ranking.
- Remaining useful life estimate.
- Recommended maintenance window.
- Confidence and missing-signal diagnostics.

## La Rance

Site context:
- EDF tidal power station.
- Saltwater, very low head, 24 bulb turbines, 4-quadrant operation.
- Main goals: corrosion resistance, biofouling control, water-parameter correlation, ROV-compatible cleaning, and BESS sizing for tidal generation shifting.

### `la_rance_corrosion_biofouling`

Purpose: Track material sample performance and correlate corrosion/biofouling with seawater parameters.

Candidate inputs:
- Sample/coupon inspection records.
- Turbidity, velocity, temperature, and salinity summaries.
- Cleaning history.
- Coating/material metadata.

Dashboard outputs:
- Corrosion rate by material.
- Biofouling growth index.
- Coating performance ranking.
- Water-parameter correlation plots.
- Inspection due dates.

### `la_rance_cleaning_interval`

Purpose: Compare cleaning strategies and estimate the next maintenance or ROV-cleaning window.

Candidate inputs:
- Fouling observations.
- Cleaning method and effort.
- Power derating or cooling-problem indicators.
- Water-parameter exposure history.

Dashboard outputs:
- Predicted cleaning interval.
- Expected generation-loss avoidance.
- Cleaning method comparison.
- Alert for accelerated fouling.

### `la_rance_bess_sizing`

Purpose: Assess BESS hybridization for shifting tidal generation toward higher-value market periods.

Candidate inputs:
- Tide-driven generation forecast.
- Market prices.
- Candidate BESS size and C-rate.
- Battery degradation assumptions.

Dashboard outputs:
- Recommended BESS size range.
- Value of shifted energy.
- Battery cycling and degradation estimate.
- Payback and sensitivity summary.

## Alqueva

Site context:
- EDP demonstrator.
- Hybrid PSP with hydro, floating PV, and 1 MW BESS.
- Main goals: hybrid EMS, fast services, FCR/aFRR, green black-start, stable fatigue despite more starts, and predictive maintenance.

### `alqueva_hybrid_ems`

Purpose: Optimize joint PSP, PV, and BESS dispatch for energy and ancillary-service markets.

Candidate inputs:
- Reservoir and unit status.
- PV forecast.
- BESS state of charge and limits.
- Market prices and service requirements.
- Degradation-cost model.

Dashboard outputs:
- Dispatch schedule by asset.
- Energy and ancillary-service revenue.
- BESS state-of-charge trace.
- Start/stop count and fatigue impact.
- Availability and flexibility gain.

### `alqueva_fast_service_controller`

Purpose: Evaluate transient acceleration and fast service provision using BESS support.

Candidate inputs:
- Service request profiles.
- PSP ramp/start sequence models.
- BESS response and state-of-charge.
- Fatigue and W&T parameters.

Dashboard outputs:
- Achieved response time versus target.
- FCR/aFRR capability.
- Green black-start readiness.
- Degradation impact.
- Constraint violations.

### `alqueva_runner_fatigue`

Purpose: Estimate runner fatigue and RUL under increased start/stop operation.

Candidate inputs:
- Strain gauge campaign data or virtual stress estimates.
- Start/stop and ramp events.
- Hydraulic operating state.
- XFLEX Hydro benchmark assumptions where available.

Dashboard outputs:
- Fatigue accumulation.
- RUL trend.
- Benchmark comparison.
- Maintenance interval impact.

## Vilarinho das Furnas

Site context:
- EDP demonstrator.
- One multistage pump and one Francis turbine.
- Main goals: add flexibility to a non-regulating scheme using main inlet valve regulation and HSC, while managing valve fatigue and safety risk.

### `vilarinho_miv_regulation`

Purpose: Evaluate main inlet valve opening strategies for turbine and pump power regulation.

Candidate inputs:
- Valve position traces.
- Power, pressure, flow, vibration, and acoustic emission summaries.
- Operating limits from CFD/FEA studies.
- Ancillary-service target profile.

Dashboard outputs:
- Feasible regulation range.
- Valve opening recommendation.
- Power tracking error.
- Safety and cavitation flags.
- Ancillary-service capability summary.

### `vilarinho_miv_fatigue`

Purpose: Estimate fatigue risk for the main inlet valve during intermediate positions and transient operation.

Candidate inputs:
- Pressure transients.
- Acoustic emission event statistics.
- Accelerometer and vibration indicators.
- Valve position and dwell time.
- CFD/FEA-derived fatigue coefficients.

Dashboard outputs:
- Fatigue damage index.
- High-risk operating periods.
- Component risk classification.
- Recommended operating envelope.

### `vilarinho_hsc_miv_comparison`

Purpose: Compare MIV regulation plus HSC against conventional refurbishment or BESS alternatives.

Candidate inputs:
- HSC feasibility limits.
- MIV regulation result.
- Cost assumptions for valve adaptation, turbine replacement, and BESS support.
- Market/service value assumptions.

Dashboard outputs:
- Flexibility gained by option.
- CAPEX/OPEX comparison.
- Circularity notes.
- Recommended modernization path.

## Pozu Figaredo

Site context:
- HUNOSA demonstrator.
- Former coal mine environment.
- Dense-fluid PSP with one pump and one turbine, hybridized with PV and Li-ion battery.
- Main goals: validate 1D dense-fluid plant model, validate EMS, test setpoint sequences, and assess MW-scale upscaling.

### `pozu_dense_fluid_model_validation`

Purpose: Validate the 1D dense-fluid PSP model against plant experiments.

Candidate inputs:
- Step-response test data.
- Ramp-up and ramp-down measurements.
- Pressure and accelerometer traces.
- Pump/turbine status and power output.
- Dense-fluid properties.

Dashboard outputs:
- Model error metrics.
- Step-response fit.
- Ramping capability.
- Validated parameter table.
- Model-confidence status.

### `pozu_hybrid_ems_validation`

Purpose: Validate EMS setpoint sequences for dense-fluid PSP, PV, and battery operation.

Candidate inputs:
- EMS setpoints.
- PV generation.
- Battery state of charge.
- PSP power output.
- Grid point-of-interconnection constraints.

Dashboard outputs:
- Setpoint tracking error.
- Energy split by asset.
- Battery usage and degradation estimate.
- Response time versus target.
- EMS validation status.

### `pozu_upscaling_feasibility`

Purpose: Estimate feasibility and value of scaling the dense-fluid coal-mine PSP concept to MW scale.

Candidate inputs:
- Validated model parameters.
- Mine geometry and head assumptions.
- Dense-fluid properties.
- CAPEX/OPEX assumptions.
- Market/service value assumptions.

Dashboard outputs:
- MW-scale power and energy estimate.
- LCOS estimate.
- Response-time benefit.
- Site suitability score.
- Scaling sensitivity analysis.

## Suggested Implementation Order

1. `demo_kpi_assessment`
   - Shared dashboard panel that every site can use.
   - Low dependency on site-specific physics.

2. `vilarinho_miv_fatigue`
   - Builds directly on the current AE event statistics work.
   - Uses edge-computed event features, pressure, vibration, and valve position summaries.

3. `cheylas_sediment_runner_wear`
   - Strong fit for sensor/data-fusion workflows.
   - Clear dashboard story: turbidity exposure, wear indicator, and RUL.

4. `alqueva_hybrid_ems`
   - Good operational management workflow.
   - Can start with simplified dispatch and battery state-of-charge dynamics.

5. `la_rance_corrosion_biofouling`
   - Good project-specific dashboard panel.
   - Suitable for inspection records plus water-quality time series.

6. `pozu_dense_fluid_model_validation`
   - Good model-validation workflow.
   - Can use synthetic or campaign data until real measurements are available.

7. `vsmc_cascade_dispatch`
   - High-value but broader optimization scope.
   - Best implemented after the shared workflow/result conventions are stable.

## Dashboard Mapping

Recommended visible workflow tabs per demonstrator:

- VSMC:
  - `vsmc_cascade_dispatch`
  - `vsmc_soft_start_wear`
  - `vsmc_hsc_flexibility`
  - `demo_kpi_assessment`

- Cheylas:
  - `cheylas_fast_dewatering`
  - `cheylas_sediment_runner_wear`
  - `cheylas_predictive_maintenance`
  - `demo_kpi_assessment`

- La Rance:
  - `la_rance_corrosion_biofouling`
  - `la_rance_cleaning_interval`
  - `la_rance_bess_sizing`
  - `demo_kpi_assessment`

- Alqueva:
  - `alqueva_hybrid_ems`
  - `alqueva_fast_service_controller`
  - `alqueva_runner_fatigue`
  - `demo_kpi_assessment`

- Vilarinho:
  - `vilarinho_miv_regulation`
  - `vilarinho_miv_fatigue`
  - `vilarinho_hsc_miv_comparison`
  - `demo_kpi_assessment`

- Pozu Figaredo:
  - `pozu_dense_fluid_model_validation`
  - `pozu_hybrid_ems_validation`
  - `pozu_upscaling_feasibility`
  - `demo_kpi_assessment`

## Notes for Python FMU Prototypes

- Start with CSV/JSON input files and deterministic result generation.
- Make each FMU robust to missing signals and return diagnostics rather than failing on partial demo data.
- Keep physical models replaceable: early FMUs can use calibrated empirical equations or synthetic examples, later swapped for higher-fidelity co-simulation models.
- Emit traces at dashboard-friendly sizes to avoid huge Argo result payloads.
- Keep the output schema stable so dashboard components can be reused across demonstrators.
