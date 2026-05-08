import math

try:
    from pythonfmu import Fmi2Causality, Fmi2Variability
    from pythonfmu.fmi2slave import Fmi2Slave
    from pythonfmu.variables import Integer, Real

    PYTHONFMU_AVAILABLE = True
except ModuleNotFoundError:
    PYTHONFMU_AVAILABLE = False


PARAMETER_DEFAULTS = {
    "site_id": 0,
    "scenario_id": 1,
    "profile_id": 1,
    "input_score": 72.0,
    "input_confidence": 0.78,
    "input_risk_index": 0.28,
    "input_damage_index": 0.2,
    "input_value_delta_eur": 12000.0,
    "input_opex_delta_eur": -3500.0,
    "input_co2_delta_tonnes": 12.0,
    "input_rul_days": 900.0,
    "input_availability_delta_percent": 2.5,
    "input_flexibility_delta_percent": 4.0,
    "input_sediment_exposure": 0.22,
    "input_corrosion_index": 0.18,
    "input_biofouling_index": 0.24,
    "input_valve_opening_percent": 42.0,
    "input_soc_percent": 58.0,
}

OUTPUT_DEFAULTS = {
    "model_code": 0,
    "score": 0.0,
    "confidence": 0.0,
    "risk_index": 0.0,
    "status_code": 0,
    "recommendation_code": 0,
    "kpi_score": 0.0,
    "value_delta_eur": 0.0,
    "opex_delta_eur": 0.0,
    "co2_delta_tonnes": 0.0,
    "rul_days": 0.0,
    "availability_delta_percent": 0.0,
    "flexibility_delta_percent": 0.0,
    "power_mw": 0.0,
    "reservoir_level_m": 0.0,
    "soc_percent": 0.0,
    "damage_index": 0.0,
    "sediment_exposure": 0.0,
    "corrosion_index": 0.0,
    "biofouling_index": 0.0,
    "valve_opening_percent": 0.0,
    "model_error_percent": 0.0,
}

INTEGER_PARAMETERS = {"site_id", "scenario_id", "profile_id"}
INTEGER_OUTPUTS = {"model_code", "status_code", "recommendation_code"}

MODEL_CODES = {
    "hydro_cascade_dispatch": 101,
    "start_sequence_wear": 102,
    "hsc_flexibility": 103,
    "condition_monitoring": 201,
    "runner_sediment_wear": 202,
    "predictive_maintenance": 203,
    "corrosion_biofouling": 301,
    "cleaning_interval": 302,
    "bess_sizing": 401,
    "hybrid_ems": 402,
    "fast_service_controller": 403,
    "miv_regulation": 501,
    "miv_fatigue": 502,
    "kpi_assessment": 901,
    "sustainability_cba": 902,
}


def clamp(value, minimum, maximum):
    return max(minimum, min(maximum, float(value)))


def wave(time, profile_id, scale=1.0, period=24.0):
    return scale * math.sin((float(time) / max(period, 1e-6)) * 2.0 * math.pi + profile_id * 0.43)


def status_from_risk(risk):
    if risk >= 0.72:
        return 2
    if risk >= 0.42:
        return 1
    return 0


def recommendation_from_outputs(model_key, risk, score):
    if risk >= 0.72:
        return 1
    if model_key in {"hydro_cascade_dispatch", "hybrid_ems", "hsc_flexibility"} and score < 72.0:
        return 2
    if model_key in {"start_sequence_wear", "runner_sediment_wear", "miv_fatigue"} and risk >= 0.42:
        return 3
    if model_key in {"corrosion_biofouling", "cleaning_interval"} and risk >= 0.42:
        return 4
    if model_key == "sustainability_cba" and score < 60.0:
        return 5
    return 0


def base_inputs(params):
    site = int(params.get("site_id", 0))
    scenario = int(params.get("scenario_id", 1))
    profile = int(params.get("profile_id", 1))
    stress = clamp(0.16 + scenario * 0.055 + profile * 0.018 + site * 0.008, 0.0, 0.85)
    confidence = clamp(params.get("input_confidence", 0.78), 0.2, 0.99)
    return site, scenario, profile, stress, confidence


def with_common(model_key, outputs, risk, score, confidence):
    result = dict(OUTPUT_DEFAULTS)
    result.update(outputs)
    result["model_code"] = MODEL_CODES.get(model_key, 0)
    result["risk_index"] = clamp(risk, 0.0, 1.0)
    result["score"] = clamp(score, 0.0, 100.0)
    result["confidence"] = clamp(confidence, 0.0, 1.0)
    result["status_code"] = status_from_risk(result["risk_index"])
    result["recommendation_code"] = recommendation_from_outputs(model_key, result["risk_index"], result["score"])
    if result["kpi_score"] == 0.0:
        result["kpi_score"] = clamp(100.0 - result["risk_index"] * 55.0 + result["flexibility_delta_percent"], 0.0, 100.0)
    return result


def compute_model_outputs(model_key, params, current_time):
    site, scenario, profile, stress, confidence = base_inputs(params)
    t = float(current_time)
    osc = wave(t, profile, 1.0)

    input_risk = clamp(params.get("input_risk_index", 0.28), 0.0, 1.0)
    input_damage = clamp(params.get("input_damage_index", 0.2), 0.0, 1.0)
    input_score = clamp(params.get("input_score", 72.0), 0.0, 100.0)
    input_rul = max(0.0, float(params.get("input_rul_days", 900.0)))
    input_value = float(params.get("input_value_delta_eur", 12000.0))
    input_opex = float(params.get("input_opex_delta_eur", -3500.0))
    input_co2 = float(params.get("input_co2_delta_tonnes", 12.0))
    input_flex = float(params.get("input_flexibility_delta_percent", 4.0))
    input_availability = float(params.get("input_availability_delta_percent", 2.5))

    if model_key == "hydro_cascade_dispatch":
        flex = 4.5 + scenario * 1.9 + profile * 0.8
        risk = clamp(stress * 0.55 + max(0.0, 0.35 - flex / 30.0), 0.0, 1.0)
        outputs = {
            "power_mw": 185.0 + 22.0 * osc + scenario * 11.0,
            "reservoir_level_m": 421.5 + profile * 0.9 + wave(t, profile, 1.7, 72.0),
            "flexibility_delta_percent": flex,
            "availability_delta_percent": 1.2 + flex * 0.18,
            "value_delta_eur": 26000.0 + flex * 4200.0,
            "opex_delta_eur": -4500.0 - flex * 720.0,
            "co2_delta_tonnes": 6500.0 + flex * 900.0,
        }
        return with_common(model_key, outputs, risk, 78.0 + flex * 0.9 - risk * 18.0, confidence)

    if model_key == "start_sequence_wear":
        damage = clamp(0.12 + stress * 0.52 + max(0.0, scenario - 2) * 0.035 + 0.04 * abs(osc), 0.0, 1.0)
        outputs = {
            "damage_index": damage,
            "risk_index": damage,
            "rul_days": max(120.0, 1450.0 * (1.0 - damage)),
            "availability_delta_percent": 2.0 + (1.0 - damage) * 2.5,
            "flexibility_delta_percent": 3.0 + scenario * 0.9,
            "value_delta_eur": input_value + 9000.0 + scenario * 1200.0,
            "opex_delta_eur": input_opex - (1.0 - damage) * 5200.0,
        }
        return with_common(model_key, outputs, damage, 91.0 - damage * 52.0, confidence * 0.96)

    if model_key == "hsc_flexibility":
        flex = clamp(7.0 + scenario * 2.3 + 1.2 * osc, 0.0, 24.0)
        risk = clamp(0.18 + stress * 0.34 + max(0.0, flex - 15.0) * 0.018, 0.0, 1.0)
        outputs = {
            "power_mw": 70.0 + flex * 4.5,
            "flexibility_delta_percent": flex,
            "availability_delta_percent": input_availability + flex * 0.12,
            "value_delta_eur": input_value + flex * 5100.0,
            "opex_delta_eur": input_opex - flex * 450.0,
            "co2_delta_tonnes": input_co2 + flex * 820.0,
        }
        return with_common(model_key, outputs, risk, 72.0 + flex - risk * 18.0, confidence)

    if model_key == "condition_monitoring":
        sediment = clamp(params.get("input_sediment_exposure", 0.22) + 0.07 * scenario + 0.04 * max(0.0, osc), 0.0, 1.0)
        corrosion = clamp(params.get("input_corrosion_index", 0.18) + 0.03 * site + 0.02 * abs(osc), 0.0, 1.0)
        damage = clamp(input_damage * 0.45 + sediment * 0.22 + stress * 0.32, 0.0, 1.0)
        outputs = {
            "damage_index": damage,
            "sediment_exposure": sediment,
            "corrosion_index": corrosion,
            "biofouling_index": clamp(params.get("input_biofouling_index", 0.24) + 0.03 * abs(osc), 0.0, 1.0),
            "rul_days": max(90.0, 1800.0 * (1.0 - damage)),
            "availability_delta_percent": input_availability,
        }
        return with_common(model_key, outputs, damage, 95.0 - damage * 60.0, confidence)

    if model_key == "runner_sediment_wear":
        sediment = clamp(params.get("input_sediment_exposure", input_risk) + 0.05 * scenario + 0.04 * max(0.0, osc), 0.0, 1.0)
        damage = clamp(input_damage * 0.35 + sediment * 0.58 + stress * 0.18, 0.0, 1.0)
        outputs = {
            "sediment_exposure": sediment,
            "damage_index": damage,
            "rul_days": max(80.0, input_rul * (1.0 - damage * 0.42)),
            "opex_delta_eur": input_opex - max(0.0, 0.55 - damage) * 3600.0,
        }
        return with_common(model_key, outputs, damage, 88.0 - damage * 58.0, confidence * 0.93)

    if model_key == "predictive_maintenance":
        risk = clamp(input_risk * 0.5 + input_damage * 0.32 + max(0.0, 600.0 - input_rul) / 2200.0, 0.0, 1.0)
        outputs = {
            "damage_index": clamp(input_damage, 0.0, 1.0),
            "rul_days": max(60.0, min(input_rul, 1600.0) * (1.0 - risk * 0.22)),
            "availability_delta_percent": input_availability + max(0.0, 0.65 - risk) * 2.8,
            "opex_delta_eur": input_opex - max(0.0, 0.65 - risk) * 4500.0,
        }
        return with_common(model_key, outputs, risk, input_score * 0.45 + (1.0 - risk) * 55.0, confidence)

    if model_key == "corrosion_biofouling":
        corrosion = clamp(0.19 + scenario * 0.055 + 0.05 * abs(osc), 0.0, 1.0)
        fouling = clamp(0.26 + profile * 0.045 + 0.06 * max(0.0, osc), 0.0, 1.0)
        risk = clamp(corrosion * 0.52 + fouling * 0.4, 0.0, 1.0)
        outputs = {
            "corrosion_index": corrosion,
            "biofouling_index": fouling,
            "damage_index": risk,
            "rul_days": 2100.0 * (1.0 - risk * 0.55),
            "opex_delta_eur": -3500.0 - max(0.0, 0.55 - risk) * 6000.0,
        }
        return with_common(model_key, outputs, risk, 92.0 - risk * 50.0, confidence * 0.88)

    if model_key == "cleaning_interval":
        fouling = clamp(params.get("input_biofouling_index", input_risk), 0.0, 1.0)
        corrosion = clamp(params.get("input_corrosion_index", input_risk * 0.8), 0.0, 1.0)
        risk = clamp(fouling * 0.66 + corrosion * 0.18, 0.0, 1.0)
        interval_days = max(45.0, 730.0 * (1.0 - risk * 0.72))
        outputs = {
            "biofouling_index": fouling,
            "corrosion_index": corrosion,
            "rul_days": interval_days,
            "availability_delta_percent": max(0.4, 4.0 * (1.0 - risk)),
            "value_delta_eur": input_value + (730.0 - interval_days) * 38.0,
        }
        return with_common(model_key, outputs, risk, 86.0 - risk * 44.0, confidence)

    if model_key == "bess_sizing":
        soc = clamp(54.0 + 18.0 * wave(t, profile, 1.0, 12.0), 12.0, 96.0)
        flex = 5.0 + scenario * 1.8
        degradation = clamp(0.18 + abs(soc - 55.0) / 180.0 + scenario * 0.025, 0.0, 1.0)
        outputs = {
            "soc_percent": soc,
            "flexibility_delta_percent": flex,
            "availability_delta_percent": input_availability + flex * 0.2,
            "value_delta_eur": input_value + flex * 6200.0,
            "damage_index": degradation,
            "rul_days": 1600.0 * (1.0 - degradation * 0.55),
        }
        return with_common(model_key, outputs, degradation, 74.0 + flex * 1.3 - degradation * 22.0, confidence)

    if model_key == "hybrid_ems":
        soc = clamp(params.get("input_soc_percent", 58.0) + 15.0 * wave(t, profile, 1.0, 24.0), 8.0, 98.0)
        flex = input_flex + 6.5 + scenario
        risk = clamp(input_risk * 0.34 + abs(soc - 55.0) / 220.0 + stress * 0.2, 0.0, 1.0)
        outputs = {
            "soc_percent": soc,
            "power_mw": 160.0 + 35.0 * osc + scenario * 8.0,
            "flexibility_delta_percent": flex,
            "availability_delta_percent": input_availability + flex * 0.25,
            "value_delta_eur": input_value + flex * 7000.0,
            "opex_delta_eur": input_opex - max(0.0, 0.75 - risk) * 3800.0,
        }
        return with_common(model_key, outputs, risk, 70.0 + flex * 1.5 - risk * 18.0, confidence)

    if model_key == "fast_service_controller":
        flex = 8.0 + scenario * 1.7 + max(0.0, osc) * 1.5
        damage = clamp(input_damage * 0.35 + stress * 0.38 + flex * 0.012, 0.0, 1.0)
        outputs = {
            "power_mw": 210.0 + flex * 5.0,
            "damage_index": damage,
            "flexibility_delta_percent": flex,
            "availability_delta_percent": input_availability + max(0.0, 1.0 - damage) * 2.0,
            "value_delta_eur": input_value + flex * 6500.0,
            "rul_days": max(100.0, input_rul * (1.0 - damage * 0.28)),
        }
        return with_common(model_key, outputs, damage, 82.0 + flex * 0.8 - damage * 35.0, confidence)

    if model_key == "miv_regulation":
        valve = clamp(params.get("input_valve_opening_percent", 42.0) + 12.0 * wave(t, profile, 1.0, 8.0), 12.0, 88.0)
        risk = clamp(0.12 + abs(valve - 50.0) / 140.0 + stress * 0.32, 0.0, 1.0)
        flex = 4.0 + valve / 8.0
        outputs = {
            "valve_opening_percent": valve,
            "power_mw": 52.0 + valve * 0.74,
            "flexibility_delta_percent": flex,
            "availability_delta_percent": input_availability + 1.2,
            "damage_index": risk,
        }
        return with_common(model_key, outputs, risk, 86.0 - risk * 34.0 + flex * 0.7, confidence * 0.92)

    if model_key == "miv_fatigue":
        valve = clamp(params.get("input_valve_opening_percent", 42.0), 0.0, 100.0)
        fatigue = clamp(input_damage * 0.42 + abs(valve - 50.0) / 115.0 + input_risk * 0.3, 0.0, 1.0)
        outputs = {
            "valve_opening_percent": valve,
            "damage_index": fatigue,
            "rul_days": max(75.0, input_rul * (1.0 - fatigue * 0.5)),
            "opex_delta_eur": input_opex - max(0.0, 0.58 - fatigue) * 3900.0,
        }
        return with_common(model_key, outputs, fatigue, 90.0 - fatigue * 58.0, confidence)

    if model_key == "kpi_assessment":
        score = clamp(input_score * 0.48 + (1.0 - input_risk) * 36.0 + max(0.0, input_flex) * 1.1 + max(0.0, input_availability) * 1.2, 0.0, 100.0)
        risk = clamp(input_risk * 0.75 + max(0.0, 65.0 - score) / 180.0, 0.0, 1.0)
        outputs = {
            "kpi_score": score,
            "availability_delta_percent": input_availability,
            "flexibility_delta_percent": input_flex,
            "value_delta_eur": input_value,
            "opex_delta_eur": input_opex,
            "co2_delta_tonnes": input_co2,
            "rul_days": input_rul,
        }
        return with_common(model_key, outputs, risk, score, confidence)

    if model_key == "sustainability_cba":
        net_value = input_value - max(0.0, -input_opex)
        score = clamp(55.0 + net_value / 18000.0 + input_co2 / 850.0 + input_availability * 1.2 - input_risk * 18.0, 0.0, 100.0)
        risk = clamp(input_risk * 0.62 + max(0.0, 52.0 - score) / 150.0, 0.0, 1.0)
        outputs = {
            "value_delta_eur": net_value,
            "opex_delta_eur": input_opex,
            "co2_delta_tonnes": input_co2,
            "availability_delta_percent": input_availability,
            "flexibility_delta_percent": input_flex,
            "kpi_score": score,
        }
        return with_common(model_key, outputs, risk, score, confidence * 0.9)

    return with_common(model_key, {}, input_risk, input_score, confidence)


if PYTHONFMU_AVAILABLE:

    class ReplicaBase(Fmi2Slave):
        MODEL_KEY = "generic"

        def __init__(self, **kwargs):
            super().__init__(**kwargs)

            for name, default in PARAMETER_DEFAULTS.items():
                setattr(self, name, default)
                if name in INTEGER_PARAMETERS:
                    variable = Integer(
                        name,
                        causality=Fmi2Causality.parameter,
                        variability=Fmi2Variability.tunable,
                        start=int(default),
                    )
                else:
                    variable = Real(
                        name,
                        causality=Fmi2Causality.parameter,
                        variability=Fmi2Variability.tunable,
                        start=float(default),
                    )
                self.register_variable(variable)

            for name, default in OUTPUT_DEFAULTS.items():
                setattr(self, name, default)
                if name in INTEGER_OUTPUTS:
                    variable = Integer(
                        name,
                        causality=Fmi2Causality.output,
                        variability=Fmi2Variability.discrete,
                    )
                else:
                    variable = Real(
                        name,
                        causality=Fmi2Causality.output,
                        variability=Fmi2Variability.continuous,
                    )
                self.register_variable(variable)

        def enter_initialization_mode(self):
            self._update_outputs(0.0)

        def do_step(self, current_time, step_size):
            self._update_outputs(current_time)
            return True

        def _parameter_values(self):
            return {name: getattr(self, name) for name in PARAMETER_DEFAULTS}

        def _update_outputs(self, current_time):
            outputs = compute_model_outputs(self.MODEL_KEY, self._parameter_values(), current_time)
            for name in OUTPUT_DEFAULTS:
                value = outputs.get(name, OUTPUT_DEFAULTS[name])
                if name in INTEGER_OUTPUTS:
                    value = int(round(value))
                else:
                    value = float(value)
                setattr(self, name, value)
