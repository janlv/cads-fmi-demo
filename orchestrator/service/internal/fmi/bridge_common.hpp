// Shared helpers for the FMIL bridge: configuration structs, value representation, JSON
// serialisation, input series loading, cancellation and the C error-handling wrapper.
#pragma once

#include <cstdint>
#include <functional>
#include <map>
#include <optional>
#include <sstream>
#include <stdexcept>
#include <string>
#include <vector>

namespace cads {

[[noreturn]] void fail(const std::string& msg);

// Thrown when cads_request_cancel() was observed at a communication point.
struct Cancelled : std::runtime_error {
    Cancelled() : std::runtime_error("run cancelled") {}
};

// Process-wide cancel flag (atomic). Checked once per communication point.
void requestCancel();
void resetCancel();
bool cancelRequested();
void throwIfCancelled();

double parseNumber(const std::string& input);
std::string trimCopy(const std::string& input);
std::string formatDouble(double value);  // %.15g with round-trip check, else %.17g

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

struct InputSeriesPoint {
    double time{};
    std::vector<NumericAssignment> values;
};

struct InputSeriesData {
    std::vector<InputSeriesPoint> points;
};

InputSeriesData loadInputSeries(const InputSeriesConfig& cfg);

struct OutputValue {
    enum class Type { Real, Integer, Boolean, RealArray, IntegerArray, BooleanArray } type{Type::Real};
    double realVal{};
    int64_t intVal{};
    bool boolVal{};
    std::vector<double> realArray;
    std::vector<int64_t> intArray;
    std::vector<bool> boolArray;

    static OutputValue real(double v) {
        OutputValue o;
        o.type = Type::Real;
        o.realVal = v;
        return o;
    }
    static OutputValue integer(int64_t v) {
        OutputValue o;
        o.type = Type::Integer;
        o.intVal = v;
        return o;
    }
    static OutputValue boolean(bool v) {
        OutputValue o;
        o.type = Type::Boolean;
        o.boolVal = v;
        return o;
    }
};

// Values object of one bridge call: final outputs plus optional trace.
struct FmuExecutionResult {
    std::map<std::string, OutputValue> values;
    std::vector<double> traceTimes;
    std::map<std::string, std::vector<OutputValue>> traceSignals;
};

std::string escapeJsonString(const std::string& value);
void writeJsonFloat(std::ostringstream& oss, double value);
void writeJsonValue(std::ostringstream& oss, const OutputValue& value);
std::string serializeJson(const FmuExecutionResult& result);

// Minimal streaming JSON writer used for the stats envelope. Commas are inserted
// automatically between members and array elements.
class JsonWriter {
public:
    JsonWriter& beginObject();
    JsonWriter& endObject();
    JsonWriter& beginArray();
    JsonWriter& endArray();
    JsonWriter& key(const std::string& k);
    JsonWriter& value(const std::string& v);
    JsonWriter& value(const char* v);
    JsonWriter& value(double v);
    JsonWriter& value(int64_t v);
    JsonWriter& value(bool v);
    JsonWriter& null();
    JsonWriter& optional(const std::optional<double>& v);
    // Splices pre-serialised JSON (e.g. the values object) as one value.
    JsonWriter& raw(const std::string& json);
    std::string str() const { return oss_.str(); }

private:
    void separate();
    std::ostringstream oss_;
    std::vector<bool> first_;
    bool afterKey_{false};
};

// Per-run statistics mirrored by the Go Stats/FMUInfo structs (types.go).
struct FmuInfoRecord {
    std::string model;
    std::string fmiVersion;
    std::string modelName;
    std::string modelVersion;
    std::string guid;
    std::string generationTool;
    std::optional<double> declaredStep;
    double stepUsed{};
    int64_t doStepCalls{};
    int64_t clippedSubsteps{};
    double doStepWallSeconds{};
};

struct EventRecord {
    std::string name;
    double time{};
    std::string edge;  // "rising" | "falling"
};

struct Termination {
    std::string model;
    double time{};
};

struct RunStats {
    double wallSeconds{};
    double simulatedSeconds{};
    int64_t communicationPoints{};
    std::vector<FmuInfoRecord> fmus;
    std::vector<EventRecord> events;
    std::optional<Termination> terminatedBy;
    std::optional<double> failedAt;
};

// {"values": <values>, "stats": <stats>}
std::string serializeEnvelope(const FmuExecutionResult& values, const RunStats& stats);

// Runs body and maps its outcome to the C return convention:
// 0 ok (json_out = envelope), 1 error, 2 cancelled. On 1/2, err_out holds the message and
// json_out holds the partial envelope if body stored one in `partial` before throwing.
int wrapCall(char** json_out, char** err_out, const std::function<std::string(std::string& partial)>& body);

}  // namespace cads
