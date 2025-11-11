#include "runner_bridge.h"

#include <FMI/fmi_import_context.h>
#include <FMI2/fmi2_import.h>
#include <FMI2/fmi2_import_capi.h>
#include <FMI2/fmi2_import_convenience.h>
#include <FMI2/fmi2_import_variable_list.h>
#include <FMI3/fmi3_import.h>
#include <FMI3/fmi3_import_capi.h>
#include <FMI3/fmi3_import_convenience.h>
#include <FMI3/fmi3_import_variable_list.h>
#include <JM/jm_callbacks.h>

#include <unistd.h>
#include <dlfcn.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstdarg>
#include <cstring>
#include <filesystem>
#include <map>
#include <mutex>
#include <optional>
#include <sstream>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace fs = std::filesystem;

struct Assignment {
    std::string name;
    std::string value;
};

struct Config {
    std::string fmuPath;
    std::optional<double> startTime;
    std::optional<double> stopTime;
    std::optional<double> stepSize;
    std::vector<Assignment> startValues;
    std::vector<std::string> outputs;
};

struct OutputValue {
    enum class Type { Real, Integer, Boolean } type;
    double realVal{};
    int intVal{};
    bool boolVal{};
};

struct FmuExecutionResult {
    std::map<std::string, OutputValue> values;
};

void preloadLibPythonIfAvailable() {
    static std::once_flag once;
    std::call_once(once, [] {
        auto tryLoad = [](const char* candidate) -> bool {
            if (!candidate || candidate[0] == '\0') {
                return false;
            }
            void* handle = dlopen(candidate, RTLD_NOW | RTLD_GLOBAL);
            if (handle) {
                static std::vector<void*> loadedHandles;
                loadedHandles.push_back(handle);
                return true;
            }
            return false;
        };

        const char* envHint = std::getenv("CADS_LIBPYTHON_HINT");
        if (tryLoad(envHint)) {
            return;
        }

        constexpr const char* kDefaultCandidates[] = {
            "libpython3.12.so.1.0",
            "libpython3.12.so",
            "libpython3.11.so.1.0",
            "libpython3.11.so",
            "libpython3.10.so.1.0",
            "libpython3.10.so",
        };
        for (const char* candidate : kDefaultCandidates) {
            if (tryLoad(candidate)) {
                return;
            }
        }
    });
}

void fmi2LoggerCallback(
    fmi2_component_environment_t,
    fmi2_string_t instanceName,
    fmi2_status_t,
    fmi2_string_t category,
    fmi2_string_t message,
    ...) {
    va_list args;
    va_start(args, message);
    const char* inst = instanceName ? instanceName : "-";
    const char* cat = category ? category : "-";
    std::fprintf(stderr, "[FMI2][%s][%s] ", inst, cat);
    std::vfprintf(stderr, message, args);
    std::fprintf(stderr, "\n");
    va_end(args);
}

[[noreturn]] void fail(const std::string& msg) {
    throw std::runtime_error(msg);
}

double parseNumber(const std::string& input) {
    char* end = nullptr;
    double val = std::strtod(input.c_str(), &end);
    if (!end || *end != '\0' || !std::isfinite(val)) {
        fail("Unable to parse numeric value from '" + input + "'");
    }
    return val;
}

std::string makeTempDir() {
    fs::path base = fs::temp_directory_path();
    std::string templ = (base / "cads-fmi-XXXXXX").string();
    std::vector<char> buf(templ.begin(), templ.end());
    buf.push_back('\0');
    char* res = mkdtemp(buf.data());
    if (!res) {
        fail("Failed to create temporary directory");
    }
    return std::string(res);
}

struct ScopedTempDir {
    std::string path;
    explicit ScopedTempDir(std::string p) : path(std::move(p)) {}
    ~ScopedTempDir() {
        std::error_code ec;
        if (!path.empty()) {
            fs::remove_all(path, ec);
        }
    }
};

struct ScopedCtx {
    fmi_import_context_t* ctx{nullptr};
    explicit ScopedCtx(jm_callbacks* cb) : ctx(fmi_import_allocate_context(cb)) {}
    ~ScopedCtx() {
        if (ctx) {
            fmi_import_free_context(ctx);
        }
    }
};

struct ScopedFmu2 {
    fmi2_import_t* fmu{nullptr};
    explicit ScopedFmu2(fmi2_import_t* f) : fmu(f) {}
    ~ScopedFmu2() {
        if (fmu) {
            fmi2_import_free(fmu);
        }
    }
};

struct ScopedFmu3 {
    fmi3_import_t* fmu{nullptr};
    explicit ScopedFmu3(fmi3_import_t* f) : fmu(f) {}
    ~ScopedFmu3() {
        if (fmu) {
            fmi3_import_free(fmu);
        }
    }
};

std::string serializeJson(const FmuExecutionResult& result) {
    std::ostringstream oss;
    oss << "{";
    bool first = true;
    for (const auto& [name, value] : result.values) {
        if (!first) {
            oss << ",";
        }
        first = false;
        oss << "\"" << name << "\":";
        switch (value.type) {
            case OutputValue::Type::Real:
                oss << value.realVal;
                break;
            case OutputValue::Type::Integer:
                oss << value.intVal;
                break;
            case OutputValue::Type::Boolean:
                oss << (value.boolVal ? "true" : "false");
                break;
        }
    }
    oss << "}";
    return oss.str();
}

struct StepTimings {
    double start;
    double stop;
    double step;
};

StepTimings deriveTimingsFmi2(fmi2_import_t* fmu, const Config& cfg) {
    StepTimings t{};
    if (cfg.startTime) {
        t.start = *cfg.startTime;
    } else if (fmi2_import_get_default_experiment_has_start(fmu)) {
        t.start = fmi2_import_get_default_experiment_start(fmu);
    } else {
        t.start = 0.0;
    }

    if (cfg.stopTime) {
        t.stop = *cfg.stopTime;
    } else if (fmi2_import_get_default_experiment_has_stop(fmu)) {
        t.stop = fmi2_import_get_default_experiment_stop(fmu);
    } else {
        t.stop = t.start + 1.0;
    }

    if (cfg.stepSize) {
        t.step = *cfg.stepSize;
    } else if (fmi2_import_get_default_experiment_has_step(fmu)) {
        t.step = fmi2_import_get_default_experiment_step(fmu);
    } else {
        t.step = std::max(1e-3, (t.stop - t.start));
    }
    return t;
}

StepTimings deriveTimingsFmi3(fmi3_import_t* fmu, const Config& cfg) {
    StepTimings t{};
    if (cfg.startTime) {
        t.start = *cfg.startTime;
    } else if (fmi3_import_get_default_experiment_has_start(fmu)) {
        t.start = fmi3_import_get_default_experiment_start(fmu);
    } else {
        t.start = 0.0;
    }

    if (cfg.stopTime) {
        t.stop = *cfg.stopTime;
    } else if (fmi3_import_get_default_experiment_has_stop(fmu)) {
        t.stop = fmi3_import_get_default_experiment_stop(fmu);
    } else {
        t.stop = t.start + 1.0;
    }

    if (cfg.stepSize) {
        t.step = *cfg.stepSize;
    } else if (fmi3_import_get_default_experiment_has_step_size(fmu)) {
        t.step = fmi3_import_get_default_experiment_step_size(fmu);
    } else {
        t.step = std::max(1e-3, (t.stop - t.start));
    }
    return t;
}

std::vector<std::string> autoOutputsFmi2(fmi2_import_t* fmu) {
    std::vector<std::string> names;
    fmi2_import_variable_list_t* list = fmi2_import_get_variable_list(fmu, 0);
    size_t n = fmi2_import_get_variable_list_size(list);
    for (size_t i = 0; i < n; ++i) {
        fmi2_import_variable_t* var = fmi2_import_get_variable(list, i);
        fmi2_causality_enu_t causality = fmi2_import_get_causality(var);
        if (causality == fmi2_causality_enu_output || causality == fmi2_causality_enu_calculated_parameter) {
            names.emplace_back(fmi2_import_get_variable_name(var));
        }
    }
    fmi2_import_free_variable_list(list);
    if (names.empty()) {
        names.push_back("time");
    }
    return names;
}

std::vector<std::string> autoOutputsFmi3(fmi3_import_t* fmu) {
    std::vector<std::string> names;
    fmi3_import_variable_list_t* list = fmi3_import_get_variable_list(fmu, 0);
    size_t n = fmi3_import_get_variable_list_size(list);
    for (size_t i = 0; i < n; ++i) {
        fmi3_import_variable_t* var = fmi3_import_get_variable(list, i);
        fmi3_causality_enu_t causality = fmi3_import_get_variable_causality(var);
        if (causality == fmi3_causality_enu_output || causality == fmi3_causality_enu_calculated_parameter) {
            names.emplace_back(fmi3_import_get_variable_name(var));
        }
    }
    fmi3_import_free_variable_list(list);
    if (names.empty()) {
        names.push_back("time");
    }
    return names;
}

void applyStartValueFmi2(fmi2_import_t* fmu, const Assignment& assign) {
    fmi2_import_variable_t* var = fmi2_import_get_variable_by_name(fmu, assign.name.c_str());
    if (!var) {
        fail("Unknown variable '" + assign.name + "'");
    }
    fmi2_value_reference_t vr = fmi2_import_get_variable_vr(var);
    fmi2_base_type_enu_t baseType = fmi2_import_get_variable_base_type(var);
    double val = parseNumber(assign.value);
    switch (baseType) {
        case fmi2_base_type_real: {
            fmi2_real_t v = static_cast<fmi2_real_t>(val);
            if (fmi2_import_set_real(fmu, &vr, 1, &v) != fmi2_status_ok) {
                fail("Failed setting real " + assign.name);
            }
            break;
        }
        case fmi2_base_type_int: {
            fmi2_integer_t intVal = static_cast<fmi2_integer_t>(std::llround(val));
            if (fmi2_import_set_integer(fmu, &vr, 1, &intVal) != fmi2_status_ok) {
                fail("Failed setting integer " + assign.name);
            }
            break;
        }
        case fmi2_base_type_bool: {
            fmi2_boolean_t boolVal = (val != 0.0) ? fmi2_true : fmi2_false;
            if (fmi2_import_set_boolean(fmu, &vr, 1, &boolVal) != fmi2_status_ok) {
                fail("Failed setting boolean " + assign.name);
            }
            break;
        }
        default:
            fail("Unsupported base type for " + assign.name);
    }
}

OutputValue readOutputFmi2(fmi2_import_t* fmu, const std::string& name) {
    fmi2_import_variable_t* var = fmi2_import_get_variable_by_name(fmu, name.c_str());
    if (!var) {
        fail("Output variable '" + name + "' not found");
    }
    fmi2_value_reference_t vr = fmi2_import_get_variable_vr(var);
    fmi2_base_type_enu_t baseType = fmi2_import_get_variable_base_type(var);
    OutputValue ov{};
    switch (baseType) {
        case fmi2_base_type_real: {
            fmi2_real_t value{};
            fmi2_import_get_real(fmu, &vr, 1, &value);
            ov.type = OutputValue::Type::Real;
            ov.realVal = value;
            break;
        }
        case fmi2_base_type_int: {
            fmi2_integer_t iv{};
            fmi2_import_get_integer(fmu, &vr, 1, &iv);
            ov.type = OutputValue::Type::Integer;
            ov.intVal = iv;
            break;
        }
        case fmi2_base_type_bool: {
            fmi2_boolean_t bv{};
            fmi2_import_get_boolean(fmu, &vr, 1, &bv);
            ov.type = OutputValue::Type::Boolean;
            ov.boolVal = (bv != fmi2_false);
            break;
        }
        default:
            fail("Unsupported output type for " + name);
    }
    return ov;
}

FmuExecutionResult runFmi2(const Config& cfg, const std::string& unpackDir, fmi_import_context_t* ctx) {
    ScopedFmu2 fmu(fmi2_import_parse_xml(ctx, unpackDir.c_str(), nullptr));
    if (!fmu.fmu) {
        fail("Failed parsing FMI2 XML");
    }

    if (fmi2_import_get_fmu_kind(fmu.fmu) != fmi2_fmu_kind_cs) {
        fail("FMU is not Co-Simulation");
    }

    fmi2_callback_functions_t callbacks{};
    callbacks.allocateMemory = calloc;
    callbacks.freeMemory = free;
    callbacks.logger = fmi2LoggerCallback;
    callbacks.componentEnvironment = nullptr;

    if (fmi2_import_create_dllfmu(fmu.fmu, fmi2_fmu_kind_cs, &callbacks) != jm_status_success) {
        fail("Failed loading FMU binaries");
    }

    if (fmi2_import_instantiate(fmu.fmu, "cads-runner", fmi2_cosimulation, nullptr, fmi2_false) != jm_status_success) {
        fail("Failed to instantiate FMI2 FMU");
    }

    StepTimings timings = deriveTimingsFmi2(fmu.fmu, cfg);
    if (timings.step <= 0.0) {
        timings.step = (timings.stop - timings.start);
        if (timings.step <= 0.0) {
            timings.step = 1.0;
        }
    }

    double tolerance = fmi2_import_get_default_experiment_has_tolerance(fmu.fmu)
                           ? fmi2_import_get_default_experiment_tolerance(fmu.fmu)
                           : 1e-4;

    if (fmi2_import_setup_experiment(fmu.fmu, fmi2_true, tolerance, timings.start, fmi2_true, timings.stop) != fmi2_status_ok) {
        fail("fmi2_setup_experiment failed");
    }

    if (fmi2_import_enter_initialization_mode(fmu.fmu) != fmi2_status_ok) {
        fail("Failed entering initialization mode");
    }

    for (const auto& entry : cfg.startValues) {
        applyStartValueFmi2(fmu.fmu, entry);
    }

    if (fmi2_import_exit_initialization_mode(fmu.fmu) != fmi2_status_ok) {
        fail("Failed exiting initialization mode");
    }

    double current = timings.start;
    while (current < timings.stop - 1e-12) {
        double step = std::min(timings.step, timings.stop - current);
        if (fmi2_import_do_step(fmu.fmu, current, step, fmi2_true) != fmi2_status_ok) {
            fail("fmi2_do_step failed");
        }
        current += step;
    }

    FmuExecutionResult result;
    std::vector<std::string> outputs = cfg.outputs.empty() ? autoOutputsFmi2(fmu.fmu) : cfg.outputs;
    for (const auto& name : outputs) {
        result.values[name] = readOutputFmi2(fmu.fmu, name);
    }

    fmi2_import_terminate(fmu.fmu);
    fmi2_import_free_instance(fmu.fmu);
    fmi2_import_destroy_dllfmu(fmu.fmu);
    return result;
}

void applyStartValueFmi3(fmi3_import_t* fmu, const Assignment& assign) {
    fmi3_import_variable_t* var = fmi3_import_get_variable_by_name(fmu, assign.name.c_str());
    if (!var) {
        fail("Unknown variable '" + assign.name + "'");
    }
    fmi3_value_reference_t vr = fmi3_import_get_variable_vr(var);
    fmi3_base_type_enu_t baseType = fmi3_import_get_variable_base_type(var);
    double val = parseNumber(assign.value);

    switch (baseType) {
        case fmi3_base_type_float64: {
            fmi3_float64_t v = static_cast<fmi3_float64_t>(val);
            if (fmi3_import_set_float64(fmu, &vr, 1, &v, 1) != fmi3_status_ok) {
                fail("Failed setting real " + assign.name);
            }
            break;
        }
        case fmi3_base_type_int32: {
            fmi3_int32_t iv = static_cast<fmi3_int32_t>(std::llround(val));
            if (fmi3_import_set_int32(fmu, &vr, 1, &iv, 1) != fmi3_status_ok) {
                fail("Failed setting integer " + assign.name);
            }
            break;
        }
        case fmi3_base_type_bool: {
            fmi3_boolean_t bv = (val != 0.0) ? fmi3_true : fmi3_false;
            if (fmi3_import_set_boolean(fmu, &vr, 1, &bv, 1) != fmi3_status_ok) {
                fail("Failed setting boolean " + assign.name);
            }
            break;
        }
        default:
            fail("Unsupported FMI3 base type for " + assign.name);
    }
}

OutputValue readOutputFmi3(fmi3_import_t* fmu, const std::string& name) {
    fmi3_import_variable_t* var = fmi3_import_get_variable_by_name(fmu, name.c_str());
    if (!var) {
        fail("Output variable '" + name + "' not found");
    }
    fmi3_value_reference_t vr = fmi3_import_get_variable_vr(var);
    fmi3_base_type_enu_t baseType = fmi3_import_get_variable_base_type(var);
    OutputValue ov{};
    switch (baseType) {
        case fmi3_base_type_float64: {
            fmi3_float64_t value{};
            fmi3_import_get_float64(fmu, &vr, 1, &value, 1);
            ov.type = OutputValue::Type::Real;
            ov.realVal = value;
            break;
        }
        case fmi3_base_type_int32: {
            fmi3_int32_t iv{};
            fmi3_import_get_int32(fmu, &vr, 1, &iv, 1);
            ov.type = OutputValue::Type::Integer;
            ov.intVal = iv;
            break;
        }
        case fmi3_base_type_bool: {
            fmi3_boolean_t bv{};
            fmi3_import_get_boolean(fmu, &vr, 1, &bv, 1);
            ov.type = OutputValue::Type::Boolean;
            ov.boolVal = (bv != fmi3_false);
            break;
        }
        default:
            fail("Unsupported output type for " + name);
    }
    return ov;
}

FmuExecutionResult runFmi3(const Config& cfg, const std::string& unpackDir, fmi_import_context_t* ctx) {
    ScopedFmu3 fmu(fmi3_import_parse_xml(ctx, unpackDir.c_str(), nullptr));
    if (!fmu.fmu) {
        fail("Failed parsing FMI3 XML");
    }
    if (fmi3_import_get_fmu_kind(fmu.fmu) != fmi3_fmu_kind_cs) {
        fail("FMI3 FMU is not Co-Simulation");
    }

    if (fmi3_import_create_dllfmu(fmu.fmu, fmi3_fmu_kind_cs, nullptr, nullptr) != jm_status_success) {
        fail("Failed loading FMI3 binaries");
    }

    if (fmi3_import_instantiate_co_simulation(
            fmu.fmu, "cads-runner", nullptr, fmi3_false, fmi3_false,
            fmi3_false, fmi3_false, nullptr, 0, nullptr) != jm_status_success) {
        fail("Failed instantiating FMI3 FMU");
    }

    StepTimings timings = deriveTimingsFmi3(fmu.fmu, cfg);
    if (timings.step <= 0.0) {
        timings.step = (timings.stop - timings.start);
        if (timings.step <= 0.0) {
            timings.step = 1.0;
        }
    }

    double tolerance = fmi3_import_get_default_experiment_has_tolerance(fmu.fmu)
                           ? fmi3_import_get_default_experiment_tolerance(fmu.fmu)
                           : 1e-4;

    if (fmi3_import_enter_initialization_mode(
            fmu.fmu, fmi3_true, tolerance, timings.start, fmi3_true, timings.stop) != fmi3_status_ok) {
        fail("Failed entering FMI3 initialization");
    }

    for (const auto& entry : cfg.startValues) {
        applyStartValueFmi3(fmu.fmu, entry);
    }

    if (fmi3_import_exit_initialization_mode(fmu.fmu) != fmi3_status_ok) {
        fail("Failed exiting FMI3 initialization");
    }

    double current = timings.start;
    while (current < timings.stop - 1e-12) {
        double step = std::min(timings.step, timings.stop - current);
        fmi3_boolean_t eventNeeded = fmi3_false;
        fmi3_boolean_t terminate = fmi3_false;
        fmi3_boolean_t earlyReturn = fmi3_false;
        fmi3_float64_t lastSuccessfulTime{};
        if (fmi3_import_do_step(
                fmu.fmu, current, step, fmi3_false,
                &eventNeeded, &terminate, &earlyReturn, &lastSuccessfulTime) != fmi3_status_ok) {
            fail("fmi3_do_step failed");
        }
        if (terminate == fmi3_true) {
            break;
        }
        current += step;
    }

    FmuExecutionResult result;
    std::vector<std::string> outputs = cfg.outputs.empty() ? autoOutputsFmi3(fmu.fmu) : cfg.outputs;
    for (const auto& name : outputs) {
        result.values[name] = readOutputFmi3(fmu.fmu, name);
    }

    fmi3_import_terminate(fmu.fmu);
    fmi3_import_free_instance(fmu.fmu);
    fmi3_import_destroy_dllfmu(fmu.fmu);
    return result;
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
    if (cfg.outputs && cfg.output_count > 0) {
        result.outputs.reserve(cfg.output_count);
        for (size_t i = 0; i < cfg.output_count; ++i) {
            const char* name = cfg.outputs[i];
            if (!name) {
                fail("Output name cannot be null");
            }
            result.outputs.emplace_back(name);
        }
    }
    return result;
}

std::string runConfiguredFmu(const Config& cfg) {
    preloadLibPythonIfAvailable();

    if (!fs::exists(cfg.fmuPath)) {
        fail("FMU not found: " + cfg.fmuPath);
    }

    jm_callbacks callbacks = *jm_get_default_callbacks();
    ScopedCtx ctx(&callbacks);
    if (!ctx.ctx) {
        fail("Failed to create FMIL context");
    }

    ScopedTempDir tempDir(makeTempDir());
    fmi_version_enu_t version = fmi_import_get_fmi_version(ctx.ctx, cfg.fmuPath.c_str(), tempDir.path.c_str());
    if (version == fmi_version_unknown_enu) {
        fail("Unable to detect FMI version");
    }

    FmuExecutionResult result;
    if (version == fmi_version_2_0_enu) {
        result = runFmi2(cfg, tempDir.path, ctx.ctx);
    } else if (version == fmi_version_3_0_enu) {
        result = runFmi3(cfg, tempDir.path, ctx.ctx);
    } else {
        fail("Unsupported FMI version");
    }
    return serializeJson(result);
}

extern "C" int cads_run_fmu(const cads_fmu_config* cfg, char** json_out, char** err_out) {
    if (json_out) {
        *json_out = nullptr;
    }
    if (err_out) {
        *err_out = nullptr;
    }
    if (!cfg) {
        if (err_out) {
            const char* msg = "Config pointer is null";
            *err_out = static_cast<char*>(std::malloc(std::strlen(msg) + 1));
            std::strcpy(*err_out, msg);
        }
        return 1;
    }

    try {
        Config native = fromCConfig(*cfg);
        std::string json = runConfiguredFmu(native);
        if (json_out) {
            *json_out = static_cast<char*>(std::malloc(json.size() + 1));
            if (!*json_out) {
                fail("Failed allocating JSON buffer");
            }
            std::memcpy(*json_out, json.c_str(), json.size() + 1);
        }
        return 0;
    } catch (const std::exception& ex) {
        if (err_out) {
            const std::string msg = ex.what();
            *err_out = static_cast<char*>(std::malloc(msg.size() + 1));
            if (*err_out) {
                std::memcpy(*err_out, msg.c_str(), msg.size() + 1);
            }
        }
        return 1;
    }
}

extern "C" void cads_free_string(char* ptr) {
    std::free(ptr);
}
