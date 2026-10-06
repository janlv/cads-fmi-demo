#include "fmu_instance.hpp"

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

#include <dlfcn.h>
#include <unistd.h>

#include <chrono>
#include <cmath>
#include <cstdarg>
#include <cstdio>
#include <cstdlib>
#include <filesystem>
#include <limits>
#include <mutex>
#include <unordered_map>

namespace fs = std::filesystem;

namespace cads {

// ---------------------------------------------------------------------------------------------
// Package: temp dir, FMIL callbacks and context. Member order gives destruction
// context -> callbacks -> temp dir.
// ---------------------------------------------------------------------------------------------

namespace {

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
    ScopedTempDir(const ScopedTempDir&) = delete;
    ScopedTempDir& operator=(const ScopedTempDir&) = delete;
};

struct ScopedCtx {
    fmi_import_context_t* ctx{nullptr};
    explicit ScopedCtx(jm_callbacks* cb) : ctx(fmi_import_allocate_context(cb)) {}
    ~ScopedCtx() {
        if (ctx) {
            fmi_import_free_context(ctx);
        }
    }
    ScopedCtx(const ScopedCtx&) = delete;
    ScopedCtx& operator=(const ScopedCtx&) = delete;
};

const char* safeStr(const char* s) {
    return s ? s : "";
}

double secondsSince(std::chrono::steady_clock::time_point start) {
    return std::chrono::duration<double>(std::chrono::steady_clock::now() - start).count();
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

const char* fmi2StatusName(fmi2_status_t s) {
    switch (s) {
        case fmi2_status_ok:
            return "ok";
        case fmi2_status_warning:
            return "warning";
        case fmi2_status_discard:
            return "discard";
        case fmi2_status_error:
            return "error";
        case fmi2_status_fatal:
            return "fatal";
        case fmi2_status_pending:
            return "pending";
    }
    return "unknown";
}

const char* fmi3StatusName(fmi3_status_t s) {
    switch (s) {
        case fmi3_status_ok:
            return "ok";
        case fmi3_status_warning:
            return "warning";
        case fmi3_status_discard:
            return "discard";
        case fmi3_status_error:
            return "error";
        case fmi3_status_fatal:
            return "fatal";
    }
    return "unknown";
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

// Returns the element count of a (possibly array) FMI3 variable. `isStatic` is set when the
// count depends only on modelDescription start values and can be cached.
size_t resolveFmi3ValueCount(fmi3_import_t* fmu, fmi3_import_variable_t* var, const std::string& name, bool& isStatic) {
    isStatic = true;
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
            isStatic = false;
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

}  // namespace

struct FmuPackage {
    ScopedTempDir tempDir;
    jm_callbacks callbacks;
    ScopedCtx ctx;

    FmuPackage() : tempDir(makeTempDir()), callbacks(*jm_get_default_callbacks()), ctx(&callbacks) {
        if (!ctx.ctx) {
            fail("Failed to create FMIL context");
        }
    }
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

// ---------------------------------------------------------------------------------------------
// Base
// ---------------------------------------------------------------------------------------------

FmuInstance::FmuInstance(std::unique_ptr<FmuPackage> pkg, std::string path)
    : pkg_(std::move(pkg)), path_(std::move(path)) {}

FmuInstance::~FmuInstance() = default;

double FmuInstance::getNumber(const std::string& name) {
    OutputValue v = get(name);
    switch (v.type) {
        case OutputValue::Type::Real:
            return v.realVal;
        case OutputValue::Type::Integer:
            return static_cast<double>(v.intVal);
        case OutputValue::Type::Boolean:
            return v.boolVal ? 1.0 : 0.0;
        default:
            fail(label_ + ": variable '" + name + "' is an array; only scalar variables can be exchanged");
    }
}

StepResult FmuInstance::doStep(double t, double h) {
    time_ = t;
    auto started = std::chrono::steady_clock::now();
    StepResult r = doStepImpl(t, h);
    stats_.doStepWall += secondsSince(started);
    stats_.doStepCalls += 1;
    if (stats_.stepUsed > 0.0 && h < stats_.stepUsed * (1.0 - 1e-9)) {
        stats_.clippedSubsteps += 1;
    }
    if (!r.terminate) {
        time_ = t + h;
    }
    return r;
}

void FmuInstance::statusFailure(const char* what, const char* status, bool fatal) {
    state_ = fatal ? State::Fatal : State::Error;
    fail(label_ + ": " + what + " returned " + status + " at t=" + formatDouble(time_));
}

// ---------------------------------------------------------------------------------------------
// FMI 2.0
// ---------------------------------------------------------------------------------------------

namespace {

class Fmi2Instance final : public FmuInstance {
public:
    Fmi2Instance(std::unique_ptr<FmuPackage> pkg, std::string path) : FmuInstance(std::move(pkg), std::move(path)) {}

    ~Fmi2Instance() override {
        if (!fmu_) {
            return;
        }
        if (instantiated_) {
            if (state_ == State::StepMode) {
                fmi2_import_terminate(fmu_);
            }
            if (state_ != State::Fatal) {
                fmi2_import_free_instance(fmu_);
            }
        }
        if (dllLoaded_) {
            fmi2_import_destroy_dllfmu(fmu_);
        }
        fmi2_import_free(fmu_);
        fmu_ = nullptr;
    }

    void load() {
        fmu_ = fmi2_import_parse_xml(pkg_->ctx.ctx, pkg_->tempDir.path.c_str(), nullptr);
        if (!fmu_) {
            fail("Failed parsing FMI2 XML");
        }
        if (fmi2_import_get_fmu_kind(fmu_) != fmi2_fmu_kind_cs) {
            fail("FMU is not Co-Simulation");
        }

        meta_.fmiVersion = "2.0";
        meta_.modelName = safeStr(fmi2_import_get_model_name(fmu_));
        meta_.modelVersion = safeStr(fmi2_import_get_model_version(fmu_));
        meta_.guid = safeStr(fmi2_import_get_GUID(fmu_));
        meta_.generationTool = safeStr(fmi2_import_get_generation_tool(fmu_));
        if (fmi2_import_get_default_experiment_has_step(fmu_)) {
            meta_.declaredStep = fmi2_import_get_default_experiment_step(fmu_);
        }
        if (fmi2_import_get_default_experiment_has_start(fmu_)) {
            meta_.defaultStart = fmi2_import_get_default_experiment_start(fmu_);
        }
        if (fmi2_import_get_default_experiment_has_stop(fmu_)) {
            meta_.defaultStop = fmi2_import_get_default_experiment_stop(fmu_);
        }
        if (fmi2_import_get_default_experiment_has_tolerance(fmu_)) {
            meta_.tolerance = fmi2_import_get_default_experiment_tolerance(fmu_);
        }
        label_ = meta_.modelName.empty() ? fs::path(path_).stem().string() : meta_.modelName;

        callbacks_.allocateMemory = calloc;
        callbacks_.freeMemory = free;
        callbacks_.logger = fmi2LoggerCallback;
        callbacks_.componentEnvironment = nullptr;
        if (fmi2_import_create_dllfmu(fmu_, fmi2_fmu_kind_cs, &callbacks_) != jm_status_success) {
            fail("Failed loading FMU binaries");
        }
        dllLoaded_ = true;
    }

    void instantiate(const std::string& instanceName) override {
        if (fmi2_import_instantiate(fmu_, instanceName.c_str(), fmi2_cosimulation, nullptr, fmi2_false) != jm_status_success) {
            fail("Failed to instantiate FMI2 FMU");
        }
        instantiated_ = true;
        state_ = State::Instantiated;
    }

    void enterInit(double start, double stop) override {
        time_ = start;
        double tolerance = meta_.tolerance.value_or(1e-4);
        check(fmi2_import_setup_experiment(fmu_, fmi2_true, tolerance, start, fmi2_true, stop), "fmi2SetupExperiment");
        check(fmi2_import_enter_initialization_mode(fmu_), "fmi2EnterInitializationMode");
        state_ = State::Initializing;
    }

    void exitInit() override {
        check(fmi2_import_exit_initialization_mode(fmu_), "fmi2ExitInitializationMode");
        state_ = State::StepMode;
    }

    void terminate() override {
        if (state_ != State::StepMode) {
            return;
        }
        fmi2_status_t s = fmi2_import_terminate(fmu_);
        state_ = State::Terminated;
        if (s != fmi2_status_ok && s != fmi2_status_warning) {
            statusFailure("fmi2Terminate", fmi2StatusName(s), s == fmi2_status_fatal);
        }
    }

    bool hasVariable(const std::string& name) override {
        return lookup(name) != nullptr;
    }

    void setNumber(const std::string& name, double value) override {
        const Var* v = lookup(name);
        if (!v) {
            fail("Unknown variable '" + name + "'");
        }
        switch (v->baseType) {
            case fmi2_base_type_real: {
                fmi2_real_t rv = static_cast<fmi2_real_t>(value);
                check(fmi2_import_set_real(fmu_, &v->vr, 1, &rv), "fmi2SetReal", name);
                break;
            }
            case fmi2_base_type_int: {
                fmi2_integer_t iv = static_cast<fmi2_integer_t>(std::llround(value));
                check(fmi2_import_set_integer(fmu_, &v->vr, 1, &iv), "fmi2SetInteger", name);
                break;
            }
            case fmi2_base_type_bool: {
                fmi2_boolean_t bv = (value != 0.0) ? fmi2_true : fmi2_false;
                check(fmi2_import_set_boolean(fmu_, &v->vr, 1, &bv), "fmi2SetBoolean", name);
                break;
            }
            default:
                fail("Unsupported base type for " + name);
        }
    }

    OutputValue get(const std::string& name) override {
        const Var* v = lookup(name);
        if (!v) {
            fail("Variable '" + name + "' not found");
        }
        switch (v->baseType) {
            case fmi2_base_type_real: {
                fmi2_real_t value{};
                check(fmi2_import_get_real(fmu_, &v->vr, 1, &value), "fmi2GetReal", name);
                return OutputValue::real(value);
            }
            case fmi2_base_type_int: {
                fmi2_integer_t iv{};
                check(fmi2_import_get_integer(fmu_, &v->vr, 1, &iv), "fmi2GetInteger", name);
                return OutputValue::integer(iv);
            }
            case fmi2_base_type_bool: {
                fmi2_boolean_t bv{};
                check(fmi2_import_get_boolean(fmu_, &v->vr, 1, &bv), "fmi2GetBoolean", name);
                return OutputValue::boolean(bv != fmi2_false);
            }
            default:
                fail("Unsupported variable type for " + name);
        }
    }

    std::vector<std::string> autoOutputs() override {
        std::vector<std::string> names;
        fmi2_import_variable_list_t* list = fmi2_import_get_variable_list(fmu_, 0);
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

protected:
    StepResult doStepImpl(double t, double h) override {
        check(fmi2_import_do_step(fmu_, t, h, fmi2_true), "fmi2DoStep");
        StepResult r;
        r.lastTime = t + h;
        return r;
    }

private:
    struct Var {
        fmi2_value_reference_t vr;
        fmi2_base_type_enu_t baseType;
    };

    const Var* lookup(const std::string& name) {
        auto it = cache_.find(name);
        if (it != cache_.end()) {
            return &it->second;
        }
        fmi2_import_variable_t* var = fmi2_import_get_variable_by_name(fmu_, name.c_str());
        if (!var) {
            return nullptr;
        }
        Var v{fmi2_import_get_variable_vr(var), fmi2_import_get_variable_base_type(var)};
        return &cache_.emplace(name, v).first->second;
    }

    void check(fmi2_status_t s, const char* what, const std::string& var = std::string()) {
        if (s == fmi2_status_ok || s == fmi2_status_warning) {
            return;
        }
        std::string desc = var.empty() ? std::string(what) : std::string(what) + "(" + var + ")";
        statusFailure(desc.c_str(), fmi2StatusName(s), s == fmi2_status_fatal);
    }

    fmi2_import_t* fmu_{nullptr};
    fmi2_callback_functions_t callbacks_{};
    bool dllLoaded_{false};
    bool instantiated_{false};
    std::unordered_map<std::string, Var> cache_;
};

// ---------------------------------------------------------------------------------------------
// FMI 3.0
// ---------------------------------------------------------------------------------------------

class Fmi3Instance final : public FmuInstance {
public:
    Fmi3Instance(std::unique_ptr<FmuPackage> pkg, std::string path) : FmuInstance(std::move(pkg), std::move(path)) {}

    ~Fmi3Instance() override {
        if (!fmu_) {
            return;
        }
        if (instantiated_) {
            if (state_ == State::StepMode) {
                fmi3_import_terminate(fmu_);
            }
            if (state_ != State::Fatal) {
                fmi3_import_free_instance(fmu_);
            }
        }
        if (dllLoaded_) {
            fmi3_import_destroy_dllfmu(fmu_);
        }
        fmi3_import_free(fmu_);
        fmu_ = nullptr;
    }

    void load() {
        fmu_ = fmi3_import_parse_xml(pkg_->ctx.ctx, pkg_->tempDir.path.c_str(), nullptr);
        if (!fmu_) {
            fail("Failed parsing FMI3 XML");
        }
        if (fmi3_import_get_fmu_kind(fmu_) != fmi3_fmu_kind_cs) {
            fail("FMI3 FMU is not Co-Simulation");
        }

        meta_.fmiVersion = "3.0";
        meta_.modelName = safeStr(fmi3_import_get_model_name(fmu_));
        meta_.modelVersion = safeStr(fmi3_import_get_model_version(fmu_));
        meta_.guid = safeStr(fmi3_import_get_instantiation_token(fmu_));
        meta_.generationTool = safeStr(fmi3_import_get_generation_tool(fmu_));
        if (fmi3_import_get_default_experiment_has_step_size(fmu_)) {
            meta_.declaredStep = fmi3_import_get_default_experiment_step_size(fmu_);
        }
        if (fmi3_import_get_default_experiment_has_start(fmu_)) {
            meta_.defaultStart = fmi3_import_get_default_experiment_start(fmu_);
        }
        if (fmi3_import_get_default_experiment_has_stop(fmu_)) {
            meta_.defaultStop = fmi3_import_get_default_experiment_stop(fmu_);
        }
        if (fmi3_import_get_default_experiment_has_tolerance(fmu_)) {
            meta_.tolerance = fmi3_import_get_default_experiment_tolerance(fmu_);
        }
        label_ = meta_.modelName.empty() ? fs::path(path_).stem().string() : meta_.modelName;

        if (fmi3_import_create_dllfmu(fmu_, fmi3_fmu_kind_cs, nullptr, nullptr) != jm_status_success) {
            fail("Failed loading FMI3 binaries");
        }
        dllLoaded_ = true;
    }

    void instantiate(const std::string& instanceName) override {
        if (fmi3_import_instantiate_co_simulation(
                fmu_, instanceName.c_str(), nullptr, fmi3_false, fmi3_false,
                fmi3_false, fmi3_false, nullptr, 0, nullptr) != jm_status_success) {
            fail("Failed instantiating FMI3 FMU");
        }
        instantiated_ = true;
        state_ = State::Instantiated;
    }

    void enterInit(double start, double stop) override {
        time_ = start;
        double tolerance = meta_.tolerance.value_or(1e-4);
        check(fmi3_import_enter_initialization_mode(fmu_, fmi3_true, tolerance, start, fmi3_true, stop),
              "fmi3EnterInitializationMode");
        state_ = State::Initializing;
    }

    void exitInit() override {
        check(fmi3_import_exit_initialization_mode(fmu_), "fmi3ExitInitializationMode");
        state_ = State::StepMode;
    }

    void terminate() override {
        if (state_ != State::StepMode) {
            return;
        }
        fmi3_status_t s = fmi3_import_terminate(fmu_);
        state_ = State::Terminated;
        if (s != fmi3_status_ok && s != fmi3_status_warning) {
            statusFailure("fmi3Terminate", fmi3StatusName(s), s == fmi3_status_fatal);
        }
    }

    bool hasVariable(const std::string& name) override {
        return lookup(name) != nullptr;
    }

    void setNumber(const std::string& name, double value) override {
        Var* v = lookup(name);
        if (!v) {
            fail("Unknown variable '" + name + "'");
        }
        switch (v->baseType) {
            case fmi3_base_type_float64: {
                fmi3_float64_t fv = static_cast<fmi3_float64_t>(value);
                check(fmi3_import_set_float64(fmu_, &v->vr, 1, &fv, 1), "fmi3SetFloat64", name);
                break;
            }
            case fmi3_base_type_int32: {
                fmi3_int32_t iv = static_cast<fmi3_int32_t>(std::llround(value));
                check(fmi3_import_set_int32(fmu_, &v->vr, 1, &iv, 1), "fmi3SetInt32", name);
                break;
            }
            case fmi3_base_type_bool: {
                fmi3_boolean_t bv = (value != 0.0) ? fmi3_true : fmi3_false;
                check(fmi3_import_set_boolean(fmu_, &v->vr, 1, &bv, 1), "fmi3SetBoolean", name);
                break;
            }
            default:
                fail("Unsupported FMI3 base type for " + name);
        }
    }

    OutputValue get(const std::string& name) override {
        Var* v = lookup(name);
        if (!v) {
            fail("Variable '" + name + "' not found");
        }
        size_t valueCount = v->staticCount;
        if (valueCount == 0) {
            bool isStatic = false;
            valueCount = resolveFmi3ValueCount(fmu_, v->var, name, isStatic);
            if (isStatic) {
                v->staticCount = valueCount;
            }
        }
        OutputValue ov{};
        switch (v->baseType) {
            case fmi3_base_type_float64: {
                std::vector<fmi3_float64_t> values(valueCount);
                check(fmi3_import_get_float64(fmu_, &v->vr, 1, values.data(), valueCount), "fmi3GetFloat64", name);
                if (valueCount == 1) {
                    return OutputValue::real(values[0]);
                }
                ov.type = OutputValue::Type::RealArray;
                ov.realArray.assign(values.begin(), values.end());
                break;
            }
            case fmi3_base_type_int32: {
                std::vector<fmi3_int32_t> values(valueCount);
                check(fmi3_import_get_int32(fmu_, &v->vr, 1, values.data(), valueCount), "fmi3GetInt32", name);
                if (valueCount == 1) {
                    return OutputValue::integer(values[0]);
                }
                ov.type = OutputValue::Type::IntegerArray;
                ov.intArray.reserve(values.size());
                for (fmi3_int32_t value : values) {
                    ov.intArray.push_back(value);
                }
                break;
            }
            case fmi3_base_type_bool: {
                std::unique_ptr<fmi3_boolean_t[]> rawValues(new fmi3_boolean_t[valueCount]);
                check(fmi3_import_get_boolean(fmu_, &v->vr, 1, rawValues.get(), valueCount), "fmi3GetBoolean", name);
                if (valueCount == 1) {
                    return OutputValue::boolean(rawValues[0] != fmi3_false);
                }
                ov.type = OutputValue::Type::BooleanArray;
                ov.boolArray.reserve(valueCount);
                for (size_t i = 0; i < valueCount; ++i) {
                    ov.boolArray.push_back(rawValues[i] != fmi3_false);
                }
                break;
            }
            default:
                fail("Unsupported variable type for " + name);
        }
        return ov;
    }

    std::vector<std::string> autoOutputs() override {
        std::vector<std::string> names;
        fmi3_import_variable_list_t* list = fmi3_import_get_variable_list(fmu_, 0);
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

protected:
    StepResult doStepImpl(double t, double h) override {
        fmi3_boolean_t eventNeeded = fmi3_false;
        fmi3_boolean_t terminate = fmi3_false;
        fmi3_boolean_t earlyReturn = fmi3_false;
        fmi3_float64_t lastSuccessfulTime{};
        check(fmi3_import_do_step(fmu_, t, h, fmi3_false, &eventNeeded, &terminate, &earlyReturn, &lastSuccessfulTime),
              "fmi3DoStep");
        StepResult r;
        r.terminate = (terminate == fmi3_true);
        r.eventNeeded = (eventNeeded == fmi3_true);
        r.lastTime = lastSuccessfulTime;
        return r;
    }

private:
    struct Var {
        fmi3_import_variable_t* var;
        fmi3_value_reference_t vr;
        fmi3_base_type_enu_t baseType;
        size_t staticCount;  // 0 = not cached (dynamic or not resolved yet)
    };

    Var* lookup(const std::string& name) {
        auto it = cache_.find(name);
        if (it != cache_.end()) {
            return &it->second;
        }
        fmi3_import_variable_t* var = fmi3_import_get_variable_by_name(fmu_, name.c_str());
        if (!var) {
            return nullptr;
        }
        Var v{var, fmi3_import_get_variable_vr(var), fmi3_import_get_variable_base_type(var), 0};
        return &cache_.emplace(name, v).first->second;
    }

    void check(fmi3_status_t s, const char* what, const std::string& var = std::string()) {
        if (s == fmi3_status_ok || s == fmi3_status_warning) {
            return;
        }
        std::string desc = var.empty() ? std::string(what) : std::string(what) + "(" + var + ")";
        statusFailure(desc.c_str(), fmi3StatusName(s), s == fmi3_status_fatal);
    }

    fmi3_import_t* fmu_{nullptr};
    bool dllLoaded_{false};
    bool instantiated_{false};
    std::unordered_map<std::string, Var> cache_;
};

}  // namespace

std::unique_ptr<FmuInstance> openFmu(const std::string& path) {
    preloadLibPythonIfAvailable();

    if (!fs::exists(path)) {
        fail("FMU not found: " + path);
    }

    auto pkg = std::make_unique<FmuPackage>();
    fmi_version_enu_t version = fmi_import_get_fmi_version(pkg->ctx.ctx, path.c_str(), pkg->tempDir.path.c_str());
    if (version == fmi_version_unknown_enu) {
        fail("Unable to detect FMI version");
    }

    // load() runs on a fully constructed object owned by a unique_ptr, so a throw inside it
    // still runs the destructor and releases whatever was acquired.
    if (version == fmi_version_2_0_enu) {
        auto inst = std::make_unique<Fmi2Instance>(std::move(pkg), path);
        inst->load();
        return inst;
    }
    if (version == fmi_version_3_0_enu) {
        auto inst = std::make_unique<Fmi3Instance>(std::move(pkg), path);
        inst->load();
        return inst;
    }
    fail("Unsupported FMI version");
}

}  // namespace cads
