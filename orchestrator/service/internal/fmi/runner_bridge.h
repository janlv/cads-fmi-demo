#pragma once

#include <stdbool.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    const char* name;
    const char* value;
} cads_assignment;

typedef struct {
    const char* csv_path;
} cads_input_series;

typedef struct {
    const char* fmu_path;
    bool has_start_time;
    double start_time;
    bool has_stop_time;
    double stop_time;
    bool has_step_size;
    double step_size;
    const cads_assignment* start_values;
    size_t start_value_count;
    const cads_input_series* input_series;
    const char* const* outputs;
    size_t output_count;
    const char* const* trace_outputs;
    size_t trace_output_count;
    const char* const* trace_inputs;
    size_t trace_input_count;
    bool has_trace_interval;
    double trace_interval;
} cads_fmu_config;

int cads_run_fmu(const cads_fmu_config* cfg, char** json_out, char** err_out);
void cads_free_string(char* ptr);

#ifdef __cplusplus
}
#endif
