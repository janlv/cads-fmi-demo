//go:build cgo

package fmi

/*
#cgo CXXFLAGS: -std=c++17
#cgo LDFLAGS: -lfmilib_shared -lpugixml -lzip -lm -ldl -lstdc++
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

// Config describes a single FMU execution.
type Config struct {
	FMUPath     string
	StartTime   *float64
	StopTime    *float64
	StepSize    *float64
	StartValues map[string]string
	Outputs     []string
	InputSeries *InputSeriesConfig
	Trace       *TraceConfig
}

type InputSeriesConfig struct {
	CSVPath string
}

type TraceConfig struct {
	Outputs     []string
	Inputs      []string
	SampleEvery *float64
}

// Run executes the FMU using FMIL and returns the final snapshot of requested outputs plus
// optional sampled trace data when configured.
func Run(cfg Config) (map[string]any, error) {
	if cfg.FMUPath == "" {
		return nil, fmt.Errorf("fmi: FMU path is required")
	}

	cCfg := C.cads_fmu_config{}
	cPath := C.CString(cfg.FMUPath)
	defer C.free(unsafe.Pointer(cPath))
	cCfg.fmu_path = cPath

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

	var assignmentBacking []*C.char
	if len(cfg.StartValues) > 0 {
		keys := make([]string, 0, len(cfg.StartValues))
		for k := range cfg.StartValues {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		mem := C.malloc(C.size_t(len(keys)) * C.size_t(C.sizeof_cads_assignment))
		if mem == nil {
			return nil, fmt.Errorf("fmi: failed to allocate start value buffer")
		}
		defer C.free(mem)
		assignments := unsafe.Slice((*C.cads_assignment)(mem), len(keys))
		for i, key := range keys {
			value := cfg.StartValues[key]
			nameC := C.CString(key)
			valueC := C.CString(value)
			assignmentBacking = append(assignmentBacking, nameC, valueC)
			assignments[i] = C.cads_assignment{name: nameC, value: valueC}
		}
		cCfg.start_values = (*C.cads_assignment)(mem)
		cCfg.start_value_count = C.size_t(len(keys))
	}

	if cfg.InputSeries != nil && cfg.InputSeries.CSVPath != "" {
		cstr := C.CString(cfg.InputSeries.CSVPath)
		assignmentBacking = append(assignmentBacking, cstr)
		inputSeries := (*C.cads_input_series)(C.malloc(C.size_t(C.sizeof_cads_input_series)))
		if inputSeries == nil {
			return nil, fmt.Errorf("fmi: failed to allocate input series buffer")
		}
		defer C.free(unsafe.Pointer(inputSeries))
		*inputSeries = C.cads_input_series{csv_path: cstr}
		cCfg.input_series = inputSeries
	}

	if len(cfg.Outputs) > 0 {
		ptrSize := unsafe.Sizeof((*C.char)(nil))
		mem := C.malloc(C.size_t(len(cfg.Outputs)) * C.size_t(ptrSize))
		if mem == nil {
			return nil, fmt.Errorf("fmi: failed to allocate outputs buffer")
		}
		defer C.free(mem)
		outputPtrs := unsafe.Slice((**C.char)(mem), len(cfg.Outputs))
		for i, name := range cfg.Outputs {
			cstr := C.CString(name)
			assignmentBacking = append(assignmentBacking, cstr)
			outputPtrs[i] = cstr
		}
		cCfg.outputs = (**C.char)(mem)
		cCfg.output_count = C.size_t(len(outputPtrs))
	}

	if cfg.Trace != nil {
		if cfg.Trace.SampleEvery != nil {
			cCfg.has_trace_interval = true
			cCfg.trace_interval = C.double(*cfg.Trace.SampleEvery)
		}
		if len(cfg.Trace.Outputs) > 0 {
			ptrSize := unsafe.Sizeof((*C.char)(nil))
			mem := C.malloc(C.size_t(len(cfg.Trace.Outputs)) * C.size_t(ptrSize))
			if mem == nil {
				return nil, fmt.Errorf("fmi: failed to allocate trace outputs buffer")
			}
			defer C.free(mem)
			outputPtrs := unsafe.Slice((**C.char)(mem), len(cfg.Trace.Outputs))
			for i, name := range cfg.Trace.Outputs {
				cstr := C.CString(name)
				assignmentBacking = append(assignmentBacking, cstr)
				outputPtrs[i] = cstr
			}
			cCfg.trace_outputs = (**C.char)(mem)
			cCfg.trace_output_count = C.size_t(len(outputPtrs))
		}
		if len(cfg.Trace.Inputs) > 0 {
			ptrSize := unsafe.Sizeof((*C.char)(nil))
			mem := C.malloc(C.size_t(len(cfg.Trace.Inputs)) * C.size_t(ptrSize))
			if mem == nil {
				return nil, fmt.Errorf("fmi: failed to allocate trace inputs buffer")
			}
			defer C.free(mem)
			inputPtrs := unsafe.Slice((**C.char)(mem), len(cfg.Trace.Inputs))
			for i, name := range cfg.Trace.Inputs {
				cstr := C.CString(name)
				assignmentBacking = append(assignmentBacking, cstr)
				inputPtrs[i] = cstr
			}
			cCfg.trace_inputs = (**C.char)(mem)
			cCfg.trace_input_count = C.size_t(len(inputPtrs))
		}
	}

	defer func() {
		for _, ptr := range assignmentBacking {
			C.free(unsafe.Pointer(ptr))
		}
	}()

	var jsonOut *C.char
	var errOut *C.char

	code := C.cads_run_fmu(&cCfg, &jsonOut, &errOut)

	if code != 0 {
		if errOut != nil {
			defer C.cads_free_string(errOut)
			return nil, fmt.Errorf("fmi runner: %s", C.GoString(errOut))
		}
		return nil, fmt.Errorf("fmi runner failed without error message")
	}
	defer C.cads_free_string(jsonOut)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(C.GoString(jsonOut)), &parsed); err != nil {
		return nil, fmt.Errorf("decode FMU result: %w", err)
	}
	return parsed, nil
}
