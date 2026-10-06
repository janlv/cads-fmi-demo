//go:build cgo

package fmi

/*
#cgo CXXFLAGS: -std=c++17
#cgo linux LDFLAGS: -lfmilib_shared -lpugixml -lzip -lm -ldl -lstdc++
#cgo darwin LDFLAGS: -lfmilib_shared -lm -lc++
#include <stdlib.h>
#include "runner_bridge.h"
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"sort"
	"unsafe"
)

// cArena owns C allocations made while marshalling one bridge call.
type cArena struct {
	ptrs []unsafe.Pointer
}

func (a *cArena) str(s string) *C.char {
	p := C.CString(s)
	a.ptrs = append(a.ptrs, unsafe.Pointer(p))
	return p
}

// alloc returns zeroed C memory for n elements of the given size (never nil for n > 0).
func (a *cArena) alloc(n int, size uintptr) unsafe.Pointer {
	if n <= 0 {
		return nil
	}
	p := C.calloc(C.size_t(n), C.size_t(size))
	if p == nil {
		panic("fmi: C allocation failed")
	}
	a.ptrs = append(a.ptrs, p)
	return p
}

func (a *cArena) strArray(values []string) (**C.char, C.size_t) {
	if len(values) == 0 {
		return nil, 0
	}
	mem := a.alloc(len(values), unsafe.Sizeof((*C.char)(nil)))
	arr := unsafe.Slice((**C.char)(mem), len(values))
	for i, v := range values {
		arr[i] = a.str(v)
	}
	return (**C.char)(mem), C.size_t(len(values))
}

func (a *cArena) assignments(values map[string]string) (*C.cads_assignment, C.size_t) {
	if len(values) == 0 {
		return nil, 0
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	mem := a.alloc(len(keys), C.sizeof_cads_assignment)
	arr := unsafe.Slice((*C.cads_assignment)(mem), len(keys))
	for i, k := range keys {
		arr[i] = C.cads_assignment{name: a.str(k), value: a.str(values[k])}
	}
	return (*C.cads_assignment)(mem), C.size_t(len(keys))
}

func (a *cArena) inputSeries(cfg *InputSeriesConfig) *C.cads_input_series {
	if cfg == nil || cfg.CSVPath == "" {
		return nil
	}
	p := (*C.cads_input_series)(a.alloc(1, C.sizeof_cads_input_series))
	p.csv_path = a.str(cfg.CSVPath)
	return p
}

func (a *cArena) varRef(v VarRef) C.cads_varref {
	return C.cads_varref{model: a.str(v.Model), _var: a.str(v.Var)}
}

func (a *cArena) varRefs(values []VarRef) (*C.cads_varref, C.size_t) {
	if len(values) == 0 {
		return nil, 0
	}
	mem := a.alloc(len(values), C.sizeof_cads_varref)
	arr := unsafe.Slice((*C.cads_varref)(mem), len(values))
	for i, v := range values {
		arr[i] = a.varRef(v)
	}
	return (*C.cads_varref)(mem), C.size_t(len(values))
}

func (a *cArena) free() {
	for _, p := range a.ptrs {
		C.free(p)
	}
	a.ptrs = nil
}

// decodeEnvelope parses the bridge's {"values","stats"} JSON.
func decodeEnvelope(raw *C.char) (*Result, error) {
	var res Result
	if err := json.Unmarshal([]byte(C.GoString(raw)), &res); err != nil {
		return nil, fmt.Errorf("decode FMU result: %w", err)
	}
	if res.Values == nil {
		res.Values = map[string]any{}
	}
	return &res, nil
}

// finish converts a bridge return code plus outputs into (*Result, error). On failure the
// result is non-nil only when the bridge supplied a partial envelope.
func finish(code C.int, jsonOut, errOut *C.char, prefix string) (*Result, error) {
	if jsonOut != nil {
		defer C.cads_free_string(jsonOut)
	}
	if errOut != nil {
		defer C.cads_free_string(errOut)
	}
	if code == 0 {
		if jsonOut == nil {
			return nil, fmt.Errorf("%s returned no result", prefix)
		}
		return decodeEnvelope(jsonOut)
	}

	var partial *Result
	if jsonOut != nil {
		partial, _ = decodeEnvelope(jsonOut)
	}
	if code == 2 {
		return partial, ErrCancelled
	}
	if errOut != nil {
		return partial, fmt.Errorf("%s: %s", prefix, C.GoString(errOut))
	}
	return partial, fmt.Errorf("%s failed without error message", prefix)
}

// Run executes the FMU using FMIL and returns the final snapshot of requested outputs plus
// optional sampled trace data, together with run statistics.
func Run(cfg Config) (*Result, error) {
	if cfg.FMUPath == "" {
		return nil, fmt.Errorf("fmi: FMU path is required")
	}

	var a cArena
	defer a.free()

	cCfg := C.cads_fmu_config{}
	cCfg.fmu_path = a.str(cfg.FMUPath)
	if cfg.StartTime != nil {
		cCfg.has_start_time = true
		cCfg.start_time = C.double(*cfg.StartTime)
	}
	if cfg.StopTime != nil {
		cCfg.has_stop_time = true
		cCfg.stop_time = C.double(*cfg.StopTime)
	}
	if cfg.StepSize != nil {
		cCfg.has_step_size = true
		cCfg.step_size = C.double(*cfg.StepSize)
	}
	cCfg.start_values, cCfg.start_value_count = a.assignments(cfg.StartValues)
	cCfg.input_series = a.inputSeries(cfg.InputSeries)
	cCfg.outputs, cCfg.output_count = a.strArray(cfg.Outputs)
	if cfg.Trace != nil {
		if cfg.Trace.SampleEvery != nil {
			cCfg.has_trace_interval = true
			cCfg.trace_interval = C.double(*cfg.Trace.SampleEvery)
		}
		cCfg.trace_outputs, cCfg.trace_output_count = a.strArray(cfg.Trace.Outputs)
		cCfg.trace_inputs, cCfg.trace_input_count = a.strArray(cfg.Trace.Inputs)
	}

	var jsonOut, errOut *C.char
	code := C.cads_run_fmu(&cCfg, &jsonOut, &errOut)
	res, err := finish(code, jsonOut, errOut, "fmi runner")
	if err != nil {
		// The single-FMU path never produces partial results.
		return nil, err
	}
	return res, nil
}

// RunCoSim executes a multi-FMU co-simulation. On failure or cancellation it returns the
// partial result (trace so far, stats with failed_at) together with the error when the bridge
// supplied one; cancellation is reported as ErrCancelled.
func RunCoSim(cfg CoSimConfig) (*Result, error) {
	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("fmi: co-simulation requires at least one model")
	}
	scheme, ok := SchemeCode(cfg.Scheme)
	if !ok {
		return nil, fmt.Errorf("fmi: unknown co-simulation scheme %q", cfg.Scheme)
	}

	var a cArena
	defer a.free()

	cCfg := C.cads_cosim_config{}
	cCfg.scheme = C.int(scheme)
	cCfg.start_time = C.double(cfg.StartTime)
	cCfg.stop_time = C.double(cfg.StopTime)
	cCfg.communication_step = C.double(cfg.CommunicationStep)

	models := unsafe.Slice((*C.cads_cosim_model)(a.alloc(len(cfg.Models), C.sizeof_cads_cosim_model)), len(cfg.Models))
	for i, m := range cfg.Models {
		models[i].name = a.str(m.Name)
		models[i].fmu_path = a.str(m.FMUPath)
		models[i].start_values, models[i].start_value_count = a.assignments(m.StartValues)
		models[i].input_series = a.inputSeries(m.InputSeries)
	}
	cCfg.models = &models[0]
	cCfg.model_count = C.size_t(len(models))

	if len(cfg.Connections) > 0 {
		conns := unsafe.Slice((*C.cads_connection)(a.alloc(len(cfg.Connections), C.sizeof_cads_connection)), len(cfg.Connections))
		for i, c := range cfg.Connections {
			conns[i].from = a.varRef(c.From)
			conns[i].to = a.varRef(c.To)
		}
		cCfg.connections = &conns[0]
		cCfg.connection_count = C.size_t(len(conns))
	}

	if len(cfg.Events) > 0 {
		events := unsafe.Slice((*C.cads_event)(a.alloc(len(cfg.Events), C.sizeof_cads_event)), len(cfg.Events))
		for i, e := range cfg.Events {
			op, ok := ParseOp(e.Op)
			if !ok {
				return nil, fmt.Errorf("fmi: event %q: unknown operator %q", e.Name, e.Op)
			}
			events[i].name = a.str(e.Name)
			events[i].lhs = a.varRef(e.LHS)
			events[i].op = C.int(op)
			events[i].rhs = C.double(e.RHS)
			events[i].target = a.varRef(e.Target)
			events[i].value = C.double(e.Value)
			events[i].reset = C.double(e.Reset)
			events[i].pulse = C.bool(e.Pulse)
		}
		cCfg.events = &events[0]
		cCfg.event_count = C.size_t(len(events))
	}

	cCfg.outputs, cCfg.output_count = a.varRefs(cfg.Outputs)
	cCfg.trace_signals, cCfg.trace_signal_count = a.varRefs(cfg.TraceSignals)
	if cfg.TraceSampleEvery != nil {
		cCfg.has_trace_interval = true
		cCfg.trace_interval = C.double(*cfg.TraceSampleEvery)
	}

	var jsonOut, errOut *C.char
	code := C.cads_run_cosim(&cCfg, &jsonOut, &errOut)
	return finish(code, jsonOut, errOut, "fmi cosim")
}

// RequestCancel asks a running Run/RunCoSim to stop at its next communication point (the call
// then returns ErrCancelled). The flag stays set until ResetCancel.
func RequestCancel() {
	C.cads_request_cancel()
}

// ResetCancel clears the cancel flag before a new run.
func ResetCancel() {
	C.cads_reset_cancel()
}
