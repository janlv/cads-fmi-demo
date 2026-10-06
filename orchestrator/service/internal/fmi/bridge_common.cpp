#include "bridge_common.hpp"

#include <atomic>
#include <cctype>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <limits>

namespace cads {

namespace {
std::atomic<bool> g_cancel{false};

char* dupForC(const std::string& s) {
    char* out = static_cast<char*>(std::malloc(s.size() + 1));
    if (out) {
        std::memcpy(out, s.c_str(), s.size() + 1);
    }
    return out;
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
}  // namespace

void fail(const std::string& msg) {
    throw std::runtime_error(msg);
}

void requestCancel() {
    g_cancel.store(true);
}

void resetCancel() {
    g_cancel.store(false);
}

bool cancelRequested() {
    return g_cancel.load();
}

void throwIfCancelled() {
    if (g_cancel.load()) {
        throw Cancelled();
    }
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

std::string formatDouble(double value) {
    char buf[64];
    std::snprintf(buf, sizeof(buf), "%.15g", value);
    if (std::strtod(buf, nullptr) != value) {
        std::snprintf(buf, sizeof(buf), "%.17g", value);
    }
    return buf;
}

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

// ---------------------------------------------------------------------------------------------
// JSON
// ---------------------------------------------------------------------------------------------

void writeJsonFloat(std::ostringstream& oss, double value) {
    if (std::isfinite(value)) {
        oss << formatDouble(value);
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
                if (static_cast<unsigned char>(ch) < 0x20) {
                    char buf[8];
                    std::snprintf(buf, sizeof(buf), "\\u%04x", static_cast<unsigned>(static_cast<unsigned char>(ch)));
                    oss << buf;
                } else {
                    oss << ch;
                }
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

void JsonWriter::separate() {
    if (afterKey_) {
        afterKey_ = false;
        return;
    }
    if (!first_.empty()) {
        if (!first_.back()) {
            oss_ << ",";
        }
        first_.back() = false;
    }
}

JsonWriter& JsonWriter::beginObject() {
    separate();
    oss_ << "{";
    first_.push_back(true);
    return *this;
}

JsonWriter& JsonWriter::endObject() {
    oss_ << "}";
    first_.pop_back();
    return *this;
}

JsonWriter& JsonWriter::beginArray() {
    separate();
    oss_ << "[";
    first_.push_back(true);
    return *this;
}

JsonWriter& JsonWriter::endArray() {
    oss_ << "]";
    first_.pop_back();
    return *this;
}

JsonWriter& JsonWriter::key(const std::string& k) {
    separate();
    oss_ << "\"" << escapeJsonString(k) << "\":";
    afterKey_ = true;
    return *this;
}

JsonWriter& JsonWriter::value(const std::string& v) {
    separate();
    oss_ << "\"" << escapeJsonString(v) << "\"";
    return *this;
}

JsonWriter& JsonWriter::value(const char* v) {
    return value(std::string(v ? v : ""));
}

JsonWriter& JsonWriter::value(double v) {
    separate();
    writeJsonFloat(oss_, v);
    return *this;
}

JsonWriter& JsonWriter::value(int64_t v) {
    separate();
    oss_ << v;
    return *this;
}

JsonWriter& JsonWriter::value(bool v) {
    separate();
    oss_ << (v ? "true" : "false");
    return *this;
}

JsonWriter& JsonWriter::null() {
    separate();
    oss_ << "null";
    return *this;
}

JsonWriter& JsonWriter::optional(const std::optional<double>& v) {
    return v ? value(*v) : null();
}

JsonWriter& JsonWriter::raw(const std::string& json) {
    separate();
    oss_ << json;
    return *this;
}

std::string serializeEnvelope(const FmuExecutionResult& values, const RunStats& stats) {
    JsonWriter w;
    w.beginObject();
    w.key("values").raw(serializeJson(values));
    w.key("stats").beginObject();
    w.key("wall_seconds").value(stats.wallSeconds);
    w.key("simulated_seconds").value(stats.simulatedSeconds);
    w.key("communication_points").value(stats.communicationPoints);
    w.key("fmus").beginArray();
    for (const auto& f : stats.fmus) {
        w.beginObject();
        w.key("model").value(f.model);
        w.key("fmi_version").value(f.fmiVersion);
        w.key("model_name").value(f.modelName);
        w.key("model_version").value(f.modelVersion);
        w.key("guid").value(f.guid);
        w.key("generation_tool").value(f.generationTool);
        w.key("declared_step").optional(f.declaredStep);
        w.key("step_used").value(f.stepUsed);
        w.key("do_step_calls").value(f.doStepCalls);
        w.key("clipped_substeps").value(f.clippedSubsteps);
        w.key("do_step_wall_seconds").value(f.doStepWallSeconds);
        w.endObject();
    }
    w.endArray();
    w.key("events").beginArray();
    for (const auto& e : stats.events) {
        w.beginObject();
        w.key("name").value(e.name);
        w.key("time").value(e.time);
        w.key("edge").value(e.edge);
        w.endObject();
    }
    w.endArray();
    w.key("terminated_by");
    if (stats.terminatedBy) {
        w.beginObject();
        w.key("model").value(stats.terminatedBy->model);
        w.key("time").value(stats.terminatedBy->time);
        w.endObject();
    } else {
        w.null();
    }
    w.key("failed_at").optional(stats.failedAt);
    w.endObject();
    w.endObject();
    return w.str();
}

// ---------------------------------------------------------------------------------------------
// C call wrapper
// ---------------------------------------------------------------------------------------------

int wrapCall(char** json_out, char** err_out, const std::function<std::string(std::string& partial)>& body) {
    if (json_out) {
        *json_out = nullptr;
    }
    if (err_out) {
        *err_out = nullptr;
    }
    std::string partial;
    auto reportError = [&](const std::string& msg, int code) {
        if (err_out) {
            *err_out = dupForC(msg);
        }
        if (json_out && !partial.empty()) {
            *json_out = dupForC(partial);
        }
        return code;
    };
    try {
        std::string json = body(partial);
        if (json_out) {
            *json_out = dupForC(json);
            if (!*json_out) {
                partial.clear();
                return reportError("Failed allocating JSON buffer", 1);
            }
        }
        return 0;
    } catch (const Cancelled& ex) {
        return reportError(ex.what(), 2);
    } catch (const std::exception& ex) {
        return reportError(ex.what(), 1);
    } catch (...) {
        return reportError("unknown error in FMI bridge", 1);
    }
}

}  // namespace cads
