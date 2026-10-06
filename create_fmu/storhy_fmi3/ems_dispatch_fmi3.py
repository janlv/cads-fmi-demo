"""FMI 3.0 co-simulation FMU EmsDispatchFmi3 (deterministic placeholder, not validated physics).

Equations and variables: see EmsDispatchFmi3 in storhy_fmi3_models.py.
"""

from pythonfmu3 import Fmi3Slave

from storhy_fmi3_common import initialize_model, setup_experiment, setup_model, step_model


class EmsDispatchFmi3(Fmi3Slave):
    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        setup_model(self, "EmsDispatchFmi3")

    def setup_experiment(self, start_time):
        setup_experiment(self, start_time)

    def enter_initialization_mode(self):
        initialize_model(self)

    def exit_initialization_mode(self):
        initialize_model(self)

    def do_step(self, current_time, step_size):
        return step_model(self, current_time, step_size)
