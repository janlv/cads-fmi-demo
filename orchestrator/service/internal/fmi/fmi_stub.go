//go:build !cgo

package fmi

import "fmt"

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

// Run reports that the FMIL-backed runner is unavailable without CGO.
func Run(cfg Config) (map[string]any, error) {
	if cfg.FMUPath == "" {
		return nil, fmt.Errorf("fmi: FMU path is required")
	}
	return nil, fmt.Errorf("fmi runner requires CGO and FMIL headers/libraries")
}
