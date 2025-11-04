from pythonfmu import Fmi2Causality, Fmi2Variability
from pythonfmu.fmi2slave import Fmi2Slave
from pythonfmu.variables import Real, Boolean
import time, os, csv, math, random, statistics
from datetime import datetime, timedelta

class Producer(Fmi2Slave):

    def __init__(self, **kwargs):
        super().__init__(**kwargs)

        # Outputs (exposed as FMI variables)
        self.mean = 0.0
        self.std = 0.0
        self.vmin = 0.0
        self.vmax = 0.0
        self.rollingMean = 0.0
        self.done = False

        self.register_variable(Real("mean", causality=Fmi2Causality.output,
                                    variability=Fmi2Variability.continuous))
        self.register_variable(Real("std", causality=Fmi2Causality.output,
                                    variability=Fmi2Variability.continuous))
        self.register_variable(Real("vmin", causality=Fmi2Causality.output,
                                    variability=Fmi2Variability.continuous))
        self.register_variable(Real("vmax", causality=Fmi2Causality.output,
                                    variability=Fmi2Variability.continuous))
        self.register_variable(Real("rollingMean", causality=Fmi2Causality.output,
                                    variability=Fmi2Variability.continuous))
        self.register_variable(Boolean("done", causality=Fmi2Causality.output,
                                       variability=Fmi2Variability.discrete))

        # Parameters
        self.csv_path = "data/measurements.csv"
        self.duration_sec = 30.0  # wall-clock demo duration

    def enter_initialization_mode(self):
        # Ensure CSV exists (generate synthetic if missing)
        if not os.path.exists(self.csv_path):
            os.makedirs(os.path.dirname(self.csv_path), exist_ok=True)
            start = datetime.now()
            with open(self.csv_path, "w", newline="") as f:
                w = csv.writer(f)
                w.writerow(["timestamp","value"])
                val = 100.0
                for i in range(3000):  # ~3000 points, ~5 min at 0.1s spacing
                    val += random.uniform(-0.5, 0.5) + 0.01*math.sin(i/25.0)
                    t = start + timedelta(seconds=i*0.1)
                    w.writerow([t.isoformat(), f"{val:.3f}"])

        values = []
        with open(self.csv_path, "r") as f:
            rdr = csv.DictReader(f)
            for row in rdr:
                try:
                    values.append(float(row["value"]))
                except Exception:
                    pass

        if not values:
            raise RuntimeError("No data in CSV")

        # Compute features
        self.mean = statistics.fmean(values)
        self.vmin = min(values)
        self.vmax = max(values)
        self.std = statistics.pstdev(values)

        # simple rolling mean (tail 100)
        tail = values[-100:] if len(values) >= 100 else values
        self.rollingMean = statistics.fmean(tail)

        # stash for reporting
        self._start_time = time.time()

    def do_step(self, current_time, step_size):
        # Fake workload for the demo: keep running until ~30 s wall time
        if time.time() - self._start_time >= self.duration_sec:
            self.done = True
        time.sleep(0.1)  # small wait so the demo visibly "runs"
        return True
