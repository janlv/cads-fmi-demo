#!/usr/bin/env python3
"""Generate the synthetic Cheylas IoT/turbidity measurement batch (stdlib only).

The values are deterministic placeholders standing in for the NORCE
acoustic-emission/vibration edge box and a turbidity probe at Le Cheylas. They
are NOT plant data. The generator writes:

* ``data/storhy/cheylas/measurements/batch-2026-05-20/`` with ``metadata.yaml``
  and one ``time,value`` CSV per channel (RFC3339 UTC, 300 s cadence, 24 h),
  the form ``scripts/ingest_measurements.py`` loads into the STOR-HY database;
* seconds-relative fallback CSVs next to the batches
  (``time,<fmu input variable>``), which the runtime uses when no database is
  configured;
* ``batch-bad-example/``, a deliberately malformed batch (a channel without a
  unit) used to exercise the ingestion rejection path.

Usage: python3 scripts/dev/generate_synthetic_measurements.py [--out DIR]
"""

import argparse
import math
import random
from datetime import datetime, timedelta, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUT = ROOT / "data" / "storhy" / "cheylas" / "measurements"

SEED = 20260520
START = datetime(2026, 5, 20, tzinfo=timezone.utc)
INTERVAL_S = 300
DURATION_S = 86400
CYCLES_PER_DAY = 8
CYCLE_H = 24.0 / CYCLES_PER_DAY

SITE = "cheylas"
BATCH = "2026-05-20-iot"
SOURCE = "NORCE AE/vibration edge box (synthetic placeholder)"

# (channel, unit, file, FMU input variable fed from this channel)
CHANNELS = (
    ("vibration_rms_mm_s", "mm/s", "vibration_rms_mm_s.csv", "vibration_rms_measured_mm_s"),
    ("turbidity_g_l", "g/l", "turbidity_g_l.csv", "measured_concentration_g_l"),
)

# Short vibration bursts: (start hour, end hour, extra mm/s).
VIBRATION_BURSTS = ((7.25, 7.6, 2.6), (16.7, 17.0, 3.1))


def load_profile(hour):
    """Pump/turbine duty in [0, 1]: running during the first half of each cycle."""
    return max(0.0, math.sin(2.0 * math.pi * hour / CYCLE_H))


def vibration(hour, rng):
    value = 1.8 + 1.5 * load_profile(hour) + 0.15 * math.sin(2.0 * math.pi * hour / 24.0)
    value += rng.gauss(0.0, 0.06)
    for start, end, extra in VIBRATION_BURSTS:
        if start <= hour <= end:
            value += extra * math.sin(math.pi * (hour - start) / (end - start))
    return max(0.0, value)


def turbidity(hour, rng):
    # Dewatering after each running phase stirs up sediment: a peak centred
    # a little after the end of the running half of every cycle.
    phase = (hour % CYCLE_H) - 0.6 * CYCLE_H
    peak = 0.55 * math.exp(-0.5 * (phase / 0.25) ** 2)
    value = 0.4 + peak + 0.05 * math.sin(2.0 * math.pi * hour / 24.0)
    value += rng.gauss(0.0, 0.015)
    return max(0.02, value)


def series():
    rng = random.Random(SEED)
    rows = []
    for k in range(DURATION_S // INTERVAL_S + 1):
        t = k * INTERVAL_S
        hour = t / 3600.0
        rows.append((t, vibration(hour, rng), turbidity(hour, rng)))
    return rows


def rfc3339(seconds):
    return (START + timedelta(seconds=seconds)).strftime("%Y-%m-%dT%H:%M:%SZ")


def write_metadata(path, channels, site=SITE, batch=BATCH):
    lines = [
        f"site: {site}",
        f"batch: {batch}",
        f"source: {SOURCE}",
        "description: >-",
        "  Synthetic placeholder batch for the CADS demo; deterministic, not plant data.",
        "channels:",
    ]
    for entry in channels:
        lines.append(f"  - channel: {entry['channel']}")
        if entry.get("unit") is not None:
            lines.append(f"    unit: {entry['unit']}")
        lines.append(f"    sampling_interval_s: {entry['sampling_interval_s']}")
        lines.append(f"    file: {entry['file']}")
    path.write_text("\n".join(lines) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT, help="Output directory (default: %(default)s)")
    args = parser.parse_args()

    rows = series()
    batch_dir = args.out / "batch-2026-05-20"
    batch_dir.mkdir(parents=True, exist_ok=True)
    write_metadata(
        batch_dir / "metadata.yaml",
        [{"channel": c, "unit": u, "sampling_interval_s": INTERVAL_S, "file": f} for c, u, f, _ in CHANNELS],
    )
    for index, (channel, _, filename, variable) in enumerate(CHANNELS, start=1):
        with open(batch_dir / filename, "w") as fh:
            fh.write("time,value\n")
            for row in rows:
                fh.write(f"{rfc3339(row[0])},{row[index]:.4f}\n")
        with open(args.out / filename, "w") as fh:
            fh.write(f"time,{variable}\n")
            for row in rows:
                fh.write(f"{row[0]},{row[index]:.4f}\n")

    bad_dir = args.out / "batch-bad-example"
    bad_dir.mkdir(parents=True, exist_ok=True)
    write_metadata(
        bad_dir / "metadata.yaml",
        [{"channel": "vibration_rms_mm_s", "unit": None, "sampling_interval_s": INTERVAL_S, "file": "vibration_rms_mm_s.csv"}],
        batch="bad-example-missing-unit",
    )
    with open(bad_dir / "vibration_rms_mm_s.csv", "w") as fh:
        fh.write("time,value\n")
        for row in rows[:12]:
            fh.write(f"{rfc3339(row[0])},{row[1]:.4f}\n")

    print(f"wrote {len(rows)} rows per channel to {batch_dir}")
    print(f"wrote fallback CSVs and {bad_dir.name} to {args.out}")


if __name__ == "__main__":
    main()
