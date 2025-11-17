from pythonfmu import Fmi2Causality, Fmi2Variability
from pythonfmu.fmi2slave import Fmi2Slave
from pythonfmu.variables import Real, Boolean


class Consumer(Fmi2Slave):
    PARAMS = ("mean_in", "std_in", "min_in", "max_in", "rm_in")

    def __init__(self, **kwargs):
        super().__init__(**kwargs)

        for name in self.PARAMS:
            setattr(self, name, 0.0)
            self.register_variable(
                Real(name, causality=Fmi2Causality.parameter,
                     variability=Fmi2Variability.tunable, start=0.0)
            )

        self.health_score = 0.0
        self.anomaly = False
        self.register_variable(
            Real("health_score", causality=Fmi2Causality.output,
                 variability=Fmi2Variability.continuous)
        )
        self.register_variable(
            Boolean("anomaly", causality=Fmi2Causality.output,
                    variability=Fmi2Variability.discrete)
        )

    def enter_initialization_mode(self):
        # Simple scoring: high variance or big range lowers score, large rolling-mean drift flags anomaly
        value_range = max(1e-9, self.max_in - self.min_in)
        self.health_score = max(0.0, 100.0 - (self.std_in * 2.0 + value_range * 0.05))
        if self.std_in > 0:
            threshold = 2.5 * self.std_in
        else:
            threshold = 1.0
        self.anomaly = abs(self.rm_in - self.mean_in) > threshold

    def do_step(self, current_time, step_size):
        return True
