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
  unit) used to exercise the ingestion rejection path;
* with ``--days N`` (N > 1), the following days as ``upcoming/batch-<date>/``
  (288 rows each, 00:05Z to next-day 00:00Z, so consecutive batches keep the
  300 s cadence without a duplicate midnight sample). ``run_db.sh`` does not
  load ``upcoming/`` at startup; ``run_db.sh --next`` ingests them one by one
  to replay new data arriving. Vibration drifts up by 0.04 mm/s per day so the
  damage rate trends up.

Day 0 (``--start``, default 2026-05-20) keeps the original single-day output
byte for byte.

Usage: python3 scripts/dev/generate_synthetic_measurements.py [--out DIR]
       [--start YYYY-MM-DD] [--days N]
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
UPCOMING_DIR = "upcoming"
DAILY_DRIFT_MM_S = 0.04
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


def vibration(hour, rng, drift=0.0):
    value = 1.8 + 1.5 * load_profile(hour) + 0.15 * math.sin(2.0 * math.pi * hour / 24.0)
    if drift:
        value += drift
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


def day_series(day):
    """Rows for day ``day`` > 0: t = 300..86400 s after that day's midnight.

    Bursts and cycles are keyed by hour of day; the RNG is seeded per day.
    """
    rng = random.Random(SEED + day)
    drift = DAILY_DRIFT_MM_S * day
    rows = []
    for k in range(1, DURATION_S // INTERVAL_S + 1):
        t = k * INTERVAL_S
        hour = t / 3600.0
        rows.append((t, vibration(hour, rng, drift), turbidity(hour, rng)))
    return rows


def rfc3339(seconds, start=START):
    return (start + timedelta(seconds=seconds)).strftime("%Y-%m-%dT%H:%M:%SZ")


def channel_entries():
    return [{"channel": c, "unit": u, "sampling_interval_s": INTERVAL_S, "file": f} for c, u, f, _ in CHANNELS]


def write_batch(batch_dir, rows, start, batch):
    batch_dir.mkdir(parents=True, exist_ok=True)
    write_metadata(batch_dir / "metadata.yaml", channel_entries(), batch=batch)
    for index, (_, _, filename, _) in enumerate(CHANNELS, start=1):
        with open(batch_dir / filename, "w") as fh:
            fh.write("time,value\n")
            for row in rows:
                fh.write(f"{rfc3339(row[0], start)},{row[index]:.4f}\n")


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


def parse_date(text):
    try:
        return datetime.strptime(text, "%Y-%m-%d").replace(tzinfo=timezone.utc)
    except ValueError:
        raise argparse.ArgumentTypeError(f"expected YYYY-MM-DD, got {text!r}")


def positive_int(text):
    value = int(text)
    if value < 1:
        raise argparse.ArgumentTypeError("must be >= 1")
    return value


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT, help="Output directory (default: %(default)s)")
    parser.add_argument(
        "--start", type=parse_date, default=START, help="First day, written as batch-<date> (default: 2026-05-20)"
    )
    parser.add_argument(
        "--days", type=positive_int, default=1, help="Days to generate; days after the first go to upcoming/ (default: 1)"
    )
    args = parser.parse_args(argv)

    start = args.start
    date = start.strftime("%Y-%m-%d")
    rows = series()
    batch_dir = args.out / f"batch-{date}"
    write_batch(batch_dir, rows, start, f"{date}-iot")
    for index, (channel, _, filename, variable) in enumerate(CHANNELS, start=1):
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
            fh.write(f"{rfc3339(row[0], start)},{row[1]:.4f}\n")

    print(f"wrote {len(rows)} rows per channel to {batch_dir}")
    print(f"wrote fallback CSVs and {bad_dir.name} to {args.out}")

    for day in range(1, args.days):
        day_start = start + timedelta(days=day)
        day_date = day_start.strftime("%Y-%m-%d")
        day_rows = day_series(day)
        day_dir = args.out / UPCOMING_DIR / f"batch-{day_date}"
        write_batch(day_dir, day_rows, day_start, f"{day_date}-iot")
        print(f"wrote {len(day_rows)} rows per channel to {day_dir}")


if __name__ == "__main__":
    main()
