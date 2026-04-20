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
#include <cctype>
#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstdarg>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <limits>
#include <map>
#include <memory>
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

struct NumericAssignment {
    std::string name;
    double value{};
};

struct InputSeriesConfig {
    std::string csvPath;
};

struct TraceConfig {
    std::vector<std::string> outputs;
    std::vector<std::string> inputs;
    std::optional<double> sampleEvery;

    bool enabled() const {
        return !outputs.empty() || !inputs.empty();
    }
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

struct OutputValue {
    enum class Type { Real, Integer, Boolean, RealArray, IntegerArray, BooleanArray } type;
    double realVal{};
    int64_t intVal{};
    bool boolVal{};
    std::vector<double> realArray;
    std::vector<int64_t> intArray;
    std::vector<bool> boolArray;
};

struct FmuExecutionResult {
    std::map<std::string, OutputValue> values;
    std::vector<double> traceTimes;
    std::map<std::string, std::vector<OutputValue>> traceSignals;
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

std::string trimCopy(const std::string& input) {
    size_t start = 0;
    while (start < input.size() && std::isspace(static_cast<unsigned char>(input[start]))) {
        start += 1;
    }
    size_t stop = input.size();
    while (stop > start && std::isspace(static_cast<unsigned char>(input[stop - 1]))) {
        stop -= 1;
    }
    return input.substr(start, stop - start);
}

std::vector<std::string> splitCsvLine(const std::string& line) {
    std::vector<std::string> fields;
    std::string current;
    for (char ch : line) {
        if (ch == ',') {
            fields.push_back(trimCopy(current));
            current.clear();
            continue;
        }
        current.push_back(ch);
    }
    fields.push_back(trimCopy(current));
    return fields;
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

void writeJsonFloat(std::ostringstream& oss, double value) {
    if (std::isfinite(value)) {
        oss << value;
        return;
    }
    oss << "null";
}

std::string escapeJsonString(const std::string& value) {
    std::ostringstream oss;
    for (char ch : value) {
        switch (ch) {
            case '\\':
                oss << "\\\\";
                break;
            case '"':
                oss << "\\\"";
                break;
            case '\n':
                oss << "\\n";
                break;
            case '\r':
                oss << "\\r";
                break;
            case '\t':
                oss << "\\t";
                break;
            default:
                oss << ch;
                break;
        }
    }
    return oss.str();
}

void writeJsonValue(std::ostringstream& oss, const OutputValue& value) {
    switch (value.type) {
        case OutputValue::Type::Real:
            writeJsonFloat(oss, value.realVal);
            break;
        case OutputValue::Type::Integer:
            oss << value.intVal;
            break;
        case OutputValue::Type::Boolean:
            oss << (value.boolVal ? "true" : "false");
            break;
        case OutputValue::Type::RealArray:
            oss << "[";
            for (size_t i = 0; i < value.realArray.size(); ++i) {
                if (i > 0) {
                    oss << ",";
                }
                writeJsonFloat(oss, value.realArray[i]);
            }
            oss << "]";
            break;
        case OutputValue::Type::IntegerArray:
            oss << "[";
            for (size_t i = 0; i < value.intArray.size(); ++i) {
                if (i > 0) {
                    oss << ",";
                }
                oss << value.intArray[i];
            }
            oss << "]";
            break;
        case OutputValue::Type::BooleanArray:
            oss << "[";
            for (size_t i = 0; i < value.boolArray.size(); ++i) {
                if (i > 0) {
                    oss << ",";
                }
                oss << (value.boolArray[i] ? "true" : "false");
            }
            oss << "]";
            break;
    }
}

std::string serializeJson(const FmuExecutionResult& result) {
    std::ostringstream oss;
    oss << "{";
    bool first = true;
    for (const auto& [name, value] : result.values) {
        if (!first) {
            oss << ",";
        }
        first = false;
        oss << "\"" << escapeJsonString(name) << "\":";
        writeJsonValue(oss, value);
    }

    if (!result.traceTimes.empty() && !result.traceSignals.empty()) {
        if (!first) {
            oss << ",";
        }
        oss << "\"trace\":{";
        oss << "\"time\":[";
        for (size_t i = 0; i < result.traceTimes.size(); ++i) {
            if (i > 0) {
                oss << ",";
            }
            writeJsonFloat(oss, result.traceTimes[i]);
        }
        oss << "],\"signals\":{";
        bool firstSignal = true;
        for (const auto& [name, values] : result.traceSignals) {
            if (!firstSignal) {
                oss << ",";
            }
            firstSignal = false;
            oss << "\"" << escapeJsonString(name) << "\":[";
            for (size_t i = 0; i < values.size(); ++i) {
                if (i > 0) {
                    oss << ",";
                }
                writeJsonValue(oss, values[i]);
            }
            oss << "]";
        }
        oss << "}}";
    }
    oss << "}";
    return oss.str();
}

struct InputSeriesPoint {
    double time{};
    std::vector<NumericAssignment> values;
};

struct InputSeriesData {
    std::vector<InputSeriesPoint> points;
};

struct StepTimings {
    double start;
    double stop;
    double step;
};

InputSeriesData loadInputSeries(const InputSeriesConfig& cfg) {
    std::ifstream stream(cfg.csvPath);
    if (!stream.is_open()) {
        fail("Failed opening input CSV '" + cfg.csvPath + "'");
    }

    std::string headerLine;
    if (!std::getline(stream, headerLine)) {
        fail("Input CSV '" + cfg.csvPath + "' is empty");
    }

    std::vector<std::string> headers = splitCsvLine(headerLine);
    if (headers.empty()) {
        fail("Input CSV '" + cfg.csvPath + "' is missing headers");
    }
    for (const auto& header : headers) {
        if (header.empty()) {
            fail("Input CSV '" + cfg.csvPath + "' contains an empty header");
        }
    }

    InputSeriesData series;
    std::string line;
    double lastTime = -std::numeric_limits<double>::infinity();
    size_t lineNumber = 1;
    while (std::getline(stream, line)) {
        lineNumber += 1;
        line = trimCopy(line);
        if (line.empty()) {
            continue;
        }

        std::vector<std::string> fields = splitCsvLine(line);
        if (fields.size() != headers.size()) {
            fail("Input CSV '" + cfg.csvPath + "' line " + std::to_string(lineNumber) + " has " +
                 std::to_string(fields.size()) + " columns, expected " + std::to_string(headers.size()));
        }

        InputSeriesPoint point;
        point.time = parseNumber(fields[0]);
        if (point.time + 1e-12 < lastTime) {
            fail("Input CSV '" + cfg.csvPath + "' is not sorted by time");
        }
        lastTime = point.time;
        point.values.reserve(headers.size());
        for (size_t i = 0; i < headers.size(); ++i) {
            point.values.push_back({headers[i], parseNumber(fields[i])});
        }
        series.points.push_back(std::move(point));
    }

    if (series.points.empty()) {
        fail("Input CSV '" + cfg.csvPath + "' does not contain any samples");
    }
    return series;
}

void alignTimingsWithSeries(StepTimings& timings, const Config& cfg, const std::optional<InputSeriesData>& series) {
    if (!series || series->points.empty()) {
        return;
    }

    if (!cfg.startTime) {
        timings.start = series->points.front().time;
    }
    if (!cfg.stopTime) {
        timings.stop = series->points.back().time;
    }
    if (!cfg.stepSize) {
        if (series->points.size() > 1) {
            double derived = series->points[1].time - series->points[0].time;
            if (derived > 0.0) {
                timings.step = derived;
            }
        } else {
            timings.step = std::max(1e-3, timings.stop - timings.start);
        }
    }
}

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

void applyNumericValueFmi2(fmi2_import_t* fmu, const std::string& name, double value) {
    fmi2_import_variable_t* var = fmi2_import_get_variable_by_name(fmu, name.c_str());
    if (!var) {
        fail("Unknown variable '" + name + "'");
    }
    fmi2_value_reference_t vr = fmi2_import_get_variable_vr(var);
    fmi2_base_type_enu_t baseType = fmi2_import_get_variable_base_type(var);
    switch (baseType) {
        case fmi2_base_type_real: {
            fmi2_real_t v = static_cast<fmi2_real_t>(value);
            if (fmi2_import_set_real(fmu, &vr, 1, &v) != fmi2_status_ok) {
                fail("Failed setting real " + name);
            }
            break;
        }
        case fmi2_base_type_int: {
            fmi2_integer_t intVal = static_cast<fmi2_integer_t>(std::llround(value));
            if (fmi2_import_set_integer(fmu, &vr, 1, &intVal) != fmi2_status_ok) {
                fail("Failed setting integer " + name);
            }
            break;
        }
        case fmi2_base_type_bool: {
            fmi2_boolean_t boolVal = (value != 0.0) ? fmi2_true : fmi2_false;
            if (fmi2_import_set_boolean(fmu, &vr, 1, &boolVal) != fmi2_status_ok) {
                fail("Failed setting boolean " + name);
            }
            break;
        }
        default:
            fail("Unsupported base type for " + name);
    }
}

void applyStartValueFmi2(fmi2_import_t* fmu, const Assignment& assign) {
    applyNumericValueFmi2(fmu, assign.name, parseNumber(assign.value));
}

void applySeriesPointFmi2(fmi2_import_t* fmu, const InputSeriesPoint& point) {
    for (const auto& entry : point.values) {
        applyNumericValueFmi2(fmu, entry.name, entry.value);
    }
}

OutputValue readVariableFmi2(fmi2_import_t* fmu, const std::string& name) {
    fmi2_import_variable_t* var = fmi2_import_get_variable_by_name(fmu, name.c_str());
    if (!var) {
        fail("Variable '" + name + "' not found");
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
            fail("Unsupported variable type for " + name);
    }
    return ov;
}

size_t checkedMultiply(size_t lhs, size_t rhs, const std::string& what) {
    if (lhs == 0 || rhs == 0) {
        fail("Array dimension for " + what + " resolved to zero");
    }
    if (lhs > std::numeric_limits<size_t>::max() / rhs) {
        fail("Array size overflow for " + what);
    }
    return lhs * rhs;
}

size_t normalizeDimensionSize(double value, const std::string& what) {
    if (!std::isfinite(value) || value <= 0.0) {
        fail("Array dimension for " + what + " must be a positive finite number");
    }
    double rounded = std::round(value);
    if (std::fabs(value - rounded) > 1e-9) {
        fail("Array dimension for " + what + " must be an integer");
    }
    if (rounded > static_cast<double>(std::numeric_limits<size_t>::max())) {
        fail("Array dimension for " + what + " is too large");
    }
    return static_cast<size_t>(rounded);
}

size_t readFmi3SizeVariable(fmi3_import_t* fmu, fmi3_import_variable_t* var, const std::string& owner) {
    if (!var) {
        fail("Missing dimension variable for " + owner);
    }
    if (fmi3_import_variable_is_array(var)) {
        fail("Array-valued dimension variables are not supported for " + owner);
    }

    fmi3_value_reference_t vr = fmi3_import_get_variable_vr(var);
    fmi3_base_type_enu_t baseType = fmi3_import_get_variable_base_type(var);

    switch (baseType) {
        case fmi3_base_type_float64: {
            fmi3_float64_t value{};
            if (fmi3_import_get_float64(fmu, &vr, 1, &value, 1) != fmi3_status_ok) {
                fail("Failed reading float64 dimension for " + owner);
            }
            return normalizeDimensionSize(value, owner);
        }
        case fmi3_base_type_float32: {
            fmi3_float32_t value{};
            if (fmi3_import_get_float32(fmu, &vr, 1, &value, 1) != fmi3_status_ok) {
                fail("Failed reading float32 dimension for " + owner);
            }
            return normalizeDimensionSize(value, owner);
        }
        case fmi3_base_type_int64: {
            fmi3_int64_t value{};
            if (fmi3_import_get_int64(fmu, &vr, 1, &value, 1) != fmi3_status_ok) {
                fail("Failed reading int64 dimension for " + owner);
            }
            return normalizeDimensionSize(static_cast<double>(value), owner);
        }
        case fmi3_base_type_int32: {
            fmi3_int32_t value{};
            if (fmi3_import_get_int32(fmu, &vr, 1, &value, 1) != fmi3_status_ok) {
                fail("Failed reading int32 dimension for " + owner);
            }
            return normalizeDimensionSize(static_cast<double>(value), owner);
        }
        case fmi3_base_type_int16: {
            fmi3_int16_t value{};
            if (fmi3_import_get_int16(fmu, &vr, 1, &value, 1) != fmi3_status_ok) {
                fail("Failed reading int16 dimension for " + owner);
            }
            return normalizeDimensionSize(static_cast<double>(value), owner);
        }
        case fmi3_base_type_int8: {
            fmi3_int8_t value{};
            if (fmi3_import_get_int8(fmu, &vr, 1, &value, 1) != fmi3_status_ok) {
                fail("Failed reading int8 dimension for " + owner);
            }
            return normalizeDimensionSize(static_cast<double>(value), owner);
        }
        case fmi3_base_type_uint64: {
            fmi3_uint64_t value{};
            if (fmi3_import_get_uint64(fmu, &vr, 1, &value, 1) != fmi3_status_ok) {
                fail("Failed reading uint64 dimension for " + owner);
            }
            if (value > std::numeric_limits<size_t>::max()) {
                fail("Array dimension for " + owner + " is too large");
            }
            return static_cast<size_t>(value);
        }
        case fmi3_base_type_uint32: {
            fmi3_uint32_t value{};
            if (fmi3_import_get_uint32(fmu, &vr, 1, &value, 1) != fmi3_status_ok) {
                fail("Failed reading uint32 dimension for " + owner);
            }
            return static_cast<size_t>(value);
        }
        case fmi3_base_type_uint16: {
            fmi3_uint16_t value{};
            if (fmi3_import_get_uint16(fmu, &vr, 1, &value, 1) != fmi3_status_ok) {
                fail("Failed reading uint16 dimension for " + owner);
            }
            return static_cast<size_t>(value);
        }
        case fmi3_base_type_uint8: {
            fmi3_uint8_t value{};
            if (fmi3_import_get_uint8(fmu, &vr, 1, &value, 1) != fmi3_status_ok) {
                fail("Failed reading uint8 dimension for " + owner);
            }
            return static_cast<size_t>(value);
        }
        default:
            fail("Unsupported dimension base type for " + owner);
    }
}

size_t resolveFmi3ValueCount(fmi3_import_t* fmu, fmi3_import_variable_t* var, const std::string& name) {
    if (!fmi3_import_variable_is_array(var)) {
        return 1;
    }

    fmi3_import_dimension_list_t* dims = fmi3_import_get_variable_dimension_list(var);
    if (!dims) {
        fail("Missing dimension metadata for array output " + name);
    }

    size_t count = 1;
    size_t dimCount = fmi3_import_get_dimension_list_size(dims);
    if (dimCount == 0) {
        fail("Array output " + name + " does not define any dimensions");
    }
    for (size_t i = 0; i < dimCount; ++i) {
        fmi3_import_dimension_t* dim = fmi3_import_get_dimension(dims, i);
        if (!dim) {
            fail("Failed reading dimension metadata for array output " + name);
        }

        size_t dimSize = 0;
        if (fmi3_import_get_dimension_has_start(dim)) {
            fmi3_uint64_t start = fmi3_import_get_dimension_start(dim);
            if (start > std::numeric_limits<size_t>::max()) {
                fail("Array dimension for " + name + " is too large");
            }
            dimSize = static_cast<size_t>(start);
            if (dimSize == 0) {
                fail("Array dimension for " + name + " resolved to zero");
            }
        } else if (fmi3_import_get_dimension_has_vr(dim)) {
            fmi3_value_reference_t dimVR = fmi3_import_get_dimension_vr(dim);
            fmi3_import_variable_t* dimVar = fmi3_import_get_variable_by_vr(fmu, dimVR);
            dimSize = readFmi3SizeVariable(fmu, dimVar, name);
        } else {
            fail("Array dimension for " + name + " is missing both start and valueReference");
        }
        count = checkedMultiply(count, dimSize, name);
    }
    return count;
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

    std::optional<InputSeriesData> inputSeries;
    if (cfg.inputSeries) {
        inputSeries = loadInputSeries(*cfg.inputSeries);
    }

    StepTimings timings = deriveTimingsFmi2(fmu.fmu, cfg);
    alignTimingsWithSeries(timings, cfg, inputSeries);
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

    size_t nextInputIndex = 0;
    auto applySeriesThrough = [&](double time) {
        if (!inputSeries) {
            return;
        }
        while (nextInputIndex < inputSeries->points.size() &&
               inputSeries->points[nextInputIndex].time <= time + 1e-12) {
            applySeriesPointFmi2(fmu.fmu, inputSeries->points[nextInputIndex]);
            nextInputIndex += 1;
        }
    };
    applySeriesThrough(timings.start);

    if (fmi2_import_exit_initialization_mode(fmu.fmu) != fmi2_status_ok) {
        fail("Failed exiting initialization mode");
    }

    FmuExecutionResult result;
    std::vector<std::string> traceNames = buildTraceNames(cfg.trace);
    double traceInterval = traceNames.empty() ? 0.0 : resolveTraceInterval(cfg, timings);
    auto captureTrace = [&](double time) {
        if (traceNames.empty()) {
            return;
        }
        result.traceTimes.push_back(time);
        for (const auto& name : traceNames) {
            result.traceSignals[name].push_back(readVariableFmi2(fmu.fmu, name));
        }
    };

    double current = timings.start;
    if (!traceNames.empty()) {
        captureTrace(current);
    }
    double nextTraceTime = current + traceInterval;
    while (current < timings.stop - 1e-12) {
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
            fail("fmi2 execution stalled due to zero-length step");
        }
        double step = next - current;
        if (fmi2_import_do_step(fmu.fmu, current, step, fmi2_true) != fmi2_status_ok) {
            fail("fmi2_do_step failed");
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

    std::vector<std::string> outputs = cfg.outputs.empty() ? autoOutputsFmi2(fmu.fmu) : cfg.outputs;
    for (const auto& name : outputs) {
        result.values[name] = readVariableFmi2(fmu.fmu, name);
    }

    fmi2_import_terminate(fmu.fmu);
    fmi2_import_free_instance(fmu.fmu);
    fmi2_import_destroy_dllfmu(fmu.fmu);
    return result;
}

void applyNumericValueFmi3(fmi3_import_t* fmu, const std::string& name, double value) {
    fmi3_import_variable_t* var = fmi3_import_get_variable_by_name(fmu, name.c_str());
    if (!var) {
        fail("Unknown variable '" + name + "'");
    }
    fmi3_value_reference_t vr = fmi3_import_get_variable_vr(var);
    fmi3_base_type_enu_t baseType = fmi3_import_get_variable_base_type(var);

    switch (baseType) {
        case fmi3_base_type_float64: {
            fmi3_float64_t v = static_cast<fmi3_float64_t>(value);
            if (fmi3_import_set_float64(fmu, &vr, 1, &v, 1) != fmi3_status_ok) {
                fail("Failed setting real " + name);
            }
            break;
        }
        case fmi3_base_type_int32: {
            fmi3_int32_t iv = static_cast<fmi3_int32_t>(std::llround(value));
            if (fmi3_import_set_int32(fmu, &vr, 1, &iv, 1) != fmi3_status_ok) {
                fail("Failed setting integer " + name);
            }
            break;
        }
        case fmi3_base_type_bool: {
            fmi3_boolean_t bv = (value != 0.0) ? fmi3_true : fmi3_false;
            if (fmi3_import_set_boolean(fmu, &vr, 1, &bv, 1) != fmi3_status_ok) {
                fail("Failed setting boolean " + name);
            }
            break;
        }
        default:
            fail("Unsupported FMI3 base type for " + name);
    }
}

void applyStartValueFmi3(fmi3_import_t* fmu, const Assignment& assign) {
    applyNumericValueFmi3(fmu, assign.name, parseNumber(assign.value));
}

void applySeriesPointFmi3(fmi3_import_t* fmu, const InputSeriesPoint& point) {
    for (const auto& entry : point.values) {
        applyNumericValueFmi3(fmu, entry.name, entry.value);
    }
}

OutputValue readVariableFmi3(fmi3_import_t* fmu, const std::string& name) {
    fmi3_import_variable_t* var = fmi3_import_get_variable_by_name(fmu, name.c_str());
    if (!var) {
        fail("Variable '" + name + "' not found");
    }
    fmi3_value_reference_t vr = fmi3_import_get_variable_vr(var);
    fmi3_base_type_enu_t baseType = fmi3_import_get_variable_base_type(var);
    size_t valueCount = resolveFmi3ValueCount(fmu, var, name);
    OutputValue ov{};
    switch (baseType) {
        case fmi3_base_type_float64: {
            std::vector<fmi3_float64_t> values(valueCount);
            if (fmi3_import_get_float64(fmu, &vr, 1, values.data(), valueCount) != fmi3_status_ok) {
                fail("Failed reading float64 output " + name);
            }
            if (valueCount == 1) {
                ov.type = OutputValue::Type::Real;
                ov.realVal = values[0];
            } else {
                ov.type = OutputValue::Type::RealArray;
                ov.realArray.assign(values.begin(), values.end());
            }
            break;
        }
        case fmi3_base_type_int32: {
            std::vector<fmi3_int32_t> values(valueCount);
            if (fmi3_import_get_int32(fmu, &vr, 1, values.data(), valueCount) != fmi3_status_ok) {
                fail("Failed reading int32 output " + name);
            }
            if (valueCount == 1) {
                ov.type = OutputValue::Type::Integer;
                ov.intVal = values[0];
            } else {
                ov.type = OutputValue::Type::IntegerArray;
                ov.intArray.reserve(values.size());
                for (fmi3_int32_t value : values) {
                    ov.intArray.push_back(value);
                }
            }
            break;
        }
        case fmi3_base_type_bool: {
            std::unique_ptr<fmi3_boolean_t[]> rawValues(new fmi3_boolean_t[valueCount]);
            if (fmi3_import_get_boolean(fmu, &vr, 1, rawValues.get(), valueCount) != fmi3_status_ok) {
                fail("Failed reading boolean output " + name);
            }
            if (valueCount == 1) {
                ov.type = OutputValue::Type::Boolean;
                ov.boolVal = (rawValues[0] != fmi3_false);
            } else {
                ov.type = OutputValue::Type::BooleanArray;
                ov.boolArray.reserve(valueCount);
                for (size_t i = 0; i < valueCount; ++i) {
                    ov.boolArray.push_back(rawValues[i] != fmi3_false);
                }
            }
            break;
        }
        default:
            fail("Unsupported variable type for " + name);
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

    std::optional<InputSeriesData> inputSeries;
    if (cfg.inputSeries) {
        inputSeries = loadInputSeries(*cfg.inputSeries);
    }

    StepTimings timings = deriveTimingsFmi3(fmu.fmu, cfg);
    alignTimingsWithSeries(timings, cfg, inputSeries);
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

    size_t nextInputIndex = 0;
    auto applySeriesThrough = [&](double time) {
        if (!inputSeries) {
            return;
        }
        while (nextInputIndex < inputSeries->points.size() &&
               inputSeries->points[nextInputIndex].time <= time + 1e-12) {
            applySeriesPointFmi3(fmu.fmu, inputSeries->points[nextInputIndex]);
            nextInputIndex += 1;
        }
    };
    applySeriesThrough(timings.start);

    if (fmi3_import_exit_initialization_mode(fmu.fmu) != fmi3_status_ok) {
        fail("Failed exiting FMI3 initialization");
    }

    FmuExecutionResult result;
    std::vector<std::string> traceNames = buildTraceNames(cfg.trace);
    double traceInterval = traceNames.empty() ? 0.0 : resolveTraceInterval(cfg, timings);
    auto captureTrace = [&](double time) {
        if (traceNames.empty()) {
            return;
        }
        result.traceTimes.push_back(time);
        for (const auto& name : traceNames) {
            result.traceSignals[name].push_back(readVariableFmi3(fmu.fmu, name));
        }
    };

    double current = timings.start;
    if (!traceNames.empty()) {
        captureTrace(current);
    }
    double nextTraceTime = current + traceInterval;
    while (current < timings.stop - 1e-12) {
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
            fail("fmi3 execution stalled due to zero-length step");
        }
        double step = next - current;
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

    std::vector<std::string> outputs = cfg.outputs.empty() ? autoOutputsFmi3(fmu.fmu) : cfg.outputs;
    for (const auto& name : outputs) {
        result.values[name] = readVariableFmi3(fmu.fmu, name);
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
    if (cfg.input_series) {
        if (!cfg.input_series->csv_path || cfg.input_series->csv_path[0] == '\0') {
            fail("Input series CSV path is required");
        }
        result.inputSeries = InputSeriesConfig{cfg.input_series->csv_path};
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
    if (cfg.trace_outputs && cfg.trace_output_count > 0) {
        result.trace.outputs.reserve(cfg.trace_output_count);
        for (size_t i = 0; i < cfg.trace_output_count; ++i) {
            const char* name = cfg.trace_outputs[i];
            if (!name) {
                fail("Trace output name cannot be null");
            }
            result.trace.outputs.emplace_back(name);
        }
    }
    if (cfg.trace_inputs && cfg.trace_input_count > 0) {
        result.trace.inputs.reserve(cfg.trace_input_count);
        for (size_t i = 0; i < cfg.trace_input_count; ++i) {
            const char* name = cfg.trace_inputs[i];
            if (!name) {
                fail("Trace input name cannot be null");
            }
            result.trace.inputs.emplace_back(name);
        }
    }
    if (cfg.has_trace_interval) {
        result.trace.sampleEvery = cfg.trace_interval;
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
