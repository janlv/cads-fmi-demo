// Single-FMU execution path and the C entry points of the FMIL bridge.
//
// Layout of the bridge:
//   bridge_common.{hpp,cpp}  JSON, input series, cancel flag, wrapCall
//   fmu_instance.{hpp,cpp}   FmuInstance (FMI 2.0 / 3.0 behind one API), openFmu
//   runner_bridge.cpp        runSingle + cads_run_fmu, cancel entry points
//   cosim.cpp                co-simulation master + cads_run_cosim
#include "runner_bridge.h"

#include "bridge_common.hpp"
#include "fmu_instance.hpp"

#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace cads {
namespace {

struct TraceConfig {
    std::vector<std::string> outputs;
    std::vector<std::string> inputs;
    std::optional<double> sampleEvery;
};

struct Config {
    std::string fmuPath;
    std::optional<double> startTime;
    std::optional<double> stopTime;
    std::optional<double> stepSize;
    std::vector<Assignment> startValues;
    std::vector<std::string> outputs;
    std::optional<InputSeriesConfig> inputSeries;
    TraceConfig trace;
};

struct StepTimings {
    double start;
    double stop;
    double step;
};

std::vector<std::string> buildTraceNames(const TraceConfig& trace) {
    std::vector<std::string> names;
    auto appendUnique = [&names](const std::vector<std::string>& values) {
        for (const auto& value : values) {
            if (std::find(names.begin(), names.end(), value) == names.end()) {
                names.push_back(value);
            }
        }
    };
    appendUnique(trace.outputs);
    appendUnique(trace.inputs);
    return names;
}

double resolveTraceInterval(const Config& cfg, const StepTimings& timings) {
    if (cfg.trace.sampleEvery) {
        if (*cfg.trace.sampleEvery <= 0.0) {
            fail("trace sample interval must be positive");
        }
        return *cfg.trace.sampleEvery;
    }
    if (timings.step > 0.0) {
        return timings.step;
    }
    return std::max(1e-3, timings.stop - timings.start);
}

// Start/stop: YAML, else DefaultExperiment, else defaults; an input series supplies missing
// start/stop. Step (ARCH-COMP-002): the FMU's DefaultExperiment stepSize wins, the YAML
// step_size is only a fallback, then the series sample spacing, then max(1e-3, stop-start).
StepTimings deriveTimings(const FmuMeta& meta, const Config& cfg, const std::optional<InputSeriesData>& series,
                          const std::string& label) {
    StepTimings t{};
    t.start = cfg.startTime ? *cfg.startTime : meta.defaultStart.value_or(0.0);
    t.stop = cfg.stopTime ? *cfg.stopTime : (meta.defaultStop ? *meta.defaultStop : t.start + 1.0);

    if (meta.declaredStep) {
        t.step = *meta.declaredStep;
        if (cfg.stepSize && std::fabs(*cfg.stepSize - *meta.declaredStep) >
                                1e-12 * std::max(std::fabs(*cfg.stepSize), std::fabs(*meta.declaredStep))) {
            std::fprintf(stderr,
                         "[cads] WARN %s: YAML step_size=%g ignored, FMU DefaultExperiment stepSize=%g wins (ARCH-COMP-002)\n",
                         label.c_str(), *cfg.stepSize, *meta.declaredStep);
        }
    } else if (cfg.stepSize) {
        t.step = *cfg.stepSize;
    } else {
        t.step = std::max(1e-3, (t.stop - t.start));
    }

    if (series && !series->points.empty()) {
        if (!cfg.startTime) {
            t.start = series->points.front().time;
        }
        if (!cfg.stopTime) {
            t.stop = series->points.back().time;
        }
        if (!meta.declaredStep && !cfg.stepSize) {
            if (series->points.size() > 1) {
                double derived = series->points[1].time - series->points[0].time;
                if (derived > 0.0) {
                    t.step = derived;
                }
            } else {
                t.step = std::max(1e-3, t.stop - t.start);
            }
        }
    }

    if (t.step <= 0.0) {
        t.step = (t.stop - t.start);
        if (t.step <= 0.0) {
            t.step = 1.0;
        }
    }
    return t;
}

FmuInfoRecord infoRecord(const std::string& model, const FmuInstance& fmu) {
    FmuInfoRecord r;
    r.model = model;
    r.fmiVersion = fmu.meta().fmiVersion;
    r.modelName = fmu.meta().modelName;
    r.modelVersion = fmu.meta().modelVersion;
    r.guid = fmu.meta().guid;
    r.generationTool = fmu.meta().generationTool;
    r.declaredStep = fmu.meta().declaredStep;
    r.stepUsed = fmu.stats().stepUsed;
    r.doStepCalls = fmu.stats().doStepCalls;
    r.clippedSubsteps = fmu.stats().clippedSubsteps;
    r.doStepWallSeconds = fmu.stats().doStepWall;
    return r;
}

// Runs one FMU from start to stop and returns the {"values","stats"} envelope. In stats,
// "model" (fmus[].model, terminated_by.model) is empty: the caller knows the step name.
std::string runSingle(const Config& cfg) {
    auto wallStart = std::chrono::steady_clock::now();
    std::unique_ptr<FmuInstance> fmu = openFmu(cfg.fmuPath);
    fmu->instantiate("cads-runner");

    std::optional<InputSeriesData> inputSeries;
    if (cfg.inputSeries) {
        inputSeries = loadInputSeries(*cfg.inputSeries);
    }

    StepTimings timings = deriveTimings(fmu->meta(), cfg, inputSeries, fmu->label());
    fmu->stats().stepUsed = timings.step;

    fmu->enterInit(timings.start, timings.stop);
    for (const auto& entry : cfg.startValues) {
        fmu->setNumber(entry.name, parseNumber(entry.value));
    }

    size_t nextInputIndex = 0;
    auto applySeriesThrough = [&](double time) {
        if (!inputSeries) {
            return;
        }
        while (nextInputIndex < inputSeries->points.size() &&
               inputSeries->points[nextInputIndex].time <= time + 1e-12) {
            for (const auto& entry : inputSeries->points[nextInputIndex].values) {
                fmu->setNumber(entry.name, entry.value);
            }
            nextInputIndex += 1;
        }
    };
    applySeriesThrough(timings.start);
    fmu->exitInit();

    FmuExecutionResult result;
    RunStats stats;
    std::vector<std::string> traceNames = buildTraceNames(cfg.trace);
    double traceInterval = traceNames.empty() ? 0.0 : resolveTraceInterval(cfg, timings);
    auto captureTrace = [&](double time) {
        if (traceNames.empty()) {
            return;
        }
        result.traceTimes.push_back(time);
        for (const auto& name : traceNames) {
            result.traceSignals[name].push_back(fmu->get(name));
        }
    };

    double current = timings.start;
    if (!traceNames.empty()) {
        captureTrace(current);
    }
    double nextTraceTime = current + traceInterval;
    while (current < timings.stop - 1e-12) {
        throwIfCancelled();
        double next = std::min(current + timings.step, timings.stop);
        if (!traceNames.empty() && nextTraceTime < next - 1e-12) {
            next = nextTraceTime;
        }
        if (next <= current + 1e-12) {
            applySeriesThrough(current);
            if (!traceNames.empty() && nextTraceTime <= current + 1e-12) {
                captureTrace(current);
                nextTraceTime += traceInterval;
                continue;
            }
            fail(fmu->meta().fmiVersion == "3.0" ? "fmi3 execution stalled due to zero-length step"
                                                 : "fmi2 execution stalled due to zero-length step");
        }
        StepResult step = fmu->doStep(current, next - current);
        if (step.terminate) {
            // FMI 3: the FMU asked to end the simulation; stop here as before.
            stats.terminatedBy = Termination{"", current};
            break;
        }
        current = next;
        applySeriesThrough(current);
        if (!traceNames.empty() && nextTraceTime <= current + 1e-12) {
            captureTrace(current);
            nextTraceTime += traceInterval;
        }
    }

    if (!traceNames.empty() && (result.traceTimes.empty() || std::fabs(result.traceTimes.back() - timings.stop) > 1e-9)) {
        captureTrace(timings.stop);
    }

    std::vector<std::string> outputs = cfg.outputs.empty() ? fmu->autoOutputs() : cfg.outputs;
    for (const auto& name : outputs) {
        result.values[name] = fmu->get(name);
    }

    fmu->terminate();

    stats.simulatedSeconds = current - timings.start;
    stats.communicationPoints = fmu->stats().doStepCalls;
    stats.fmus.push_back(infoRecord("", *fmu));
    fmu.reset();
    stats.wallSeconds = std::chrono::duration<double>(std::chrono::steady_clock::now() - wallStart).count();
    return serializeEnvelope(result, stats);
}

std::vector<std::string> copyNames(const char* const* names, size_t count, const char* what) {
    std::vector<std::string> out;
    if (!names || count == 0) {
        return out;
    }
    out.reserve(count);
    for (size_t i = 0; i < count; ++i) {
        if (!names[i]) {
            fail(std::string(what) + " name cannot be null");
        }
        out.emplace_back(names[i]);
    }
    return out;
}

Config fromCConfig(const cads_fmu_config& cfg) {
    Config result;
    if (!cfg.fmu_path) {
        fail("FMU path is required");
    }
    result.fmuPath = cfg.fmu_path;
    if (cfg.has_start_time) {
        result.startTime = cfg.start_time;
    }
    if (cfg.has_stop_time) {
        result.stopTime = cfg.stop_time;
    }
    if (cfg.has_step_size) {
        result.stepSize = cfg.step_size;
    }
    if (cfg.start_values && cfg.start_value_count > 0) {
        result.startValues.reserve(cfg.start_value_count);
        for (size_t i = 0; i < cfg.start_value_count; ++i) {
            const cads_assignment& entry = cfg.start_values[i];
            if (!entry.name || !entry.value) {
                fail("Start values must include both name and value");
            }
            result.startValues.push_back({entry.name, entry.value});
        }
    }
    if (cfg.input_series) {
        if (!cfg.input_series->csv_path || cfg.input_series->csv_path[0] == '\0') {
            fail("Input series CSV path is required");
        }
        result.inputSeries = InputSeriesConfig{cfg.input_series->csv_path};
    }
    result.outputs = copyNames(cfg.outputs, cfg.output_count, "Output");
    result.trace.outputs = copyNames(cfg.trace_outputs, cfg.trace_output_count, "Trace output");
    result.trace.inputs = copyNames(cfg.trace_inputs, cfg.trace_input_count, "Trace input");
    if (cfg.has_trace_interval) {
        result.trace.sampleEvery = cfg.trace_interval;
    }
    return result;
}

}  // namespace
}  // namespace cads

extern "C" int cads_run_fmu(const cads_fmu_config* cfg, char** json_out, char** err_out) {
    return cads::wrapCall(json_out, err_out, [cfg](std::string&) -> std::string {
        if (!cfg) {
            cads::fail("Config pointer is null");
        }
        return cads::runSingle(cads::fromCConfig(*cfg));
    });
}

extern "C" void cads_free_string(char* ptr) {
    std::free(ptr);
}

extern "C" void cads_request_cancel(void) {
    cads::requestCancel();
}

extern "C" void cads_reset_cancel(void) {
    cads::resetCancel();
}
