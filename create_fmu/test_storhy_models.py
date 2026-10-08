"""Tests for the STOR-HY FMI 3.0 placeholder models (stdlib only).

The drivers below mirror the CADS co-simulation master semantics: inputs are
applied from the latest exchanged values, each model sub-steps with its own
DefaultExperiment step size up to the communication point, Gauss-Seidel
advances models in listed order and Jacobi advances all from the same values.
"""

import importlib.util
import math
import re
import subprocess
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path
from xml.etree import ElementTree

MODELS_DIR = Path(__file__).resolve().parent / "storhy_models"
sys.path.insert(0, str(MODELS_DIR))

from storhy_model_logic import MODEL_SPECS, ModelRunner  # noqa: E402

START = 0.0
STOP = 86400.0
H = 900.0
POINTS = int(round((STOP - START) / H))

HAVE_PYTHONFMU3 = importlib.util.find_spec("pythonfmu3") is not None


def _split(ref):
    model, var = ref.split(".", 1)
    return model, var


def _apply(models, connections):
    for src, dst in connections:
        sm, sv = _split(src)
        dm, dv = _split(dst)
        models[dm].set(dv, models[sm].get(sv))


def _record(models, signals, trace):
    for ref in signals:
        m, v = _split(ref)
        trace.setdefault(ref, []).append(models[m].get(v))


def run_cosim(models, connections, scheme, signals, events=(), order=None):
    """Run 96 communication steps of 900 s and return the sampled trace.

    ``events`` items are ``(name, predicate(models) -> bool, target_ref, mode)``
    with mode ``level`` or ``pulse`` (value 1 / reset 0), evaluated at every
    communication point on the latest exchanged values.
    """
    order = order or list(models)
    trace = {"time": []}
    event_state = {name: {"prev": False, "pulse": False, "count": 0} for name, *_ in events}

    def evaluate_events():
        for name, predicate, _, mode in events:
            state = event_state[name]
            now = bool(predicate(models))
            rising = now and not state["prev"]
            if rising:
                state["count"] += 1
            state["active"] = now if mode == "level" else rising
            state["prev"] = now

    def apply_events():
        for name, _, target, _ in events:
            m, v = _split(target)
            models[m].set(v, 1 if event_state[name].get("active") else 0)

    # Initialisation: propagate connected values a few times (fixed point).
    for _ in range(len(models)):
        _apply(models, connections)
        for runner in models.values():
            runner.spec["init"](runner.values, START)
    evaluate_events()
    trace["time"].append(START)
    _record(models, signals, trace)

    for k in range(POINTS):
        t = START + k * H
        t_next = START + (k + 1) * H
        apply_events()
        if scheme == "jacobi":
            _apply(models, connections)
            for name in order:
                models[name].advance(t_next)
        elif scheme == "gauss_seidel":
            for name in order:
                incoming = [(s, d) for s, d in connections if _split(d)[0] == name]
                _apply(models, incoming)
                models[name].advance(t_next)
        else:
            raise ValueError(scheme)
        evaluate_events()
        trace["time"].append(t_next)
        _record(models, signals, trace)
    trace["_events"] = event_state
    return trace


def run_gauss_seidel():
    models = {
        "ems": ModelRunner("EmsDispatch"),
        "battery": ModelRunner("BatteryDegradation", {"initial_soc_percent": 50.0}),
    }
    connections = [
        ("battery.soc_percent", "ems.soc_percent"),
        ("ems.power_setpoint_mw", "battery.power_setpoint_mw"),
    ]
    signals = [
        "battery.soc_percent",
        "battery.soh_percent",
        "battery.rul_days",
        "battery.power_actual_mw",
        "ems.power_setpoint_mw",
        "ems.price_eur_mwh",
        "ems.revenue_eur",
        "ems.mode",
    ]
    return models, run_cosim(models, connections, "gauss_seidel", signals)


def run_jacobi():
    models = {
        "runner": ModelRunner("RunnerStress", {"flow_demand_pu": 0.75}),
        "iot": ModelRunner("IoTIndicator"),
        "rul": ModelRunner("Rul"),
    }
    connections = [
        ("runner.load_pu", "iot.load_pu"),
        ("runner.stress_amplitude_mpa", "rul.stress_amplitude_mpa"),
        ("iot.condition_indicator", "rul.condition_indicator"),
    ]
    signals = [
        "runner.load_pu",
        "runner.stress_amplitude_mpa",
        "runner.start_stop_count",
        "iot.vibration_rms_mm_s",
        "iot.condition_indicator",
        "rul.damage_index",
        "rul.rul_days",
        "rul.status_code",
    ]
    return models, run_cosim(models, connections, "jacobi", signals)


SEDIMENT_W2 = {
    "profile_mode": 1,
    "cycles_per_day": 8.0,
    "exposure_rate_per_g_l_h": 0.16,
    "initial_exposure": 0.3,
}
RUL_W2 = {"stress_amplitude_mpa": 55.0, "initial_damage": 0.3, "demo_acceleration": 100.0}
W2_CONNECTIONS = [("sediment.sediment_exposure", "rul.condition_indicator")]


def _w2_models():
    return {
        "sediment": ModelRunner("SedimentExposure", SEDIMENT_W2),
        "rul": ModelRunner("Rul", RUL_W2),
    }


def run_event():
    """W2: Cheylas sediment exposure -> RUL with a pulsed cleaning event on the same model."""
    models = _w2_models()
    events = [
        (
            "high_exposure",
            lambda m: m["sediment"].get("sediment_exposure") > 0.8,
            "sediment.cleaning_trigger",
            "pulse",
        ),
    ]
    signals = [
        "sediment.sediment_exposure",
        "sediment.head_m",
        "sediment.sediment_concentration_g_l",
        "sediment.cleaning_count",
        "sediment.cumulative_cleaning_cost_eur",
        "sediment.downtime_h",
        "rul.damage_index",
        "rul.rul_days",
        "rul.status_code",
    ]
    return models, run_cosim(models, W2_CONNECTIONS, "gauss_seidel", signals, events)


def run_cost(inputs, overrides=None):
    """Sequential DegradationCost step over one day with constant inputs."""
    params = dict(overrides or {})
    params.update(inputs)
    model = ModelRunner("DegradationCost", params)
    names = [
        "degradation_cost_eur",
        "degradation_cost_rate_eur_per_day",
        "gross_revenue_eur",
        "net_benefit_eur",
        "benefit_cost_ratio",
        "status_code",
        "recommendation_code",
    ]
    trace = {name: [model.get(name)] for name in names}
    for k in range(POINTS):
        model.advance((k + 1) * H)
        for name in names:
            trace[name].append(model.get(name))
    return model, trace


def _assert_finite(test, trace):
    for name, series in trace.items():
        if name.startswith("_"):
            continue
        test.assertEqual(len(series), POINTS + 1, name)
        for value in series:
            test.assertTrue(math.isfinite(float(value)), name)


class SpecTests(unittest.TestCase):
    def test_specs_are_consistent(self):
        allowed = {"Float64", "Int32", "Boolean"}
        for name, spec in MODEL_SPECS.items():
            with self.subTest(model=name):
                self.assertIn(spec["step_size"], (300.0, 900.0))
                self.assertIn("not validated physics", spec["description"])
                names = set()
                for group in ("parameters", "inputs", "outputs"):
                    for var, (type_name, _, description) in spec[group].items():
                        self.assertIn(type_name, allowed, var)
                        self.assertNotIn(var, names, var)
                        self.assertNotEqual(var, "time")
                        self.assertTrue(description)
                        names.add(var)
                self.assertTrue(spec["inputs"])
                self.assertTrue(spec["outputs"])

    def test_model_files_use_literal_slave_class_line(self):
        for name in MODEL_SPECS:
            matches = [
                p for p in MODELS_DIR.glob("*_model.py")
                if re.search(rf"^class {name}\(Fmi3Slave\):$", p.read_text(), re.MULTILINE)
            ]
            self.assertEqual(len(matches), 1, name)

    def test_substepping_uses_declared_step(self):
        battery = ModelRunner("BatteryDegradation")
        ems = ModelRunner("EmsDispatch")
        for k in range(POINTS):
            battery.advance((k + 1) * H)
            ems.advance((k + 1) * H)
        self.assertEqual(battery.do_step_calls, 288)
        self.assertEqual(ems.do_step_calls, 96)


class GaussSeidelTests(unittest.TestCase):
    def test_deterministic_and_finite(self):
        _, first = run_gauss_seidel()
        _, second = run_gauss_seidel()
        self.assertEqual(first, second)
        _assert_finite(self, first)

    def test_bounds_and_behaviour(self):
        _, trace = run_gauss_seidel()
        for soc in trace["battery.soc_percent"]:
            self.assertGreaterEqual(soc, 4.999)
            self.assertLessEqual(soc, 98.001)
        soh = trace["battery.soh_percent"]
        for a, b in zip(soh, soh[1:]):
            self.assertLess(b, a)
        self.assertLess(soh[-1], 100.0)
        self.assertGreater(soh[-1], 90.0)
        # EMS never asks for discharge at or below its minimum SoC.
        for soc, setpoint in zip(trace["battery.soc_percent"], trace["ems.power_setpoint_mw"]):
            if soc <= 20.0:
                self.assertLessEqual(setpoint, 0.0)
        # Both charge and discharge happen, and arbitrage earns money.
        self.assertIn(1, trace["ems.mode"])
        self.assertIn(2, trace["ems.mode"])
        self.assertGreater(trace["ems.revenue_eur"][-1], 0.0)
        for rul in trace["battery.rul_days"]:
            self.assertGreater(rul, 0.0)
            self.assertLessEqual(rul, 7300.0)

    def test_coupling_matters(self):
        models, _ = run_gauss_seidel()
        uncoupled = ModelRunner("BatteryDegradation")
        uncoupled.advance(STOP)
        self.assertNotAlmostEqual(models["battery"].get("soh_percent"), uncoupled.get("soh_percent"), places=6)


class JacobiTests(unittest.TestCase):
    def test_deterministic_and_finite(self):
        _, first = run_jacobi()
        _, second = run_jacobi()
        self.assertEqual(first, second)
        _assert_finite(self, first)

    def test_damage_monotone_and_rul_falls(self):
        _, trace = run_jacobi()
        damage = trace["rul.damage_index"]
        rul = trace["rul.rul_days"]
        for a, b in zip(damage, damage[1:]):
            self.assertGreaterEqual(b, a)
        for a, b in zip(rul, rul[1:]):
            self.assertLessEqual(b, a)
        self.assertGreater(damage[-1], damage[0])
        self.assertLess(rul[-1], rul[0])
        self.assertLessEqual(damage[-1], 1.0)
        for ci in trace["iot.condition_indicator"]:
            self.assertGreaterEqual(ci, 0.0)
            self.assertLessEqual(ci, 1.0)
        self.assertGreaterEqual(trace["runner.start_stop_count"][-1], 6)
        self.assertIn(trace["rul.status_code"][-1], (0, 1, 2))


class EventTests(unittest.TestCase):
    def test_deterministic_and_finite(self):
        _, first = run_event()
        _, second = run_event()
        self.assertEqual(first, second)
        _assert_finite(self, first)

    def test_cleanings_reset_exposure(self):
        models, trace = run_event()
        count = trace["sediment.cleaning_count"][-1]
        self.assertGreaterEqual(count, 2)
        self.assertLessEqual(count, 4)
        self.assertEqual(trace["_events"]["high_exposure"]["count"], count)
        exposure = trace["sediment.sediment_exposure"]
        drops = [(a, b) for a, b in zip(exposure, exposure[1:]) if b < a - 0.3]
        self.assertEqual(len(drops), count)
        for before, after in drops:
            self.assertGreater(before, 0.8)
            self.assertLess(after, 0.3)
        self.assertAlmostEqual(trace["sediment.cumulative_cleaning_cost_eur"][-1], 18000.0 * count)
        self.assertAlmostEqual(trace["sediment.downtime_h"][-1], 2.0 * count)
        self.assertLessEqual(models["sediment"].get("hours_since_cleaning"), 24.0)
        # Cycling profile: Cheylas-like head around 260 m.
        for head in trace["sediment.head_m"]:
            self.assertGreater(head, 250.0)
            self.assertLess(head, 270.0)

    def test_exposure_drives_rul(self):
        models, trace = run_event()
        damage = trace["rul.damage_index"]
        for a, b in zip(damage, damage[1:]):
            self.assertGreaterEqual(b, a)
        self.assertGreater(damage[-1], RUL_W2["initial_damage"] + 0.05)
        uncoupled = ModelRunner("Rul", RUL_W2)
        uncoupled.advance(STOP)
        self.assertGreater(models["rul"].get("damage_index"), uncoupled.get("damage_index"))

    def test_no_event_no_cleaning(self):
        models = _w2_models()
        trace = run_cosim(models, W2_CONNECTIONS, "gauss_seidel", ["sediment.cleaning_count", "sediment.sediment_exposure"])
        self.assertEqual(trace["sediment.cleaning_count"][-1], 0)
        exposure = trace["sediment.sediment_exposure"]
        for a, b in zip(exposure, exposure[1:]):
            self.assertGreaterEqual(b, a)

    def test_min_interval_limits_held_trigger(self):
        sediment = ModelRunner("SedimentExposure", {"cleaning_trigger": True})
        sediment.advance(STOP)
        # Held for 24 h with a 4 h minimum interval: cleanings at 0, 4, ..., 20 h.
        self.assertEqual(sediment.get("cleaning_count"), 6)

    def test_tidal_profile_still_available(self):
        sediment = ModelRunner("SedimentExposure")
        heads = []
        for k in range(POINTS):
            sediment.advance((k + 1) * H)
            heads.append(sediment.get("head_m"))
        self.assertLess(min(heads), 2.0)
        self.assertGreater(max(heads), 8.0)


MEASUREMENTS_DIR = Path(__file__).resolve().parents[1] / "data" / "storhy" / "cheylas" / "measurements"


def _read_fallback_csv(name):
    """Read a seconds-relative fallback CSV (``time,<variable>``) as [(t, value)]."""
    with open(MEASUREMENTS_DIR / name) as fh:
        header = fh.readline().strip().split(",")
        rows = [tuple(float(x) for x in line.split(",")) for line in fh if line.strip()]
    return header[1], rows


def _drive(model, variable, rows, outputs):
    """Sample-and-hold ``rows`` into ``variable`` at every model sub-step."""
    h = model.spec["step_size"]
    lookup = {t: value for t, value in rows}
    trace = {name: [] for name in outputs}
    while model.time < STOP - 1e-9:
        model.set(variable, lookup[model.time])
        model.advance(model.time + h)
        for name in outputs:
            trace[name].append(model.get(name))
    return trace


class MeasuredInputTests(unittest.TestCase):
    def test_iot_follows_measured_vibration(self):
        iot = ModelRunner("IoTIndicator", {"load_pu": 0.75})
        iot.set("vibration_rms_measured_mm_s", 5.0)
        iot.advance(6 * 3600.0)
        self.assertAlmostEqual(iot.get("vibration_rms_mm_s"), 5.0, places=3)
        self.assertAlmostEqual(iot.get("condition_indicator"), (5.0 - 1.4) / (7.1 - 1.4), places=3)
        # The measured path ignores the synthetic load-driven signal.
        iot.set("load_pu", 1.4)
        iot.advance(12 * 3600.0)
        self.assertAlmostEqual(iot.get("vibration_rms_mm_s"), 5.0, places=3)
        # A higher measured level raises the indicator through the EWMA.
        iot.set("vibration_rms_measured_mm_s", 6.5)
        iot.advance(12 * 3600.0 + 900.0)
        self.assertGreater(iot.get("vibration_rms_mm_s"), 5.0)
        self.assertLess(iot.get("vibration_rms_mm_s"), 6.5)
        iot.advance(STOP)
        self.assertAlmostEqual(iot.get("condition_indicator"), (6.5 - 1.4) / (7.1 - 1.4), places=3)

    def test_iot_zero_measured_keeps_synthetic_signal(self):
        synthetic = ModelRunner("IoTIndicator", {"load_pu": 0.75})
        explicit = ModelRunner("IoTIndicator", {"load_pu": 0.75, "vibration_rms_measured_mm_s": 0.0})
        synthetic.advance(STOP)
        explicit.advance(STOP)
        self.assertEqual(synthetic.values, explicit.values)

    def test_sediment_measured_concentration_overrides_profile(self):
        sediment = ModelRunner("SedimentExposure", dict(SEDIMENT_W2, initial_exposure=0.3))
        sediment.set("measured_concentration_g_l", 1.0)
        sediment.advance(3600.0)
        self.assertAlmostEqual(sediment.get("sediment_concentration_g_l"), 1.0)
        self.assertAlmostEqual(sediment.get("sediment_exposure"), 0.3 + 0.16 * 1.0, places=9)
        # Back to 0: the cycling profile takes over again.
        profile = ModelRunner("SedimentExposure", SEDIMENT_W2)
        profile.advance(7200.0)
        sediment.set("measured_concentration_g_l", 0.0)
        sediment.advance(7200.0)
        self.assertAlmostEqual(sediment.get("sediment_concentration_g_l"), profile.get("sediment_concentration_g_l"))

    @unittest.skipUnless((MEASUREMENTS_DIR / "vibration_rms_mm_s.csv").is_file(), "fallback CSVs not generated")
    def test_bundled_vibration_series_drives_indicator(self):
        variable, rows = _read_fallback_csv("vibration_rms_mm_s.csv")
        self.assertEqual(variable, "vibration_rms_measured_mm_s")
        self.assertEqual(len(rows), 289)
        iot = ModelRunner("IoTIndicator", {"load_pu": 0.75})
        trace = _drive(iot, variable, rows, ["vibration_rms_mm_s", "condition_indicator"])
        measured = [value for _, value in rows]
        self.assertGreaterEqual(min(trace["vibration_rms_mm_s"]), min(measured) - 1e-9)
        self.assertLessEqual(max(trace["vibration_rms_mm_s"]), max(measured) + 1e-9)
        for value in trace["condition_indicator"]:
            self.assertTrue(0.0 <= value <= 1.0)

    @unittest.skipUnless((MEASUREMENTS_DIR / "turbidity_g_l.csv").is_file(), "fallback CSVs not generated")
    def test_bundled_turbidity_series_drives_exposure(self):
        variable, rows = _read_fallback_csv("turbidity_g_l.csv")
        self.assertEqual(variable, "measured_concentration_g_l")
        sediment = ModelRunner("SedimentExposure", SEDIMENT_W2)
        trace = _drive(sediment, variable, rows, ["sediment_concentration_g_l", "sediment_exposure"])
        self.assertEqual(trace["sediment_concentration_g_l"], [value for _, value in rows[:-1]])
        exposure = trace["sediment_exposure"]
        for a, b in zip(exposure, exposure[1:]):
            self.assertGreaterEqual(b, a)
        self.assertGreater(exposure[-1], 0.8)


class RunnerEnergyTests(unittest.TestCase):
    def test_energy_monotone_and_only_in_turbine_mode(self):
        models, _ = run_jacobi()
        runner = ModelRunner("RunnerStress", {"flow_demand_pu": 0.75})
        energy = [runner.get("energy_mwh")]
        modes = [runner.get("operating_mode")]
        for k in range(POINTS):
            runner.advance((k + 1) * H)
            energy.append(runner.get("energy_mwh"))
            modes.append(runner.get("operating_mode"))
        self.assertEqual(energy[0], 0.0)
        for i, (a, b) in enumerate(zip(energy, energy[1:])):
            self.assertGreaterEqual(b, a)
            if modes[i] != 1:
                self.assertEqual(b, a)
        # 12 h generating at roughly 0.6-0.75 pu of 240 MW.
        self.assertGreater(energy[-1], 1000.0)
        self.assertLess(energy[-1], 12.0 * 240.0 * 1.1)
        self.assertAlmostEqual(models["runner"].get("energy_mwh"), energy[-1])


class DegradationCostTests(unittest.TestCase):
    def test_fatigue_cost_and_balance(self):
        model, trace = run_cost({"rul_days": 300.0, "damage_rate_per_day": 0.002, "energy_mwh": 1500.0})
        cost = trace["degradation_cost_eur"]
        for a, b in zip(cost, cost[1:]):
            self.assertGreaterEqual(b, a)
        self.assertAlmostEqual(cost[-1], 2.0e6 * 0.002, places=6)
        self.assertAlmostEqual(model.get("degradation_cost_rate_eur_per_day"), 2.0e6 * 0.002, places=6)
        self.assertAlmostEqual(model.get("gross_revenue_eur"), 1500.0 * 60.0)
        for gross, c, net in zip(trace["gross_revenue_eur"], cost, trace["net_benefit_eur"]):
            self.assertAlmostEqual(net, gross - c)
        self.assertAlmostEqual(model.get("benefit_cost_ratio"), 90000.0 / 4000.0)
        self.assertEqual(model.get("status_code"), 1)
        self.assertEqual(model.get("recommendation_code"), 0)

    def test_soh_fade_charged_once(self):
        model, trace = run_cost({"rul_days": 2000.0, "soh_percent": 97.0, "revenue_eur": 5000.0})
        cost = trace["degradation_cost_eur"]
        self.assertEqual(cost[0], 0.0)
        self.assertAlmostEqual(cost[1], 2.0e6 * 0.03)
        self.assertAlmostEqual(cost[-1], 2.0e6 * 0.03)
        self.assertAlmostEqual(model.get("gross_revenue_eur"), 5000.0)
        self.assertLess(model.get("benefit_cost_ratio"), 1.0)
        self.assertEqual(model.get("status_code"), 0)
        self.assertEqual(model.get("recommendation_code"), 1)

    def test_thresholds(self):
        cases = [(1000.0, 0), (364.0, 1), (89.0, 2)]
        for rul, status in cases:
            with self.subTest(rul=rul):
                model, _ = run_cost({"rul_days": rul, "damage_rate_per_day": 0.001, "energy_mwh": 1000.0})
                self.assertEqual(model.get("status_code"), status)
                self.assertEqual(model.get("recommendation_code"), 2 if status == 2 else 0)


@unittest.skipUnless(HAVE_PYTHONFMU3, "pythonfmu3 is not installed")
class ModelDescriptionTests(unittest.TestCase):
    def test_built_fmu_model_description(self):
        with tempfile.TemporaryDirectory() as tmp:
            subprocess.run(
                [
                    sys.executable, "-m", "pythonfmu3", "build",
                    "-f", str(MODELS_DIR / "battery_degradation_model.py"),
                    "-d", tmp,
                    str(MODELS_DIR / "storhy_model_common.py"),
                    str(MODELS_DIR / "storhy_model_logic.py"),
                ],
                check=True,
                stdout=subprocess.DEVNULL,
            )
            with zipfile.ZipFile(Path(tmp) / "BatteryDegradation.fmu") as fmu:
                root = ElementTree.fromstring(fmu.read("modelDescription.xml"))

        self.assertEqual(root.get("fmiVersion"), "3.0")
        self.assertIn("not validated physics", root.get("description"))
        experiment = root.find("DefaultExperiment")
        self.assertIsNotNone(experiment)
        self.assertEqual(float(experiment.get("stepSize")), 300.0)
        self.assertEqual(float(experiment.get("startTime")), 0.0)
        self.assertEqual(float(experiment.get("stopTime")), 86400.0)
        variables = list(root.find("ModelVariables"))
        self.assertTrue({v.tag for v in variables} <= {"Float64", "Int32", "Boolean"})
        independents = [v for v in variables if v.get("causality") == "independent"]
        self.assertEqual([v.get("name") for v in independents], ["time"])
        inputs = [v for v in variables if v.get("causality") == "input"]
        self.assertEqual({v.get("name") for v in inputs}, {"power_setpoint_mw", "ambient_temp_c"})
        for v in inputs:
            self.assertIsNotNone(v.get("start"), v.get("name"))


if __name__ == "__main__":
    unittest.main()
