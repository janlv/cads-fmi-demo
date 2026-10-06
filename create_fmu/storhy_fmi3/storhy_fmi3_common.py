"""pythonfmu3 glue shared by the STOR-HY FMI 3.0 placeholder FMUs.

pythonfmu3 discovers the FMU class with a regex that requires the literal line
``class X(Fmi3Slave):`` in each model file, so shared behaviour lives in plain
functions here instead of a common base class. The model equations live in
``storhy_fmi3_models.py`` (stdlib only). Nothing in here prints to stdout: the
CADS runner reserves stdout for the result JSON.
"""

from pythonfmu3 import (
    Boolean,
    DefaultExperiment,
    Fmi3Causality,
    Fmi3Variability,
    Float64,
    Int32,
)

from storhy_fmi3_models import BOOLEAN, FLOAT64, INT32, MODEL_SPECS, coerce, new_values

AUTHOR = "NORCE - STOR-HY CADS demo"
MODEL_VERSION = "0.1.0"
DEFAULT_START_S = 0.0
DEFAULT_STOP_S = 86400.0

_VARIABLE_CLASSES = {FLOAT64: Float64, INT32: Int32, BOOLEAN: Boolean}


def _make_variable(slave, name, type_name, causality, variability, description):
    values = slave._storhy_values

    def getter(_name=name):
        return values[_name]

    def setter(value, _name=name, _type=type_name):
        values[_name] = coerce(_type, value)

    return _VARIABLE_CLASSES[type_name](
        name,
        causality=causality,
        variability=variability,
        description=description,
        getter=getter,
        setter=setter,
    )


def setup_model(slave, model_name):
    """Register metadata, DefaultExperiment and all variables on ``slave``."""
    spec = MODEL_SPECS[model_name]
    slave.author = AUTHOR
    slave.version = MODEL_VERSION
    slave.description = spec["description"]
    slave.default_experiment = DefaultExperiment(
        start_time=DEFAULT_START_S, stop_time=DEFAULT_STOP_S, step_size=spec["step_size"]
    )
    slave._storhy_spec = spec
    slave._storhy_values = new_values(model_name)
    slave._storhy_start = DEFAULT_START_S
    slave.time = DEFAULT_START_S

    # FMI 3.0 requires exactly one independent variable.
    slave.register_variable(
        Float64(
            "time",
            causality=Fmi3Causality.independent,
            variability=Fmi3Variability.continuous,
            description="Simulation time [s]",
        )
    )

    for name, (type_name, _, description) in spec["parameters"].items():
        slave.register_variable(
            _make_variable(slave, name, type_name, Fmi3Causality.parameter, Fmi3Variability.tunable, description)
        )

    for group, causality in (("inputs", Fmi3Causality.input), ("outputs", Fmi3Causality.output)):
        for name, (type_name, _, description) in spec[group].items():
            variability = Fmi3Variability.continuous if type_name == FLOAT64 else Fmi3Variability.discrete
            slave.register_variable(_make_variable(slave, name, type_name, causality, variability, description))

    # Outputs must be valid before initialisation (they are read for the
    # modelDescription start values and by masters during init).
    spec["init"](slave._storhy_values, DEFAULT_START_S)


def setup_experiment(slave, start_time):
    slave._storhy_start = float(start_time)
    slave.time = float(start_time)


def initialize_model(slave):
    """(Re)initialise state from the current parameters and inputs."""
    slave.time = slave._storhy_start
    slave._storhy_spec["init"](slave._storhy_values, slave._storhy_start)


def step_model(slave, current_time, step_size):
    """Advance over [t, t + dt]; outputs are left evaluated at t + dt."""
    slave._storhy_spec["step"](slave._storhy_values, float(current_time), float(step_size))
    slave.time = float(current_time) + float(step_size)
    return True
