from pythonfmu import Fmi2Causality, Fmi2Variability
from pythonfmu.fmi2slave import Fmi2Slave
from pythonfmu.variables import Real, Boolean

class Consumer(Fmi2Slave):

    def __init__(self, **kwargs):
        super().__init__(**kwargs)

        # Inputs (fed as start values by the orchestrator)
        self.mean_in = 0.0
        self.std_in = 0.0
        self.min_in = 0.0
        self.max_in = 0.0
        self.rm_in  = 0.0

        self.register_variable(Real("mean_in", causality=Fmi2Causality.parameter,
                                    variability=Fmi2Variability.tunable, start=self.mean_in))
        self.register_variable(Real("std_in", causality=Fmi2Causality.parameter,
                                    variability=Fmi2Variability.tunable, start=self.std_in))
        self.register_variable(Real("min_in", causality=Fmi2Causality.parameter,
                                    variability=Fmi2Variability.tunable, start=self.min_in))
        self.register_variable(Real("max_in", causality=Fmi2Causality.parameter,
                                    variability=Fmi2Variability.tunable, start=self.max_in))
        self.register_variable(Real("rm_in", causality=Fmi2Causality.parameter,
                                    variability=Fmi2Variability.tunable, start=self.rm_in))

        # Outputs
        self.health_score = 0.0
        self.anomaly = False

        self.register_variable(Real("health_score", causality=Fmi2Causality.output,
                                    variability=Fmi2Variability.continuous))
        self.register_variable(Boolean("anomaly", causality=Fmi2Causality.output,
                                       variability=Fmi2Variability.discrete))

    def enter_initialization_mode(self):
        # Simple scoring: high variance or big range lowers score, large rolling-mean drift flags anomaly
        value_range = max(1e-9, self.max_in - self.min_in)
        self.health_score = max(0.0, 100.0 - (self.std_in*2.0 + value_range*0.05))
        self.anomaly = abs(self.rm_in - self.mean_in) > (2.5 * self.std_in if self.std_in > 0 else 1.0)

    def do_step(self, current_time, step_size):
        return True
