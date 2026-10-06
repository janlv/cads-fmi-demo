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

/* Runs one FMU. Returns 0 on success with *json_out = {"values": {...}, "stats": {...}},
 * 1 on error, 2 when cancelled (cads_request_cancel). *err_out holds the message on 1/2. */
int cads_run_fmu(const cads_fmu_config* cfg, char** json_out, char** err_out);
void cads_free_string(char* ptr);

/* ---- Co-simulation master ---------------------------------------------------------------- */

/* One variable of one model: model "events" refers to the synthetic event signals
 * ("<event>.active" in trace signals, "<event>.count" in outputs). */
typedef struct {
    const char* model;
    const char* var;
} cads_varref;

typedef struct {
    cads_varref from;
    cads_varref to;
} cads_connection;

/* Comparison operator codes; must match OpLT..OpNE in types.go. */
enum {
    CADS_OP_LT = 0,
    CADS_OP_LE = 1,
    CADS_OP_GT = 2,
    CADS_OP_GE = 3,
    CADS_OP_EQ = 4,
    CADS_OP_NE = 5
};

/* Drives target to value while "lhs op rhs" holds (level) or for one communication interval
 * after a rising edge (pulse), and to reset otherwise. */
typedef struct {
    const char* name;
    cads_varref lhs;
    int op;
    double rhs;
    cads_varref target;
    double value;
    double reset;
    bool pulse;
} cads_event;

typedef struct {
    const char* name;
    const char* fmu_path;
    const cads_assignment* start_values;
    size_t start_value_count;
    const cads_input_series* input_series; /* optional, may be NULL */
} cads_cosim_model;

/* Scheme codes; must match SchemeCode in types.go. */
enum {
    CADS_SCHEME_JACOBI = 0,
    CADS_SCHEME_GAUSS_SEIDEL = 1
};

typedef struct {
    int scheme;
    double start_time;
    double stop_time;
    double communication_step;
    const cads_cosim_model* models;
    size_t model_count;
    const cads_connection* connections;
    size_t connection_count;
    const cads_event* events;
    size_t event_count;
    const cads_varref* outputs; /* empty: every output of every model, prefixed */
    size_t output_count;
    const cads_varref* trace_signals;
    size_t trace_signal_count;
    bool has_trace_interval;
    double trace_interval; /* default: communication_step */
} cads_cosim_config;

/* Returns 0 ok, 1 error, 2 cancelled. On 1/2 *json_out may hold a partial envelope
 * (values with the trace so far, stats with failed_at) and *err_out the message. */
int cads_run_cosim(const cads_cosim_config* cfg, char** json_out, char** err_out);

/* Process-wide cancel flag, checked once per communication point. */
void cads_request_cancel(void);
void cads_reset_cancel(void);

#ifdef __cplusplus
}
#endif
