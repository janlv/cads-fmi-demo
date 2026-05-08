import math
import sys
import unittest
from pathlib import Path


REPLICA_DIR = Path(__file__).resolve().parent / "storhy_replicas"
sys.path.insert(0, str(REPLICA_DIR))

from storhy_replica_common import MODEL_CODES, OUTPUT_DEFAULTS, compute_model_outputs  # noqa: E402


class StorhyReplicaTests(unittest.TestCase):
    def test_all_current_models_emit_common_numeric_outputs(self):
        params = {
            "site_id": 1,
            "scenario_id": 3,
            "profile_id": 2,
            "input_score": 78.0,
            "input_confidence": 0.82,
            "input_risk_index": 0.31,
            "input_damage_index": 0.22,
            "input_rul_days": 880.0,
            "input_value_delta_eur": 21000.0,
            "input_opex_delta_eur": -4300.0,
            "input_co2_delta_tonnes": 18.0,
            "input_flexibility_delta_percent": 5.5,
            "input_availability_delta_percent": 2.7,
        }

        for model_key in MODEL_CODES:
            with self.subTest(model_key=model_key):
                outputs = compute_model_outputs(model_key, params, current_time=12.0)
                self.assertEqual(set(outputs), set(OUTPUT_DEFAULTS))
                for name, value in outputs.items():
                    self.assertIsInstance(value, (int, float), name)
                    self.assertTrue(math.isfinite(float(value)), name)
                self.assertGreater(outputs["model_code"], 0)
                self.assertGreaterEqual(outputs["score"], 0.0)
                self.assertLessEqual(outputs["score"], 100.0)

    def test_retired_pozu_figaredo_models_are_not_registered(self):
        catalog_text = " ".join(MODEL_CODES)
        self.assertNotIn("pozu", catalog_text)
        self.assertNotIn("pozo", catalog_text)
        self.assertNotIn("figaredo", catalog_text)

    def test_predictive_maintenance_reacts_to_high_damage(self):
        low_risk = compute_model_outputs(
            "predictive_maintenance",
            {
                "input_risk_index": 0.2,
                "input_damage_index": 0.12,
                "input_rul_days": 1200.0,
                "input_score": 82.0,
            },
            current_time=4.0,
        )
        high_risk = compute_model_outputs(
            "predictive_maintenance",
            {
                "input_risk_index": 0.82,
                "input_damage_index": 0.75,
                "input_rul_days": 210.0,
                "input_score": 58.0,
            },
            current_time=4.0,
        )

        self.assertLess(high_risk["score"], low_risk["score"])
        self.assertGreater(high_risk["risk_index"], low_risk["risk_index"])
        self.assertGreaterEqual(high_risk["status_code"], low_risk["status_code"])


if __name__ == "__main__":
    unittest.main()
