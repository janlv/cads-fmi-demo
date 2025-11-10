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
	"runtime"
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
}

// Run executes the FMU using FMIL and returns the final snapshot of requested outputs.
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

	var assignmentStorage []C.cads_assignment
	var assignmentBacking []*C.char
	if len(cfg.StartValues) > 0 {
		keys := make([]string, 0, len(cfg.StartValues))
		for k := range cfg.StartValues {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		assignmentStorage = make([]C.cads_assignment, len(keys))
		for i, key := range keys {
			value := cfg.StartValues[key]
			nameC := C.CString(key)
			valueC := C.CString(value)
			assignmentBacking = append(assignmentBacking, nameC, valueC)
			assignmentStorage[i] = C.cads_assignment{name: nameC, value: valueC}
		}
		cCfg.start_values = (*C.cads_assignment)(unsafe.Pointer(&assignmentStorage[0]))
		cCfg.start_value_count = C.size_t(len(assignmentStorage))
	}

	var outputPtrs []*C.char
	if len(cfg.Outputs) > 0 {
		outputPtrs = make([]*C.char, len(cfg.Outputs))
		for i, name := range cfg.Outputs {
			cstr := C.CString(name)
			assignmentBacking = append(assignmentBacking, cstr)
			outputPtrs[i] = cstr
		}
		cCfg.outputs = (**C.char)(unsafe.Pointer(&outputPtrs[0]))
		cCfg.output_count = C.size_t(len(outputPtrs))
	}

	defer func() {
		for _, ptr := range assignmentBacking {
			C.free(unsafe.Pointer(ptr))
		}
	}()

	var jsonOut *C.char
	var errOut *C.char

	code := C.cads_run_fmu(&cCfg, &jsonOut, &errOut)

	runtime.KeepAlive(assignmentStorage)
	runtime.KeepAlive(outputPtrs)

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
