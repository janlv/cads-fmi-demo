// FmuInstance: one loaded FMU (FMI 2.0 or 3.0 co-simulation) behind a version-neutral API.
//
// Lifetime: openFmu() unpacks the archive into a private temp dir, parses the model
// description, checks the co-simulation kind and loads the binary. The destructor is noexcept
// and, driven by the instance state, terminates (when in step mode), frees the instance,
// unloads the binary, frees the model description, the FMIL context and finally removes the
// temp dir. This holds on every exit path, including exceptions.
#pragma once

#include "bridge_common.hpp"

#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace cads {

struct FmuMeta {
    std::string fmiVersion;  // "2.0" | "3.0"
    std::string modelName;
    std::string modelVersion;
    std::string guid;  // FMI 2 guid or FMI 3 instantiationToken
    std::string generationTool;
    std::optional<double> declaredStep;  // DefaultExperiment stepSize
    std::optional<double> defaultStart;
    std::optional<double> defaultStop;
    std::optional<double> tolerance;
};

struct FmuStats {
    int64_t doStepCalls{};
    int64_t clippedSubsteps{};
    double doStepWall{};
    double stepUsed{};
};

struct StepResult {
    bool terminate{false};
    bool eventNeeded{false};
    double lastTime{};
};

struct FmuPackage;  // temp dir + FMIL callbacks + context (defined in fmu_instance.cpp)

class FmuInstance {
public:
    enum class State { Loaded, Instantiated, Initializing, StepMode, Terminated, Error, Fatal };

    virtual ~FmuInstance();
    FmuInstance(const FmuInstance&) = delete;
    FmuInstance& operator=(const FmuInstance&) = delete;

    // Label used in error messages (co-simulation model name, else modelName).
    const std::string& label() const { return label_; }
    void setLabel(const std::string& label) { label_ = label; }

    const FmuMeta& meta() const { return meta_; }
    FmuStats& stats() { return stats_; }
    const FmuStats& stats() const { return stats_; }
    State state() const { return state_; }

    virtual void instantiate(const std::string& instanceName) = 0;
    virtual void enterInit(double start, double stop) = 0;
    virtual void exitInit() = 0;
    virtual void terminate() = 0;

    virtual bool hasVariable(const std::string& name) = 0;
    virtual void setNumber(const std::string& name, double value) = 0;
    virtual OutputValue get(const std::string& name) = 0;
    double getNumber(const std::string& name);
    virtual std::vector<std::string> autoOutputs() = 0;

    // Advances from t by h, timing the call and updating stats. A sub-step shorter than the
    // step in use (stats().stepUsed) counts as clipped.
    StepResult doStep(double t, double h);

protected:
    FmuInstance(std::unique_ptr<FmuPackage> pkg, std::string path);
    virtual StepResult doStepImpl(double t, double h) = 0;
    [[noreturn]] void statusFailure(const char* what, const char* status, bool fatal);

    // Declared first so it is destroyed last (after the derived destructor has released the
    // FMU): context -> temp dir.
    std::unique_ptr<FmuPackage> pkg_;
    std::string path_;
    std::string label_;
    FmuMeta meta_;
    FmuStats stats_;
    State state_{State::Loaded};
    double time_{};  // last known simulation time, for error messages
};

// Make sure libpython is globally visible before loading Python-exported FMUs.
void preloadLibPythonIfAvailable();

// Detects the FMI version, unpacks, parses, checks for co-simulation, loads the binary and
// fills meta(). Throws on any failure (all partially acquired resources are released).
std::unique_ptr<FmuInstance> openFmu(const std::string& path);

}  // namespace cads
