import tempfile
import unittest
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from ae_event_stats_fmu import (
    load_event_table,
    parse_arrival_time,
    percentile,
    resolve_dataset_path,
    rolling_metrics,
    summarize_events,
)


class AEEventStatsTest(unittest.TestCase):
    def test_parse_arrival_time_with_spaced_fraction(self):
        self.assertAlmostEqual(
            parse_arrival_time(" 4:22:12:35:527 071000"),
            (((4 * 24 + 22) * 60 + 12) * 60 + 35) + 0.527071,
        )

    def test_loads_ch2_and_ch6_fixture_counts(self):
        ch2 = load_event_table(resolve_dataset_path(2, root=Path.cwd()))
        ch6 = load_event_table(resolve_dataset_path(6, root=Path.cwd()))

        self.assertEqual(len(ch2.events), 249)
        self.assertEqual(ch2.invalid_rows, 0)
        self.assertEqual(ch2.metadata["Sensor location"], "MIV S2")
        self.assertAlmostEqual(ch2.events[-1].elapsed_seconds, 40018.2166675)

        self.assertEqual(len(ch6.events), 19564)
        self.assertEqual(ch6.invalid_rows, 0)
        self.assertEqual(ch6.metadata["Sensor location"], "MIV S6")
        self.assertAlmostEqual(ch6.events[-1].elapsed_seconds, 6550.0264236)

    def test_summarizes_expected_core_metrics(self):
        ch6 = load_event_table(resolve_dataset_path(6, root=Path.cwd()))
        summary = summarize_events(ch6)

        self.assertEqual(summary["event_count"], 19564)
        self.assertAlmostEqual(summary["amplitude_p50"], 34.0)
        self.assertAlmostEqual(summary["rms_p50"], 0.012)
        self.assertAlmostEqual(summary["asl_p50"], 19.0)
        self.assertAlmostEqual(summary["frequency_centroid_p50"], 776.4)
        self.assertGreater(summary["energy_sum"], 0.47)

    def test_rolling_metrics_use_windowed_values(self):
        ch2 = load_event_table(resolve_dataset_path(2, root=Path.cwd()))
        values = rolling_metrics(ch2, current_time=1.0, window_seconds=1.0)

        self.assertGreater(values["rolling_event_rate_hz"], 0.0)
        self.assertGreater(values["rolling_amplitude_p95"], 0.0)
        self.assertGreaterEqual(values["cumulative_energy"], 0.0)

    def test_percentile_uses_linear_interpolation(self):
        self.assertEqual(percentile([10.0, 20.0, 30.0], 50), 20.0)
        self.assertEqual(percentile([0.0, 100.0], 95), 95.0)

    def test_invalid_rows_are_counted_and_skipped(self):
        content = """Sensor location:,demo

Arrival time,Amplitude,Counts,Duration,Energy,Rise counts,Rise Time,RMS(mV),ASL(dB),External Parametrics1,External Parametrics2,External Parametrics3,External Parametrics4,External Parametrics5,Frequency Centroid(kHz),Peak Frequency(kHz),Partial Power1(%),Partial Power2(%),Partial Power3(%),Partial Power4(%),Partial Power5(%),AverageFreq,EchoFreq,InitFreq
 1:00:00:00:000 000000,10,1,1,0.1,1,1,0.2,20,0,0,0,0,0,100,50,0,0,0,0,0,40,40,40
bad-time,11,1,1,0.1,1,1,0.3,21,0,0,0,0,0,101,51,0,0,0,0,0,41,41,41
 1:00:00:01:000 000000,12,1,1,0.2,1,1,0.4,22,0,0,0,0,0,102,52,0,0,0,0,0,42,42,42
"""
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir) / "events.csv"
            path.write_text(content)
            table = load_event_table(path)

        self.assertEqual(len(table.events), 2)
        self.assertEqual(table.invalid_rows, 1)
        self.assertAlmostEqual(table.events[-1].elapsed_seconds, 1.0)


if __name__ == "__main__":
    unittest.main()
