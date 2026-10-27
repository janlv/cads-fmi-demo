"""Tests for scripts/dev/generate_synthetic_measurements.py (stdlib only)."""

import contextlib
import hashlib
import io
import tempfile
import unittest
from pathlib import Path

from _loader import ROOT, load_script

gen = load_script("scripts/dev/generate_synthetic_measurements.py", "generate_synthetic_measurements")

COMMITTED = ROOT / "data" / "storhy" / "cheylas" / "measurements"
DAY0_FILES = (
    "batch-2026-05-20/metadata.yaml",
    "batch-2026-05-20/vibration_rms_mm_s.csv",
    "batch-2026-05-20/turbidity_g_l.csv",
    "batch-bad-example/metadata.yaml",
    "batch-bad-example/vibration_rms_mm_s.csv",
    "vibration_rms_mm_s.csv",
    "turbidity_g_l.csv",
)


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def generate(out, *extra):
    with contextlib.redirect_stdout(io.StringIO()):
        gen.main(["--out", str(out), *extra])


class Day0Tests(unittest.TestCase):
    def test_day0_matches_committed_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            generate(tmp)
            for rel in DAY0_FILES:
                with self.subTest(file=rel):
                    self.assertEqual(sha256(Path(tmp) / rel), sha256(COMMITTED / rel))
            self.assertFalse((Path(tmp) / "upcoming").exists())

    def test_day0_unchanged_when_more_days_are_generated(self):
        with tempfile.TemporaryDirectory() as tmp:
            generate(tmp, "--days", "3")
            for rel in DAY0_FILES:
                with self.subTest(file=rel):
                    self.assertEqual(sha256(Path(tmp) / rel), sha256(COMMITTED / rel))


class UpcomingTests(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.out = Path(self._tmp.name)
        generate(self.out, "--days", "3")

    def tearDown(self):
        self._tmp.cleanup()

    def test_layout(self):
        upcoming = sorted(p.name for p in (self.out / "upcoming").iterdir())
        self.assertEqual(upcoming, ["batch-2026-05-21", "batch-2026-05-22"])
        for name in upcoming:
            files = sorted(p.name for p in (self.out / "upcoming" / name).iterdir())
            self.assertEqual(files, ["metadata.yaml", "turbidity_g_l.csv", "vibration_rms_mm_s.csv"])

    def test_rows_span_0005_to_next_midnight(self):
        for date, next_date in (("2026-05-21", "2026-05-22"), ("2026-05-22", "2026-05-23")):
            for channel in ("vibration_rms_mm_s", "turbidity_g_l"):
                lines = (self.out / "upcoming" / f"batch-{date}" / f"{channel}.csv").read_text().splitlines()
                with self.subTest(date=date, channel=channel):
                    self.assertEqual(lines[0], "time,value")
                    rows = lines[1:]
                    self.assertEqual(len(rows), 288)
                    self.assertTrue(rows[0].startswith(f"{date}T00:05:00Z,"))
                    self.assertTrue(rows[-1].startswith(f"{next_date}T00:00:00Z,"))
                    for row in rows:
                        float(row.split(",")[1])

    def test_no_duplicate_midnight_across_batches(self):
        day0 = (self.out / "batch-2026-05-20" / "vibration_rms_mm_s.csv").read_text().splitlines()[1:]
        day1 = (self.out / "upcoming" / "batch-2026-05-21" / "vibration_rms_mm_s.csv").read_text().splitlines()[1:]
        stamps0 = {r.split(",")[0] for r in day0}
        stamps1 = {r.split(",")[0] for r in day1}
        self.assertEqual(stamps0 & stamps1, set())

    def test_metadata_form(self):
        meta = (self.out / "upcoming" / "batch-2026-05-22" / "metadata.yaml").read_text()
        expected = (self.out / "batch-2026-05-20" / "metadata.yaml").read_text().replace(
            "batch: 2026-05-20-iot", "batch: 2026-05-22-iot"
        )
        self.assertEqual(meta, expected)
        self.assertIn("site: cheylas\n", meta)

    def test_days_are_deterministic_and_distinct(self):
        with tempfile.TemporaryDirectory() as tmp:
            generate(tmp, "--days", "3")
            rel = "upcoming/batch-2026-05-22/vibration_rms_mm_s.csv"
            self.assertEqual(sha256(Path(tmp) / rel), sha256(self.out / rel))
        a = (self.out / "upcoming/batch-2026-05-21/vibration_rms_mm_s.csv").read_text().splitlines()[1:]
        b = (self.out / "upcoming/batch-2026-05-22/vibration_rms_mm_s.csv").read_text().splitlines()[1:]
        self.assertNotEqual([r.split(",")[1] for r in a], [r.split(",")[1] for r in b])

    def test_vibration_drifts_up(self):
        def mean(date):
            rows = (self.out / "upcoming" / f"batch-{date}" / "vibration_rms_mm_s.csv").read_text().splitlines()[1:]
            values = [float(r.split(",")[1]) for r in rows]
            return sum(values) / len(values)

        self.assertGreater(mean("2026-05-22"), mean("2026-05-21"))


if __name__ == "__main__":
    unittest.main()
