"""Pure-Python state-update functions for the STOR-HY FMI 3.0 placeholder FMUs.

Every model here is a deterministic placeholder used to demonstrate coupled
co-simulation in the CADS runtime. The equations are plausible in shape but
they are NOT validated physics or engineering models: parameters are
illustrative and several degradation processes are deliberately accelerated so
that a one-day simulation shows visible change.

This module is stdlib-only so it can be imported (and unit tested) without
pythonfmu3. Each model is described by a spec in ``MODEL_SPECS``:

* ``step_size``: DefaultExperiment stepSize in seconds (ARCH-COMP-002).
* ``parameters`` / ``inputs`` / ``outputs``: ``{name: (type, default, description)}``
  where type is one of ``"Float64"``, ``"Int32"``, ``"Boolean"``.
* ``init(v, start_time)``: resets internal state from parameters and inputs and
  computes outputs at ``start_time``.
* ``step(v, t, dt)``: advances internal state over ``[t, t + dt]`` and leaves the
  outputs evaluated at ``t + dt``.

``v`` is a plain dict holding parameters, inputs, outputs and internal state
(internal state keys start with ``_``). Model time is in SI seconds.
"""

import math

DAY_S = 86400.0
HOUR_S = 3600.0
M2_TIDE_PERIOD_S = 44712.0  # principal lunar semi-diurnal period (12.42 h)

PLACEHOLDER_NOTE = (
    "Deterministic placeholder for the CADS co-simulation demo; "
    "not validated physics."
)

FLOAT64 = "Float64"
INT32 = "Int32"
BOOLEAN = "Boolean"


def clamp(value, minimum, maximum):
    return max(minimum, min(maximum, float(value)))


def _hour_of_day(t):
    return (float(t) % DAY_S) / HOUR_S


# ---------------------------------------------------------------------------
# EmsDispatchFmi3: price-driven battery dispatch (Alqueva hybrid)
# ---------------------------------------------------------------------------

EMS_MODE_IDLE = 0
EMS_MODE_CHARGE = 1
EMS_MODE_DISCHARGE = 2


def ems_price(v, t):
    """Synthetic day-ahead price: sine with an afternoon peak, EUR/MWh."""
    phase = 2.0 * math.pi * (_hour_of_day(t) - 9.0) / 24.0
    return v["price_base_eur_mwh"] + v["price_amplitude_eur_mwh"] * math.sin(phase)


def _ems_dispatch(v, t):
    price = ems_price(v, t)
    soc = float(v["soc_percent"])
    rated = float(v["rated_power_mw"])
    taper = max(float(v["soc_taper_percent"]), 1e-6)
    setpoint = 0.0
    mode = EMS_MODE_IDLE
    if price <= v["charge_below_eur_mwh"] and soc < v["soc_max_percent"]:
        factor = clamp((v["soc_max_percent"] - soc) / taper, 0.0, 1.0)
        setpoint = -rated * factor
    elif price >= v["discharge_above_eur_mwh"] and soc > v["soc_min_percent"]:
        factor = clamp((soc - v["soc_min_percent"]) / taper, 0.0, 1.0)
        setpoint = rated * factor
    if setpoint < 0.0:
        mode = EMS_MODE_CHARGE
    elif setpoint > 0.0:
        mode = EMS_MODE_DISCHARGE
    v["price_eur_mwh"] = price
    v["power_setpoint_mw"] = setpoint
    v["mode"] = mode


def ems_init(v, start_time):
    v["_revenue"] = 0.0
    v["revenue_eur"] = 0.0
    _ems_dispatch(v, start_time)


def ems_step(v, t, dt):
    # Settle the interval that just ended with the set-point and price that
    # were published at its start (positive set-point = discharge = export).
    v["_revenue"] += v["power_setpoint_mw"] * v["price_eur_mwh"] * dt / HOUR_S
    v["revenue_eur"] = v["_revenue"]
    _ems_dispatch(v, t + dt)


# ---------------------------------------------------------------------------
# BatteryDegradationFmi3: energy balance + accelerated fade (Alqueva hybrid)
# ---------------------------------------------------------------------------

BATTERY_SOC_FLOOR = 5.0
BATTERY_SOC_CEILING = 98.0
BATTERY_EOL_FADE = 0.2  # end of life at 80 % state of health
BATTERY_RUL_CAP_DAYS = 7300.0


def _battery_temp_factor(cell_temp_c):
    # Doubling of fade rate per 10 degC above 25 degC (Arrhenius-like rule of thumb).
    return 2.0 ** ((float(cell_temp_c) - 25.0) / 10.0)


def _battery_outputs(v, elapsed_s, power_actual):
    capacity = v["capacity_mwh"] * (1.0 - v["_fade"])
    v["soc_percent"] = 100.0 * v["_energy"] / max(capacity, 1e-9)
    v["soh_percent"] = 100.0 * (1.0 - v["_fade"])
    v["cycle_count"] = v["_throughput"] / max(2.0 * v["capacity_mwh"], 1e-9)
    v["power_actual_mw"] = power_actual
    v["cell_temp_c"] = v["ambient_temp_c"] + v["thermal_rise_c_per_mw"] * abs(power_actual)
    accel = max(float(v["aging_acceleration"]), 1e-9)
    elapsed_days = elapsed_s / DAY_S
    if elapsed_days > 0.0 and v["_fade"] > 0.0:
        real_rate = (v["_fade"] / accel) / elapsed_days
    else:
        real_rate = v["calendar_fade_per_day"] * _battery_temp_factor(v["cell_temp_c"])
    remaining = max(0.0, BATTERY_EOL_FADE - v["_fade"])
    v["rul_days"] = clamp(remaining / max(real_rate, 1e-12), 0.0, BATTERY_RUL_CAP_DAYS)


def battery_init(v, start_time):
    v["_start"] = float(start_time)
    v["_fade"] = 0.0
    v["_throughput"] = 0.0
    soc0 = clamp(v["initial_soc_percent"], BATTERY_SOC_FLOOR, BATTERY_SOC_CEILING)
    v["_energy"] = v["capacity_mwh"] * soc0 / 100.0
    _battery_outputs(v, 0.0, 0.0)


def battery_step(v, t, dt):
    dt_h = dt / HOUR_S
    eff = math.sqrt(clamp(v["round_trip_efficiency"], 0.5, 1.0))
    capacity = v["capacity_mwh"] * (1.0 - v["_fade"])
    e_min = capacity * BATTERY_SOC_FLOOR / 100.0
    e_max = capacity * BATTERY_SOC_CEILING / 100.0
    p = clamp(v["power_setpoint_mw"], -v["max_power_mw"], v["max_power_mw"])

    # Positive power = discharge (energy leaves the battery with losses).
    if p >= 0.0:
        d_energy = -p * dt_h / eff
    else:
        d_energy = -p * dt_h * eff
    energy = v["_energy"] + d_energy
    energy = min(max(energy, min(e_min, v["_energy"])), max(e_max, v["_energy"]))
    d_energy = energy - v["_energy"]
    if d_energy <= 0.0:
        power_actual = -d_energy * eff / dt_h if dt_h > 0 else 0.0
    else:
        power_actual = -d_energy / eff / dt_h if dt_h > 0 else 0.0

    cell_temp = v["ambient_temp_c"] + v["thermal_rise_c_per_mw"] * abs(power_actual)
    fade_increment = (
        v["cycle_fade_per_mwh"] * abs(d_energy) + v["calendar_fade_per_day"] * dt / DAY_S
    ) * _battery_temp_factor(cell_temp) * v["aging_acceleration"]
    v["_fade"] = min(0.95, v["_fade"] + max(0.0, fade_increment))
    v["_throughput"] += abs(d_energy)
    # Fade shrinks the usable capacity; keep the stored energy within the new ceiling.
    v["_energy"] = min(energy, v["capacity_mwh"] * (1.0 - v["_fade"]) * BATTERY_SOC_CEILING / 100.0)
    _battery_outputs(v, t + dt - v["_start"], power_actual)


# ---------------------------------------------------------------------------
# RunnerStressFmi3: daily pump/turbine cycling and runner stress (Cheylas)
# ---------------------------------------------------------------------------

RUNNER_MODE_STANDSTILL = 0
RUNNER_MODE_GENERATING = 1
RUNNER_MODE_PUMPING = 2

# (start hour, end hour, mode) of a synthetic daily operating schedule.
RUNNER_SCHEDULE = (
    (0.0, 5.0, RUNNER_MODE_PUMPING),
    (5.0, 7.0, RUNNER_MODE_STANDSTILL),
    (7.0, 11.0, RUNNER_MODE_GENERATING),
    (11.0, 13.0, RUNNER_MODE_STANDSTILL),
    (13.0, 16.0, RUNNER_MODE_GENERATING),
    (16.0, 17.0, RUNNER_MODE_STANDSTILL),
    (17.0, 22.0, RUNNER_MODE_GENERATING),
    (22.0, 24.0, RUNNER_MODE_PUMPING),
)


def runner_mode_at(t):
    hour = _hour_of_day(t)
    for start, end, mode in RUNNER_SCHEDULE:
        if start <= hour < end:
            return mode
    return RUNNER_MODE_STANDSTILL


def _runner_evaluate(v, t, transition):
    mode = runner_mode_at(t)
    demand = clamp(v["flow_demand_pu"], 0.0, 1.2)
    if mode == RUNNER_MODE_GENERATING:
        load = clamp(demand * (0.85 + 0.15 * math.sin(2.0 * math.pi * _hour_of_day(t) / 6.0)), 0.2, 1.1)
        stress = v["stress_generating_mpa"] + v["part_load_stress_mpa"] * abs(load - v["best_efficiency_load_pu"])
    elif mode == RUNNER_MODE_PUMPING:
        load = v["pumping_load_pu"]
        stress = v["stress_pumping_mpa"]
    else:
        load = 0.0
        stress = v["stress_standstill_mpa"]
    if transition:
        stress += v["transient_stress_mpa"]
    v["load_pu"] = load
    v["operating_mode"] = mode
    v["stress_amplitude_mpa"] = stress
    v["start_stop_count"] = int(v["_start_stop_count"])


def runner_init(v, start_time):
    v["_prev_mode"] = runner_mode_at(start_time)
    v["_start_stop_count"] = 0
    v["_energy"] = 0.0
    v["energy_mwh"] = 0.0
    _runner_evaluate(v, start_time, False)


def runner_step(v, t, dt):
    # Settle the interval that just ended with the load and mode published at
    # its start; only turbine (generating) operation counts as generated energy.
    if v["operating_mode"] == RUNNER_MODE_GENERATING:
        v["_energy"] += max(0.0, float(v["load_pu"])) * v["rated_power_mw"] * dt / HOUR_S
    v["energy_mwh"] = v["_energy"]
    mode = runner_mode_at(t + dt)
    transition = mode != v["_prev_mode"]
    if transition:
        v["_start_stop_count"] += 1
    v["_prev_mode"] = mode
    _runner_evaluate(v, t + dt, transition)


# ---------------------------------------------------------------------------
# IoTIndicatorFmi3: EWMA vibration condition indicator (Cheylas)
# ---------------------------------------------------------------------------

def _iot_raw_vibration(v, t):
    load = clamp(v["load_pu"], 0.0, 1.5)
    # Deterministic "noise": incommensurate sines instead of a random generator.
    ripple = 0.12 * math.sin(t / 731.0) + 0.07 * math.sin(t / 197.0 + 1.3)
    return (
        v["baseline_vibration_mm_s"]
        + v["load_vibration_gain_mm_s"] * load
        + v["part_load_vibration_mm_s"] * abs(load - 0.75) * (1.0 if load > 0.0 else 0.0)
        + v["_wear_drift"]
        + ripple
    )


def _iot_outputs(v):
    v["vibration_rms_mm_s"] = v["_ewma"]
    span = max(v["alarm_vibration_mm_s"] - v["baseline_vibration_mm_s"], 1e-6)
    v["condition_indicator"] = clamp((v["_ewma"] - v["baseline_vibration_mm_s"]) / span, 0.0, 1.0)


def iot_init(v, start_time):
    v["_wear_drift"] = float(v["initial_wear_drift_mm_s"])
    v["_ewma"] = _iot_raw_vibration(v, start_time)
    _iot_outputs(v)


def iot_step(v, t, dt):
    load = clamp(v["load_pu"], 0.0, 1.5)
    v["_wear_drift"] += v["wear_drift_mm_s_per_h"] * load * dt / HOUR_S
    alpha = 1.0 - math.exp(-dt / max(v["ewma_time_constant_s"], 1e-6))
    v["_ewma"] += alpha * (_iot_raw_vibration(v, t + dt) - v["_ewma"])
    _iot_outputs(v)


# ---------------------------------------------------------------------------
# RulFmi3: Miner-rule fatigue damage and remaining useful life (Cheylas)
# ---------------------------------------------------------------------------

RUL_STATUS_OK = 0
RUL_STATUS_WATCH = 1
RUL_STATUS_ALARM = 2
RUL_CAP_DAYS = 20000.0


def _rul_outputs(v):
    v["damage_index"] = v["_damage"]
    v["damage_rate_per_day"] = v["_rate"]
    # Conservative planning rate: never lower than the design rate nor than the
    # worst smoothed rate seen so far, so the RUL estimate only ever falls.
    v["rul_days"] = clamp((1.0 - v["_damage"]) / max(v["_planning_rate"], 1e-12), 0.0, RUL_CAP_DAYS)
    if v["_damage"] >= 0.8 or v["rul_days"] < 90.0:
        status = RUL_STATUS_ALARM
    elif v["condition_indicator"] >= 0.5 or v["_smoothed_rate"] > v["design_damage_rate_per_day"]:
        status = RUL_STATUS_WATCH
    else:
        status = RUL_STATUS_OK
    v["status_code"] = status


def rul_damage_rate_per_day(v):
    """Instantaneous Miner damage rate per day from Basquin S-N curve."""
    stress = max(0.0, float(v["stress_amplitude_mpa"]))
    if stress <= 0.0:
        return 0.0
    per_cycle = (stress / v["sn_reference_stress_mpa"]) ** v["sn_exponent"] / v["sn_reference_cycles"]
    condition = 1.0 + v["condition_gain"] * clamp(v["condition_indicator"], 0.0, 1.0)
    return per_cycle * v["cycles_per_hour"] * 24.0 * condition


def rul_init(v, start_time):
    v["_damage"] = clamp(v["initial_damage"], 0.0, 1.0)
    v["_rate"] = rul_damage_rate_per_day(v)
    v["_smoothed_rate"] = v["_rate"]
    v["_planning_rate"] = max(v["design_damage_rate_per_day"], v["_smoothed_rate"])
    _rul_outputs(v)


def rul_step(v, t, dt):
    rate = rul_damage_rate_per_day(v)
    # demo_acceleration compresses time for the accumulated damage only; the
    # reported rate and the RUL stay at real (un-accelerated) rates.
    accel = max(float(v["demo_acceleration"]), 0.0)
    v["_damage"] = min(1.0, v["_damage"] + rate * accel * dt / DAY_S)
    v["_rate"] = rate
    alpha = 1.0 - math.exp(-dt / max(v["rate_smoothing_s"], 1e-6))
    v["_smoothed_rate"] += alpha * (rate - v["_smoothed_rate"])
    v["_planning_rate"] = max(v["_planning_rate"], v["_smoothed_rate"], v["design_damage_rate_per_day"])
    _rul_outputs(v)


# ---------------------------------------------------------------------------
# SedimentExposureFmi3: sediment exposure with cleaning accounting (La Rance
# tidal profile or Cheylas cycling/dewatering profile)
# ---------------------------------------------------------------------------

SEDIMENT_PROFILE_TIDAL = 0
SEDIMENT_PROFILE_CYCLING = 1
CYCLING_MEAN_HEAD_M = 260.0
CYCLING_HEAD_SWING_M = 6.0


def _sediment_hydraulics(v, t):
    if int(v["profile_mode"]) == SEDIMENT_PROFILE_CYCLING:
        hour = float(t) / HOUR_S
        cycles = max(float(v["cycles_per_day"]), 0.0)
        # Each pump/turbine/dewatering cycle stirs up sediment (|sin| bursts),
        # modulated by a slower daily inflow swing.
        cycling = 1.0 + 0.9 * abs(math.sin(2.0 * math.pi * hour * cycles / 48.0))
        daily = 1.0 + 0.25 * math.sin(2.0 * math.pi * hour / 24.0)
        conc = v["base_concentration_g_l"] * cycling * daily
        head = CYCLING_MEAN_HEAD_M + CYCLING_HEAD_SWING_M * math.sin(2.0 * math.pi * hour / 24.0)
        return head, conc
    phase = 2.0 * math.pi * float(t) / M2_TIDE_PERIOD_S
    head = v["mean_head_m"] + v["tidal_amplitude_m"] * math.sin(phase)
    # Concentration peaks at maximum tidal flow (maximum |d head / dt|).
    conc = v["base_concentration_g_l"] + v["peak_concentration_g_l"] * abs(math.cos(phase))
    return head, conc


def _sediment_outputs(v, t):
    head, conc = _sediment_hydraulics(v, t)
    v["head_m"] = head
    v["sediment_concentration_g_l"] = conc
    v["sediment_exposure"] = v["_exposure"]
    v["cleaning_count"] = int(v["_count"])
    v["cumulative_cleaning_cost_eur"] = v["_cost"]
    v["downtime_h"] = v["_downtime"]
    v["hours_since_cleaning"] = max(0.0, (float(t) - v["_last_clean"]) / HOUR_S)


def sediment_init(v, start_time):
    v["_exposure"] = max(0.0, float(v["initial_exposure"]))
    v["_count"] = 0
    v["_cost"] = 0.0
    v["_downtime"] = 0.0
    v["_last_clean"] = float(start_time) - v["initial_hours_since_cleaning"] * HOUR_S
    _sediment_outputs(v, start_time)


def sediment_step(v, t, dt):
    # A cleaning request is honoured at the start of the interval when the
    # minimum interval since the previous cleaning has elapsed, so a trigger
    # held over several sub-steps cleans only once.
    since_h = (float(t) - v["_last_clean"]) / HOUR_S
    if bool(v["cleaning_trigger"]) and since_h >= v["min_cleaning_interval_h"] - 1e-9:
        v["_exposure"] = max(0.0, float(v["post_cleaning_exposure"]))
        v["_count"] += 1
        v["_cost"] += v["cleaning_cost_eur"]
        v["_downtime"] += max(0.0, float(v["downtime_per_cleaning_h"]))
        v["_last_clean"] = float(t)
    _, conc = _sediment_hydraulics(v, t + 0.5 * dt)
    v["_exposure"] += v["exposure_rate_per_g_l_h"] * conc * dt / HOUR_S
    _sediment_outputs(v, t + dt)


# ---------------------------------------------------------------------------
# DegradationCostFmi3: degradation cost versus market revenue (M14)
# ---------------------------------------------------------------------------

COST_STATUS_OK = 0
COST_STATUS_WARNING = 1
COST_STATUS_ALARM = 2
COST_RECOMMEND_CONTINUE = 0
COST_RECOMMEND_REDUCE_CYCLING = 1
COST_RECOMMEND_MAINTENANCE = 2
COST_BCR_CAP = 1000.0


def _soh_fade(v):
    return clamp((100.0 - float(v["soh_percent"])) / 100.0, 0.0, 1.0)


def _cost_outputs(v):
    v["degradation_cost_eur"] = v["_cost"]
    v["degradation_cost_rate_eur_per_day"] = v["_cost_rate"]
    gross = float(v["revenue_eur"]) + float(v["energy_mwh"]) * v["price_eur_mwh"]
    v["gross_revenue_eur"] = gross
    v["net_benefit_eur"] = gross - v["_cost"]
    if v["_cost"] > 0.0:
        ratio = min(gross / v["_cost"], COST_BCR_CAP)
    else:
        ratio = COST_BCR_CAP if gross > 0.0 else 0.0
    v["benefit_cost_ratio"] = ratio
    rul = float(v["rul_days"])
    if rul < v["rul_alarm_days"]:
        status = COST_STATUS_ALARM
    elif rul < v["rul_warning_days"]:
        status = COST_STATUS_WARNING
    else:
        status = COST_STATUS_OK
    v["status_code"] = status
    if status == COST_STATUS_ALARM:
        recommendation = COST_RECOMMEND_MAINTENANCE
    elif v["_cost"] > 0.0 and ratio < 1.0:
        recommendation = COST_RECOMMEND_REDUCE_CYCLING
    else:
        recommendation = COST_RECOMMEND_CONTINUE
    v["recommendation_code"] = recommendation


def cost_init(v, start_time):
    v["_cost"] = 0.0
    v["_cost_rate"] = 0.0
    # Capacity fade is charged relative to a new asset (100 % SoH), so a fade
    # already present in the input is charged in the first step.
    v["_charged_fade"] = 0.0
    _cost_outputs(v)


def cost_step(v, t, dt):
    damage = max(0.0, float(v["damage_rate_per_day"])) * dt / DAY_S
    fade = _soh_fade(v)
    damage += max(0.0, fade - v["_charged_fade"])
    v["_charged_fade"] = max(v["_charged_fade"], fade)
    increment = v["asset_value_eur"] * damage
    v["_cost"] += increment
    v["_cost_rate"] = increment * DAY_S / dt if dt > 0.0 else 0.0
    _cost_outputs(v)


# ---------------------------------------------------------------------------
# Specs
# ---------------------------------------------------------------------------

MODEL_SPECS = {
    "EmsDispatchFmi3": {
        "step_size": 900.0,
        "description": "Alqueva hybrid EMS: price-driven battery set-point with SoC taper. " + PLACEHOLDER_NOTE,
        "parameters": {
            "rated_power_mw": (FLOAT64, 10.0, "Battery power the EMS may dispatch [MW]"),
            "price_base_eur_mwh": (FLOAT64, 60.0, "Mean synthetic day-ahead price [EUR/MWh]"),
            "price_amplitude_eur_mwh": (FLOAT64, 35.0, "Daily price swing amplitude [EUR/MWh]"),
            "charge_below_eur_mwh": (FLOAT64, 50.0, "Charge when price is at or below [EUR/MWh]"),
            "discharge_above_eur_mwh": (FLOAT64, 75.0, "Discharge when price is at or above [EUR/MWh]"),
            "soc_min_percent": (FLOAT64, 20.0, "No discharge at or below this SoC [%]"),
            "soc_max_percent": (FLOAT64, 90.0, "No charge at or above this SoC [%]"),
            "soc_taper_percent": (FLOAT64, 10.0, "SoC band over which power tapers to zero [%]"),
        },
        "inputs": {
            "soc_percent": (FLOAT64, 50.0, "Battery state of charge from the battery model [%]"),
        },
        "outputs": {
            "power_setpoint_mw": (FLOAT64, 0.0, "Battery set-point, positive = discharge [MW]"),
            "price_eur_mwh": (FLOAT64, 0.0, "Synthetic price at the current time [EUR/MWh]"),
            "mode": (INT32, 0, "0 idle, 1 charge, 2 discharge"),
            "revenue_eur": (FLOAT64, 0.0, "Cumulative arbitrage revenue [EUR]"),
        },
        "init": ems_init,
        "step": ems_step,
    },
    "BatteryDegradationFmi3": {
        "step_size": 300.0,
        "description": "Alqueva BESS: energy balance with temperature-scaled, accelerated fade. " + PLACEHOLDER_NOTE,
        "parameters": {
            "capacity_mwh": (FLOAT64, 40.0, "Nominal energy capacity [MWh]"),
            "max_power_mw": (FLOAT64, 10.0, "Power limit [MW]"),
            "initial_soc_percent": (FLOAT64, 50.0, "State of charge at start [%]"),
            "round_trip_efficiency": (FLOAT64, 0.9, "Round-trip efficiency [-]"),
            "thermal_rise_c_per_mw": (FLOAT64, 0.8, "Cell temperature rise per MW of power [degC/MW]"),
            "cycle_fade_per_mwh": (FLOAT64, 5e-7, "Capacity fade per MWh of throughput at 25 degC [-]"),
            "calendar_fade_per_day": (FLOAT64, 2e-5, "Calendar capacity fade per day at 25 degC [-]"),
            "aging_acceleration": (
                FLOAT64,
                100.0,
                "Demo time-compression factor applied to fade so one simulated day shows visible "
                "ageing; RUL is reported at real (un-accelerated) rates [-]",
            ),
        },
        "inputs": {
            "power_setpoint_mw": (FLOAT64, 0.0, "Requested power, positive = discharge [MW]"),
            "ambient_temp_c": (FLOAT64, 20.0, "Ambient temperature [degC]"),
        },
        "outputs": {
            "soc_percent": (FLOAT64, 0.0, "State of charge of the faded capacity [%]"),
            "soh_percent": (FLOAT64, 0.0, "State of health (remaining capacity) [%]"),
            "rul_days": (FLOAT64, 0.0, "Days until 80 % SoH at the observed real fade rate [d]"),
            "cycle_count": (FLOAT64, 0.0, "Equivalent full cycles [-]"),
            "power_actual_mw": (FLOAT64, 0.0, "Delivered power after SoC limits [MW]"),
            "cell_temp_c": (FLOAT64, 0.0, "Cell temperature [degC]"),
        },
        "init": battery_init,
        "step": battery_step,
    },
    "RunnerStressFmi3": {
        "step_size": 900.0,
        "description": "Cheylas runner: daily pump/turbine cycling, load and stress amplitude. " + PLACEHOLDER_NOTE,
        "parameters": {
            "pumping_load_pu": (FLOAT64, 0.9, "Load while pumping (fixed speed) [pu]"),
            "best_efficiency_load_pu": (FLOAT64, 0.85, "Best-efficiency load in turbine mode [pu]"),
            "stress_generating_mpa": (FLOAT64, 45.0, "Stress amplitude at best efficiency [MPa]"),
            "part_load_stress_mpa": (FLOAT64, 60.0, "Extra stress per pu away from best efficiency [MPa]"),
            "stress_pumping_mpa": (FLOAT64, 55.0, "Stress amplitude while pumping [MPa]"),
            "stress_standstill_mpa": (FLOAT64, 5.0, "Residual stress amplitude at standstill [MPa]"),
            "transient_stress_mpa": (FLOAT64, 35.0, "Extra stress in an interval with a mode change [MPa]"),
            "rated_power_mw": (FLOAT64, 240.0, "Rated turbine power, scales generated energy [MW]"),
        },
        "inputs": {
            "flow_demand_pu": (FLOAT64, 0.7, "Turbine flow demand from dispatch [pu]"),
        },
        "outputs": {
            "load_pu": (FLOAT64, 0.0, "Unit load [pu]"),
            "operating_mode": (INT32, 0, "0 standstill, 1 generating, 2 pumping"),
            "stress_amplitude_mpa": (FLOAT64, 0.0, "Runner blade stress amplitude [MPa]"),
            "start_stop_count": (INT32, 0, "Mode changes since start [-]"),
            "energy_mwh": (FLOAT64, 0.0, "Cumulative energy generated in turbine mode [MWh]"),
        },
        "init": runner_init,
        "step": runner_step,
    },
    "IoTIndicatorFmi3": {
        "step_size": 300.0,
        "description": "Cheylas IoT: EWMA vibration RMS and normalised condition indicator. " + PLACEHOLDER_NOTE,
        "parameters": {
            "baseline_vibration_mm_s": (FLOAT64, 1.4, "Vibration at standstill, healthy [mm/s]"),
            "load_vibration_gain_mm_s": (FLOAT64, 1.6, "Vibration added per pu load [mm/s]"),
            "part_load_vibration_mm_s": (FLOAT64, 2.5, "Vibration per pu away from 0.75 pu load [mm/s]"),
            "alarm_vibration_mm_s": (FLOAT64, 7.1, "Vibration mapped to indicator 1 [mm/s]"),
            "ewma_time_constant_s": (FLOAT64, 1800.0, "EWMA filter time constant [s]"),
            "initial_wear_drift_mm_s": (FLOAT64, 0.4, "Wear-related vibration offset at start [mm/s]"),
            "wear_drift_mm_s_per_h": (FLOAT64, 0.01, "Wear drift per running hour at 1 pu [mm/s/h]"),
        },
        "inputs": {
            "load_pu": (FLOAT64, 0.0, "Unit load from the runner model [pu]"),
        },
        "outputs": {
            "vibration_rms_mm_s": (FLOAT64, 0.0, "Filtered vibration RMS [mm/s]"),
            "condition_indicator": (FLOAT64, 0.0, "0 healthy .. 1 alarm [-]"),
        },
        "init": iot_init,
        "step": iot_step,
    },
    "RulFmi3": {
        "step_size": 900.0,
        "description": "Cheylas RUL: Miner-rule fatigue damage from stress and condition. " + PLACEHOLDER_NOTE,
        "parameters": {
            "initial_damage": (FLOAT64, 0.35, "Accumulated damage at start [-]"),
            "sn_reference_stress_mpa": (FLOAT64, 80.0, "S-N curve reference stress [MPa]"),
            "sn_reference_cycles": (FLOAT64, 1e7, "Cycles to failure at the reference stress [-]"),
            "sn_exponent": (FLOAT64, 6.0, "Basquin exponent [-]"),
            "cycles_per_hour": (FLOAT64, 3600.0, "Equivalent load cycles per hour [1/h]"),
            "condition_gain": (FLOAT64, 1.5, "Damage multiplier per unit condition indicator [-]"),
            "design_damage_rate_per_day": (FLOAT64, 0.0015, "Design damage rate, RUL floor [1/d]"),
            "rate_smoothing_s": (FLOAT64, 21600.0, "Smoothing time constant for the planning rate [s]"),
            "demo_acceleration": (
                FLOAT64,
                1.0,
                "Demo time-compression factor applied to accumulated damage only; the damage rate and "
                "RUL are reported at real (un-accelerated) rates [-]",
            ),
        },
        "inputs": {
            "stress_amplitude_mpa": (FLOAT64, 0.0, "Stress amplitude from the runner model [MPa]"),
            "condition_indicator": (FLOAT64, 0.0, "Condition indicator from the IoT model [-]"),
        },
        "outputs": {
            "damage_index": (FLOAT64, 0.0, "Accumulated Miner damage [-]"),
            "rul_days": (FLOAT64, 0.0, "Remaining useful life at the planning rate [d]"),
            "damage_rate_per_day": (FLOAT64, 0.0, "Instantaneous damage rate [1/d]"),
            "status_code": (INT32, 0, "0 ok, 1 watch, 2 alarm"),
        },
        "init": rul_init,
        "step": rul_step,
    },
    "SedimentExposureFmi3": {
        "step_size": 300.0,
        "description": "Sediment exposure (La Rance tidal or Cheylas cycling profile) with cleaning accounting: "
        "a cleaning trigger resets exposure and adds cost and downtime. " + PLACEHOLDER_NOTE,
        "parameters": {
            "profile_mode": (INT32, 0, "0 tidal (La Rance), 1 pump/turbine cycling and dewatering (Cheylas)"),
            "cycles_per_day": (FLOAT64, 8.0, "Operating cycles per day in cycling mode [1/d]"),
            "mean_head_m": (FLOAT64, 5.0, "Mean head across the barrage, tidal mode [m]"),
            "tidal_amplitude_m": (FLOAT64, 4.0, "M2 tidal head amplitude, tidal mode [m]"),
            "base_concentration_g_l": (FLOAT64, 0.4, "Base suspended sediment concentration [g/L]"),
            "peak_concentration_g_l": (FLOAT64, 1.2, "Extra concentration at peak tidal flow, tidal mode [g/L]"),
            "exposure_rate_per_g_l_h": (FLOAT64, 0.1, "Exposure gained per g/L per hour [1/(g/L h)]"),
            "initial_exposure": (FLOAT64, 0.3, "Exposure at start [-]"),
            "post_cleaning_exposure": (FLOAT64, 0.05, "Exposure left right after a cleaning [-]"),
            "cleaning_cost_eur": (FLOAT64, 18000.0, "Cost of one cleaning [EUR]"),
            "min_cleaning_interval_h": (FLOAT64, 4.0, "Minimum time between cleanings [h]"),
            "downtime_per_cleaning_h": (FLOAT64, 2.0, "Unit downtime per cleaning [h]"),
            "initial_hours_since_cleaning": (FLOAT64, 24.0, "Hours since the last cleaning at start [h]"),
        },
        "inputs": {
            "cleaning_trigger": (BOOLEAN, False, "Cleaning request, e.g. pulsed by a master event [-]"),
        },
        "outputs": {
            "head_m": (FLOAT64, 0.0, "Hydraulic head [m]"),
            "sediment_concentration_g_l": (FLOAT64, 0.0, "Suspended sediment concentration [g/L]"),
            "sediment_exposure": (FLOAT64, 0.0, "Cumulative exposure since the last cleaning [-]"),
            "cleaning_count": (INT32, 0, "Cleanings performed since start [-]"),
            "cumulative_cleaning_cost_eur": (FLOAT64, 0.0, "Cumulative cleaning cost [EUR]"),
            "downtime_h": (FLOAT64, 0.0, "Cumulative cleaning downtime [h]"),
            "hours_since_cleaning": (FLOAT64, 0.0, "Hours since the last cleaning [h]"),
        },
        "init": sediment_init,
        "step": sediment_step,
    },
    "DegradationCostFmi3": {
        "step_size": 900.0,
        "description": "Degradation cost versus market revenue: values consumed asset life against gross "
        "revenue and flags RUL thresholds. " + PLACEHOLDER_NOTE,
        "parameters": {
            "asset_value_eur": (FLOAT64, 2.0e6, "Replacement value of the degrading asset [EUR]"),
            "price_eur_mwh": (FLOAT64, 60.0, "Price used to value energy_mwh [EUR/MWh]"),
            "planning_horizon_days": (FLOAT64, 365.0, "Planning horizon for the decision (informative) [d]"),
            "rul_warning_days": (FLOAT64, 365.0, "Warning when RUL falls below [d]"),
            "rul_alarm_days": (FLOAT64, 90.0, "Alarm when RUL falls below [d]"),
        },
        "inputs": {
            "rul_days": (FLOAT64, 1000.0, "Remaining useful life from a health model [d]"),
            "damage_rate_per_day": (FLOAT64, 0.0, "Fatigue damage rate, fraction of life per day [1/d]"),
            "soh_percent": (FLOAT64, 100.0, "State of health; fade below 100 % is charged as consumed life [%]"),
            "revenue_eur": (FLOAT64, 0.0, "Cumulative market revenue supplied directly, e.g. by the EMS [EUR]"),
            "energy_mwh": (FLOAT64, 0.0, "Cumulative generated energy, valued at price_eur_mwh [MWh]"),
        },
        "outputs": {
            "degradation_cost_eur": (FLOAT64, 0.0, "Cumulative cost of consumed asset life [EUR]"),
            "degradation_cost_rate_eur_per_day": (FLOAT64, 0.0, "Degradation cost rate over the last step [EUR/d]"),
            "gross_revenue_eur": (FLOAT64, 0.0, "revenue_eur + energy_mwh * price_eur_mwh [EUR]"),
            "net_benefit_eur": (FLOAT64, 0.0, "gross_revenue_eur - degradation_cost_eur [EUR]"),
            "benefit_cost_ratio": (FLOAT64, 0.0, "gross_revenue_eur / degradation_cost_eur, capped at 1000 [-]"),
            "status_code": (INT32, 0, "0 ok, 1 warning (RUL < warning), 2 alarm (RUL < alarm)"),
            "recommendation_code": (INT32, 0, "0 continue, 1 reduce cycling, 2 schedule maintenance"),
        },
        "init": cost_init,
        "step": cost_step,
    },
}


def new_values(model_name, overrides=None):
    """Return a fresh value dict with parameter/input/output defaults."""
    spec = MODEL_SPECS[model_name]
    values = {}
    for group in ("parameters", "inputs", "outputs"):
        for name, (_, default, _) in spec[group].items():
            values[name] = default
    if overrides:
        for name, value in overrides.items():
            if name not in spec["parameters"] and name not in spec["inputs"]:
                raise KeyError(f"{model_name} has no parameter or input named {name!r}")
            values[name] = value
    return values


def coerce(type_name, value):
    if type_name == INT32:
        return int(round(float(value)))
    if type_name == BOOLEAN:
        return bool(value)
    return float(value)


class ModelRunner:
    """Minimal stateful wrapper mirroring the FMU life cycle (used by tests).

    ``set`` coerces like the CADS runtime (Int32 rounded, Boolean = value != 0)
    and ``advance`` sub-steps with the model's own step size up to ``t_end``,
    clipping the last sub-step, as the co-simulation master does.
    """

    def __init__(self, model_name, overrides=None, start_time=0.0):
        self.name = model_name
        self.spec = MODEL_SPECS[model_name]
        self.values = new_values(model_name, overrides)
        self.time = float(start_time)
        self.do_step_calls = 0
        self.spec["init"](self.values, self.time)

    def set(self, name, value):
        spec = self.spec
        if name in spec["inputs"]:
            type_name = spec["inputs"][name][0]
        elif name in spec["parameters"]:
            type_name = spec["parameters"][name][0]
        else:
            raise KeyError(f"{self.name} has no input named {name!r}")
        self.values[name] = coerce(type_name, value)

    def get(self, name):
        return self.values[name]

    def advance(self, t_end):
        h = self.spec["step_size"]
        while self.time < t_end - 1e-9:
            dt = min(h, t_end - self.time)
            self.spec["step"](self.values, self.time, dt)
            self.time += dt
            self.do_step_calls += 1
