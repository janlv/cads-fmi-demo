import bisect
import csv
import math
import re
from dataclasses import dataclass
from pathlib import Path

try:
    from pythonfmu import Fmi2Causality, Fmi2Variability
    from pythonfmu.fmi2slave import Fmi2Slave
    from pythonfmu.variables import Integer, Real

    PYTHONFMU_AVAILABLE = True
except ModuleNotFoundError:
    PYTHONFMU_AVAILABLE = False


DATASET_FILES = {
    2: "Test-18000s-ch1-ch2-5s_260204221347248_CH2.csv",
    6: "Trial-interval-Every3600s-For30s-CH3-Ch6_260123103224255_CH6.csv",
}

RAW_DATA_DIR = Path("data") / "ae_event_statistics" / "raw"

METRIC_COLUMNS = {
    "amplitude": "Amplitude",
    "rms": "RMS(mV)",
    "asl": "ASL(dB)",
    "energy": "Energy",
    "frequency_centroid": "Frequency Centroid(kHz)",
    "peak_frequency": "Peak Frequency(kHz)",
    "average_frequency": "AverageFreq",
}


@dataclass(frozen=True)
class AEEvent:
    elapsed_seconds: float
    amplitude: float
    rms: float
    asl: float
    energy: float
    frequency_centroid: float
    peak_frequency: float
    average_frequency: float


@dataclass(frozen=True)
class AEEventTable:
    events: tuple[AEEvent, ...]
    invalid_rows: int
    metadata: dict[str, str]
    source_path: Path

    @property
    def times(self):
        return [event.elapsed_seconds for event in self.events]


def parse_arrival_time(raw):
    parts = str(raw).strip().split(":")
    if len(parts) != 5:
        raise ValueError(f"invalid arrival time {raw!r}")

    day = int(parts[0])
    hour = int(parts[1])
    minute = int(parts[2])
    second = int(parts[3])
    fraction_digits = re.sub(r"\D", "", parts[4])
    fraction = float(f"0.{fraction_digits}") if fraction_digits else 0.0
    return (((day * 24 + hour) * 60 + minute) * 60 + second) + fraction


def resolve_dataset_path(dataset_id, root=None):
    dataset_id = int(dataset_id)
    filename = DATASET_FILES.get(dataset_id)
    if not filename:
        raise ValueError(f"unsupported AE dataset_id {dataset_id}")

    roots = []
    if root:
        roots.append(Path(root))
    roots.extend([Path.cwd(), Path("/app"), Path(__file__).resolve().parents[1]])

    for candidate_root in roots:
        candidate = candidate_root / RAW_DATA_DIR / filename
        if candidate.exists():
            return candidate

    return Path.cwd() / RAW_DATA_DIR / filename


def load_event_table(path):
    path = Path(path)
    lines = path.read_text(encoding="utf-8-sig", errors="replace").splitlines()
    header_index = None
    for index, line in enumerate(lines):
        if line.startswith("Arrival time,"):
            header_index = index
            break
    if header_index is None:
        raise ValueError(f"{path} does not contain an AE event header")

    metadata = {}
    for line in lines[:header_index]:
        if "," not in line or not line.strip():
            continue
        key, value = line.split(",", 1)
        key = key.strip().rstrip(":")
        if key:
            metadata[key] = value.strip()

    events = []
    invalid_rows = 0
    first_arrival = None
    reader = csv.DictReader(lines[header_index:])
    for row in reader:
        try:
            absolute_time = parse_arrival_time(row["Arrival time"])
            values = {
                name: _parse_float(row[column])
                for name, column in METRIC_COLUMNS.items()
            }
        except Exception:
            invalid_rows += 1
            continue

        if first_arrival is None:
            first_arrival = absolute_time
        events.append(
            AEEvent(
                elapsed_seconds=absolute_time - first_arrival,
                amplitude=values["amplitude"],
                rms=values["rms"],
                asl=values["asl"],
                energy=values["energy"],
                frequency_centroid=values["frequency_centroid"],
                peak_frequency=values["peak_frequency"],
                average_frequency=values["average_frequency"],
            )
        )

    events.sort(key=lambda event: event.elapsed_seconds)
    return AEEventTable(tuple(events), invalid_rows, metadata, path)


def summarize_events(table):
    events = table.events
    duration = events[-1].elapsed_seconds if events else 0.0
    event_count = len(events)
    summary = {
        "event_count": event_count,
        "invalid_rows": table.invalid_rows,
        "duration_seconds": duration,
        "event_rate_hz": event_count / duration if duration > 0 else 0.0,
        "energy_sum": sum(event.energy for event in events),
    }

    for source, prefix in [
        ("amplitude", "amplitude"),
        ("rms", "rms"),
        ("asl", "asl"),
    ]:
        values = [getattr(event, source) for event in events]
        summary[f"{prefix}_p50"] = percentile(values, 50)
        summary[f"{prefix}_p95"] = percentile(values, 95)
        summary[f"{prefix}_max"] = max(values) if values else 0.0

    for source, output_name in [
        ("frequency_centroid", "frequency_centroid_p50"),
        ("peak_frequency", "peak_frequency_p50"),
        ("average_frequency", "average_frequency_p50"),
    ]:
        summary[output_name] = percentile([getattr(event, source) for event in events], 50)

    return summary


def rolling_metrics(table, current_time, window_seconds):
    events = table.events
    if not events:
        return {
            "current_time_seconds": current_time,
            "rolling_event_rate_hz": 0.0,
            "rolling_amplitude_p95": 0.0,
            "rolling_rms_p95": 0.0,
            "rolling_asl_p95": 0.0,
            "cumulative_energy": 0.0,
        }

    times = table.times
    end_index = bisect.bisect_right(times, current_time + 1e-12)
    window_start = max(0.0, current_time - max(window_seconds, 0.0))
    start_index = bisect.bisect_left(times, window_start - 1e-12, 0, end_index)
    window_events = events[start_index:end_index]
    elapsed_window = current_time - window_start
    cumulative_events = events[:end_index]

    return {
        "current_time_seconds": current_time,
        "rolling_event_rate_hz": len(window_events) / elapsed_window if elapsed_window > 0 else 0.0,
        "rolling_amplitude_p95": percentile([event.amplitude for event in window_events], 95),
        "rolling_rms_p95": percentile([event.rms for event in window_events], 95),
        "rolling_asl_p95": percentile([event.asl for event in window_events], 95),
        "cumulative_energy": sum(event.energy for event in cumulative_events),
    }


def percentile(values, q):
    values = sorted(value for value in values if math.isfinite(value))
    if not values:
        return 0.0
    if len(values) == 1:
        return values[0]

    rank = (q / 100.0) * (len(values) - 1)
    lower = math.floor(rank)
    upper = math.ceil(rank)
    if lower == upper:
        return values[int(rank)]
    weight = rank - lower
    return values[lower] * (1.0 - weight) + values[upper] * weight


def _parse_float(raw):
    value = float(str(raw).strip())
    if not math.isfinite(value):
        raise ValueError(f"non-finite value {raw!r}")
    return value


if PYTHONFMU_AVAILABLE:

    class AEEventStats(Fmi2Slave):
        OUTPUTS = (
            "event_count",
            "invalid_rows",
            "duration_seconds",
            "event_rate_hz",
            "amplitude_p50",
            "amplitude_p95",
            "amplitude_max",
            "rms_p50",
            "rms_p95",
            "rms_max",
            "asl_p50",
            "asl_p95",
            "asl_max",
            "energy_sum",
            "frequency_centroid_p50",
            "peak_frequency_p50",
            "average_frequency_p50",
            "current_time_seconds",
            "rolling_event_rate_hz",
            "rolling_amplitude_p95",
            "rolling_rms_p95",
            "rolling_asl_p95",
            "cumulative_energy",
        )

        INTEGER_OUTPUTS = {"event_count", "invalid_rows"}

        def __init__(self, **kwargs):
            super().__init__(**kwargs)

            self.dataset_id = 2
            self.window_seconds = 300.0
            self._table = None
            self._summary = None

            self.register_variable(
                Integer(
                    "dataset_id",
                    causality=Fmi2Causality.parameter,
                    variability=Fmi2Variability.fixed,
                    start=self.dataset_id,
                )
            )
            self.register_variable(
                Real(
                    "window_seconds",
                    causality=Fmi2Causality.parameter,
                    variability=Fmi2Variability.fixed,
                    start=self.window_seconds,
                )
            )

            for name in self.OUTPUTS:
                setattr(self, name, 0)
                variable_type = Integer if name in self.INTEGER_OUTPUTS else Real
                self.register_variable(
                    variable_type(
                        name,
                        causality=Fmi2Causality.output,
                        variability=Fmi2Variability.discrete
                        if name in self.INTEGER_OUTPUTS
                        else Fmi2Variability.continuous,
                    )
                )

        def enter_initialization_mode(self):
            self._table = None
            self._summary = None
            for name in self.OUTPUTS:
                setattr(self, name, 0)

        def exit_initialization_mode(self):
            self._ensure_analysis()
            self._update_to_time(0.0)

        def do_step(self, current_time, step_size):
            self._ensure_analysis()
            self._update_to_time(current_time + step_size)
            return True

        def _ensure_analysis(self):
            if self._table is not None and self._summary is not None:
                return

            path = resolve_dataset_path(int(self.dataset_id))
            self._table = load_event_table(path)
            self._summary = summarize_events(self._table)
            for name, value in self._summary.items():
                setattr(self, name, int(value) if name in self.INTEGER_OUTPUTS else float(value))

        def _update_to_time(self, current_time):
            assert self._table is not None
            assert self._summary is not None

            duration = self._summary["duration_seconds"]
            bounded_time = max(0.0, min(float(current_time), float(duration)))
            values = rolling_metrics(self._table, bounded_time, float(self.window_seconds))
            for name, value in values.items():
                setattr(self, name, float(value))
