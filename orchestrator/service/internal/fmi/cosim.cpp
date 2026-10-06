// Co-simulation master (ARCH-COMP-002/003): couples several FMU instances through
// communication points of size H.
//
// Schemes
//   jacobi        (lock-step / parallel): every model reads its inputs from the values exchanged
//                 at t, all models advance to t+H, then all outputs are refreshed.
//   gauss_seidel  (sequential / ping-pong): models advance in listed order and each one reads
//                 the freshest values, including those its predecessors produced for t+H.
//
// Each model advances with its own step h_i (FMU DefaultExperiment stepSize, else H); H must be
// >= h_i, and when H is not a multiple of h_i the last sub-step of an interval is clipped.
// Communication points are integer-indexed (t_k = start + k*H, last one = stop) so no time drift
// accumulates.
//
// Events are evaluated at each communication point on the latest exchanged values. An event
// drives its target to `value` while the condition holds (level) or for one interval after a
// rising edge (pulse), else to `reset`. Every edge is logged in stats.events; the trace gets
// events.<name>.active and the outputs events.<name>.count.
//
// Execution is strictly serial, including Jacobi: the demo FMUs are pythonfmu exports that
// embed one CPython interpreter per process, and calling into them from several threads at once
// is not safe (GIL/thread-state handling is up to the exporter). Jacobi keeps its lock-step data
// semantics; only the wall-clock parallelism is not exploited.
#include "runner_bridge.h"

#include "bridge_common.hpp"
#include "fmu_instance.hpp"

#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <map>
#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace cads {
namespace {

constexpr const char* kEventsModel = "events";

struct CsVarRef {
    std::string model;
    std::string var;
    std::string label() const { return model + "." + var; }
};

struct CsConnection {
    CsVarRef from;
    CsVarRef to;
};

struct CsEvent {
    std::string name;
    CsVarRef lhs;
    int op{};
    double rhs{};
    CsVarRef target;
    double value{1.0};
    double reset{0.0};
    bool pulse{false};
};

struct CsModel {
    std::string name;
    std::string fmuPath;
    std::vector<Assignment> startValues;
    std::optional<InputSeriesConfig> inputSeries;
};

struct CsConfig {
    int scheme{CADS_SCHEME_GAUSS_SEIDEL};
    double start{};
    double stop{};
    double commStep{};
    std::vector<CsModel> models;
    std::vector<CsConnection> connections;
    std::vector<CsEvent> events;
    std::vector<CsVarRef> outputs;
    std::vector<CsVarRef> traceSignals;
    std::optional<double> sampleEvery;
};

// Number of steps of size h needed to cover span, treating near-integer ratios as exact.
size_t stepCount(double span, double h) {
    double ratio = span / h;
    double rounded = std::round(ratio);
    if (rounded >= 1.0 && std::fabs(ratio - rounded) <= 1e-9 * std::max(1.0, ratio)) {
        return static_cast<size_t>(rounded);
    }
    return static_cast<size_t>(std::max(1.0, std::ceil(ratio)));
}

bool isMultiple(double big, double small) {
    double ratio = big / small;
    return std::fabs(ratio - std::round(ratio)) <= 1e-9 * std::max(1.0, ratio);
}

bool compare(double x, int op, double rhs) {
    switch (op) {
        case CADS_OP_LT:
            return x < rhs;
        case CADS_OP_LE:
            return x <= rhs;
        case CADS_OP_GT:
            return x > rhs;
        case CADS_OP_GE:
            return x >= rhs;
        case CADS_OP_EQ:
            return x == rhs;
        case CADS_OP_NE:
            return x != rhs;
    }
    fail("unknown comparison operator code " + std::to_string(op));
}

struct Slot {  // one exchanged scalar, refreshed from its model after it advances
    size_t model;
    std::string var;
    double value{};
};

struct ModelRt {
    const CsModel* cfg{nullptr};
    std::unique_ptr<FmuInstance> fmu;
    double h{};
    std::optional<InputSeriesData> series;
    size_t nextSeries{0};
    std::vector<size_t> slots;             // slots sourced from this model
    std::vector<std::pair<std::string, size_t>> inbound;  // (input var, slot)
    std::vector<size_t> eventTargets;      // events whose target is on this model
};

struct EventRt {
    const CsEvent* cfg{nullptr};
    size_t lhsSlot{};
    size_t targetModel{};
    bool prevCond{false};
    bool active{false};
    int64_t count{0};
};

// Destroys instances in reverse order of creation on every exit path.
struct InstanceGuard {
    std::vector<ModelRt>& models;
    ~InstanceGuard() {
        for (auto it = models.rbegin(); it != models.rend(); ++it) {
            it->fmu.reset();
        }
    }
};

std::string runCoSim(const CsConfig& cfg, std::string& partial) {
    auto wallStart = std::chrono::steady_clock::now();

    if (cfg.models.empty()) {
        fail("co-simulation requires at least one model");
    }
    if (cfg.scheme != CADS_SCHEME_JACOBI && cfg.scheme != CADS_SCHEME_GAUSS_SEIDEL) {
        fail("unknown co-simulation scheme code " + std::to_string(cfg.scheme));
    }
    const double H = cfg.commStep;
    if (!std::isfinite(H) || H <= 0.0) {
        fail("communication_step must be positive");
    }
    if (!std::isfinite(cfg.start) || !std::isfinite(cfg.stop) || cfg.stop <= cfg.start) {
        fail("stop_time must be greater than start_time");
    }
    if ((cfg.stop - cfg.start) / H > 1e8) {
        fail("too many communication points; increase communication_step");
    }
    const double sampleEvery = cfg.sampleEvery ? *cfg.sampleEvery : H;
    if (!std::isfinite(sampleEvery) || sampleEvery <= 0.0) {
        fail("trace sample interval must be positive");
    }

    std::map<std::string, size_t> modelIndex;
    for (size_t i = 0; i < cfg.models.size(); ++i) {
        const std::string& name = cfg.models[i].name;
        if (name.empty()) {
            fail("co-simulation model name cannot be empty");
        }
        if (name == kEventsModel) {
            fail("model name 'events' is reserved");
        }
        if (!modelIndex.emplace(name, i).second) {
            fail("duplicate co-simulation model name '" + name + "'");
        }
    }
    std::map<std::string, size_t> eventIndex;
    for (size_t i = 0; i < cfg.events.size(); ++i) {
        if (!eventIndex.emplace(cfg.events[i].name, i).second) {
            fail("duplicate event name '" + cfg.events[i].name + "'");
        }
    }

    std::vector<ModelRt> models(cfg.models.size());
    InstanceGuard guard{models};
    std::vector<Slot> slots;
    std::map<std::string, size_t> slotIndex;
    std::vector<EventRt> events(cfg.events.size());

    FmuExecutionResult result;
    RunStats stats;
    bool started = false;
    double tCurrent = cfg.start;

    auto collectStats = [&]() {
        stats.fmus.clear();
        for (const auto& m : models) {
            if (!m.fmu) {
                continue;
            }
            FmuInfoRecord r;
            r.model = m.cfg->name;
            r.fmiVersion = m.fmu->meta().fmiVersion;
            r.modelName = m.fmu->meta().modelName;
            r.modelVersion = m.fmu->meta().modelVersion;
            r.guid = m.fmu->meta().guid;
            r.generationTool = m.fmu->meta().generationTool;
            r.declaredStep = m.fmu->meta().declaredStep;
            r.stepUsed = m.fmu->stats().stepUsed;
            r.doStepCalls = m.fmu->stats().doStepCalls;
            r.clippedSubsteps = m.fmu->stats().clippedSubsteps;
            r.doStepWallSeconds = m.fmu->stats().doStepWall;
            stats.fmus.push_back(std::move(r));
        }
        stats.simulatedSeconds = tCurrent - cfg.start;
        stats.wallSeconds = std::chrono::duration<double>(std::chrono::steady_clock::now() - wallStart).count();
    };

    try {
        // ---- open and validate ------------------------------------------------------------
        for (size_t i = 0; i < cfg.models.size(); ++i) {
            ModelRt& m = models[i];
            m.cfg = &cfg.models[i];
            try {
                m.fmu = openFmu(m.cfg->fmuPath);
            } catch (const Cancelled&) {
                throw;
            } catch (const std::exception& ex) {
                fail("model '" + m.cfg->name + "': " + ex.what());
            }
            m.fmu->setLabel(m.cfg->name);
            if (m.cfg->inputSeries) {
                m.series = loadInputSeries(*m.cfg->inputSeries);
            }

            const auto& declared = m.fmu->meta().declaredStep;
            if (declared && !(std::isfinite(*declared) && *declared > 0.0)) {
                fail("model '" + m.cfg->name + "': DefaultExperiment stepSize must be positive");
            }
            m.h = declared ? *declared : H;
            if (m.h > H * (1.0 + 1e-9)) {
                fail("model '" + m.cfg->name + "': declared step " + formatDouble(m.h) +
                     " exceeds communication_step " + formatDouble(H) + " (ARCH-COMP-002)");
            }
            if (!isMultiple(H, m.h)) {
                std::fprintf(stderr,
                             "[cads] WARN %s: communication_step=%g is not a multiple of declared step %g; "
                             "the last sub-step of each interval is clipped\n",
                             m.cfg->name.c_str(), H, m.h);
            }
            m.fmu->stats().stepUsed = m.h;
        }

        auto requireVar = [&](const CsVarRef& ref, const char* where) -> size_t {
            auto it = modelIndex.find(ref.model);
            if (it == modelIndex.end()) {
                fail("unknown model '" + ref.model + "' in " + where + " (" + ref.label() + ")");
            }
            if (!models[it->second].fmu->hasVariable(ref.var)) {
                fail("unknown variable '" + ref.label() + "' in " + where);
            }
            return it->second;
        };
        auto slotFor = [&](const CsVarRef& ref, const char* where) -> size_t {
            size_t mi = requireVar(ref, where);
            auto it = slotIndex.find(ref.label());
            if (it != slotIndex.end()) {
                return it->second;
            }
            slots.push_back(Slot{mi, ref.var, 0.0});
            models[mi].slots.push_back(slots.size() - 1);
            slotIndex.emplace(ref.label(), slots.size() - 1);
            return slots.size() - 1;
        };
        auto requireEventRef = [&](const CsVarRef& ref, const char* suffix, const char* where) {
            const std::string s(suffix);
            if (ref.var.size() <= s.size() || ref.var.compare(ref.var.size() - s.size(), s.size(), s) != 0 ||
                eventIndex.count(ref.var.substr(0, ref.var.size() - s.size())) == 0) {
                fail("unknown variable '" + ref.label() + "' in " + where);
            }
        };

        for (const auto& m : models) {
            for (const auto& sv : m.cfg->startValues) {
                requireVar(CsVarRef{m.cfg->name, sv.name}, "start_values");
            }
        }
        for (const auto& c : cfg.connections) {
            size_t fromSlot = slotFor(c.from, "connections.from");
            size_t toModel = requireVar(c.to, "connections.to");
            models[toModel].inbound.emplace_back(c.to.var, fromSlot);
        }
        for (size_t e = 0; e < cfg.events.size(); ++e) {
            const CsEvent& ev = cfg.events[e];
            events[e].cfg = &ev;
            events[e].lhsSlot = slotFor(ev.lhs, "events.when");
            events[e].targetModel = requireVar(ev.target, "events.set");
            models[events[e].targetModel].eventTargets.push_back(e);
            if (ev.op < CADS_OP_LT || ev.op > CADS_OP_NE) {
                fail("event '" + ev.name + "': unknown comparison operator code " + std::to_string(ev.op));
            }
        }
        for (const auto& ref : cfg.outputs) {
            if (ref.model == kEventsModel) {
                requireEventRef(ref, ".count", "outputs");
            } else {
                requireVar(ref, "outputs");
            }
        }
        for (const auto& ref : cfg.traceSignals) {
            if (ref.model == kEventsModel) {
                requireEventRef(ref, ".active", "trace.signals");
            } else {
                requireVar(ref, "trace.signals");
            }
        }

        // ---- helpers ------------------------------------------------------------------------
        auto applySeriesThrough = [&](ModelRt& m, double time) {
            if (!m.series) {
                return;
            }
            while (m.nextSeries < m.series->points.size() && m.series->points[m.nextSeries].time <= time + 1e-12) {
                for (const auto& entry : m.series->points[m.nextSeries].values) {
                    m.fmu->setNumber(entry.name, entry.value);
                }
                m.nextSeries += 1;
            }
        };
        auto applyConnections = [&](ModelRt& m) {
            for (const auto& [var, slot] : m.inbound) {
                m.fmu->setNumber(var, slots[slot].value);
            }
        };
        auto applyInputs = [&](ModelRt& m, double t) {
            applySeriesThrough(m, t);
            applyConnections(m);
            for (size_t e : m.eventTargets) {
                const EventRt& ev = events[e];
                m.fmu->setNumber(ev.cfg->target.var, ev.active ? ev.cfg->value : ev.cfg->reset);
            }
        };
        auto refresh = [&](ModelRt& m) {
            for (size_t s : m.slots) {
                slots[s].value = m.fmu->getNumber(slots[s].var);
            }
        };
        auto refreshAll = [&]() {
            for (auto& m : models) {
                refresh(m);
            }
        };
        auto evaluateEvents = [&](double t) {
            for (auto& ev : events) {
                bool cond = compare(slots[ev.lhsSlot].value, ev.cfg->op, ev.cfg->rhs);
                bool rising = cond && !ev.prevCond;
                if (rising) {
                    ev.count += 1;
                    stats.events.push_back(EventRecord{ev.cfg->name, t, "rising"});
                } else if (!cond && ev.prevCond) {
                    stats.events.push_back(EventRecord{ev.cfg->name, t, "falling"});
                }
                ev.active = ev.cfg->pulse ? rising : cond;
                ev.prevCond = cond;
            }
        };
        // Advances model m from t to tEnd in sub-steps of m.h (last one clipped). Returns the
        // start time of the sub-step in which the FMU requested termination, if any.
        auto advance = [&](ModelRt& m, double t, double tEnd) -> std::optional<double> {
            size_t n = stepCount(tEnd - t, m.h);
            for (size_t j = 0; j < n; ++j) {
                double s = t + static_cast<double>(j) * m.h;
                double e = (j + 1 == n) ? tEnd : t + static_cast<double>(j + 1) * m.h;
                if (j > 0) {
                    applySeriesThrough(m, s);
                }
                StepResult r = m.fmu->doStep(s, e - s);
                if (r.terminate) {
                    return s;
                }
            }
            return std::nullopt;
        };

        // Trace: user signals, then events.<name>.active for every event.
        std::vector<std::pair<std::string, const CsVarRef*>> traceSignals;
        auto addTrace = [&](const std::string& label, const CsVarRef* ref) {
            for (const auto& entry : traceSignals) {
                if (entry.first == label) {
                    return;
                }
            }
            traceSignals.emplace_back(label, ref);
        };
        for (const auto& ref : cfg.traceSignals) {
            addTrace(ref.label(), &ref);
        }
        for (const auto& ev : cfg.events) {
            addTrace(std::string(kEventsModel) + "." + ev.name + ".active", nullptr);
        }
        auto captureTrace = [&](double t) {
            if (traceSignals.empty()) {
                return;
            }
            result.traceTimes.push_back(t);
            for (const auto& [label, ref] : traceSignals) {
                OutputValue v;
                if (ref && ref->model != kEventsModel) {
                    v = models[modelIndex.at(ref->model)].fmu->get(ref->var);
                } else {
                    // events.<name>.active
                    std::string name = label.substr(std::string(kEventsModel).size() + 1);
                    name = name.substr(0, name.size() - std::string(".active").size());
                    v = OutputValue::boolean(events[eventIndex.at(name)].active);
                }
                result.traceSignals[label].push_back(std::move(v));
            }
        };

        // ---- initialization -----------------------------------------------------------------
        started = true;
        throwIfCancelled();
        for (auto& m : models) {
            m.fmu->instantiate(m.cfg->name);
            m.fmu->enterInit(cfg.start, cfg.stop);
            for (const auto& sv : m.cfg->startValues) {
                m.fmu->setNumber(sv.name, parseNumber(sv.value));
            }
            applySeriesThrough(m, cfg.start);
        }
        // Fixed-point propagation: models.size() passes settle any acyclic connection chain.
        refreshAll();
        for (size_t pass = 0; pass < models.size(); ++pass) {
            for (auto& m : models) {
                applyConnections(m);
                refresh(m);
            }
        }
        for (auto& m : models) {
            m.fmu->exitInit();
        }
        refreshAll();
        evaluateEvents(cfg.start);
        captureTrace(cfg.start);

        // ---- communication loop -------------------------------------------------------------
        const size_t intervals = stepCount(cfg.stop - cfg.start, H);
        auto timeAt = [&](size_t k) { return k >= intervals ? cfg.stop : cfg.start + static_cast<double>(k) * H; };
        size_t traceIndex = 1;
        const double eps = 1e-9 * std::max(1.0, H);

        for (size_t k = 0; k < intervals; ++k) {
            throwIfCancelled();
            const double t = timeAt(k);
            const double tEnd = timeAt(k + 1);
            tCurrent = t;

            std::optional<double> termTime;
            size_t termModel = 0;
            if (cfg.scheme == CADS_SCHEME_JACOBI) {
                for (auto& m : models) {
                    applyInputs(m, t);
                }
                for (size_t i = 0; i < models.size() && !termTime; ++i) {
                    termTime = advance(models[i], t, tEnd);
                    termModel = i;
                }
                refreshAll();
            } else {
                for (size_t i = 0; i < models.size() && !termTime; ++i) {
                    applyInputs(models[i], t);
                    termTime = advance(models[i], t, tEnd);
                    termModel = i;
                    refresh(models[i]);
                }
            }

            if (termTime) {
                // FMI 3 terminateSimulation: stop the master at this point.
                stats.terminatedBy = Termination{models[termModel].cfg->name, *termTime};
                tCurrent = *termTime;
                break;
            }

            tCurrent = tEnd;
            stats.communicationPoints += 1;
            evaluateEvents(tEnd);
            double nextTrace = cfg.start + static_cast<double>(traceIndex) * sampleEvery;
            if (tEnd >= nextTrace - eps) {
                captureTrace(tEnd);
                while (cfg.start + static_cast<double>(traceIndex) * sampleEvery <= tEnd + eps) {
                    traceIndex += 1;
                }
            }
        }
        if (!traceSignals.empty() && (result.traceTimes.empty() || std::fabs(result.traceTimes.back() - tCurrent) > 1e-9)) {
            captureTrace(tCurrent);
        }

        // ---- results ------------------------------------------------------------------------
        if (cfg.outputs.empty()) {
            for (auto& m : models) {
                for (const auto& var : m.fmu->autoOutputs()) {
                    if (m.fmu->hasVariable(var)) {
                        result.values[m.cfg->name + "." + var] = m.fmu->get(var);
                    }
                }
            }
        } else {
            for (const auto& ref : cfg.outputs) {
                if (ref.model == kEventsModel) {
                    continue;  // added below
                }
                result.values[ref.label()] = models[modelIndex.at(ref.model)].fmu->get(ref.var);
            }
        }
        for (const auto& ev : events) {
            result.values[std::string(kEventsModel) + "." + ev.cfg->name + ".count"] = OutputValue::integer(ev.count);
        }

        for (auto& m : models) {
            m.fmu->terminate();
        }
        collectStats();
        return serializeEnvelope(result, stats);
    } catch (...) {
        try {
            result.values.clear();
            if (started) {
                stats.failedAt = tCurrent;
            }
            collectStats();
            partial = serializeEnvelope(result, stats);
        } catch (...) {
            partial.clear();
        }
        throw;
    }
}

// ---- C conversion ---------------------------------------------------------------------------

std::string requireStr(const char* s, const std::string& what) {
    if (!s || s[0] == '\0') {
        fail(what + " is required");
    }
    return s;
}

CsVarRef fromCRef(const cads_varref& ref, const std::string& what) {
    return CsVarRef{requireStr(ref.model, what + " model"), requireStr(ref.var, what + " variable")};
}

std::vector<CsVarRef> fromCRefs(const cads_varref* refs, size_t count, const std::string& what) {
    std::vector<CsVarRef> out;
    if (!refs) {
        return out;
    }
    out.reserve(count);
    for (size_t i = 0; i < count; ++i) {
        out.push_back(fromCRef(refs[i], what));
    }
    return out;
}

CsConfig fromCCoSimConfig(const cads_cosim_config& c) {
    CsConfig cfg;
    cfg.scheme = c.scheme;
    cfg.start = c.start_time;
    cfg.stop = c.stop_time;
    cfg.commStep = c.communication_step;
    if (c.models) {
        for (size_t i = 0; i < c.model_count; ++i) {
            const cads_cosim_model& m = c.models[i];
            CsModel model;
            model.name = requireStr(m.name, "co-simulation model name");
            model.fmuPath = requireStr(m.fmu_path, "FMU path of model '" + model.name + "'");
            if (m.start_values) {
                for (size_t j = 0; j < m.start_value_count; ++j) {
                    const cads_assignment& a = m.start_values[j];
                    if (!a.name || !a.value) {
                        fail("Start values must include both name and value");
                    }
                    model.startValues.push_back({a.name, a.value});
                }
            }
            if (m.input_series) {
                model.inputSeries = InputSeriesConfig{requireStr(m.input_series->csv_path, "Input series CSV path")};
            }
            cfg.models.push_back(std::move(model));
        }
    }
    if (c.connections) {
        for (size_t i = 0; i < c.connection_count; ++i) {
            cfg.connections.push_back(
                CsConnection{fromCRef(c.connections[i].from, "connection source"), fromCRef(c.connections[i].to, "connection target")});
        }
    }
    if (c.events) {
        for (size_t i = 0; i < c.event_count; ++i) {
            const cads_event& e = c.events[i];
            CsEvent ev;
            ev.name = requireStr(e.name, "event name");
            ev.lhs = fromCRef(e.lhs, "event '" + ev.name + "' condition");
            ev.op = e.op;
            ev.rhs = e.rhs;
            ev.target = fromCRef(e.target, "event '" + ev.name + "' target");
            ev.value = e.value;
            ev.reset = e.reset;
            ev.pulse = e.pulse;
            cfg.events.push_back(std::move(ev));
        }
    }
    cfg.outputs = fromCRefs(c.outputs, c.output_count, "output");
    cfg.traceSignals = fromCRefs(c.trace_signals, c.trace_signal_count, "trace signal");
    if (c.has_trace_interval) {
        cfg.sampleEvery = c.trace_interval;
    }
    return cfg;
}

}  // namespace
}  // namespace cads

extern "C" int cads_run_cosim(const cads_cosim_config* cfg, char** json_out, char** err_out) {
    return cads::wrapCall(json_out, err_out, [cfg](std::string& partial) -> std::string {
        if (!cfg) {
            cads::fail("Config pointer is null");
        }
        return cads::runCoSim(cads::fromCCoSimConfig(*cfg), partial);
    });
}
