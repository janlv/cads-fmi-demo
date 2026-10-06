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

FMI3_DIR = Path(__file__).resolve().parent / "storhy_fmi3"
sys.path.insert(0, str(FMI3_DIR))

from storhy_fmi3_models import MODEL_SPECS, ModelRunner  # noqa: E402

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
        "ems": ModelRunner("EmsDispatchFmi3"),
        "battery": ModelRunner("BatteryDegradationFmi3", {"initial_soc_percent": 50.0}),
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
        "runner": ModelRunner("RunnerStressFmi3", {"flow_demand_pu": 0.75}),
        "iot": ModelRunner("IoTIndicatorFmi3"),
        "rul": ModelRunner("RulFmi3"),
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
        "sediment": ModelRunner("SedimentExposureFmi3", SEDIMENT_W2),
        "rul": ModelRunner("RulFmi3", RUL_W2),
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
    """Sequential DegradationCostFmi3 step over one day with constant inputs."""
    params = dict(overrides or {})
    params.update(inputs)
    model = ModelRunner("DegradationCostFmi3", params)
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

    def test_model_files_use_literal_fmi3slave_class_line(self):
        for name in MODEL_SPECS:
            matches = [
                p for p in FMI3_DIR.glob("*_fmi3.py")
                if re.search(rf"^class {name}\(Fmi3Slave\):$", p.read_text(), re.MULTILINE)
            ]
            self.assertEqual(len(matches), 1, name)

    def test_substepping_uses_declared_step(self):
        battery = ModelRunner("BatteryDegradationFmi3")
        ems = ModelRunner("EmsDispatchFmi3")
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
        uncoupled = ModelRunner("BatteryDegradationFmi3")
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
        uncoupled = ModelRunner("RulFmi3", RUL_W2)
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
        sediment = ModelRunner("SedimentExposureFmi3", {"cleaning_trigger": True})
        sediment.advance(STOP)
        # Held for 24 h with a 4 h minimum interval: cleanings at 0, 4, ..., 20 h.
        self.assertEqual(sediment.get("cleaning_count"), 6)

    def test_tidal_profile_still_available(self):
        sediment = ModelRunner("SedimentExposureFmi3")
        heads = []
        for k in range(POINTS):
            sediment.advance((k + 1) * H)
            heads.append(sediment.get("head_m"))
        self.assertLess(min(heads), 2.0)
        self.assertGreater(max(heads), 8.0)


class RunnerEnergyTests(unittest.TestCase):
    def test_energy_monotone_and_only_in_turbine_mode(self):
        models, _ = run_jacobi()
        runner = ModelRunner("RunnerStressFmi3", {"flow_demand_pu": 0.75})
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
                    "-f", str(FMI3_DIR / "battery_degradation_fmi3.py"),
                    "-d", tmp,
                    str(FMI3_DIR / "storhy_fmi3_common.py"),
                    str(FMI3_DIR / "storhy_fmi3_models.py"),
                ],
                check=True,
                stdout=subprocess.DEVNULL,
            )
            with zipfile.ZipFile(Path(tmp) / "BatteryDegradationFmi3.fmu") as fmu:
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
