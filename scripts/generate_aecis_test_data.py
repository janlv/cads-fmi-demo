#!/usr/bin/env python3
"""
Generate deterministic synthetic AE-style input data for CalculateAECIs testing.

Outputs:
- CSV with columns `time_1,rawsig`
- MAT file with variable `U = [time_1, rawsig]` to mirror FMI_surya/inputfile.mat
- JSON with expected CI values for each 10-second window
"""

from __future__ import annotations

import argparse
import csv
import json
import math
from pathlib import Path

import numpy as np

try:
    from scipy.io import savemat
except Exception:  # pragma: no cover - optional dependency
    savemat = None


BASELINE_LEVEL = 2.48


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate synthetic CalculateAECIs input data."
    )
    parser.add_argument(
        "--duration",
        type=float,
        default=30.0,
        help="Signal duration in seconds (default: %(default)s).",
    )
    parser.add_argument(
        "--sample-rate",
        type=float,
        default=1000.0,
        help="Signal sample rate in Hz (default: %(default)s).",
    )
    parser.add_argument(
        "--window",
        type=float,
        default=10.0,
        help="Window size in seconds for CI summaries (default: %(default)s).",
    )
    parser.add_argument(
        "--seed",
        type=int,
        default=42,
        help="Random seed for deterministic output (default: %(default)s).",
    )
    parser.add_argument(
        "--output-prefix",
        default="data/calculate_aecis_synthetic",
        help="Prefix used for CSV/MAT/JSON outputs (default: %(default)s).",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.duration <= 0:
        raise SystemExit("--duration must be positive")
    if args.sample_rate <= 0:
        raise SystemExit("--sample-rate must be positive")
    if args.window <= 0:
        raise SystemExit("--window must be positive")

    prefix = Path(args.output_prefix)
    prefix.parent.mkdir(parents=True, exist_ok=True)

    times, signal, regimes = generate_signal(
        duration_s=args.duration,
        sample_rate_hz=args.sample_rate,
        seed=args.seed,
        window_s=args.window,
    )
    ci_windows = summarize_windows(times, signal, args.window)

    csv_path = prefix.with_suffix(".csv")
    write_csv(csv_path, times, signal)

    mat_path = prefix.with_suffix(".mat")
    mat_written = write_mat(mat_path, times, signal)

    json_path = prefix.with_suffix(".json")
    write_summary(
        json_path=json_path,
        seed=args.seed,
        sample_rate_hz=args.sample_rate,
        duration_s=args.duration,
        window_s=args.window,
        regimes=regimes,
        signal=signal,
        ci_windows=ci_windows,
        csv_path=csv_path,
        mat_path=mat_path if mat_written else None,
    )

    print(f"[aecis-data] wrote {csv_path}")
    if mat_written:
        print(f"[aecis-data] wrote {mat_path}")
    else:
        print("[aecis-data] skipped MAT output because scipy is unavailable")
    print(f"[aecis-data] wrote {json_path}")
    return 0


def generate_signal(
    duration_s: float,
    sample_rate_hz: float,
    seed: int,
    window_s: float,
) -> tuple[np.ndarray, np.ndarray, list[dict[str, object]]]:
    rng = np.random.default_rng(seed)
    sample_count = int(round(duration_s * sample_rate_hz))
    times = np.arange(sample_count, dtype=np.float64) / sample_rate_hz

    signal = np.full(sample_count, BASELINE_LEVEL, dtype=np.float64)
    signal += 0.035 * np.sin(2.0 * math.pi * 0.18 * times)
    signal += 0.016 * np.sin(2.0 * math.pi * 0.91 * times + 0.7)
    signal += 0.009 * np.sin(2.0 * math.pi * 2.4 * times + 1.9)

    signal += white_noise(rng, sample_count, sigma=0.005)
    signal += colored_noise(rng, sample_count, sample_rate_hz, sigma=0.010)

    crack_start = duration_s * 0.34
    crack_progress = np.clip((times - crack_start) / max(duration_s * 0.58, 1e-6), 0.0, 1.0)
    signal += 0.018 * np.power(crack_progress, 1.15)
    signal += 0.012 * np.power(crack_progress, 1.35) * np.sin(2.0 * math.pi * 0.42 * times + 1.1)
    signal += white_noise(rng, sample_count, sigma=0.0035) * np.power(crack_progress, 1.2)
    signal += colored_noise(rng, sample_count, sample_rate_hz, sigma=0.008) * np.power(crack_progress, 1.4)

    window_count = max(1, int(math.ceil(duration_s / window_s)))
    regimes = []
    for index in range(window_count):
        start = index * window_s
        stop = min(duration_s, (index + 1) * window_s)
        severity = index / max(1, window_count - 1)
        if severity < 0.34:
            regime = "background_noise"
            burst_count = 5
            amplitude = (0.010, 0.028)
            width = (0.10, 0.24)
        elif severity < 0.67:
            regime = "crack_nucleation"
            burst_count = 11
            amplitude = (0.025, 0.055)
            width = (0.12, 0.28)
            signal += 0.008 * np.clip((times - start) / max(stop - start, 1e-6), 0.0, 1.0)
        else:
            regime = "propagating_crack"
            burst_count = 22
            amplitude = (0.055, 0.120)
            width = (0.16, 0.36)
            signal += 0.022 * np.clip((times - start) / max(stop - start, 1e-6), 0.0, 1.0)

        add_bursts(
            signal=signal,
            times=times,
            sample_rate_hz=sample_rate_hz,
            rng=rng,
            start_s=start,
            stop_s=stop,
            burst_count=burst_count,
            amplitude_range=amplitude,
            width_range=width,
        )
        if regime == "propagating_crack":
            add_crack_clusters(
                signal=signal,
                times=times,
                rng=rng,
                start_s=start,
                stop_s=stop,
            )
        regimes.append(
            {
                "start_s": round(start, 6),
                "stop_s": round(stop, 6),
                "regime": regime,
                "burst_count": burst_count,
            }
        )

    return times, signal, regimes


def white_noise(rng: np.random.Generator, count: int, sigma: float) -> np.ndarray:
    return rng.normal(0.0, sigma, count)


def colored_noise(
    rng: np.random.Generator,
    count: int,
    sample_rate_hz: float,
    sigma: float,
) -> np.ndarray:
    kernel_size = max(5, int(sample_rate_hz * 0.05))
    kernel_size = min(kernel_size, max(5, count // 8))
    if kernel_size % 2 == 0:
        kernel_size += 1

    kernel = np.hanning(kernel_size)
    kernel /= kernel.sum()
    source = rng.normal(0.0, 1.0, count)
    smooth = np.convolve(source, kernel, mode="same")
    smooth_std = smooth.std()
    if smooth_std > 0:
        smooth *= sigma / smooth_std
    return smooth


def add_bursts(
    signal: np.ndarray,
    times: np.ndarray,
    sample_rate_hz: float,
    rng: np.random.Generator,
    start_s: float,
    stop_s: float,
    burst_count: int,
    amplitude_range: tuple[float, float],
    width_range: tuple[float, float],
) -> None:
    if stop_s <= start_s:
        return

    carrier_min = max(2.0, sample_rate_hz * 0.015)
    carrier_max = max(carrier_min + 1.0, min(sample_rate_hz * 0.12, sample_rate_hz * 0.45))
    mask = (times >= start_s) & (times < stop_s)
    segment_times = times[mask]

    for _ in range(burst_count):
        center = rng.uniform(start_s + 0.05, max(start_s + 0.06, stop_s - 0.05))
        amplitude = rng.uniform(*amplitude_range)
        width = rng.uniform(*width_range)
        carrier_hz = rng.uniform(carrier_min, carrier_max)
        phase = rng.uniform(0.0, 2.0 * math.pi)
        envelope = np.exp(-0.5 * ((segment_times - center) / width) ** 2)
        burst = amplitude * envelope * np.sin(
            2.0 * math.pi * carrier_hz * (segment_times - center) + phase
        )
        signal[mask] += burst


def add_crack_clusters(
    signal: np.ndarray,
    times: np.ndarray,
    rng: np.random.Generator,
    start_s: float,
    stop_s: float,
) -> None:
    if stop_s <= start_s:
        return
    centers = np.linspace(start_s + 0.9, stop_s - 0.6, num=6)
    for idx, center in enumerate(centers):
        growth = idx / max(1, len(centers) - 1)
        envelope = np.exp(-0.5 * ((times - center) / (0.32 - 0.08 * growth)) ** 2)
        modulation = 0.08 + 0.09 * growth
        comb = (
            0.45 * np.sin(2.0 * math.pi * (1.4 + 0.25 * growth) * (times - center))
            + 0.30 * np.sin(2.0 * math.pi * (3.2 + 0.5 * growth) * (times - center) + 0.7)
            + 0.25 * np.sin(2.0 * math.pi * (6.4 + 0.8 * growth) * (times - center) + 1.4)
        )
        signal += modulation * envelope * comb
        signal += rng.normal(0.0, 0.002 + 0.0025 * growth, len(times)) * envelope


def summarize_windows(
    times: np.ndarray,
    signal: np.ndarray,
    window_s: float,
) -> list[dict[str, object]]:
    summaries = []
    total_duration = float(times[-1]) if len(times) > 0 else 0.0
    start = 0.0
    while start <= total_duration + 1e-12:
        stop = start + window_s
        mask = (times >= start) & (times < stop if stop < total_duration else times <= stop)
        segment = signal[mask]
        if len(segment) == 0:
            break
        summaries.append(
            {
                "start_s": round(start, 6),
                "stop_s": round(float(times[mask][-1]), 6),
                "sample_count": int(len(segment)),
                "ci_vector": ci_vector(segment),
            }
        )
        start += window_s
    return summaries


def ci_vector(segment: np.ndarray) -> list[float]:
    mean_val = float(np.mean(segment))
    rms_val = float(np.sqrt(np.mean(np.square(segment))))
    peak_to_peak = float(np.ptp(segment))

    centered = segment - mean_val
    std = float(np.std(segment))
    if std <= 1e-12:
        skew = 0.0
        kurtosis = 3.0
    else:
        normalized = centered / std
        skew = float(np.mean(np.power(normalized, 3)))
        kurtosis = float(np.mean(np.power(normalized, 4)))

    return [
        round(mean_val, 9),
        round(rms_val, 9),
        round(peak_to_peak, 9),
        round(skew, 9),
        round(kurtosis, 9),
    ]


def write_csv(path: Path, times: np.ndarray, signal: np.ndarray) -> None:
    with path.open("w", newline="") as handle:
        writer = csv.writer(handle)
        writer.writerow(["time_1", "rawsig"])
        for t, value in zip(times, signal, strict=True):
            writer.writerow([f"{t:.9f}", f"{value:.9f}"])


def write_mat(path: Path, times: np.ndarray, signal: np.ndarray) -> bool:
    if savemat is None:
        return False
    matrix = np.column_stack((times, signal))
    savemat(path, {"U": matrix})
    return True


def write_summary(
    json_path: Path,
    seed: int,
    sample_rate_hz: float,
    duration_s: float,
    window_s: float,
    regimes: list[dict[str, object]],
    signal: np.ndarray,
    ci_windows: list[dict[str, object]],
    csv_path: Path,
    mat_path: Path | None,
) -> None:
    summary = {
        "seed": seed,
        "sample_rate_hz": sample_rate_hz,
        "duration_s": duration_s,
        "window_s": window_s,
        "baseline_level": BASELINE_LEVEL,
        "signal_stats": {
            "min": round(float(signal.min()), 9),
            "max": round(float(signal.max()), 9),
            "mean": round(float(signal.mean()), 9),
            "std": round(float(signal.std()), 9),
        },
        "regimes": regimes,
        "ci_windows": ci_windows,
        "csv_path": str(csv_path),
        "mat_path": str(mat_path) if mat_path is not None else None,
    }
    with json_path.open("w") as handle:
        json.dump(summary, handle, indent=2)
        handle.write("\n")


if __name__ == "__main__":
    raise SystemExit(main())
