from pythonfmu import Fmi2Causality, Fmi2Variability, ScalarVariable, Fmi2CoSimulation

class Consumer(Fmi2CoSimulation):

    def __init__(self, **kwargs):
        super().__init__(**kwargs)

        # Inputs (fed as start values by the orchestrator)
        self.mean_in = 0.0
        self.std_in = 0.0
        self.min_in = 0.0
        self.max_in = 0.0
        self.rm_in  = 0.0

        self.add_variable(ScalarVariable("mean_in", causality=Fmi2Causality.parameter, variability=Fmi2Variability.tunable, start=0.0))
        self.add_variable(ScalarVariable("std_in",  causality=Fmi2Causality.parameter, variability=Fmi2Variability.tunable, start=0.0))
        self.add_variable(ScalarVariable("min_in",  causality=Fmi2Causality.parameter, variability=Fmi2Variability.tunable, start=0.0))
        self.add_variable(ScalarVariable("max_in",  causality=Fmi2Causality.parameter, variability=Fmi2Variability.tunable, start=0.0))
        self.add_variable(ScalarVariable("rm_in",   causality=Fmi2Causality.parameter, variability=Fmi2Variability.tunable, start=0.0))

        # Outputs
        self.health_score = 0.0
        self.anomaly = False

        self.add_variable(ScalarVariable("health_score", causality=Fmi2Causality.output, variability=Fmi2Variability.tunable, start=0.0))
        self.add_variable(ScalarVariable("anomaly",       causality=Fmi2Causality.output, variability=Fmi2Variability.discrete, start=False))

    def enterInitializationMode(self):
        # Simple scoring: high variance or big range lowers score, large rolling-mean drift flags anomaly
        value_range = max(1e-9, self.max_in - self.min_in)
        self.health_score = max(0.0, 100.0 - (self.std_in*2.0 + value_range*0.05))
        self.anomaly = abs(self.rm_in - self.mean_in) > (2.5 * self.std_in if self.std_in > 0 else 1.0)
