from pythonfmu import Fmi2Causality, Fmi2Variability
from pythonfmu.fmi2slave import Fmi2Slave
from pythonfmu.variables import Real, Boolean, Integer
import os, csv, math, random, statistics
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

        # Tunable parameter
        self.num_points = 10000

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
        self.register_variable(Integer("num_points", causality=Fmi2Causality.parameter,
                                       variability=Fmi2Variability.fixed, start=self.num_points))

        # Parameters
        self.csv_path = "data/measurements.csv"
        self._has_run = False

    def enter_initialization_mode(self):
        self._has_run = False

    def _ensure_csv(self):
        if os.path.exists(self.csv_path):
            return
        self._generate_csv()

    def _generate_csv(self, num_points=3000):
        os.makedirs(os.path.dirname(self.csv_path), exist_ok=True)
        start = datetime.now()
        with open(self.csv_path, "w", newline="") as f:
            f.write(f"# num_points={num_points}\n")
            w = csv.writer(f)
            w.writerow(["timestamp","value"])
            val = 100.0
            for i in range(num_points):  # ~num_points points, ~5 min at 0.1s spacing
                val += random.uniform(-0.5, 0.5) + 0.01*math.sin(i/25.0)
                t = start + timedelta(seconds=i*0.1)
                w.writerow([t.isoformat(), f"{val:.3f}"])

    def _load_values(self):
        values = []
        with open(self.csv_path, "r") as f:
            #self.logger.info("producer", f"Reading data from {self.csv_path}")
            filtered = (line for line in f if not line.lstrip().startswith("#"))
            rdr = csv.DictReader(filtered)
            for row in rdr:
                try:
                    values.append(float(row["value"]))
                except Exception:
                    pass
            #self.logger.info("producer", f"Read {len(values)} data points")
        return values

    def _update_statistics(self, values):
        if not values:
            raise RuntimeError("No data in CSV")

        self.mean = statistics.fmean(values)
        self.vmin = min(values)
        self.vmax = max(values)
        self.std = statistics.pstdev(values)

        tail = values[-100:] if len(values) >= 100 else values
        self.rollingMean = statistics.fmean(tail)

    def do_step(self, current_time, step_size):
        num_points = max(1, int(self.num_points))
        if not self._has_run:
            #self._ensure_csv()
            self._generate_csv(num_points)
            values = self._load_values()
            self._update_statistics(values)
            self._has_run = True
        self.done = True
        return True
